#!/usr/bin/env bash
# Bundles the collector's current server list into the app
# (internal/sources/lists/snapshot.txt.gz), so a first start under heavy
# filtering has servers to try. Keeps the old file when the list cannot be
# downloaded.
# The lists go out sealed with a key only signed-in apps get (sources.
# SealKeyed); with LIST_KEYS, a file of those keys (the project's storage,
# private/listkeys.json), the sealed copies are opened here: the bundled
# list stays plain, so it works before anyone signs in.
set -uo pipefail
root="$(cd "$(dirname "$0")/.." && pwd)"
out="$root/internal/sources/lists/snapshot.txt.gz"
# The project's own storage first (the account service's /list/), then the
# public repository the collector publishes to, then this one's old branch.
own="$(bash "$root/build/account-url.sh" 2>/dev/null | cut -d, -f1)"
urls=()
[ -n "$own" ] && urls+=("$own/list/servers/fresh.txt")
urls+=("https://raw.githubusercontent.com/Albatrossvpn/alba-data/servers/fresh.txt")
urls+=("https://raw.githubusercontent.com/Albatrossvpn/Albatross/servers/fresh.txt")
tmp="$(mktemp)"
opener=""
if [ -n "${LIST_KEYS:-}" ] && [ -s "$LIST_KEYS" ]; then
  # For this machine, whatever the build around it targets.
  (cd "$root" && CGO_ENABLED=0 GOOS='' GOARCH='' go build -o "$tmp.open" ./cmd/collect) && opener="$tmp.open"
fi
# get <url> <file>: downloads the sealed copy of a list and opens it, else
# the plain copy (published while LISTS_PLAIN is 1).
get() {
  if [ -n "$opener" ] && curl -fsSL --retry 3 --retry-delay 3 --max-time 120 "$1.sealed" -o "$2.sealed" &&
    "$opener" -keys "$LIST_KEYS" -open "$2.sealed" > "$2"; then
    rm -f "$2.sealed"
    return 0
  fi
  rm -f "$2.sealed"
  curl -fsSL --retry 3 --retry-delay 3 --max-time 120 "$1" -o "$2"
}
got=""
for url in "${urls[@]}"; do
  if get "$url" "$tmp" && [ "$(wc -l <"$tmp")" -gt 100 ]; then got=1; break; fi
done
if [ -n "$got" ]; then
  gzip -9 -c "$tmp" >"$out"
  echo "bundled $(wc -l <"$tmp") servers"
else
  echo "no server list to bundle; keeping $(basename "$out")"
fi
rm -f "$tmp"

# The relay list (own-0.bin, sealed with the build key), so a fresh
# install during a shutdown knows the relays inside the country. Kept as
# it was when unreachable.
ownout="$root/internal/sources/lists/own-0.bin"
for url in "${urls[@]/fresh.txt/own-0.bin}"; do
  if get "$url" "$tmp.own" && [ -s "$tmp.own" ]; then
    mv "$tmp.own" "$ownout"
    echo "bundled the relay list ($(stat -c%s "$ownout") bytes)"
    break
  fi
done
rm -f "$tmp.own" "$tmp.open"
