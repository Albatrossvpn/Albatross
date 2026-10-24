#!/usr/bin/env bash
# Prints the account service's addresses for a release build, comma
# separated, in the order the app tries them: the Worker's own domains
# (custom_domain routes in backend/wrangler.toml, then the repository
# variable SITE_DOMAIN). The Worker answers on nothing else; a workers.dev
# address would tie it to the Cloudflare account's other Workers.
set -euo pipefail
root="$(cd "$(dirname "$0")/.." && pwd)"
{
  sed -n 's/.*pattern *= *"\([^"]*\)", *custom_domain *= *true.*/\1/p' "$root/backend/wrangler.toml"
  for d in ${SITE_DOMAIN:-}; do echo "$d"; done
} | awk 'NF && !seen[$0]++ { print "https://" $0 }' | paste -sd, - | grep . ||
  { echo "No account service domain: add a custom_domain route to backend/wrangler.toml." >&2; exit 1; }
