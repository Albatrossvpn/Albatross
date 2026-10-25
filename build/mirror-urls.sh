#!/usr/bin/env bash
# Prints the project's own storage addresses for a release build, comma
# separated: the account service's (build/account-url.sh), then the
# website's custom domains (site/wrangler.toml). Both Workers serve the
# app's own files at /list/<branch>/<path> (sources.WorkerMirror).
set -euo pipefail
root="$(cd "$(dirname "$0")/.." && pwd)"
{
  bash "$root/build/account-url.sh" | tr ',' '\n'
  sed -n 's/.*pattern *= *"\([^"]*\)", *custom_domain *= *true.*/\1/p' "$root/site/wrangler.toml" | awk 'NF { print "https://" $0 }'
} | awk 'NF && !seen[$0]++' | paste -sd, -
