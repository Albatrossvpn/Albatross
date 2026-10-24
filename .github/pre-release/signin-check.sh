#!/usr/bin/env bash
# Builds the desktop app of a commit the way releases are built (with an
# account service address) against a local stand-in for the account service,
# then drives the page's API: connecting must ask for sign-in, a wrong
# password must be refused, the right one must sign in, and sign-out must
# sign out. Debug and emulator builds have no account service, so nothing
# else exercises these routes.
# Usage: signin-check.sh <app source dir> [out dir]
set -u
src="$(cd "$1" && pwd)"
out="$(mkdir -p "${2:-out}" && cd "${2:-out}" && pwd)"
res="$out/result.txt"
: > "$res"
log() { echo "$*" | tee -a "$res"; }
work="$(mktemp -d)"
stub_port=18090

cat > "$work/stub.py" <<'PY'
import json, sys
from http.server import BaseHTTPRequestHandler, HTTPServer

ACCOUNT = {"username": "tester", "allowed": True, "quota_bytes": 0, "used_bytes": 0,
           "max_seconds": 0, "used_seconds": 0, "expires_at": 0, "resets_at": 0}
log = open(sys.argv[2], "a", buffering=1)

class H(BaseHTTPRequestHandler):
    def reply(self, code, body):
        b = json.dumps(body).encode()
        self.send_response(code)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(b)))
        self.end_headers()
        self.wfile.write(b)

    def do_POST(self):
        n = int(self.headers.get("Content-Length") or 0)
        body = json.loads(self.rfile.read(n) or b"{}")
        log.write(self.path + "\n")
        if self.path == "/api/v1/login":
            if body.get("username") == "tester" and body.get("password") == "right-password":
                return self.reply(200, {"token": "stub-token", "account": ACCOUNT})
            return self.reply(401, {"error": "Wrong username or password."})
        if self.path == "/api/v1/usage":
            if self.headers.get("Authorization") == "Bearer stub-token":
                return self.reply(200, {"token": "stub-token", "account": ACCOUNT})
            return self.reply(401, {"error": "Sign in again."})
        self.reply(404, {"error": "not found"})

    def log_message(self, *a):
        pass

HTTPServer(("127.0.0.1", int(sys.argv[1])), H).serve_forever()
PY
python3 "$work/stub.py" "$stub_port" "$out/stub-requests.txt" &
stub=$!

mod="$(cd "$src" && go list -m)"
log "INFO building $(cd "$src" && git rev-parse --short HEAD) with AccountURL=http://127.0.0.1:$stub_port"
if ! (cd "$src" && go build -ldflags "-X $mod/internal/engine.AccountURL=http://127.0.0.1:$stub_port" -o "$work/albatross" ./cmd/albatross) > "$out/build.log" 2>&1; then
  log "FAIL the desktop app does not build"
  kill $stub
  exit 0
fi

"$work/albatross" -headless -data "$work/data" > "$work/stdout.txt" 2> "$out/app-stderr.txt" &
app=$!
url=""
for _ in $(seq 1 30); do
  url="$(grep -m1 -oE 'http://127\.0\.0\.1:[0-9]+/#[A-Za-z0-9_-]+' "$work/stdout.txt" || true)"
  [ -n "$url" ] && break
  sleep 1
done
if [ -z "$url" ]; then
  log "FAIL the app printed no page address within 30 s"
  kill $app $stub 2>/dev/null
  exit 0
fi
base="${url%%/#*}"
token="${url##*#}"

# call METHOD PATH [JSON]: prints the HTTP status, keeps the body in $work/body
call() {
  curl -s -o "$work/body" -w '%{http_code}' -X "$1" -H "X-Token: $token" \
    -H 'Content-Type: application/json' ${3:+--data "$3"} "$base$2"
}
account() { # prints "enabled signed_in" from /api/state
  call GET /api/state > /dev/null
  python3 -c 'import json,sys; a=json.load(open(sys.argv[1])).get("account") or {}; print(a.get("enabled"), a.get("signed_in"))' "$work/body"
}

read -r enabled signed < <(account)
if [ "$enabled" = True ] && [ "$signed" = False ]; then log "PASS the build asks users to sign in (account.enabled, not signed in)"
else log "FAIL account state before sign-in: enabled=$enabled signed_in=$signed"; fi

code="$(call POST /api/connect '{"country":""}')"
if [ "$code" = 400 ] && grep -q login_required "$work/body"; then log "PASS connect before sign-in is refused with login_required"
else log "FAIL connect before sign-in answered $code: $(head -c 200 "$work/body")"; fi

code="$(call POST /api/login '{"username":"tester","password":"wrong"}')"
if [ "$code" = 404 ]; then log "FAIL POST /api/login is not routed (404): the sign-in form cannot work"
elif [ "${code:0:1}" = 2 ]; then log "FAIL a wrong password signed in ($code)"
else log "PASS a wrong password is refused ($code: $(head -c 120 "$work/body"))"; fi

code="$(call POST /api/login '{"username":"tester","password":"right-password"}')"
cp "$work/body" "$work/login-body"
read -r enabled signed < <(account)
if [ "${code:0:1}" = 2 ] && [ "$signed" = True ]; then log "PASS sign-in with the right password works ($code, signed_in=True)"
else log "FAIL sign-in with the right password: $code, signed_in=$signed: $(head -c 200 "$work/login-body")"; fi

code="$(call POST /api/logout)"
read -r enabled signed < <(account)
if [ "${code:0:1}" = 2 ] && [ "$signed" = False ]; then log "PASS sign-out works ($code, signed_in=False)"
else log "FAIL sign-out: $code, signed_in=$signed"; fi

log "INFO the account stand-in received: $(tr '\n' ' ' < "$out/stub-requests.txt" 2>/dev/null)"
kill $app $stub 2>/dev/null
wait 2>/dev/null
exit 0
