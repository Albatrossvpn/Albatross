#!/usr/bin/env bash
# Prints the account service's address for a release build: the Worker in
# backend/ on the Cloudflare account's workers.dev subdomain. It is read from
# Cloudflare at build time so the address is never written into this public
# repository.
set -euo pipefail
: "${CLOUDFLARE_API_TOKEN:?the CLOUDFLARE_API_TOKEN secret is needed}"
: "${CLOUDFLARE_ACCOUNT_ID:?the CLOUDFLARE_ACCOUNT_ID secret is needed}"
root="$(cd "$(dirname "$0")/.." && pwd)"
name="$(sed -n 's/^name *= *"\(.*\)"/\1/p' "$root/backend/wrangler.toml" | head -1)"
sub="$(curl -fsS --retry 3 -H "Authorization: Bearer $CLOUDFLARE_API_TOKEN" \
  "https://api.cloudflare.com/client/v4/accounts/$CLOUDFLARE_ACCOUNT_ID/workers/subdomain" |
  python3 -c 'import json, sys; print(json.load(sys.stdin)["result"]["subdomain"])')"
[ -n "$name" ] && [ -n "$sub" ]
echo "https://$name.$sub.workers.dev"
