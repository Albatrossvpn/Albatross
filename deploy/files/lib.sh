# shellcheck shell=bash
# Shared by the scripts in deploy/files.
#
# Files reach the project's own storage (the R2 bucket the Workers serve
# from) in one of two ways: straight into the bucket with wrangler when the
# Cloudflare credentials are set (CLOUDFLARE_API_TOKEN, CLOUDFLARE_ACCOUNT_ID;
# wrangler installed in backend/), or through the account service as its
# administrator (ADMIN_USER, ADMIN_PASSWORD). CI uses the first: Cloudflare's
# edge turns requests from GitHub's runners away (403), and wrangler's API
# calls are not subject to that.

# Signs in to the account service and prints the token.
admin_token() {
  local account="${1%/}" token
  [ -n "${ADMIN_USER:-}" ] && [ -n "${ADMIN_PASSWORD:-}" ] || { echo "ADMIN_USER and ADMIN_PASSWORD are not set" >&2; return 1; }
  token="$(curl -fsS --retry 3 --max-time 60 -H 'Content-Type: application/json' \
    --data "$(jq -cn --arg u "$ADMIN_USER" --arg p "$ADMIN_PASSWORD" '{username:$u,password:$p}')" \
    "$account/api/v1/admin/login" | jq -r .token)"
  [ -n "$token" ] && [ "$token" != null ] || { echo "could not sign in to $account" >&2; return 1; }
  printf '%s' "$token"
}

# backend/, where wrangler.toml names the bucket and wrangler is installed.
# Resolved once, as an absolute path: the functions below change directory
# into it, and a relative path from the caller's directory would then point
# nowhere (the 1.0.0 release stored its files but could not check them).
ALBATROSS_BACKEND="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../backend" && pwd)"
bucket_dir() { echo "$ALBATROSS_BACKEND"; }

# True when wrangler can write the bucket directly.
have_wrangler() {
  [ -n "${CLOUDFLARE_API_TOKEN:-}" ] && [ -n "${CLOUDFLARE_ACCOUNT_ID:-}" ] && [ -d "$(bucket_dir)/node_modules/wrangler" ]
}

# The bucket's name, from the account service's wrangler.toml.
bucket_name() { sed -n 's/^bucket_name *= *"\([^"]*\)".*/\1/p' "$(bucket_dir)/wrangler.toml" | head -1; }

# r2_put <key> <file> <content-type> [cache-control]: stores a file in the bucket.
r2_put() {
  local key="$1" file="$2" type="$3" cc="${4:-}"
  # wrangler runs in backend/, so the file's path must not depend on here.
  file="$(cd "$(dirname "$file")" && pwd)/$(basename "$file")"
  local args=(r2 object put "$(bucket_name)/$key" --file "$file" --content-type "$type" --remote)
  [ -n "$cc" ] && args+=(--cache-control "$cc")
  (cd "$(bucket_dir)" && npx wrangler "${args[@]}" > /dev/null)
}

# r2_sha <key>: the SHA-256 of a stored object; fails when it is not stored.
r2_sha() {
  local tmp
  tmp="$(mktemp)"
  (cd "$(bucket_dir)" && npx wrangler r2 object get "$(bucket_name)/$1" --file "$tmp" --remote > /dev/null 2>&1) || { rm -f "$tmp"; return 1; }
  sha256sum < "$tmp" | cut -d' ' -f1
  rm -f "$tmp"
}

# The content type of one of the app's own files (backend/src/files.js listType).
list_type() {
  case "$1" in
    *.json) echo "application/json; charset=utf-8" ;;
    *.srs|*.bin|*.sealed|collect-linux-*) echo "application/octet-stream" ;;
    *) echo "text/plain; charset=utf-8" ;;
  esac
}
