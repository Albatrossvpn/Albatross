# The edge

A Cloudflare Worker that carries the app's traffic: VLESS over WebSocket,
with TLS ending at Cloudflare's edge. It needs no server: Cloudflare's free
plan runs it. The account service hands the link to signed-in users
(`GET /api/v1/edge`), and the app tests it like any other server. Being an
ordinary Cloudflare site, it can be reached through any Cloudflare edge
address the app finds clean, with the name hidden by Encrypted Client Hello.

Deployment is part of `.github/workflows/deploy-backend.yml` (job `edge`).
The user id (`EDGE_UUID`) is derived from `TOKEN_SECRET` and the repository
variable `EDGE_EPOCH` (default `1`): change `EDGE_EPOCH` to retire every
link handed out so far.

Limits: a Worker cannot open a socket to Cloudflare's own addresses, so
sites behind Cloudflare are reached through the relays in `PROXY_IP`
(`edge/wrangler.toml`), or not at all through the edge; the app then uses
another server for them. UDP is carried only for DNS (port 53), answered
over DNS over HTTPS.
