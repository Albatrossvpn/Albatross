#!/usr/bin/env bash
# Publishes the app's own files (the collector's lists, the update manifest,
# the ad list) into the project's own storage through the account service
# (PUT /api/v1/admin/list/<branch>/<name>, backend/src/files.js), and checks
# each copy the service now serves against the file.
#
#   deploy/files/publish-lists.sh <account-url> <branch> <file>...
#
# Each file is published under its own name in <branch> (servers, main or
# rules). Environment: ADMIN_USER and ADMIN_PASSWORD.
set -euo pipefail
. "$(dirname "$0")/lib.sh"
account="${1%/}" branch="$2"
shift 2
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
