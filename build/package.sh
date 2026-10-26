#!/usr/bin/env bash
# Builds dist/Albatross (the app plus every core it runs) and a portable zip.
# The Windows installer is made from dist/Albatross by build/windows/installer.iss.
set -euo pipefail

VERSION="${VERSION:-0.0.0-dev}"
SINGBOX_VERSION="${SINGBOX_VERSION:-1.14.4}"
XRAY_VERSION="${XRAY_VERSION:-26.10.21}"
WARPPLUS_VERSION="${WARPPLUS_VERSION:-1.2.6}"
USQUE_VERSION="${USQUE_VERSION:-4.2.1}"
# The sidecar protocols' helpers (internal/node/sidecar.go).
NAIVE_VERSION="${NAIVE_VERSION:-v154.0.8037.49-5}"
JUICITY_VERSION="${JUICITY_VERSION:-0.5.0}"
MIERU_VERSION="${MIERU_VERSION:-3.39.0}"
BROOK_VERSION="${BROOK_VERSION:-20270101}"
WSTUNNEL_VERSION="${WSTUNNEL_VERSION:-11.1.0}"
WIREPROXY_AWG_REF="${WIREPROXY_AWG_REF:-master}"
# The TUN driver sing-box loads on Windows for the whole-device mode; it
# must sit next to sing-box.exe. Prebuilt by WireGuard, checked by hash.
WINTUN_VERSION="${WINTUN_VERSION:-0.14.1}"
WINTUN_SHA256="07c256185d6ee3652e09fa55c0b673e2624b565e02c4b9091c79ca7d2f24ef51"

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
  -ldflags "-s -w -H windowsgui -X main.version=$VERSION -X github.com/Albatrossvpn/vp/internal/engine.AccountURL=${ACCOUNT_URL:-} -X github.com/Albatrossvpn/vp/internal/sources.OwnKey=${OWN_KEY:-} -X github.com/Albatrossvpn/vp/internal/sources.WorkerMirror=${MIRROR_URL:-${ACCOUNT_URL:-}} -X github.com/Albatrossvpn/vp/internal/engine.UpdateKey=${UPDATE_KEY:-} -X github.com/Albatrossvpn/vp/internal/sources.DomesticMirror=${DOMESTIC_MIRROR:-}" -o "$out/Albatross.exe" ./cmd/albatross

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
# Without wintun.dll beside it, sing-box cannot make the tunnel of the
# whole-device mode (run as administrator), and that mode fails outright.
fetch "https://www.wintun.net/builds/wintun-$WINTUN_VERSION.zip" "$dl/wintun-$WINTUN_VERSION.zip"
echo "$WINTUN_SHA256  $dl/wintun-$WINTUN_VERSION.zip" | sha256sum -c - >/dev/null
unzip -q -o "$dl/wintun-$WINTUN_VERSION.zip" -d "$tmp/wintun"
cp "$tmp/wintun/wintun/bin/amd64/wintun.dll" "$out/core/"
cp "$tmp/wintun/wintun/LICENSE.txt" "$out/licenses/wintun-LICENSE.txt"
unzip -q -o "$dl/xray-$XRAY_VERSION.zip" -d "$tmp/xray"
cp "$tmp/xray/xray.exe" "$out/core/"
cp "$tmp/xray/LICENSE" "$out/licenses/xray-LICENSE.txt"
unzip -q -o "$dl/warp-plus-$WARPPLUS_VERSION.zip" -d "$tmp/wp"
cp "$(find "$tmp/wp" -iname 'warp-plus*.exe' | head -1)" "$out/core/warp-plus.exe"
cp "$(find "$tmp/wp" -iname 'LICENSE*' | head -1)" "$out/licenses/warp-plus-LICENSE.txt"
# usque (WARP over MASQUE) publishes no checksummed Windows build: built
# from its source with this Go.
git clone -q --depth 1 --branch "v$USQUE_VERSION" https://github.com/Diniboy1123/usque "$tmp/usque"
(cd "$tmp/usque" && GOTOOLCHAIN=auto GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build -trimpath -buildvcs=false -ldflags "-s -w" -o "$out/core/usque.exe" .)
cp "$tmp/usque/LICENSE.md" "$out/licenses/usque-LICENSE.txt"
bash "$root/build/tor.sh" windows-x86_64 "$out/core" "$dl"
# The sidecar helpers: one program per protocol, each a SOCKS5 proxy the
# engine chains through sing-box (internal/core/build.go startSidecar).
fetch "https://github.com/klzgrad/naiveproxy/releases/download/$NAIVE_VERSION/naiveproxy-$NAIVE_VERSION-win-x64.zip" "$dl/naive-$NAIVE_VERSION.zip"
unzip -q -o "$dl/naive-$NAIVE_VERSION.zip" -d "$tmp/naive"
cp "$(find "$tmp/naive" -iname 'naive.exe' | head -1)" "$out/core/naive.exe"
cp "$(find "$tmp/naive" -iname 'LICENSE*' | head -1)" "$out/licenses/naiveproxy-LICENSE.txt"
fetch "https://github.com/juicity/juicity/releases/download/v$JUICITY_VERSION/juicity-windows-x86_64.zip" "$dl/juicity-$JUICITY_VERSION.zip"
unzip -q -o "$dl/juicity-$JUICITY_VERSION.zip" -d "$tmp/juicity"
cp "$(find "$tmp/juicity" -iname 'juicity-client.exe' | head -1)" "$out/core/juicity-client.exe"
cp "$(find "$tmp/juicity" -iname 'LICENSE*' | head -1)" "$out/licenses/juicity-LICENSE.txt" 2>/dev/null ||
  printf 'Juicity (https://github.com/juicity/juicity) is under the GNU AGPL v3; its source is at that address.\n' > "$out/licenses/juicity-LICENSE.txt"
