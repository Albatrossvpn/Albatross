#!/usr/bin/env bash
# Prints the account service's addresses for a release build, comma
# separated, in the order the app tries them: the Worker in backend/ on its
# custom domains (the repository variable SITE_DOMAIN, and any domain
# attached to it in Cloudflare), then on the account's workers.dev
# subdomain. A domain of its own is filtered apart from workers.dev, so each
# is the other's fallback. They are read at build time so the addresses are
# never written into this public repository.
set -euo pipefail
: "${CLOUDFLARE_API_TOKEN:?the CLOUDFLARE_API_TOKEN secret is needed}"
: "${CLOUDFLARE_ACCOUNT_ID:?the CLOUDFLARE_ACCOUNT_ID secret is needed}"
root="$(cd "$(dirname "$0")/.." && pwd)"
name="$(sed -n 's/^name *= *"\(.*\)"/\1/p' "$root/backend/wrangler.toml" | head -1)"
get() {
  curl -fsS --retry 3 -H "Authorization: Bearer $CLOUDFLARE_API_TOKEN" \
    "https://api.cloudflare.com/client/v4/accounts/$CLOUDFLARE_ACCOUNT_ID/workers/$1"
}
sub="$(get subdomain | python3 -c 'import json, sys; print(json.load(sys.stdin)["result"]["subdomain"])')"
[ -n "$name" ] && [ -n "$sub" ]
# Listing domains needs the token's Workers permission; without it the
# build still has SITE_DOMAIN and workers.dev.
domains="$(get "domains?service=$name" 2>/dev/null |
  python3 -c 'import json, sys; print("\n".join(d["hostname"] for d in json.load(sys.stdin)["result"] if d.get("service") == sys.argv[1]))' "$name" 2>/dev/null)" ||
  echo "The Worker's custom domains could not be read; using SITE_DOMAIN and workers.dev." >&2
{
  for d in ${SITE_DOMAIN:-} $domains; do echo "https://$d"; done
  echo "https://$name.$sub.workers.dev"
} | awk '!seen[$0]++' | paste -sd, -
