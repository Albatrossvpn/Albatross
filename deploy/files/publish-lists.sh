#!/usr/bin/env bash
# Publishes the app's own files (the collector's lists, the update manifest,
# the ad list) into the project's own storage, where both Workers serve them
# at /list/<branch>/<name> (backend/src/files.js), and checks each stored
# copy against the file.
#
#   deploy/files/publish-lists.sh <account-url> <branch> <file>...
#
# Each file goes under its own name in <branch> (servers, main or rules).
# With the Cloudflare credentials set the files go straight into the bucket
# (deploy/files/lib.sh); otherwise through the account service as its
# administrator (ADMIN_USER, ADMIN_PASSWORD).
set -euo pipefail
. "$(dirname "$0")/lib.sh"
account="${1%/}" branch="$2"
shift 2
if have_wrangler; then
  for f in "$@"; do
    name="$(basename "$f")"
    r2_put "list/$branch/$name" "$f" "$(list_type "$name")"
    want="$(sha256sum < "$f" | cut -d' ' -f1)"
    got="$(r2_sha "list/$branch/$name")"
    [ "$got" = "$want" ] || { echo "stored list/$branch/$name does not match $f" >&2; exit 1; }
    echo "published $branch/$name ($(stat -c%s "$f") bytes) into the bucket"
  done
  exit 0
fi
token="$(admin_token "$account")"
for f in "$@"; do
  name="$(basename "$f")"
  curl -fsS --retry 3 --max-time 300 -X PUT -H "Authorization: Bearer $token" -H 'Content-Type: application/octet-stream' \
    --data-binary @"$f" "$account/api/v1/admin/list/$branch/$name" > /dev/null
  got="$(curl -fsS --retry 3 --max-time 300 "$account/list/$branch/$name" | sha256sum | cut -d' ' -f1)"
  want="$(sha256sum < "$f" | cut -d' ' -f1)"
  [ "$got" = "$want" ] || { echo "$account/list/$branch/$name does not match $f" >&2; exit 1; }
  echo "published $branch/$name ($(stat -c%s "$f") bytes)"
done
