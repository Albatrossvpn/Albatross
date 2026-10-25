#!/usr/bin/env bash
# Sets up a tester inside the country: a machine on an Iranian network that
# connects through the candidate servers for real, every two hours, and
# reports which ones work from here. The collector abroad cannot see the
# filtering; this machine can. Its report (list/servers/iran.json) puts the
# servers confirmed from inside first in hot.txt and leaves the failing ones
# out; while it is fresh and confirms enough servers, hot.txt holds only
# confirmed servers, and this machine publishes hot.txt itself as well, so
# the apps get it at once.
#
#   curl -fsSL https://accounts.example.com/list/tools/tester-install.sh -o install.sh
#   sudo ACCOUNT_URL=https://accounts.example.com ADMIN_USER=... ADMIN_PASSWORD=... bash install.sh
#
# Needs Ubuntu 22.04/24.04 or Debian 12, x86-64 or ARM64, with internet
# access as an ordinary user here has (that is the point). Optional:
#   EVERY=2h        how often to test (systemd OnUnitActiveSec form)
#   REAL=1500       how many servers to connect through per run
#   CORES_URL=...   where to get the cores' archives if GitHub is not reachable
set -euo pipefail
: "${ACCOUNT_URL:?set ACCOUNT_URL to the account service, e.g. https://accounts.example.com}"
: "${ADMIN_USER:?set ADMIN_USER}"
: "${ADMIN_PASSWORD:?set ADMIN_PASSWORD}"
EVERY="${EVERY:-2h}"
REAL="${REAL:-1500}"
SINGBOX_VERSION="${SINGBOX_VERSION:-1.14.4}"
XRAY_VERSION="${XRAY_VERSION:-26.10.21}"
dir=/opt/albatross-tester
arch="$(uname -m)"
case "$arch" in
  x86_64) goarch=amd64; sbarch=amd64; xarch=64 ;;
  aarch64|arm64) goarch=arm64; sbarch=arm64; xarch=arm64-v8a ;;
  *) echo "unsupported machine: $arch" >&2; exit 1 ;;
esac

apt-get update -q
apt-get install -y -q curl jq unzip ca-certificates > /dev/null
mkdir -p "$dir/cores" "$dir/work"
cd "$dir"

# The collector, built by CI and kept in the project's own storage.
curl -fsSL --retry 4 -o collect "$ACCOUNT_URL/list/tools/collect-linux-$goarch"
chmod +x collect
./collect -h > /dev/null 2>&1 || { echo "the collector does not run here" >&2; exit 1; }

# The cores it connects with.
gh="${CORES_URL:-https://github.com}"
curl -fsSL --retry 4 -o sb.tgz "$gh/SagerNet/sing-box/releases/download/v${SINGBOX_VERSION}/sing-box-${SINGBOX_VERSION}-linux-${sbarch}.tar.gz"
tar xzf sb.tgz --strip-components=1 -C cores && rm sb.tgz
curl -fsSL --retry 4 -o x.zip "$gh/XTLS/Xray-core/releases/download/v${XRAY_VERSION}/Xray-linux-${xarch}.zip"
unzip -oq x.zip xray -d cores && rm x.zip
chmod +x cores/sing-box cores/xray

cat > env <<ENV
ACCOUNT_URL=$ACCOUNT_URL
ADMIN_USER=$ADMIN_USER
ADMIN_PASSWORD=$ADMIN_PASSWORD
REAL=$REAL
ENV
chmod 600 env

# One run (deploy/tester/run.sh in the repository).
cat > run.sh <<'RUN'
#!/usr/bin/env bash
# One run of the tester inside the country (install.sh sets it up):
# fetches the candidates (the current hot.txt and the top of fresh.txt),
# connects through them for real from here, twice, and publishes the report
# (servers/iran.json) and, when enough servers were confirmed, hot.txt.
set -euo pipefail
: "${ACCOUNT_URL:?}" "${ADMIN_USER:?}" "${ADMIN_PASSWORD:?}"
cd "$(dirname "$0")"
account="${ACCOUNT_URL%/}"
work=work
rm -rf "$work" && mkdir -p "$work"
# The candidates: whatever the collector abroad found, hot first.
{ curl -fsSL --retry 3 "$account/list/servers/hot.txt"; curl -fsSL --retry 3 "$account/list/servers/fresh.txt" | head -n 3000; } > "$work/try.txt"
[ -s "$work/try.txt" ] || { echo "no candidates" >&2; exit 1; }
SING_BOX="$PWD/cores/sing-box" XRAY="$PWD/cores/xray" ./collect -hot-from "$work/try.txt" -real "${REAL:-1500}" -report "$work/iran.json" -tester iran -out "$work"

# Publish through the account service as its administrator.
token="$(curl -fsS --retry 3 --max-time 60 -H 'Content-Type: application/json' \
  --data "$(jq -cn --arg u "$ADMIN_USER" --arg p "$ADMIN_PASSWORD" '{username:$u,password:$p}')" \
  "$account/api/v1/admin/login" | jq -r .token)"
[ -n "$token" ] && [ "$token" != null ] || { echo "could not sign in to $account" >&2; exit 1; }
put() { # name file
  curl -fsS --retry 3 --max-time 300 -X PUT -H "Authorization: Bearer $token" -H 'Content-Type: application/octet-stream' \
    --data-binary @"$2" "$account/api/v1/admin/list/servers/$1" > /dev/null && echo "published servers/$1 ($(stat -c%s "$2") bytes)"
}
put iran.json "$work/iran.json"
# hot.txt from here only when this run confirmed enough servers; a thin run
# leaves the collector's list in place.
confirmed="$(jq '[.servers[] | select(.[0] == 2)] | length' "$work/iran.json")"
if [ -s "$work/hot.txt" ] && [ "$confirmed" -ge 100 ]; then
  put hot.txt "$work/hot.txt"
else
  echo "only $confirmed servers confirmed; hot.txt left to the collector"
fi
RUN
chmod +x run.sh

cat > /etc/systemd/system/albatross-tester.service <<UNIT
[Unit]
Description=Albatross tester inside the country
After=network-online.target
Wants=network-online.target

[Service]
Type=oneshot
WorkingDirectory=$dir
EnvironmentFile=$dir/env
ExecStart=$dir/run.sh
Nice=10
UNIT
cat > /etc/systemd/system/albatross-tester.timer <<UNIT
[Unit]
Description=Run the Albatross tester every $EVERY

[Timer]
OnBootSec=3min
OnUnitActiveSec=$EVERY
RandomizedDelaySec=10min

[Install]
WantedBy=timers.target
UNIT
systemctl daemon-reload
systemctl enable --now albatross-tester.timer
systemctl start --no-block albatross-tester.service
echo "Installed. First run started; follow it with: journalctl -fu albatross-tester"
