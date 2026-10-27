#!/usr/bin/env bash
# Stores this server's links (/root/albatross-links.txt, which install.sh
# writes) in the project's private storage: private/own-servers.txt in the
# bucket, which the Workers never serve. Every collection reads them from
# there beside the OWN_SERVERS secret (.github/workflows/collect.yml), so
# the apps get the server's links, and a reinstall's, without anyone
# copying them by hand. Run on the server through server.yml, which hands
# it the Cloudflare credentials; it prints how many links went, never the
# links. The stored copy is read back and compared.
#
#   bash publish-links.sh [links file] [key]
#
# A relay's links (install.sh RELAY_TO=...) go under private/own-relays.txt.
set -euo pipefail
: "${CLOUDFLARE_API_TOKEN:?the Cloudflare credentials come from server.yml}" "${CLOUDFLARE_ACCOUNT_ID:?}"
file="${1:-/root/albatross-links.txt}"
key="${2:-private/own-servers.txt}"
bucket="${BUCKET:-albatross-files}"
case "$key" in private/*) ;; *) echo "only under private/, which is never served: $key" >&2; exit 1 ;; esac
tmp="$(mktemp)"
trap 'rm -f "$tmp" "$tmp.back"' EXIT
grep -E '^[a-z0-9+.-]+://' "$file" >"$tmp" || true
n="$(wc -l <"$tmp")"
[ "$n" -gt 0 ] || { echo "no links in $file" >&2; exit 1; }
url="https://api.cloudflare.com/client/v4/accounts/$CLOUDFLARE_ACCOUNT_ID/r2/buckets/$bucket/objects/$key"
auth="Authorization: Bearer $CLOUDFLARE_API_TOKEN"
curl -fsS --retry 3 --max-time 60 -X PUT "$url" -H "$auth" -H "Content-Type: text/plain; charset=utf-8" --data-binary @"$tmp" >/dev/null
curl -fsS --retry 3 --max-time 60 "$url" -H "$auth" -o "$tmp.back"
cmp -s "$tmp" "$tmp.back" || { echo "the stored copy of $key does not match" >&2; exit 1; }
echo "stored $n links as $key"