fetch "https://github.com/enfein/mieru/releases/download/v$MIERU_VERSION/mieru_${MIERU_VERSION}_windows_amd64.zip" "$dl/mieru-$MIERU_VERSION.zip"
unzip -q -o "$dl/mieru-$MIERU_VERSION.zip" -d "$tmp/mieru"
cp "$(find "$tmp/mieru" -iname 'mieru.exe' | head -1)" "$out/core/mieru.exe"
printf 'Mieru (https://github.com/enfein/mieru) is under the GNU GPL v3; its source is at that address.\n' > "$out/licenses/mieru-LICENSE.txt"
fetch "https://github.com/txthinking/brook/releases/download/v$BROOK_VERSION/brook_windows_amd64.exe" "$dl/brook-$BROOK_VERSION.exe"
cp "$dl/brook-$BROOK_VERSION.exe" "$out/core/brook.exe"
printf 'Brook (https://github.com/txthinking/brook) is under the GNU GPL v3; its source is at that address.\n' > "$out/licenses/brook-LICENSE.txt"
fetch "https://github.com/erebe/wstunnel/releases/download/v$WSTUNNEL_VERSION/wstunnel_${WSTUNNEL_VERSION}_windows_amd64.tar.gz" "$dl/wstunnel-$WSTUNNEL_VERSION.tgz"
mkdir -p "$tmp/wst" && tar -xzf "$dl/wstunnel-$WSTUNNEL_VERSION.tgz" -C "$tmp/wst"
cp "$(find "$tmp/wst" -iname 'wstunnel.exe' | head -1)" "$out/core/wstunnel.exe"
printf 'WStunnel (https://github.com/erebe/wstunnel) is under the BSD 3-Clause license.\n' > "$out/licenses/wstunnel-LICENSE.txt"
# AmneziaWG as a SOCKS5 proxy: wireproxy's AmneziaWG fork, built from its
# source (it publishes no binaries).
git clone -q --depth 1 --branch "$WIREPROXY_AWG_REF" https://github.com/artem-russkikh/wireproxy-awg "$tmp/wpawg"
(cd "$tmp/wpawg" && GOTOOLCHAIN=auto GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build -trimpath -buildvcs=false -ldflags "-s -w" -o "$out/core/wireproxy-awg.exe" .)
cp "$tmp/wpawg/LICENSE" "$out/licenses/wireproxy-awg-LICENSE.txt"
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
  usque $USQUE_VERSION          https://github.com/Diniboy1123/usque (MIT)
  Wintun $WINTUN_VERSION           https://www.wintun.net (prebuilt binaries license; the TUN driver of the whole-device mode)
  NaiveProxy $NAIVE_VERSION  https://github.com/klzgrad/naiveproxy (BSD-3-Clause)
  Juicity $JUICITY_VERSION          https://github.com/juicity/juicity (AGPL-3.0; source at that address)
  Mieru $MIERU_VERSION           https://github.com/enfein/mieru (GPL-3.0; source at that address)
  Brook $BROOK_VERSION          https://github.com/txthinking/brook (GPL-3.0; source at that address)
  WStunnel $WSTUNNEL_VERSION         https://github.com/erebe/wstunnel (BSD-3-Clause)
  wireproxy-awg             https://github.com/artem-russkikh/wireproxy-awg (ISC; AmneziaWG as a SOCKS proxy)
  Tor Expert Bundle         https://www.torproject.org (BSD-3-Clause; transports under their own licenses)
  Snowflake proxy           https://gitlab.torproject.org/tpo/anti-censorship/pluggable-transports/snowflake (BSD-3-Clause)
  Twemoji country flags     https://github.com/talkjs/country-flag-emoji-polyfill (CC-BY 4.0 graphics by Twitter)
  Site rule sets            https://github.com/SagerNet/sing-geoip, https://github.com/SagerNet/sing-geosite (GPL-3.0-or-later),
                            https://github.com/Chocolate4U/Iran-sing-box-rules
  Ad list (updated daily)   HaGeZi, OISD, AdGuard DNS filter (GPL-3.0), PersianBlocker (AGPL-3.0);
                            https://github.com/Albatrossvpn/Albatross/tree/rules
TXT
sed -i 's/$/\r/' "$out/README.txt"

zip_name="Albatross-$VERSION-windows-amd64-portable.zip"
rm -f "$dist/$zip_name"
(cd "$dist" && zip -q -r -9 "$zip_name" Albatross)
echo "built $dist/$zip_name"
ls -la "$out" "$out/core"
