#!/usr/bin/env bash
# One discovery run in the public repository alba-data (see collect.sh): finds
# more sources and stores them in the project's own storage only. The
# sources found are this project's own: nothing of them is printed or kept
# in the public repository. GITHUB_TOKEN is that repository's token, for
# GitHub's code search.
set -uo pipefail
src="$(cd "$(dirname "$0")/../.." && pwd)"
tmp="${RUNNER_TEMP:-$(mktemp -d)}"
cd "$src"
. deploy/files/lib.sh
(cd backend && npm ci --silent >/dev/null 2>&1) || { echo "::error::npm ci failed"; exit 1; }
have_wrangler || { echo "::error::the Cloudflare credentials are missing"; exit 1; }
mkdir -p "$tmp/prev"
for f in sources.txt channels.txt discover.json; do
  (cd backend && npx wrangler r2 object get "$(bucket_name)/list/servers/$f" --file "$tmp/prev/$f" --remote >/dev/null 2>&1) || rm -f "$tmp/prev/$f"
done
if ! go run ./cmd/collect -discover "$tmp/discovered" -discover-prev "$tmp/prev" -discover-minutes 40 >"$tmp/discover.log" 2>&1; then
  echo "::error::the discovery failed"
  exit 1
fi
account="$(bash build/account-url.sh | cut -d, -f1)"
echo "::add-mask::$account"
bash deploy/files/publish-lists.sh "$account" servers "$tmp"/discovered/sources.txt "$tmp"/discovered/channels.txt "$tmp"/discovered/discover.json >/dev/null ||
  { echo "::error::publishing to the project's storage failed"; exit 1; }
echo "$(grep -c '^https' "$tmp/discovered/sources.txt") subscriptions and $(grep -vc '^#' "$tmp/discovered/channels.txt") channels stored"
