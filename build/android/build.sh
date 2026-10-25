#!/usr/bin/env bash
# Builds the Android app: the Go engine as an Android library (gomobile),
# the cores as native libraries, and one APK for every phone in dist/
# (64 and 32-bit ARM, Android 6 and up; ABIS picks others).
#
# Needs Go, Java 17+, and an Android SDK with the NDK
# (ANDROID_HOME, and ANDROID_NDK_HOME or an NDK under $ANDROID_HOME/ndk).
set -euo pipefail

VERSION="${VERSION:-0.0.0-dev}"
SINGBOX_VERSION="${SINGBOX_VERSION:-1.14.4}"
XRAY_VERSION="${XRAY_VERSION:-26.10.21}"
WARPPLUS_VERSION="${WARPPLUS_VERSION:-1.2.6}"
USQUE_VERSION="${USQUE_VERSION:-4.2.1}"

root="$(cd "$(dirname "$0")/../.." && pwd)"
cd "$root"
dist="$root/dist"
dl="$dist/downloads"
app="$root/android/app"
jni="$app/src/main/jniLibs"
mkdir -p "$dl" "$app/libs" "$app/src/main/assets/licenses"

: "${ANDROID_HOME:?set ANDROID_HOME to the Android SDK}"
if [ -z "${ANDROID_NDK_HOME:-}" ]; then
  ANDROID_NDK_HOME="$(ls -d "$ANDROID_HOME"/ndk/* 2>/dev/null | sort -V | tail -1)"
  export ANDROID_NDK_HOME
fi
[ -d "$ANDROID_NDK_HOME" ] || { echo "no Android NDK found" >&2; exit 1; }

bash "$root/build/snapshot.sh"

fetch() {
  [ -s "$2" ] || curl -fsSL --retry 4 --retry-delay 3 -o "$2" "$1"
}
gh="https://github.com"
tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT

# The lowest Android version the app runs on (minSdk in build.gradle.kts).
api=23
ndkbin="$(ls -d "$ANDROID_NDK_HOME"/toolchains/llvm/prebuilt/*/bin | head -1)"

# gobuild <abi> <out> <go build args...> builds a Go program for one ABI
# with the NDK at $api, for cores that publish no Android build for it (a
# Linux build runs there but cannot find the phone's DNS servers).
gobuild() {
  local abi="$1" out="$2" goarch cc
  shift 2
  case "$abi" in
    arm64-v8a) goarch=arm64 cc=aarch64-linux-android ;;
    armeabi-v7a) goarch=arm cc=armv7a-linux-androideabi ;;
    x86_64) goarch=amd64 cc=x86_64-linux-android ;;
  esac
  # Phones from 2025 on use 16 KB memory pages; a library whose segments
  # are aligned to 4 KB does not load there (and Android 16 refuses it).
  GOOS=android GOARCH="$goarch" GOARM=7 CGO_ENABLED=1 CC="$ndkbin/$cc$api-clang" GOTOOLCHAIN="${toolchain:-auto}" \
    CGO_LDFLAGS="-Wl,-z,max-page-size=16384 -Wl,-z,common-page-size=16384" \
    go build -trimpath -buildvcs=false -o "$out" "$@"
}

# check_alignment <lib...> fails the build when a library's loadable
# segments are not aligned to 16 KB.
check_alignment() {
  python3 - "$@" <<'PY'
import struct, sys
bad = []
for path in sys.argv[1:]:
    d = open(path, "rb").read()
    if d[:4] != b"\x7fELF":
        continue
    is64 = d[4] == 2
    if is64:
        phoff = struct.unpack_from("<Q", d, 0x20)[0]; phentsize, phnum = struct.unpack_from("<HH", d, 0x36)
    else:
        phoff = struct.unpack_from("<I", d, 0x1c)[0]; phentsize, phnum = struct.unpack_from("<HH", d, 0x2a)
    for k in range(phnum):
        off = phoff + k * phentsize
        ptype = struct.unpack_from("<I", d, off)[0]
        align = struct.unpack_from("<Q", d, off + 0x30)[0] if is64 else struct.unpack_from("<I", d, off + 0x1c)[0]
        if ptype == 1 and align < 16384:
            bad.append("%s (%d)" % (path, align))
            break
if bad:
    print("libraries not aligned to 16 KB pages: " + ", ".join(bad), file=sys.stderr)
    sys.exit(1)
PY
}

