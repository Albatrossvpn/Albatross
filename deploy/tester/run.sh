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
