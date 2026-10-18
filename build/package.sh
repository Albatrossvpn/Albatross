#!/usr/bin/env bash
# Builds dist/Azadrah (the app plus every core it runs) and a portable zip.
# The Windows installer is made from dist/Azadrah by build/windows/installer.iss.
set -euo pipefail

VERSION="${VERSION:-0.0.0-dev}"
SINGBOX_VERSION="${SINGBOX_VERSION:-1.14.3}"
XRAY_VERSION="${XRAY_VERSION:-26.10.10}"
WARPPLUS_VERSION="${WARPPLUS_VERSION:-1.2.6}"

root="$(cd "$(dirname "$0")/.." && pwd)"
cd "$root"
dist="$root/dist"
out="$dist/Azadrah"
dl="$dist/downloads"
rm -rf "$out"
mkdir -p "$out/core" "$out/licenses" "$dl"

# Windows resources (icon, manifest, version info) need a numeric version.
numeric="$(echo "$VERSION" | sed -E 's/^([0-9]+)\.([0-9]+)\.([0-9]+).*/\1.\2.\3.0/')"
[[ "$numeric" =~ ^[0-9]+\.[0-9]+\.[0-9]+\.0$ ]] || numeric="0.0.0.0"
go run github.com/tc-hib/go-winres@v0.3.3 make --in build/windows/winres.json --out cmd/azadrah/rsrc \
  --arch amd64 --product-version "$numeric" --file-version "$numeric"
GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build -trimpath \
  -ldflags "-s -w -H windowsgui -X main.version=$VERSION" -o "$out/Azadrah.exe" ./cmd/azadrah

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
cp internal/ui/web/fonts/Vazirmatn-OFL.txt "$out/licenses/Vazirmatn-OFL.txt"

cat > "$out/README.txt" <<TXT
Azadrah VPN $VERSION

Run Azadrah.exe. Pick a country and press the button; the app finds and
tests free servers by itself and connects to the fastest one.

Bundled components (see the licenses folder):
  sing-box $SINGBOX_VERSION  https://github.com/SagerNet/sing-box  (GPL-3.0-or-later; source at that address)
  Xray-core $XRAY_VERSION    https://github.com/XTLS/Xray-core    (MPL-2.0)
  warp-plus $WARPPLUS_VERSION   https://github.com/bepass-org/warp-plus (MIT)
  Vazirmatn font            https://github.com/rastikerdar/vazirmatn (OFL-1.1)
  Twemoji country flags     https://github.com/talkjs/country-flag-emoji-polyfill (CC-BY 4.0 graphics by Twitter)
  Iran rule sets            https://github.com/Chocolate4U/Iran-sing-box-rules
TXT
sed -i 's/$/\r/' "$out/README.txt"

zip_name="Azadrah-$VERSION-windows-amd64-portable.zip"
rm -f "$dist/$zip_name"
(cd "$dist" && zip -q -r -9 "$zip_name" Azadrah)
echo "built $dist/$zip_name"
ls -la "$out" "$out/core"
