#!/usr/bin/env bash
# Builds dist/Albatross (the app plus every core it runs) and a portable zip.
# The Windows installer is made from dist/Albatross by build/windows/installer.iss.
set -euo pipefail

VERSION="${VERSION:-0.0.0-dev}"
SINGBOX_VERSION="${SINGBOX_VERSION:-1.14.3}"
XRAY_VERSION="${XRAY_VERSION:-26.9.30}"
WARPPLUS_VERSION="${WARPPLUS_VERSION:-1.2.6}"

root="$(cd "$(dirname "$0")/.." && pwd)"
cd "$root"
dist="$root/dist"
out="$dist/Albatross"
dl="$dist/downloads"
rm -rf "$out"
mkdir -p "$out/core" "$out/licenses" "$dl"

bash "$root/build/snapshot.sh"

# Windows resources (icon, manifest, version info) need a numeric version.
numeric="$(echo "$VERSION" | sed -E 's/^([0-9]+)\.([0-9]+)\.([0-9]+).*/\1.\2.\3.0/')"
[[ "$numeric" =~ ^[0-9]+\.[0-9]+\.[0-9]+\.0$ ]] || numeric="0.0.0.0"
go run github.com/tc-hib/go-winres@v0.3.3 make --in build/windows/winres.json --out cmd/albatross/rsrc \
  --arch amd64 --product-version "$numeric" --file-version "$numeric"
GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build -trimpath \
  -ldflags "-s -w -H windowsgui -X main.version=$VERSION -X github.com/cezomagoanlace29861-jpg/vp/internal/engine.AccountURL=${ACCOUNT_URL:-} -X github.com/cezomagoanlace29861-jpg/vp/internal/sources.OwnKey=${OWN_KEY:-} -X github.com/cezomagoanlace29861-jpg/vp/internal/sources.WorkerMirror=${ACCOUNT_URL:-}" -o "$out/Albatross.exe" ./cmd/albatross

fetch() {
  [ -s "$2" ] || curl -fsSL --retry 4 --retry-delay 3 -o "$2" "$1"
}
fetch "https://github.com/SagerNet/sing-box/releases/download/v$SINGBOX_VERSION/sing-box-$SINGBOX_VERSION-windows-amd64.zip" "$dl/sing-box-$SINGBOX_VERSION.zip"
fetch "https://github.com/XTLS/Xray-core/releases/download/v$XRAY_VERSION/Xray-windows-64.zip" "$dl/xray-$XRAY_VERSION.zip"
fetch "https://github.com/bepass-org/warp-plus/releases/download/v$WARPPLUS_VERSION/warp-plus_windows-amd64.zip" "$dl/warp-plus-$WARPPLUS_VERSION.zip"

tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT
unzip -q -o "$dl/sing-box-$SINGBOX_VERSION.zip" -d "$tmp/sb"
cp "$tmp"/sb/*/sing-box.exe "$out/core/"
cp "$tmp"/sb/*/libcronet.dll "$out/core/" 2>/dev/null || true
cp "$tmp"/sb/*/LICENSE "$out/licenses/sing-box-LICENSE.txt"
unzip -q -o "$dl/xray-$XRAY_VERSION.zip" -d "$tmp/xray"
cp "$tmp/xray/xray.exe" "$out/core/"
cp "$tmp/xray/LICENSE" "$out/licenses/xray-LICENSE.txt"
unzip -q -o "$dl/warp-plus-$WARPPLUS_VERSION.zip" -d "$tmp/wp"
cp "$(find "$tmp/wp" -iname 'warp-plus*.exe' | head -1)" "$out/core/warp-plus.exe"
cp "$(find "$tmp/wp" -iname 'LICENSE*' | head -1)" "$out/licenses/warp-plus-LICENSE.txt"
bash "$root/build/tor.sh" windows-x86_64 "$out/core" "$dl"
# The Snowflake proxy for "Help others" is its own module: its library
# needs a newer Go than the app.
(cd "$root/tools/snowflake" && GOTOOLCHAIN="${SNOWFLAKE_GO:-go1.26.10}" GOOS=windows GOARCH=amd64 CGO_ENABLED=0 \
  go build -trimpath -ldflags "-s -w -H windowsgui" -o "$out/core/snowflake-helper.exe" .)

cat > "$out/README.txt" <<TXT
Albatross VPN $VERSION

Run Albatross.exe, pick a location and press the button.

Bundled components (see the licenses folder):
  sing-box $SINGBOX_VERSION  https://github.com/SagerNet/sing-box  (GPL-3.0-or-later; source at that address)
  Xray-core $XRAY_VERSION    https://github.com/XTLS/Xray-core    (MPL-2.0)
  warp-plus $WARPPLUS_VERSION   https://github.com/bepass-org/warp-plus (MIT)
  Tor Expert Bundle         https://www.torproject.org (BSD-3-Clause; transports under their own licenses)
  Snowflake proxy           https://gitlab.torproject.org/tpo/anti-censorship/pluggable-transports/snowflake (BSD-3-Clause)
  Twemoji country flags     https://github.com/talkjs/country-flag-emoji-polyfill (CC-BY 4.0 graphics by Twitter)
  Site rule sets            https://github.com/SagerNet/sing-geoip, https://github.com/SagerNet/sing-geosite (GPL-3.0-or-later),
                            https://github.com/Chocolate4U/Iran-sing-box-rules
  Ad list (updated daily)   HaGeZi, OISD, AdGuard DNS filter (GPL-3.0), PersianBlocker (AGPL-3.0);
                            https://github.com/cezomagoanlace29861-jpg/Albatross/tree/rules
TXT
sed -i 's/$/\r/' "$out/README.txt"

zip_name="Albatross-$VERSION-windows-amd64-portable.zip"
rm -f "$dist/$zip_name"
(cd "$dist" && zip -q -r -9 "$zip_name" Albatross)
echo "built $dist/$zip_name"
ls -la "$out" "$out/core"
