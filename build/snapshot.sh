#!/usr/bin/env bash
# Bundles the collector's current server list into the app
# (internal/sources/lists/snapshot.txt.gz), so a first start under heavy
# filtering has servers to try. Keeps the old file when the list cannot be
# downloaded.
set -uo pipefail
root="$(cd "$(dirname "$0")/.." && pwd)"
out="$root/internal/sources/lists/snapshot.txt.gz"
url="https://raw.githubusercontent.com/cezomagoanlace29861-jpg/vp/servers/fresh.txt"
tmp="$(mktemp)"
if curl -fsSL --retry 3 --retry-delay 3 --max-time 120 "$url" -o "$tmp" && [ "$(wc -l <"$tmp")" -gt 100 ]; then
  gzip -9 -c "$tmp" >"$out"
  echo "bundled $(wc -l <"$tmp") servers"
else
  echo "no server list to bundle; keeping $(basename "$out")"
fi
rm -f "$tmp"
