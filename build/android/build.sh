#!/usr/bin/env bash
# Builds the Android app: the Go engine as an Android library (gomobile),
# the cores as native libraries, and one APK per ABI in dist/.
#
# Needs Go, Java 17+, and an Android SDK with the NDK
# (ANDROID_HOME, and ANDROID_NDK_HOME or an NDK under $ANDROID_HOME/ndk).
set -euo pipefail

VERSION="${VERSION:-0.0.0-dev}"
SINGBOX_VERSION="${SINGBOX_VERSION:-1.14.3}"
XRAY_VERSION="${XRAY_VERSION:-26.10.10}"
WARPPLUS_VERSION="${WARPPLUS_VERSION:-1.2.6}"

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

fetch() {
  [ -s "$2" ] || curl -fsSL --retry 4 --retry-delay 3 -o "$2" "$1"
}
gh="https://github.com"
tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT

# Cores for each ABI. Android only lets an app run executables that ship as
# native libraries, hence the lib*.so names. warp-plus has no x86_64
# Android build; its static Linux build runs there.
cores() { # abi sing-box-arch xray-asset warp-plus-asset
  local abi="$1" out="$jni/$1"
  rm -rf "$out" && mkdir -p "$out"
  fetch "$gh/SagerNet/sing-box/releases/download/v$SINGBOX_VERSION/sing-box-$SINGBOX_VERSION-android-$2.tar.gz" "$dl/sing-box-$SINGBOX_VERSION-android-$2.tar.gz"
  mkdir -p "$tmp/sb-$abi" && tar xzf "$dl/sing-box-$SINGBOX_VERSION-android-$2.tar.gz" -C "$tmp/sb-$abi" --strip-components=1
  cp "$tmp/sb-$abi/sing-box" "$out/libsingbox.so"
  cp "$tmp/sb-$abi/LICENSE" "$app/src/main/assets/licenses/sing-box-LICENSE.txt"
  fetch "$gh/XTLS/Xray-core/releases/download/v$XRAY_VERSION/$3" "$dl/xray-$XRAY_VERSION-$3"
  unzip -q -o "$dl/xray-$XRAY_VERSION-$3" xray LICENSE -d "$tmp/x-$abi"
  cp "$tmp/x-$abi/xray" "$out/libxray.so"
  cp "$tmp/x-$abi/LICENSE" "$app/src/main/assets/licenses/xray-LICENSE.txt"
  fetch "$gh/bepass-org/warp-plus/releases/download/v$WARPPLUS_VERSION/$4" "$dl/warp-plus-$WARPPLUS_VERSION-$4"
  unzip -q -o "$dl/warp-plus-$WARPPLUS_VERSION-$4" -d "$tmp/wp-$abi"
  cp "$(find "$tmp/wp-$abi" -type f -iname 'warp-plus*' ! -iname '*.txt' | head -1)" "$out/libwarpplus.so"
  cp "$(find "$tmp/wp-$abi" -iname 'LICENSE*' | head -1)" "$app/src/main/assets/licenses/warp-plus-LICENSE.txt"
  chmod 755 "$out"/*.so
}
cores arm64-v8a arm64 Xray-android-arm64-v8a.zip warp-plus_android-arm64.zip
cores x86_64 amd64 Xray-android-amd64.zip warp-plus_linux-amd64.zip

# The engine, UI and tun2socks as one Android library.
go install golang.org/x/mobile/cmd/gomobile golang.org/x/mobile/cmd/gobind
PATH="$(go env GOPATH)/bin:$PATH" gomobile bind -target=android/arm64,android/amd64 -androidapi 24 \
  -javapkg app.skyroad -trimpath \
  -ldflags "-s -w -X github.com/cezomagoanlace29861-jpg/vp/mobile.Version=$VERSION" \
  -o "$app/libs/skyroad.aar" ./mobile

cd "$root/android"
VERSION="$VERSION" ./gradlew --no-daemon -q assembleRelease
mkdir -p "$dist"
for apk in app/build/outputs/apk/release/*.apk; do
  abi="$(basename "$apk" .apk | sed -E 's/^app-(.*)-release$/\1/')"
  cp "$apk" "$dist/Skyroad-$VERSION-android-$abi.apk"
done
ls -la "$dist"/*.apk