# smoke <name> <binary> <args...> runs a core built for this machine from
# the same source and Go as the phone's, and fails the build if it crashes
# at start (a dependency that checks Go's internals panics before main).
smoke() {
  local name="$1" out
  shift
  out="$("$@" 2>&1 || true)"
  if [ -z "$out" ] || printf '%s' "$out" | grep -qE 'panic:|fatal error:'; then
    echo "$name crashes at start:" >&2
    printf '%s\n' "$out" | head -20 >&2
    exit 1
  fi
}

# Xray publishes no 32-bit ARM Android build: build that one from its source.
xraysrc() {
  local src="$tmp/xray-src"
  if [ ! -d "$src" ]; then
    git clone -q --depth 1 --branch "v$XRAY_VERSION" "$gh/XTLS/Xray-core" "$src"
    (cd "$src" && CGO_ENABLED=0 GOTOOLCHAIN=auto go build -trimpath -buildvcs=false -ldflags "-checklinkname=0" -o "$tmp/xray-host" ./main)
    smoke Xray "$tmp/xray-host" version
  fi
  echo "$src"
}

# warp-plus publishes Android builds for 64-bit ARM only: build the others
# from its source with the Go its go.mod names. With a newer Go, Psiphon's
# TLS library in it panics at start.
wpsrc() {
  local src="$tmp/wp-src"
  if [ ! -d "$src" ]; then
    git clone -q --depth 1 --branch "v$WARPPLUS_VERSION" "$gh/bepass-org/warp-plus" "$src"
    (cd "$src" && CGO_ENABLED=0 GOTOOLCHAIN="$(wptoolchain "$src")" go build -trimpath -buildvcs=false -o "$tmp/warp-plus-host" ./cmd/warp-plus)
    smoke warp-plus "$tmp/warp-plus-host" --help
  fi
  echo "$src"
}
wptoolchain() { sed -n 's/^toolchain //p' "$1/go.mod" | grep . || echo auto; }

# usque (WARP over MASQUE) has an Android build for 64-bit ARM only; every
# ABI gets one from its source.
usqsrc() {
  local src="$tmp/usque-src"
  if [ ! -d "$src" ]; then
    git clone -q --depth 1 --branch "v$USQUE_VERSION" "$gh/Diniboy1123/usque" "$src"
    (cd "$src" && CGO_ENABLED=0 GOTOOLCHAIN=auto go build -trimpath -buildvcs=false -o "$tmp/usque-host" .)
    smoke usque "$tmp/usque-host" --help
  fi
  echo "$src"
}

# sing-box's own Android builds are linked for 4 KB memory pages, which
# phones with 16 KB pages refuse to run: every ABI is built from its source
# with the tags and flags of its release builds (release/ in its tree).
sbsrc() {
  local src="$tmp/sing-box-src"
  if [ ! -d "$src" ]; then
    git clone -q --depth 1 --branch "v$SINGBOX_VERSION" "$gh/SagerNet/sing-box" "$src"
    (cd "$src" && CGO_ENABLED=0 GOTOOLCHAIN=auto go build -trimpath -buildvcs=false -tags "$(cat release/DEFAULT_BUILD_TAGS_OTHERS)" \
      -ldflags "$(cat release/LDFLAGS) -s -w" -o "$tmp/sing-box-host" ./cmd/sing-box)
    smoke sing-box "$tmp/sing-box-host" version
  fi
  echo "$src"
}

