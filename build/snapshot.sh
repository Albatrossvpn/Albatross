#!/usr/bin/env bash
# Bundles the collector's current server list into the app
# (internal/sources/lists/snapshot.txt.gz), so a first start under heavy
# filtering has servers to try. Keeps the old file when the list cannot be
# downloaded.
set -uo pipefail
root="$(cd "$(dirname "$0")/.." && pwd)"
out="$root/internal/sources/lists/snapshot.txt.gz"
# The project's own storage first (the account service's /list/), then
# GitHub while the repository is public.
own="$(bash "$root/build/account-url.sh" 2>/dev/null | cut -d, -f1)"
urls=()
[ -n "$own" ] && urls+=("$own/list/servers/fresh.txt")
urls+=("https://raw.githubusercontent.com/Albatrossvpn/Albatross/servers/fresh.txt")
tmp="$(mktemp)"
got=""
for url in "${urls[@]}"; do
  if curl -fsSL --retry 3 --retry-delay 3 --max-time 120 "$url" -o "$tmp" && [ "$(wc -l <"$tmp")" -gt 100 ]; then got=1; break; fi
done
if [ -n "$got" ]; then
  gzip -9 -c "$tmp" >"$out"
  echo "bundled $(wc -l <"$tmp") servers"
else
  echo "no server list to bundle; keeping $(basename "$out")"
fi
rm -f "$tmp"

# The relay list (own-0.bin, sealed), so a fresh install during a shutdown
# knows the relays inside the country. Kept as it was when unreachable.
ownout="$root/internal/sources/lists/own-0.bin"
for url in "${urls[@]/fresh.txt/own-0.bin}"; do
  if curl -fsSL --retry 3 --retry-delay 3 --max-time 60 "$url" -o "$tmp.own" && [ -s "$tmp.own" ]; then
    mv "$tmp.own" "$ownout"
    echo "bundled the relay list ($(stat -c%s "$ownout") bytes)"
    break
  fi
done
rm -f "$tmp.own"
