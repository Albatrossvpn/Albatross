#!/usr/bin/env bash
# Stores one release file in the project's own storage through the account
# service (POST /api/v1/admin/files, backend/src/files.js) and checks the
# copy the website serves against the file's SHA-256.
#
#   deploy/files/push.sh <account-url> <site-url> <name> <github-url> <sha256> [force]
#
# Environment: ADMIN_USER and ADMIN_PASSWORD (the service's administrator).
set -euo pipefail
account="${1%/}" site="${2%/}" name="$3" source="$4" sha="$5" force="${6:-false}"
[ -n "${ADMIN_USER:-}" ] && [ -n "${ADMIN_PASSWORD:-}" ] || { echo "ADMIN_USER and ADMIN_PASSWORD are not set" >&2; exit 1; }
token="$(curl -fsS --max-time 60 -H 'Content-Type: application/json' \
  --data "$(jq -cn --arg u "$ADMIN_USER" --arg p "$ADMIN_PASSWORD" '{username:$u,password:$p}')" \
  "$account/api/v1/admin/login" | jq -r .token)"
[ -n "$token" ] && [ "$token" != null ] || { echo "could not sign in to $account" >&2; exit 1; }
# Streaming 150 MB from GitHub into storage takes a while.
out="$(curl -fsS --max-time 900 -H "Authorization: Bearer $token" -H 'Content-Type: application/json' \
  --data "$(jq -cn --arg n "$name" --arg u "$source" --argjson f "$force" '{name:$n,url:$u,force:$f}')" \
  "$account/api/v1/admin/files")"
echo "stored: $out"
got="$(curl -fsSL --max-time 900 "$site/download/$name" | sha256sum | cut -d' ' -f1)"
[ "$got" = "$sha" ] || { echo "$site/download/$name has SHA-256 $got, expected $sha" >&2; exit 1; }
echo "$site/download/$name matches its SHA-256."