# Cores for each ABI. Android only lets an app run executables that ship as
# native libraries, hence the lib*.so names. An empty asset name builds that
# core from source.
cores() { # abi sing-box-arch xray-asset warp-plus-asset
  local abi="$1" out="$jni/$1"
  rm -rf "$out" && mkdir -p "$out"
  local sb
  sb="$(sbsrc)"
  (cd "$sb" && gobuild "$abi" "$out/libsingbox.so" -tags "$(cat release/DEFAULT_BUILD_TAGS_OTHERS)" \
    -ldflags "-X 'github.com/sagernet/sing-box/constant.Version=$SINGBOX_VERSION' $(cat release/LDFLAGS) -s -w -buildid=" ./cmd/sing-box)
  cp "$sb/LICENSE" "$app/src/main/assets/licenses/sing-box-LICENSE.txt"
  if [ -n "$3" ]; then
    fetch "$gh/XTLS/Xray-core/releases/download/v$XRAY_VERSION/$3" "$dl/xray-$XRAY_VERSION-$3"
    unzip -q -o "$dl/xray-$XRAY_VERSION-$3" xray LICENSE -d "$tmp/x-$abi"
    cp "$tmp/x-$abi/xray" "$out/libxray.so"
    cp "$tmp/x-$abi/LICENSE" "$app/src/main/assets/licenses/xray-LICENSE.txt"
  else
    local src
    src="$(xraysrc)"
    # As Xray's own release builds: a dependency links into net's internals.
    (cd "$src" && gobuild "$abi" "$out/libxray.so" \
      -ldflags "-X github.com/xtls/xray-core/core.build=$(git rev-parse --short HEAD) -s -w -buildid= -checklinkname=0" ./main)
  fi
  if [ -n "$4" ]; then
    fetch "$gh/bepass-org/warp-plus/releases/download/v$WARPPLUS_VERSION/$4" "$dl/warp-plus-$WARPPLUS_VERSION-$4"
    unzip -q -o "$dl/warp-plus-$WARPPLUS_VERSION-$4" -d "$tmp/wp-$abi"
    cp "$(find "$tmp/wp-$abi" -type f -iname 'warp-plus*' ! -iname '*.txt' | head -1)" "$out/libwarpplus.so"
    cp "$(find "$tmp/wp-$abi" -iname 'LICENSE*' | head -1)" "$app/src/main/assets/licenses/warp-plus-LICENSE.txt"
  else
    local wp
    wp="$(wpsrc)"
    (cd "$wp" && toolchain="$(wptoolchain "$wp")" gobuild "$abi" "$out/libwarpplus.so" -ldflags "-s -w" ./cmd/warp-plus)
    cp "$wp/LICENSE" "$app/src/main/assets/licenses/warp-plus-LICENSE.txt"
  fi
  local us
  us="$(usqsrc)"
  (cd "$us" && gobuild "$abi" "$out/libusque.so" -ldflags "-s -w" .)
  cp "$us/LICENSE.md" "$app/src/main/assets/licenses/usque-LICENSE.txt"
  chmod 755 "$out"/*.so
}
cores arm64-v8a arm64 Xray-android-arm64-v8a.zip warp-plus_android-arm64.zip
cores armeabi-v7a arm "" ""
cores x86_64 amd64 Xray-android-amd64.zip ""

# Tor, the last resort. Its Android bundle holds executables (libTor.so is
# one, despite the name) that need only the system's libraries. They ship
# as lib<name>.so like the cores; pt_config.json cannot ship beside them,
# so it is built into the engine (internal/core/tor.go).
readelf="$(ls "$ANDROID_NDK_HOME"/toolchains/llvm/prebuilt/*/bin/llvm-readelf | head -1)"
tor() { # abi bundle-platform
  local out="$jni/$1" t="$tmp/tor-$1"
  bash "$root/build/tor.sh" "$2" "$t" "$dl"
  cp "$t/tor/libTor.so" "$out/libtor.so"
  for f in "$t"/tor/pluggable_transports/*; do
    [ -x "$f" ] && [ -f "$f" ] || continue
    cp "$f" "$out/lib$(basename "$f" | tr -d -).so"
  done
  cp "$t/tor/pluggable_transports/pt_config.json" "$root/internal/core/torpt/pt_config.json"
  chmod 755 "$out"/*.so
  # Anything but the system's own libraries would be missing on the phone.
  for so in "$out"/libtor.so "$out"/liblyrebird.so; do
    bad="$("$readelf" -d "$so" | sed -n 's/.*Shared library: \[\(.*\)\]/\1/p' | grep -vxE 'lib(c|m|dl|log|z)\.so' || true)"
    [ -z "$bad" ] || { echo "$so needs libraries Android does not have: $bad" >&2; exit 1; }
  done
}
tor arm64-v8a android-aarch64
tor armeabi-v7a android-armv7
tor x86_64 android-x86_64
mkdir -p "$app/src/main/assets/licenses"
printf 'Tor and its pluggable transports are from the Tor Project (https://www.torproject.org), under the 3-clause BSD license.\n' > "$app/src/main/assets/licenses/tor-LICENSE.txt"

# Today's root certificates, for phones whose own store lacks them
# (internal/roots), from this machine's ca-certificates package, which the
# OS keeps current. Only Mozilla's: never a CA this machine alone trusts,
# such as a company proxy's. Without them the committed copy stays.
moz=/usr/share/ca-certificates/mozilla
n="$(cat "$moz"/*.crt 2>/dev/null | grep -c 'BEGIN CERTIFICATE' || true)"
if [ "$n" -gt 100 ]; then
  { printf "# Mozilla's root certificates (MPL 2.0) from Debian/Ubuntu's ca-certificates.\n# build/android/build.sh refreshes this file.\n"; awk 1 "$moz"/*.crt; } > "$root/internal/roots/roots.pem"
  echo "bundled $n root certificates"
fi
printf "The engine's root certificates are Mozilla's (https://wiki.mozilla.org/CA), under the Mozilla Public License 2.0.\n" > "$app/src/main/assets/licenses/mozilla-roots-LICENSE.txt"

# The engine, UI and tun2socks as one Android library.
go install golang.org/x/mobile/cmd/gomobile golang.org/x/mobile/cmd/gobind
PATH="$(go env GOPATH)/bin:$PATH" gomobile bind -target=android/arm64,android/arm,android/amd64 -androidapi "$api" \
  -javapkg app.albatross -trimpath \
  -ldflags "-s -w -extldflags=-Wl,-z,max-page-size=16384 -X github.com/Albatrossvpn/vp/mobile.Version=$VERSION -X github.com/Albatrossvpn/vp/internal/engine.AccountURL=${ACCOUNT_URL:-} -X github.com/Albatrossvpn/vp/internal/sources.OwnKey=${OWN_KEY:-} -X github.com/Albatrossvpn/vp/internal/sources.WorkerMirror=${MIRROR_URL:-${ACCOUNT_URL:-}} -X github.com/Albatrossvpn/vp/internal/engine.UpdateKey=${UPDATE_KEY:-} -X github.com/Albatrossvpn/vp/internal/sources.DomesticMirror=${DOMESTIC_MIRROR:-}" \
  -o "$app/libs/albatross.aar" ./mobile

# The engine's library comes out of the archive for the check; the cores
# are already in place.
mkdir -p "$tmp/aar" && (cd "$tmp/aar" && unzip -oq "$app/libs/albatross.aar" 'jni/*')
check_alignment "$jni"/*/*.so "$tmp"/aar/jni/*/*.so

cd "$root/android"
VERSION="$VERSION" ./gradlew --no-daemon -q assembleRelease
apk="$dist/Albatross-$VERSION-android.apk"
cp app/build/outputs/apk/release/app-release.apk "$apk"

# Every library in the APK must run on the oldest phone: built for its ABI,
# needing only the system's libraries, and calling only what Android $api's
# libc has (the NDK's stubs for that version list it).
x="$tmp/apk" && rm -rf "$x" && mkdir -p "$x" && unzip -q "$apk" 'lib/*' -d "$x"
sysroot="$(dirname "$ndkbin")/sysroot/usr/lib"
for dir in "$x"/lib/*; do
  abi="$(basename "$dir")"
  case "$abi" in
    arm64-v8a) machine=AArch64 triple=aarch64-linux-android ;;
    armeabi-v7a) machine=ARM triple=arm-linux-androideabi ;;
    x86_64) machine=X86-64 triple=x86_64-linux-android ;;
    *) echo "unexpected ABI $abi in the APK" >&2; exit 1 ;;
  esac
  have="$tmp/have-$abi"
  "$ndkbin/llvm-nm" -D --defined-only "$sysroot/$triple/$api"/lib{c,dl,log,m,z,android,EGL,GLESv2,jnigraphics}.so |
    awk 'NF > 1 {sub(/@.*/, "", $NF); print $NF}' | sort -u > "$have"
  for so in "$dir"/*.so; do
    "$readelf" -h "$so" | grep -q "Machine:.*$machine" || { echo "$abi/$(basename "$so") is not built for $abi" >&2; exit 1; }
    bad="$("$readelf" -d "$so" | sed -n 's/.*Shared library: \[\(.*\)\]/\1/p' | grep -vxE 'lib(c|m|dl|log|z|android|EGL|GLESv2|jnigraphics)\.so' || true)"
    [ -z "$bad" ] || { echo "$abi/$(basename "$so") needs libraries Android does not have: $bad" >&2; exit 1; }
    new="$({ "$ndkbin/llvm-nm" -D --undefined-only "$so" 2> /dev/null || true; } | awk '$1 == "U" {sub(/@.*/, "", $2); print $2}' | sort -u | comm -23 - "$have")"
    [ -z "$new" ] || { echo "$abi/$(basename "$so") calls what Android $api lacks: $new" >&2; exit 1; }
  done
  echo "$abi: $(ls "$dir" | tr '\n' ' ')"
done
ls -la "$apk"
