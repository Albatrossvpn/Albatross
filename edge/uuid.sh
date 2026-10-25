#!/bin/sh
# The edge's user id, derived from TOKEN_SECRET and EDGE_EPOCH so that the
# edge and the account service agree without another secret. Printed in
# the UUID form. Change EDGE_EPOCH to retire every link handed out so far.
set -eu
[ -n "${TOKEN_SECRET:-}" ] || { echo "TOKEN_SECRET is not set" >&2; exit 1; }
h="$(printf 'albatross-edge:%s:%s' "$TOKEN_SECRET" "${EDGE_EPOCH:-1}" | sha256sum | cut -c1-32)"
printf '%s-%s-4%s-a%s-%s\n' "$(echo "$h" | cut -c1-8)" "$(echo "$h" | cut -c9-12)" "$(echo "$h" | cut -c14-16)" "$(echo "$h" | cut -c18-20)" "$(echo "$h" | cut -c21-32)"
