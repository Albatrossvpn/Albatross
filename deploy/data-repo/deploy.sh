#!/usr/bin/env bash
# Deploys the account service (backend/) and the website (site/) from the
# public repository alba-data (its deploy.yml checks this repository out
# into src/ and runs this), as .github/workflows/deploy-backend.yml does
# here: the database's migrations, then both Workers.
#
# The Workers' secrets are not touched (they stay as set), nor the edge, the
# zone's settings or the files in storage: the first deploy did those. The
# variables are given as that workflow gives them; any other variable a
# Worker has is kept (--keep-vars). wrangler's output stays in a file that is
# not kept: the log is public.
#
# Environment: CLOUDFLARE_API_TOKEN (Workers, D1, Routes), CLOUDFLARE_ACCOUNT_ID,
# and optionally SITE_DOMAIN, SITE_NAME, COMPANY_NAME, COMPANY_ADDRESS,
# CONTACT_EMAIL, SUPPORT_URL (secrets there, variables here).
set -uo pipefail
root="$(cd "$(dirname "$0")/../.." && pwd)"
tmp="${RUNNER_TEMP:-$(mktemp -d)}"
log="$tmp/deploy.log"
cd "$root/backend"
fail() { echo "::error::$1"; grep -iE "^✘|error" "$log" | grep -v "^\s*at " | tail -3 | cut -c1-200; exit 1; }
[ -n "${CLOUDFLARE_API_TOKEN:-}" ] && [ -n "${CLOUDFLARE_ACCOUNT_ID:-}" ] || { echo "::error::the Cloudflare credentials are missing"; exit 1; }
npm ci --silent >"$log" 2>&1 || fail "npm ci failed"
id="$(npx wrangler d1 list --json 2>>"$log" | node -e 'let s="";process.stdin.on("data",d=>s+=d).on("end",()=>{try{const d=JSON.parse(s).find(x=>x.name==="albatross");if(d)console.log(d.uuid)}catch{}})')"
[ -n "$id" ] || fail "the database was not found (the token needs D1)"
sed -i "s/^database_id = .*/database_id = \"$id\"/" wrangler.toml
npx wrangler d1 migrations apply DB --remote >>"$log" 2>&1 || fail "the database could not be updated"
echo "database: up to date"
args=(--keep-vars)
[ -n "${SITE_DOMAIN:-}" ] && args+=(--domain "$SITE_DOMAIN")
[ -n "${SITE_NAME:-}" ] && args+=(--var "SITE_NAME:$SITE_NAME")
args+=(--var "COMPANY_NAME:${COMPANY_NAME:-}" --var "COMPANY_ADDRESS:${COMPANY_ADDRESS:-}"
  --var "CONTACT_EMAIL:${CONTACT_EMAIL:-support@albatrossvpn.online}" --var "SUPPORT_URL:${SUPPORT_URL:-}")
edge="$(sed -n 's/.*pattern *= *"\([^"]*\)", *custom_domain *= *true.*/\1/p' ../edge/wrangler.toml | head -1)"
args+=(--var "EDGE_HOST:$edge")
npx wrangler deploy "${args[@]}" >>"$log" 2>&1 || fail "the account service could not be deployed"
echo "account service: deployed"
npx wrangler deploy --config ../site/wrangler.toml --keep-vars >>"$log" 2>&1 || fail "the website could not be deployed"
echo "website: deployed"
