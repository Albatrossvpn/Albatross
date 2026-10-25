#!/usr/bin/env bash
# Stores one release file in the project's own storage, where the website
# serves it at /download/<name> and the account service at /dl/, and checks
# the stored copy against the file's SHA-256.
#
#   deploy/files/push.sh <account-url> <site-url> <name> <github-url> <sha256> [force]
#
# With the Cloudflare credentials set the file is downloaded from the
# release and put straight into the bucket (deploy/files/lib.sh); otherwise
# the account service fetches it itself (POST /api/v1/admin/files) as its
# administrator (ADMIN_USER, ADMIN_PASSWORD).
set -euo pipefail
account="${1%/}" site="${2%/}" name="$3" source="$4" sha="$5" force="${6:-false}"
. "$(dirname "$0")/lib.sh"
case "$name" in
  *.apk) type="application/vnd.android.package-archive" ;;
  *.exe) type="application/vnd.microsoft.portable-executable" ;;
  *) type="application/octet-stream" ;;
esac
if have_wrangler; then
  if [ "$force" != true ] && [ "$(r2_sha "$name" || true)" = "$sha" ]; then
    echo "$name is already stored with SHA-256 $sha."
    exit 0
  fi
  tmp="$(mktemp -d)"
  trap 'rm -rf "$tmp"' EXIT
  curl -fsSL --retry 4 --retry-delay 5 --max-time 900 -o "$tmp/$name" "$source"
  got="$(sha256sum < "$tmp/$name" | cut -d' ' -f1)"
  [ "$got" = "$sha" ] || { echo "$source has SHA-256 $got, expected $sha" >&2; exit 1; }
  r2_put "$name" "$tmp/$name" "$type" "public, max-age=86400"
  got="$(r2_sha "$name")"
  [ "$got" = "$sha" ] || { echo "the stored $name has SHA-256 $got, expected $sha" >&2; exit 1; }
  echo "$name stored ($(stat -c%s "$tmp/$name") bytes) and matches its SHA-256."
  exit 0
fi
token="$(admin_token "$account")"
# Streaming 150 MB from GitHub into storage takes a while.
out="$(curl -fsS --max-time 900 -H "Authorization: Bearer $token" -H 'Content-Type: application/json' \
  --data "$(jq -cn --arg n "$name" --arg u "$source" --argjson f "$force" '{name:$n,url:$u,force:$f}')" \
  "$account/api/v1/admin/files")"
echo "stored: $out"
got="$(curl -fsSL --max-time 900 "$site/download/$name" | sha256sum | cut -d' ' -f1)"
[ "$got" = "$sha" ] || { echo "$site/download/$name has SHA-256 $got, expected $sha" >&2; exit 1; }
echo "$site/download/$name matches its SHA-256."
