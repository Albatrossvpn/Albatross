# Shared by the scripts in deploy/files: signs in to the account service as
# its administrator (ADMIN_USER, ADMIN_PASSWORD) and prints the token.
admin_token() {
  local account="${1%/}" token
  [ -n "${ADMIN_USER:-}" ] && [ -n "${ADMIN_PASSWORD:-}" ] || { echo "ADMIN_USER and ADMIN_PASSWORD are not set" >&2; return 1; }
  token="$(curl -fsS --retry 3 --max-time 60 -H 'Content-Type: application/json' \
    --data "$(jq -cn --arg u "$ADMIN_USER" --arg p "$ADMIN_PASSWORD" '{username:$u,password:$p}')" \
    "$account/api/v1/admin/login" | jq -r .token)"
  [ -n "$token" ] && [ "$token" != null ] || { echo "could not sign in to $account" >&2; return 1; }
  printf '%s' "$token"
}
