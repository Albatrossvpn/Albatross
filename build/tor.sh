#!/usr/bin/env bash
# Puts the Tor Expert Bundle (tor, its pluggable transports and
# pt_config.json with Tor's built-in bridges) into <dest>/tor and its
# GeoIP files into <dest>/data. TOR_VERSION pins a version; otherwise the
# newest stable one is used, since the Tor Project removes old ones.
#   build/tor.sh windows-x86_64|linux-x86_64|android-aarch64 <dest> [downloads]
set -euo pipefail
plat="$1"
dest="$2"
dl="${3:-$(mktemp -d)}"
base="https://dist.torproject.org/torbrowser"
ver="${TOR_VERSION:-}"
if [ -z "$ver" ]; then
  ver="$(curl -fsSL --retry 4 --retry-delay 3 "$base/" | grep -oE 'href="[0-9]+(\.[0-9]+)+/"' | grep -oE '[0-9]+(\.[0-9]+)+' | sort -V | tail -1)"
fi
[ -n "$ver" ] || { echo "no Tor version found" >&2; exit 1; }
file="tor-expert-bundle-$plat-$ver.tar.gz"
# A version the download server no longer lists is still in the Tor
# Project's archive.
if [ ! -s "$dl/$file" ]; then
  curl -fsSL --retry 4 --retry-delay 3 -o "$dl/$file" "$base/$ver/$file" || {
    rm -f "$dl/$file"
    curl -fsSL --retry 4 --retry-delay 3 -o "$dl/$file" "https://archive.torproject.org/tor-package-archive/torbrowser/$ver/$file"
  }
fi
x="$(mktemp -d)"
trap 'rm -rf "$x"' EXIT
tar -xzf "$dl/$file" -C "$x"
pt="$(find "$x" -name pt_config.json | head -1)"
[ -n "$pt" ] || { echo "no pt_config.json in $file" >&2; exit 1; }
top="$(dirname "$(dirname "$(dirname "$pt")")")" # .../tor/pluggable_transports/pt_config.json
mkdir -p "$dest"
rm -rf "$dest/tor" "$dest/data"
cp -r "$top/tor" "$dest/tor"
[ -d "$top/data" ] && cp -r "$top/data" "$dest/data"
echo "Tor Expert Bundle $ver in $dest"
