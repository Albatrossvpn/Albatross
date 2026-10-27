#!/usr/bin/env bash
# Sets up one Albatross server: every protocol the app runs, on one
# Ubuntu machine (22.04 or 24.04, x86-64 or ARM, for example an Oracle
# Cloud "Always Free" VM). Run as root:
#
#   sudo bash install.sh        (copied from deploy/server in the repository)
#
# Optional environment:
#   DOMAIN   a name pointing at this machine; default <ip>.sslip.io
#   EMAIL    for Let's Encrypt; default none
#   CDN_HOST a name proxied by Cloudflare (orange cloud) that points at
#            this machine (.github/workflows/cloudflare-dns.yml makes it):
#            the WebSocket, gRPC and HTTPUpgrade servers are then also
#            offered through Cloudflare's edge, where this address never
#            shows and blocking it changes nothing; remembered
#   DRY_RUN  1 = only write the configs into $OUT (for tests)
#
# At the end it prints one share link per protocol and saves them in
# /root/albatross-links.txt. See deploy/server/README.md for what to do
# with them.
#
# Relay mode, for a machine inside the country (RELAY_TO=<server ip>):
# installs nothing but forwarding rules that send every Albatross port
# (TCP, UDP, ICMP) on to the server abroad, and rewrites the server's
# links (LINKS=<file or https address>) to this machine's address. When
# only domestic addresses can be reached, the app connects here and this
# machine's own link to the outside carries the traffic. RELAY_TO2 names
# a second server: a watchdog moves the forwarding over when the first
# stops answering, and back when it returns.
#
# Mirror mode (MIRROR=1 MIRROR_SOURCE=https://accounts.example.com), on
# a machine inside the country, alone or next to the relay: serves copies
# of the app's lists, update manifest and installers on port 8081 under a
# secret path, refreshed every ten minutes while the machine's own way out
# lasts (and kept as they were when it does not). The address it prints
# goes into the repository variable DOMESTIC_MIRROR (comma-separated when
# there are several) so the apps are built knowing it; apps also pass
# it on to each other in share codes.
set -euo pipefail

SINGBOX_VERSION="${SINGBOX_VERSION:-1.14.4}"
XRAY_VERSION="${XRAY_VERSION:-26.10.21}"
# The sidecar protocols: each runs on a server program of its own.
GO_VERSION="${GO_VERSION:-1.26.10}"
JUICITY_VERSION="${JUICITY_VERSION:-0.5.0}"
MIERU_VERSION="${MIERU_VERSION:-3.39.0}"
BROOK_VERSION="${BROOK_VERSION:-20270101}"
WSTUNNEL_VERSION="${WSTUNNEL_VERSION:-11.1.0}"
# NaiveProxy's server is Caddy with klzgrad's forward proxy, which builds
# its releases on a Caddy patched for it (the newest needs that patch's
# idle timeouts): on x86-64 its release is taken, checked against the
# hash it had when pinned.
NAIVE_RELEASE="${NAIVE_RELEASE:-v2.11.7-naive2}"
NAIVE_SHA256="${NAIVE_SHA256:-c273dc07dd79c5919f97639cbdcb559d40ce90475dbd8aac80641b0a8c8065ff}"
# Elsewhere it is built from source, so pinned like the rest: Caddy's
# builder, the forward proxy (the commit of its release v2.11.7-naive,
# the newest that stock Caddy builds), the Caddy it is built on (xcaddy
# left alone picks one of its own) and AmneziaWG.
XCADDY_VERSION="${XCADDY_VERSION:-v0.4.7}"
NAIVE_PROXY_REF="${NAIVE_PROXY_REF:-c096d6a00cb28e019cc1995b04bdc6a9311d8024}"
CADDY_VERSION="${CADDY_VERSION:-v2.11.7}"
AWG_GO_REF="${AWG_GO_REF:-v3.1.20260828}"
AWG_TOOLS_REF="${AWG_TOOLS_REF:-v3.1.20260812}"
# SIDECARS=0 skips them (sing-box and Xray alone, as before).
SIDECARS="${SIDECARS:-1}"
DRY_RUN="${DRY_RUN:-0}"
OUT="${OUT:-/etc/albatross}"
REALITY_DEST="${REALITY_DEST:-www.microsoft.com}"
# DNS_TUNNEL, when set (e.g. t.example.com), is a name whose NS record
# points at this server: the app then tunnels through DNS queries to any
# resolver (Xray xdns), which is what still works when every foreign
# address is cut. UDP 53 then serves the tunnel instead of Hysteria 2.
# Several names are separated by commas (a foreign one and one under .ir,
# whose delegation the domestic resolvers keep answering in a shutdown);
# the server answers all of them and prints one link per name.
DNS_TUNNEL="${DNS_TUNNEL:-}"
CDN_HOST="${CDN_HOST:-}"
# MIRROR_PORT is where mirror mode serves; it is not among the relay's
# forwarded ports, so both can run on one machine.
MIRROR_PORT="${MIRROR_PORT:-8081}"

# Ports. Each protocol has its own so one being blocked leaves the rest.
P_REALITY=443        # VLESS + Reality + Vision (TCP)
P_XHTTP=8443         # VLESS + XHTTP + Reality (Xray, TCP)
P_TROJAN=2098        # Trojan + TLS (TCP)
P_WS=2083            # VLESS + WebSocket + TLS
P_GRPC=2087          # VLESS + gRPC + TLS
P_UPGRADE=2096       # VMess + HTTPUpgrade + TLS
P_ANYTLS=8880        # AnyTLS
P_OVPN=2082          # OpenVPN over TCP (a certificate per client, tls-crypt)
P_TGO=2053           # Trojan + WebSocket + TLS (what Trojan-Go clients speak; a Cloudflare port)
P_NAIVE=2052         # NaiveProxy: HTTP/2 CONNECT behind Caddy, looks like a web site
P_MIERU=2086         # Mieru (TCP)
P_BROOK=2095         # Brook over WebSocket + TLS
P_WST=2097           # WStunnel: SOCKS5 inside WebSocket + TLS
P_OC=2099            # OpenConnect (AnyConnect), TCP and UDP; for other clients, see links-external.txt
P_JUICITY=8446       # Juicity (QUIC, UDP)
P_AWG=8447           # AmneziaWG (UDP)
P_DECOY=18080        # loopback: the web page probes of the TLS ports get
P_SS=8388            # Shadowsocks 2022 (TCP and UDP)
P_STLS=8445          # Shadowsocks 2022 behind ShadowTLS v3 (TCP): a real TLS handshake with REALITY_DEST
P_HY2=443            # Hysteria 2 + Salamander (UDP), hops over HOP_RANGE
HOP_RANGE="20000:40000"
P_TUIC=8444          # TUIC v5 (UDP)
P_HTTP=80            # VMess (AEAD) + HTTPUpgrade, no TLS: looks like plain HTTP
# UDP 53 also lands on Hysteria 2: in the June 2025 shutdown it was the
# only UDP port that still got out.
P_HY2_DNS=53
# TCP 53 as well: allowlists that keep DNS open often keep both transports.
P_TCP53=53
# VMess over mKCP carried in ICMP echo (Xray xicmp): ping is what stays
# open when both TCP and UDP to foreign addresses are cut. The port only
# names the mKCP session inside the echo packets.
P_ICMP=7

log() { echo "== $*" >&2; }
need_root() { [ "$DRY_RUN" = 1 ] || [ "$(id -u)" = 0 ] || { echo "run as root" >&2; exit 1; }; }
need_root

arch() {
  case "$(uname -m)" in
    x86_64|amd64) echo amd64 ;;
    aarch64|arm64) echo arm64 ;;
    *) echo "unsupported CPU $(uname -m)" >&2; exit 1 ;;
  esac
}

install_cores() {
  local a; a="$(arch)"
  apt-get update -qq
  DEBIAN_FRONTEND=noninteractive apt-get install -y -qq curl unzip openssl certbot iptables-persistent openvpn python3 >/dev/null
  local tmp; tmp="$(mktemp -d)"
  curl -fsSL -o "$tmp/sb.tgz" "https://github.com/SagerNet/sing-box/releases/download/v$SINGBOX_VERSION/sing-box-$SINGBOX_VERSION-linux-$a.tar.gz"
  tar -xzf "$tmp/sb.tgz" -C "$tmp" --strip-components=1
  install -m 755 "$tmp/sing-box" /usr/local/bin/sing-box
  local xa=64; [ "$a" = arm64 ] && xa=arm64-v8a
  curl -fsSL -o "$tmp/x.zip" "https://github.com/XTLS/Xray-core/releases/download/v$XRAY_VERSION/Xray-linux-$xa.zip"
  unzip -q -o "$tmp/x.zip" xray -d "$tmp"
  install -m 755 "$tmp/xray" /usr/local/bin/xray
  rm -rf "$tmp"
}

SB="${SB:-sing-box}"
XRAY="${XRAY:-xray}"

public_ip() {
  curl -fsS4 --max-time 10 https://api.ipify.org || curl -fsS4 --max-time 10 https://ifconfig.me
}

# public_ip6 prints the machine's global IPv6 address, if it has one that
# reaches the internet.
public_ip6() {
  local a
  a="$(ip -6 addr show scope global 2>/dev/null | awk '/inet6/ && !/temporary|deprecated/ {sub("/.*", "", $2); print $2; exit}')"
  [ -n "$a" ] && curl -fsS6 --max-time 8 https://api64.ipify.org >/dev/null 2>&1 && echo "$a" || true
}

# Secrets are made once and kept, so running the script again (to update
# the cores) keeps every link working.
secrets() {
  mkdir -p "$OUT"
  chmod 700 "$OUT"
  local f="$OUT/secrets.env"
  if [ ! -s "$f" ]; then
    local kp; kp="$("$SB" generate reality-keypair)"
    {
      echo "UUID=$("$SB" generate uuid)"
      echo "REALITY_PRIVATE=$(echo "$kp" | awk '/PrivateKey/ {print $2}')"
      echo "REALITY_PUBLIC=$(echo "$kp" | awk '/PublicKey/ {print $2}')"
      echo "SHORT_ID=$("$SB" generate rand --hex 8)"
      echo "PASS=$("$SB" generate rand --hex 16)"
      echo "SS_KEY=$("$SB" generate rand --base64 32)"
      echo "OBFS=$("$SB" generate rand --hex 12)"
      echo "WS_PATH=/$("$SB" generate rand --hex 6)"
      echo "GRPC_NAME=$("$SB" generate rand --hex 6)"
      echo "XHTTP_PATH=/$("$SB" generate rand --hex 6)"
      echo "KCP_SEED=$("$SB" generate rand --hex 8)"
      echo "STLS_PASS=$("$SB" generate rand --hex 16)"
    } >"$f"
    chmod 600 "$f"
  fi
  # The tunnel name is kept with the secrets so a rerun keeps it.
  if [ -n "$DNS_TUNNEL" ]; then
    sed -i '/^DNS_TUNNEL=/d' "$f" && echo "DNS_TUNNEL=$DNS_TUNNEL" >>"$f"
  fi
  if [ -n "$CDN_HOST" ]; then
    sed -i '/^CDN_HOST=/d' "$f" && echo "CDN_HOST=$CDN_HOST" >>"$f"
  fi
  # Servers set up before the ICMP carrier get its seed added.
  grep -q "^KCP_SEED=" "$f" || echo "KCP_SEED=$("$SB" generate rand --hex 8)" >>"$f"
  grep -q "^STLS_PASS=" "$f" || echo "STLS_PASS=$("$SB" generate rand --hex 16)" >>"$f"
  grep -q "^SSH_PASS=" "$f" || echo "SSH_PASS=$("$SB" generate rand --hex 16)" >>"$f"
  grep -q "^WST_PATH=" "$f" || echo "WST_PATH=$("$SB" generate rand --hex 12)" >>"$f"
  # shellcheck disable=SC1090
  . "$f"
}

certificate() {
  CERT="$OUT/cert.pem"
  KEY="$OUT/key.pem"
  if [ "$DRY_RUN" = 1 ]; then
    [ -s "$CERT" ] || openssl req -x509 -newkey ec -pkeyopt ec_paramgen_curve:prime256v1 -nodes -days 3 \
      -keyout "$KEY" -out "$CERT" -subj "/CN=$DOMAIN" 2>/dev/null
    return
  fi
  # A real certificate, so the app checks it like any HTTPS site: no
  # "insecure" links that anyone in the middle could impersonate.
  local args=(certonly --standalone --non-interactive --agree-tos --cert-name "$DOMAIN" -d "$DOMAIN" --keep-until-expiring)
  if [ -n "${EMAIL:-}" ]; then args+=(-m "$EMAIL"); else args+=(--register-unsafely-without-email); fi
  iptables -C INPUT -p tcp --dport 80 -j ACCEPT 2>/dev/null || iptables -I INPUT -p tcp --dport 80 -j ACCEPT
  # On a rerun the HTTP inbound holds port 80, which the validation needs;
  # it starts again whatever the outcome, or a failed check would leave
  # the server's protocols down.
  systemctl stop albatross-sing-box 2>/dev/null || true
  local ok=1
  # The CDN name goes on the same certificate: Cloudflare checks it
  # against the name it connects for (SSL/TLS mode Full (strict)). Its
  # check comes through Cloudflare, which may turn it away (Always Use
  # HTTPS); the server's own name alone then does, with the mode on Full.
  if [ -n "$CDN_HOST" ] && ! certbot "${args[@]}" -d "$CDN_HOST" --expand; then
    log "No certificate for $CDN_HOST (Cloudflare kept the check from this machine); one for $DOMAIN alone. Set the zone's SSL/TLS mode to Full, not Full (strict)."
    certbot "${args[@]}" || ok=0
  elif [ -z "$CDN_HOST" ]; then
    certbot "${args[@]}" || ok=0
  fi
  systemctl start albatross-sing-box 2>/dev/null || true
  [ "$ok" = 1 ] || { log "No certificate for $DOMAIN: is port 80 open to this machine?"; exit 1; }
  ln -sf "/etc/letsencrypt/live/$DOMAIN/fullchain.pem" "$CERT"
  ln -sf "/etc/letsencrypt/live/$DOMAIN/privkey.pem" "$KEY"
  mkdir -p /etc/letsencrypt/renewal-hooks/deploy
  printf '#!/bin/sh\nsystemctl restart albatross-sing-box albatross-xray\nfor s in albatross-naive albatross-juicity albatross-brook albatross-wstunnel ocserv; do systemctl try-restart "$s" 2>/dev/null; done\n' >/etc/letsencrypt/renewal-hooks/deploy/albatross
  # Renewal needs port 80, which the HTTP inbound holds.
  mkdir -p /etc/letsencrypt/renewal-hooks/pre /etc/letsencrypt/renewal-hooks/post
  printf '#!/bin/sh\nsystemctl stop albatross-sing-box\n' >/etc/letsencrypt/renewal-hooks/pre/albatross
  printf '#!/bin/sh\nsystemctl start albatross-sing-box\n' >/etc/letsencrypt/renewal-hooks/post/albatross
  chmod 755 /etc/letsencrypt/renewal-hooks/pre/albatross /etc/letsencrypt/renewal-hooks/post/albatross
  chmod 755 /etc/letsencrypt/renewal-hooks/deploy/albatross
}

singbox_config() {
  local tls; tls="\"tls\": {\"enabled\": true, \"server_name\": \"$DOMAIN\", \"certificate_path\": \"$CERT\", \"key_path\": \"$KEY\"}"
  cat >"$OUT/sing-box.json" <<EOF
{
  "log": {"level": "warn", "timestamp": true},
  "inbounds": [
    {"type": "vless", "tag": "reality", "listen": "::", "listen_port": $P_REALITY,
     "users": [{"uuid": "$UUID", "flow": "xtls-rprx-vision"}],
     "tls": {"enabled": true, "server_name": "$REALITY_DEST",
             "reality": {"enabled": true, "handshake": {"server": "$REALITY_DEST", "server_port": 443},
                         "private_key": "$REALITY_PRIVATE", "short_id": ["$SHORT_ID"]}}},
    {"type": "trojan", "tag": "trojan", "listen": "::", "listen_port": $P_TROJAN,
     "users": [{"password": "$PASS"}], $tls,
     "fallback": {"server": "127.0.0.1", "server_port": $P_DECOY}},
    {"type": "trojan", "tag": "trojan-ws", "listen": "::", "listen_port": $P_TGO,
     "users": [{"password": "$PASS"}], $tls,
     "transport": {"type": "ws", "path": "$WS_PATH"}},
    {"type": "vless", "tag": "ws", "listen": "::", "listen_port": $P_WS,
     "users": [{"uuid": "$UUID"}], $tls,
     "transport": {"type": "ws", "path": "$WS_PATH"}},
    {"type": "vless", "tag": "grpc", "listen": "::", "listen_port": $P_GRPC,
     "users": [{"uuid": "$UUID"}], $tls,
     "transport": {"type": "grpc", "service_name": "$GRPC_NAME"}},
    {"type": "vmess", "tag": "upgrade", "listen": "::", "listen_port": $P_UPGRADE,
     "users": [{"uuid": "$UUID", "alterId": 0}], $tls,
     "transport": {"type": "httpupgrade", "path": "$WS_PATH"}},
    {"type": "anytls", "tag": "anytls", "listen": "::", "listen_port": $P_ANYTLS,
     "users": [{"password": "$PASS"}], $tls},
    {"type": "shadowsocks", "tag": "ss", "listen": "::", "listen_port": $P_SS,
     "method": "2022-blake3-aes-256-gcm", "password": "$SS_KEY"},
    {"type": "shadowtls", "tag": "stls", "listen": "::", "listen_port": $P_STLS, "version": 3,
     "users": [{"password": "$STLS_PASS"}],
     "handshake": {"server": "$REALITY_DEST", "server_port": 443}, "detour": "ss-stls"},
    {"type": "shadowsocks", "tag": "ss-stls", "listen": "127.0.0.1", "listen_port": 18445,
     "method": "2022-blake3-aes-256-gcm", "password": "$SS_KEY"},
    {"type": "hysteria2", "tag": "hy2", "listen": "::", "listen_port": $P_HY2,
     "users": [{"password": "$PASS"}],
     "obfs": {"type": "salamander", "password": "$OBFS"}, $tls},
    {"type": "vmess", "tag": "http", "listen": "::", "listen_port": $P_HTTP,
     "users": [{"uuid": "$UUID", "alterId": 0}],
     "transport": {"type": "httpupgrade", "path": "$WS_PATH"}},
    {"type": "vmess", "tag": "tcp53", "listen": "::", "listen_port": $P_TCP53,
     "users": [{"uuid": "$UUID", "alterId": 0}],
     "transport": {"type": "httpupgrade", "path": "$WS_PATH"}},
    {"type": "tuic", "tag": "tuic", "listen": "::", "listen_port": $P_TUIC,
     "users": [{"uuid": "$UUID", "password": "$PASS"}],
     "congestion_control": "bbr", $tls}
  ],
  "outbounds": [{"type": "direct", "tag": "direct"}],
  "route": {"rules": [{"ip_is_private": true, "action": "reject"}]}
}
EOF
  # TUIC and Hysteria 2 speak HTTP/3.
  python3 - "$OUT/sing-box.json" <<'PY'
import json, sys
p = sys.argv[1]
c = json.load(open(p))
for i in c["inbounds"]:
    if i["type"] in ("tuic", "hysteria2"):
        i["tls"]["alpn"] = ["h3"]
json.dump(c, open(p, "w"), indent=2)
PY
}

# dns_tunnel_inbound prints the DNS tunnel inbound (with its leading comma)
# when DNS_TUNNEL is set: VMess over mKCP inside DNS queries for the
# tunnel name, on UDP 53. The small mKCP MTU is Xray's recommendation for
# TXT answers. An answer holds 512 bytes unless both sides say more (the
# link sets edns0 too): at 512 the server drops every packet over a few
# hundred bytes, and the tunnel stalls. 1232, what resolvers have sent
# since DNS Flag Day 2020, holds the 900.
# dns_names prints the tunnel names as a JSON array.
dns_names() { python3 -c 'import json,sys; print(json.dumps([n.strip() for n in sys.argv[1].split(",") if n.strip()]))' "$DNS_TUNNEL"; }

dns_tunnel_inbound() {
  [ -n "$DNS_TUNNEL" ] || return 0
  cat <<EOF
, {
    "tag": "xdns", "listen": "0.0.0.0", "port": 53, "protocol": "vmess",
    "settings": {"clients": [{"id": "$UUID"}]},
    "streamSettings": {
      "network": "kcp",
      "kcpSettings": {"seed": "$KCP_SEED", "mtu": 900, "tti": 200, "header": {"type": "none"}},
      "finalmask": {"udp": [{"type": "xdns", "settings": {"domains": [{"names": $(dns_names), "edns0": 1232}]}}]}
    }
  }
EOF
}

xray_config() {
  cat >"$OUT/xray.json" <<EOF
{
  "log": {"loglevel": "warning"},
  "inbounds": [{
    "tag": "xhttp", "listen": "0.0.0.0", "port": $P_XHTTP, "protocol": "vless",
    "settings": {"clients": [{"id": "$UUID"}], "decryption": "none"},
    "streamSettings": {
      "network": "xhttp", "xhttpSettings": {"path": "$XHTTP_PATH", "mode": "auto"},
      "security": "reality",
      "realitySettings": {"target": "$REALITY_DEST:443", "serverNames": ["$REALITY_DEST"],
                          "privateKey": "$REALITY_PRIVATE", "shortIds": ["$SHORT_ID"]}
    }
  }, {
    "tag": "icmp", "listen": "0.0.0.0", "port": $P_ICMP, "protocol": "vmess",
    "settings": {"clients": [{"id": "$UUID"}]},
    "streamSettings": {
      "network": "kcp",
      "kcpSettings": {"seed": "$KCP_SEED", "mtu": 1250, "tti": 50, "header": {"type": "none"}},
      "finalmask": {"udp": [{"type": "xicmp"}]}
    }
  }$(dns_tunnel_inbound)],
  "outbounds": [{"protocol": "freedom", "tag": "direct"},
                {"protocol": "blackhole", "tag": "block"}],
  "routing": {"rules": [{"ip": ["0.0.0.0/8", "10.0.0.0/8", "100.64.0.0/10", "127.0.0.0/8", "169.254.0.0/16",
                                 "172.16.0.0/12", "192.168.0.0/16", "fc00::/7", "fe80::/10", "::1/128"],
                          "outboundTag": "block"}]}
}
EOF
}

services() {
  cat >/etc/systemd/system/albatross-sing-box.service <<EOF
[Unit]
Description=Albatross server (sing-box)
After=network-online.target
[Service]
ExecStart=/usr/local/bin/sing-box run -c $OUT/sing-box.json
Restart=always
RestartSec=3
LimitNOFILE=1048576
[Install]
WantedBy=multi-user.target
EOF
  cat >/etc/systemd/system/albatross-xray.service <<EOF
[Unit]
Description=Albatross server (Xray)
After=network-online.target
[Service]
ExecStart=/usr/local/bin/xray run -c $OUT/xray.json
Restart=always
RestartSec=3
LimitNOFILE=1048576
[Install]
WantedBy=multi-user.target
EOF
  systemctl daemon-reload
  systemctl enable --now albatross-sing-box albatross-xray
  systemctl restart albatross-sing-box albatross-xray
}

# free_port53 stops systemd-resolved's stub listener, which holds port 53 on
# the loopback address and would keep the cores from binding it; the
# machine keeps resolving through the upstream list resolved maintains.
free_port53() {
  [ -d /run/systemd/system ] || return 0
  mkdir -p /etc/systemd/resolved.conf.d
  printf '[Resolve]\nDNSStubListener=no\n' >/etc/systemd/resolved.conf.d/albatross.conf
  ln -sf /run/systemd/resolve/resolv.conf /etc/resolv.conf
  systemctl restart systemd-resolved 2>/dev/null || true
}

firewall() {
  # Oracle's Ubuntu images reject everything but SSH in iptables; the
  # cloud's own security list must allow the same ports (see README).
  for p in $P_REALITY $P_XHTTP $P_TROJAN $P_WS $P_GRPC $P_UPGRADE $P_ANYTLS $P_SS $P_STLS $P_TCP53 $P_OVPN $P_TGO $P_NAIVE $P_MIERU $P_BROOK $P_WST $P_OC 22 80; do
    iptables -C INPUT -p tcp --dport "$p" -j ACCEPT 2>/dev/null || iptables -I INPUT -p tcp --dport "$p" -j ACCEPT
  done
  for p in $P_SS $P_HY2 $P_TUIC $P_JUICITY $P_AWG $P_OC $HOP_RANGE; do
    iptables -C INPUT -p udp --dport "$p" -j ACCEPT 2>/dev/null || iptables -I INPUT -p udp --dport "$p" -j ACCEPT
  done
  # Hysteria 2 port hopping: the whole range, and UDP 53, land on its one
  # port. Only what is sent to this machine: the clients of the tunnels
  # below send their DNS queries, and any UDP, through it to elsewhere,
  # and a redirect of everything took those to Hysteria (a tunnel then
  # connected and reached nothing). The older, wider rules go.
  local hy2ports="$HOP_RANGE $P_HY2_DNS" r
  for r in $HOP_RANGE $P_HY2_DNS; do
    while iptables -t nat -D PREROUTING -p udp --dport "$r" -j REDIRECT --to-ports "$P_HY2" 2>/dev/null; do :; done
  done
  if [ -n "$DNS_TUNNEL" ]; then
    # UDP 53 serves the DNS tunnel; a rule sending it to Hysteria goes.
    hy2ports="$HOP_RANGE"
    iptables -t nat -D PREROUTING -p udp --dport "$P_HY2_DNS" -m addrtype --dst-type LOCAL -j REDIRECT --to-ports "$P_HY2" 2>/dev/null || true
    iptables -C INPUT -p udp --dport 53 -j ACCEPT 2>/dev/null || iptables -I INPUT -p udp --dport 53 -j ACCEPT
  fi
  for r in $hy2ports; do
    iptables -C INPUT -p udp --dport "$r" -j ACCEPT 2>/dev/null || iptables -I INPUT -p udp --dport "$r" -j ACCEPT
    iptables -t nat -C PREROUTING -p udp --dport "$r" -m addrtype --dst-type LOCAL -j REDIRECT --to-ports "$P_HY2" 2>/dev/null ||
      iptables -t nat -A PREROUTING -p udp --dport "$r" -m addrtype --dst-type LOCAL -j REDIRECT --to-ports "$P_HY2"
  done
  # The tunnels that hand the client an address (OpenVPN, AmneziaWG,
  # OpenConnect) forward its traffic, which Oracle's Ubuntu images reject
  # in FORWARD: the clients would connect and reach nothing.
  for net in 10.9.0.0/24 10.10.0.0/24 10.11.0.0/24; do
    iptables -C FORWARD -s "$net" -j ACCEPT 2>/dev/null || iptables -I FORWARD -s "$net" -j ACCEPT
    iptables -C FORWARD -d "$net" -m conntrack --ctstate RELATED,ESTABLISHED -j ACCEPT 2>/dev/null ||
      iptables -I FORWARD -d "$net" -m conntrack --ctstate RELATED,ESTABLISHED -j ACCEPT
  done
  # The ICMP carrier: echo requests must get in (the cloud's security
  # list too).
  iptables -C INPUT -p icmp --icmp-type echo-request -j ACCEPT 2>/dev/null || iptables -I INPUT -p icmp --icmp-type echo-request -j ACCEPT
  # The same ports over IPv6, where the machine has an address.
  if command -v ip6tables >/dev/null; then
    for p in $P_REALITY $P_XHTTP $P_TROJAN $P_WS $P_GRPC $P_UPGRADE $P_ANYTLS $P_SS $P_STLS $P_TCP53 $P_OVPN $P_TGO $P_NAIVE $P_MIERU $P_BROOK $P_WST $P_OC 22 80; do
      ip6tables -C INPUT -p tcp --dport "$p" -j ACCEPT 2>/dev/null || ip6tables -I INPUT -p tcp --dport "$p" -j ACCEPT
    done
    for p in $P_SS $P_HY2 $P_TUIC $P_JUICITY $P_AWG $P_OC; do
      ip6tables -C INPUT -p udp --dport "$p" -j ACCEPT 2>/dev/null || ip6tables -I INPUT -p udp --dport "$p" -j ACCEPT
    done
    ip6tables -C INPUT -p ipv6-icmp -j ACCEPT 2>/dev/null || ip6tables -I INPUT -p ipv6-icmp -j ACCEPT
  fi
  netfilter-persistent save >/dev/null 2>&1 || true
  # Faster on lossy, long routes such as Iran to Europe.
  cat >/etc/sysctl.d/90-albatross.conf <<EOF
net.core.default_qdisc=fq
net.ipv4.tcp_congestion_control=bbr
net.core.rmem_max=16777216
net.core.wmem_max=16777216
EOF
  sysctl -q --system || true
}

# openvpn_setup runs OpenVPN over TCP on P_OVPN: a CA of its own, a
# server certificate and one client certificate (the app's sing-box
# carries it), the control channel wrapped with tls-crypt, AES-GCM data.
# The app runs the profile through sing-box's openvpn-client.
openvpn_setup() {
  local d="$OUT/ovpn"
  mkdir -p "$d"
  chmod 700 "$d"
  if [ ! -s "$d/ca.crt" ]; then
    openssl req -x509 -newkey ec -pkeyopt ec_paramgen_curve:prime256v1 -nodes -days 3650 \
      -keyout "$d/ca.key" -out "$d/ca.crt" -subj "/CN=ca" 2>/dev/null
    for who in server client; do
      openssl req -newkey ec -pkeyopt ec_paramgen_curve:prime256v1 -nodes \
        -keyout "$d/$who.key" -out "$d/$who.csr" -subj "/CN=$who" 2>/dev/null
      printf 'extendedKeyUsage=%sAuth\nkeyUsage=digitalSignature,keyEncipherment\n' "$who" >"$d/$who.ext"
      openssl x509 -req -in "$d/$who.csr" -CA "$d/ca.crt" -CAkey "$d/ca.key" -CAcreateserial \
        -days 3650 -extfile "$d/$who.ext" -out "$d/$who.crt" 2>/dev/null
      rm -f "$d/$who.csr" "$d/$who.ext"
    done
    openvpn --genkey secret "$d/tc.key" 2>/dev/null || openvpn --genkey --secret "$d/tc.key"
  fi
  mkdir -p /etc/openvpn/server
  cat >/etc/openvpn/server/albatross.conf <<EOF
port $P_OVPN
proto tcp
dev tun
topology subnet
server 10.9.0.0 255.255.255.0
push "redirect-gateway def1 bypass-dhcp"
push "dhcp-option DNS 1.1.1.1"
push "dhcp-option DNS 8.8.8.8"
ca $d/ca.crt
cert $d/server.crt
key $d/server.key
dh none
ecdh-curve prime256v1
tls-crypt $d/tc.key
tls-version-min 1.2
cipher AES-256-GCM
data-ciphers AES-256-GCM:AES-128-GCM
auth SHA256
keepalive 10 60
persist-key
persist-tun
user nobody
group nogroup
verb 3
explicit-exit-notify 0
EOF
  echo "net.ipv4.ip_forward = 1" >/etc/sysctl.d/99-albatross.conf
  sysctl -q -w net.ipv4.ip_forward=1
  local dev; dev="$(ip -4 route show default | awk '{print $5; exit}')"
  iptables -t nat -C POSTROUTING -s 10.9.0.0/24 -o "$dev" -j MASQUERADE 2>/dev/null ||
    iptables -t nat -A POSTROUTING -s 10.9.0.0/24 -o "$dev" -j MASQUERADE
  systemctl enable --now openvpn-server@albatross >/dev/null 2>&1
  systemctl restart openvpn-server@albatross
}

# ssh_setup adds a user for tunnelling only: a password, port forwarding,
# no terminal (the app's sing-box opens an SSH session and carries the
# traffic through its port forwarding). The host key goes in the link so
# the app verifies the server.
ssh_setup() {
  id -u tunnel >/dev/null 2>&1 || useradd --system --no-create-home --shell /usr/sbin/nologin tunnel
  echo "tunnel:$SSH_PASS" | chpasswd
  mkdir -p /etc/ssh/sshd_config.d
  cat >/etc/ssh/sshd_config.d/albatross-tunnel.conf <<EOF
Match User tunnel
    PasswordAuthentication yes
    KbdInteractiveAuthentication no
    AllowTcpForwarding yes
    PermitTTY no
    X11Forwarding no
    AllowAgentForwarding no
    PermitTunnel no
EOF
  sshd -t && (systemctl reload ssh 2>/dev/null || systemctl reload sshd 2>/dev/null || true)
}

# ssh_hostkey prints the server's ed25519 host key as the link carries it.
ssh_hostkey() { awk '{print $1" "$2}' /etc/ssh/ssh_host_ed25519_key.pub 2>/dev/null; }

# ovpn_link prints the OpenVPN server as the app's own link form: the
# node as JSON, base64url, after "albatross://" (internal/node Link).
ovpn_link() {
  local h="$1" d="$OUT/ovpn"
  [ -s "$d/ca.crt" ] && [ -s "$d/client.crt" ] && [ -s "$d/client.key" ] && [ -s "$d/tc.key" ] || return 0
  python3 - "$h" "$P_OVPN" "$d" <<'PY'
import base64, json, sys
host, port, d = sys.argv[1], int(sys.argv[2]), sys.argv[3]
def lines(name):
    return [l for l in open(f"{d}/{name}").read().replace("\r", "").split("\n") if l.strip() and not l.startswith("#")]
node = {"proto": "openvpn", "name": "Albatross-OpenVPN", "server": host, "port": port,
        "ovpn": {"network": "tcp", "ca": lines("ca.crt"), "cert": lines("client.crt"), "key": lines("client.key"),
                 "tls_crypt": lines("tc.key"), "cipher": "AES-256-GCM", "auth": "SHA256",
                 "data_ciphers": ["AES-256-GCM", "AES-128-GCM"]}}
print("albatross://" + base64.urlsafe_b64encode(json.dumps(node, separators=(",", ":")).encode()).decode().rstrip("="))
PY
}

links() {
  local h="$1" n="Albatross"
  local hop; hop="${HOP_RANGE/:/-}"
  cat <<EOF
vless://$UUID@$h:$P_REALITY?security=reality&sni=$REALITY_DEST&fp=chrome&pbk=$REALITY_PUBLIC&sid=$SHORT_ID&flow=xtls-rprx-vision&type=tcp#$n-Reality
vless://$UUID@$h:$P_XHTTP?security=reality&sni=$REALITY_DEST&fp=chrome&pbk=$REALITY_PUBLIC&sid=$SHORT_ID&type=xhttp&path=$(urlenc "$XHTTP_PATH")&mode=auto#$n-XHTTP
trojan://$PASS@$DOMAIN:$P_TROJAN?security=tls&sni=$DOMAIN&fp=chrome&type=tcp#$n-Trojan
vless://$UUID@$DOMAIN:$P_WS?security=tls&sni=$DOMAIN&fp=chrome&type=ws&host=$DOMAIN&path=$(urlenc "$WS_PATH")#$n-WS
vless://$UUID@$DOMAIN:$P_GRPC?security=tls&sni=$DOMAIN&fp=chrome&type=grpc&serviceName=$GRPC_NAME#$n-gRPC
$(vmess_link "$DOMAIN" "$P_UPGRADE" tls Albatross-HTTPUpgrade)
$(vmess_link "$h" "$P_HTTP" "" Albatross-HTTP80)
$(vmess_link "$h" "$P_TCP53" "" Albatross-TCP53)
$(vmess_link "$h" "$P_ICMP" "" Albatross-ICMP icmp)
$(dns_links "$h")
anytls://$PASS@$DOMAIN:$P_ANYTLS?sni=$DOMAIN#$n-AnyTLS
ss://$(printf '%s' "2022-blake3-aes-256-gcm:$SS_KEY" | base64 -w0 | tr '+/' '-_' | tr -d '=')@$h:$P_SS#$n-SS2022
ss://$(printf '%s' "2022-blake3-aes-256-gcm:$SS_KEY" | base64 -w0 | tr '+/' '-_' | tr -d '=')@$h:$P_STLS?plugin=$(urlenc "shadow-tls;host=$REALITY_DEST;password=$STLS_PASS;version=3")#$n-ShadowTLS
hysteria2://$PASS@$DOMAIN:$P_HY2?sni=$DOMAIN&obfs=salamander&obfs-password=$OBFS&mport=$([ -n "$DNS_TUNNEL" ] || printf '%s,' "$P_HY2_DNS")$hop#$n-Hysteria2
tuic://$UUID:$PASS@$DOMAIN:$P_TUIC?sni=$DOMAIN&alpn=h3&congestion_control=bbr#$n-TUIC
$([ "$DRY_RUN" = 1 ] || printf 'ssh://tunnel:%s@%s:22?hk=%s#%s-SSH\n' "$SSH_PASS" "$h" "$(urlenc "$(ssh_hostkey)")" "$n")
$(ovpn_link "$h")
trojan-go://$PASS@$DOMAIN:$P_TGO?security=tls&sni=$DOMAIN&type=ws&host=$DOMAIN&path=$(urlenc "$WS_PATH")#$n-TrojanGo
$(cdn_links)
$(sidecar_links "$h")
$(v6_links)
EOF
}

# decoy_setup serves a plain web page on the loopback: what a probe of
# the Trojan port gets instead of a closed connection (sing-box's
# fallback), and what NaiveProxy's Caddy shows to a browser. A proxy
# server that answers like a web site is one marker fewer for scanners.
decoy_setup() {
  mkdir -p /var/www/albatross-decoy
  [ -s /var/www/albatross-decoy/index.html ] || cat >/var/www/albatross-decoy/index.html <<'EOF'
<!doctype html>
<html lang="en"><head><meta charset="utf-8"><title>Welcome</title>
<meta name="viewport" content="width=device-width,initial-scale=1">
<style>body{font-family:system-ui,sans-serif;margin:0;display:flex;min-height:100vh;align-items:center;justify-content:center;background:#f6f7f9;color:#222}main{text-align:center;padding:2rem}h1{font-weight:600}p{color:#666}</style>
</head><body><main><h1>Welcome</h1><p>This site is being set up. Please check back later.</p></main></body></html>
EOF
  cat >/etc/systemd/system/albatross-decoy.service <<EOF
[Unit]
Description=Albatross server (decoy web page)
After=network-online.target
[Service]
ExecStart=/usr/bin/python3 -m http.server $P_DECOY --bind 127.0.0.1 --directory /var/www/albatross-decoy
Restart=always
RestartSec=3
DynamicUser=yes
[Install]
WantedBy=multi-user.target
EOF
  systemctl daemon-reload
  systemctl enable --now albatross-decoy >/dev/null 2>&1
  systemctl restart albatross-decoy
}

# v6_links prints, when the machine has a public IPv6 address, the server
# at that address too (the TLS protocols keep the name as SNI): a second
# address, which the filtering lists separately and which the mobile
# networks here reach.
v6_links() {
  [ -n "$IP6" ] || return 0
  local h="[$IP6]" n="Albatross-v6"
  echo "vless://$UUID@$h:$P_REALITY?security=reality&sni=$REALITY_DEST&fp=chrome&pbk=$REALITY_PUBLIC&sid=$SHORT_ID&flow=xtls-rprx-vision&type=tcp#$n-Reality"
  echo "trojan://$PASS@$h:$P_TROJAN?security=tls&sni=$DOMAIN&fp=chrome&type=tcp#$n-Trojan"
  echo "hysteria2://$PASS@$h:$P_HY2?sni=$DOMAIN&obfs=salamander&obfs-password=$OBFS#$n-Hysteria2"
  echo "ss://$(printf '%s' "2022-blake3-aes-256-gcm:$SS_KEY" | base64 -w0 | tr '+/' '-_' | tr -d '=')@$h:$P_SS#$n-SS2022"
  vmess_link "$IP6" "$P_HTTP" "" "$n-HTTP80"
  [ "$DRY_RUN" = 1 ] || printf 'ssh://tunnel:%s@%s:22?hk=%s#%s-SSH\n' "$SSH_PASS" "$h" "$(urlenc "$(ssh_hostkey)")" "$n"
  [ "$SIDECARS" = 1 ] && echo "mieru://albatross:$PASS@$h:$P_MIERU?protocol=tcp&mtu=1400#$n-Mieru"
  awg_link "$h"
}

# cdn_links prints, when CDN_HOST is set, the links that go through
# Cloudflare's edge: the same WebSocket and HTTPUpgrade servers, reached
# at the CDN's addresses (the app picks edge addresses that answer from
# inside the country), so this machine's own address is never in the
# connection and blocking it changes nothing for them. Not gRPC: the edge
# carries it on port 443 alone, which REALITY holds here.
cdn_links() {
  [ -n "$CDN_HOST" ] || return 0
  local c="$CDN_HOST" n="Albatross-CDN"
  echo "vless://$UUID@$c:$P_WS?security=tls&sni=$c&fp=chrome&type=ws&host=$c&path=$(urlenc "$WS_PATH")#$n-WS"
  vmess_link "$c" "$P_UPGRADE" tls "$n-HTTPUpgrade"
  echo "trojan://$PASS@$c:$P_TGO?security=tls&sni=$c&fp=chrome&type=ws&host=$c&path=$(urlenc "$WS_PATH")#$n-TrojanWS"
}

# sidecar_links prints the links of the sidecar protocols that are set up.
sidecar_links() {
  local h="$1" n="Albatross"
  [ "$SIDECARS" = 1 ] || return 0
  echo "naive+https://albatross:$PASS@$DOMAIN:$P_NAIVE#$n-Naive"
  echo "juicity://$UUID:$PASS@$DOMAIN:$P_JUICITY?congestion_control=bbr&sni=$DOMAIN#$n-Juicity"
  echo "mieru://albatross:$PASS@$h:$P_MIERU?protocol=tcp&mtu=1400#$n-Mieru"
  echo "brook://wssserver?wssserver=$(urlenc "wss://$DOMAIN:$P_BROOK/ws")&password=$PASS&name=$n-Brook"
  echo "wstunnel://$DOMAIN:$P_WST?path=$WST_PATH#$n-WStunnel"
  awg_link "$h"
}

# sidecar_setup installs and runs the server side of the sidecar
# protocols. Each has its own program; Go builds the ones that publish no
# server binary for this machine (AmneziaWG, and NaiveProxy's Caddy off
# x86-64).
sidecar_setup() {
  local a; a="$(arch)"
  DEBIAN_FRONTEND=noninteractive apt-get install -y -qq git build-essential xz-utils ocserv >/dev/null
  local tmp; tmp="$(mktemp -d)"
  if [ ! -x /usr/local/go/bin/go ] || ! /usr/local/go/bin/go version | grep -q "go$GO_VERSION "; then
    curl -fsSL -o "$tmp/go.tgz" "https://go.dev/dl/go$GO_VERSION.linux-$a.tar.gz"
    rm -rf /usr/local/go && tar -C /usr/local -xzf "$tmp/go.tgz"
  fi
  # The builds below (Caddy, AmneziaWG) need more memory than a small
  # machine has: a swap file, and one compile at a time.
  if [ ! -e /swapfile ] && [ "$(swapon --noheadings --show 2>/dev/null | wc -l)" = 0 ]; then
    # Listed in fstab only once it is in use: a swap file that could not
    # be made would otherwise fail at every boot.
    if fallocate -l 2G /swapfile && chmod 600 /swapfile && mkswap /swapfile >/dev/null && swapon /swapfile; then
      grep -q '^/swapfile ' /etc/fstab || echo '/swapfile none swap sw 0 0' >>/etc/fstab
    else
      rm -f /swapfile
    fi
  fi
  # Go's caches in fixed places: run from a service or a cron job there is
  # no HOME, without which go finds none and builds nothing.
  export PATH="/usr/local/go/bin:/root/go/bin:$PATH" GOFLAGS="-buildvcs=false -p=1" GOTOOLCHAIN=auto GOGC=40 \
    GOPATH=/root/go GOCACHE=/root/.cache/go-build
  # NaiveProxy's server: the release on x86-64, else built (see the pins).
  if [ ! -x /usr/local/bin/caddy-naive ]; then
    if [ "$a" = amd64 ]; then
      curl -fsSL -o "$tmp/naive.tar.xz" "https://github.com/klzgrad/forwardproxy/releases/download/$NAIVE_RELEASE/caddy-forwardproxy-naive.tar.xz"
      echo "$NAIVE_SHA256  $tmp/naive.tar.xz" | sha256sum -c --quiet -
      tar -xJf "$tmp/naive.tar.xz" -C "$tmp" caddy-forwardproxy-naive/caddy
      install -m 755 "$tmp/caddy-forwardproxy-naive/caddy" /usr/local/bin/caddy-naive
    else
      go install "github.com/caddyserver/xcaddy/cmd/xcaddy@$XCADDY_VERSION" >/dev/null
      xcaddy build "$CADDY_VERSION" --with "github.com/caddyserver/forwardproxy=github.com/klzgrad/forwardproxy@$NAIVE_PROXY_REF" --output /usr/local/bin/caddy-naive >/dev/null
    fi
  fi
  cat >"$OUT/Caddyfile" <<EOF
{
  order forward_proxy before file_server
  auto_https off
  admin off
}
:$P_NAIVE {
  tls $CERT $KEY
  forward_proxy {
    basic_auth albatross $PASS
    hide_ip
    hide_via
    probe_resistance
  }
  file_server {
    root /var/www/albatross-decoy
  }
}
EOF
  # Juicity.
  local ja=x86_64; [ "$a" = arm64 ] && ja=arm64
  curl -fsSL -o "$tmp/j.zip" "https://github.com/juicity/juicity/releases/download/v$JUICITY_VERSION/juicity-linux-$ja.zip"
  unzip -q -o "$tmp/j.zip" juicity-server -d "$tmp"
  install -m 755 "$tmp/juicity-server" /usr/local/bin/juicity-server
  cat >"$OUT/juicity.json" <<EOF
{"listen": ":$P_JUICITY", "users": {"$UUID": "$PASS"}, "certificate": "$CERT", "private_key": "$KEY",
 "congestion_control": "bbr", "log_level": "warn", "disable_outbound_udp443": true}
EOF
  # Mieru (its server is mita, a package of its own with a service).
  curl -fsSL -o "$tmp/mita.deb" "https://github.com/enfein/mieru/releases/download/v$MIERU_VERSION/mita_${MIERU_VERSION}_$a.deb"
  DEBIAN_FRONTEND=noninteractive dpkg -i "$tmp/mita.deb" >/dev/null
  cat >"$OUT/mita.json" <<EOF
{"portBindings": [{"port": $P_MIERU, "protocol": "TCP"}], "users": [{"name": "albatross", "password": "$PASS"}],
 "loggingLevel": "WARN", "mtu": 1400, "dns": {"dualStack": "PREFER_IPv4"}}
EOF
  systemctl enable --now mita >/dev/null 2>&1 || true
  sleep 2
  mita apply config "$OUT/mita.json"
  mita stop >/dev/null 2>&1 || true
  mita start
  # Brook.
  curl -fsSL -o "$tmp/brook" "https://github.com/txthinking/brook/releases/download/v$BROOK_VERSION/brook_linux_$a"
  install -m 755 "$tmp/brook" /usr/local/bin/brook
  # WStunnel.
  curl -fsSL -o "$tmp/wst.tgz" "https://github.com/erebe/wstunnel/releases/download/v$WSTUNNEL_VERSION/wstunnel_${WSTUNNEL_VERSION}_linux_$a.tar.gz"
  tar -xzf "$tmp/wst.tgz" -C "$tmp" wstunnel
  install -m 755 "$tmp/wstunnel" /usr/local/bin/wstunnel
  # AmneziaWG: the userspace implementation and its tools, from source.
  if [ ! -x /usr/local/bin/amneziawg-go ]; then
    git clone -q --depth 1 --branch "$AWG_GO_REF" https://github.com/amnezia-vpn/amneziawg-go "$tmp/awg-go"
    (cd "$tmp/awg-go" && go build -trimpath -o /usr/local/bin/amneziawg-go .)
  fi
  if ! command -v awg >/dev/null; then
    git clone -q --depth 1 --branch "$AWG_TOOLS_REF" https://github.com/amnezia-vpn/amneziawg-tools "$tmp/awg-tools"
    make -s -C "$tmp/awg-tools/src" >/dev/null && make -s -C "$tmp/awg-tools/src" install >/dev/null
  fi
  awg_setup
  openconnect_setup
  rm -rf "$tmp"
  sidecar_services
}

# awg_setup writes the AmneziaWG interface: one peer (the app), the junk
# and header parameters picked once and kept with the keys.
awg_setup() {
  local d="$OUT/awg"
  mkdir -p "$d" && chmod 700 "$d"
  if [ ! -s "$d/params.env" ]; then
    # Keys are private from the moment they are written (awg warns of a
    # key file others could read).
    (umask 077
      awg genkey >"$d/server.key"; awg pubkey <"$d/server.key" >"$d/server.pub"
      awg genkey >"$d/client.key"; awg pubkey <"$d/client.key" >"$d/client.pub")
    python3 - >"$d/params.env" <<'PY2'
import random
r = random.SystemRandom()
h = r.sample(range(5, 2**31), 4)
print(f"JC={r.randint(4, 8)}\nJMIN=40\nJMAX=70\nS1={r.randint(15, 60)}\nS2={r.randint(15, 60)}")
print("H1=%d\nH2=%d\nH3=%d\nH4=%d" % tuple(h))
PY2
  fi
  # shellcheck disable=SC1090
  . "$d/params.env"
  local dev; dev="$(ip -4 route show default | awk '{print $5; exit}')"
  mkdir -p /etc/amnezia/amneziawg
  cat >/etc/amnezia/amneziawg/awg0.conf <<EOF
[Interface]
PrivateKey = $(cat "$d/server.key")
Address = 10.10.0.1/24
ListenPort = $P_AWG
Jc = $JC
Jmin = $JMIN
Jmax = $JMAX
S1 = $S1
S2 = $S2
H1 = $H1
H2 = $H2
H3 = $H3
H4 = $H4
PostUp = iptables -t nat -A POSTROUTING -s 10.10.0.0/24 -o $dev -j MASQUERADE
PostDown = iptables -t nat -D POSTROUTING -s 10.10.0.0/24 -o $dev -j MASQUERADE

[Peer]
PublicKey = $(cat "$d/client.pub")
AllowedIPs = 10.10.0.2/32
EOF
  chmod 600 /etc/amnezia/amneziawg/awg0.conf
  echo "net.ipv4.ip_forward = 1" >/etc/sysctl.d/99-albatross.conf
  sysctl -q -w net.ipv4.ip_forward=1
}

# awg_link prints the AmneziaWG link: the client key, the server's public
# key and the parameters the interface uses.
awg_link() {
  local d="$OUT/awg"
  [ -s "$d/params.env" ] && [ -s "$d/client.key" ] || return 0
  # shellcheck disable=SC1090
  . "$d/params.env"
  printf 'amneziawg://%s@%s:%s?publickey=%s&address=10.10.0.2%%2F32&mtu=1280&jc=%s&jmin=%s&jmax=%s&s1=%s&s2=%s&h1=%s&h2=%s&h3=%s&h4=%s#Albatross-AmneziaWG\n' \
    "$(urlenc "$(cat "$d/client.key")")" "$1" "$P_AWG" "$(urlenc "$(cat "$d/server.pub")")" "$JC" "$JMIN" "$JMAX" "$S1" "$S2" "$H1" "$H2" "$H3" "$H4"
}

# openconnect_setup runs ocserv (AnyConnect) with a password user. The app
# has no OpenConnect client (it needs a system tunnel the phone does not
# give); the link in links-external.txt is for the official clients.
openconnect_setup() {
  cat >/etc/ocserv/ocserv.conf <<EOF
auth = "plain[passwd=/etc/ocserv/ocpasswd]"
tcp-port = $P_OC
udp-port = $P_OC
run-as-user = ocserv
run-as-group = ocserv
socket-file = /run/ocserv.socket
server-cert = $CERT
server-key = $KEY
isolate-workers = true
max-clients = 0
max-same-clients = 0
keepalive = 32400
dpd = 90
mobile-dpd = 1800
switch-to-tcp-timeout = 25
try-mtu-discovery = true
cert-user-oid = 0.9.2342.19200300.100.1.1
tls-priorities = "NORMAL:%SERVER_PRECEDENCE:%COMPAT:-VERS-SSL3.0:-VERS-TLS1.0:-VERS-TLS1.1"
auth-timeout = 240
min-reauth-time = 300
max-ban-score = 80
ban-reset-time = 300
cookie-timeout = 300
deny-roaming = false
rekey-time = 172800
rekey-method = ssl
use-occtl = true
pid-file = /run/ocserv.pid
log-level = 1
device = vpns
predictable-ips = true
default-domain = $DOMAIN
ipv4-network = 10.11.0.0
ipv4-netmask = 255.255.255.0
tunnel-all-dns = true
dns = 1.1.1.1
dns = 8.8.8.8
ping-leases = false
cisco-client-compat = true
dtls-legacy = true
EOF
  printf '%s\n%s\n' "$PASS" "$PASS" | ocpasswd -c /etc/ocserv/ocpasswd albatross
  local dev; dev="$(ip -4 route show default | awk '{print $5; exit}')"
  iptables -t nat -C POSTROUTING -s 10.11.0.0/24 -o "$dev" -j MASQUERADE 2>/dev/null ||
    iptables -t nat -A POSTROUTING -s 10.11.0.0/24 -o "$dev" -j MASQUERADE
  echo "openconnect://albatross:$PASS@$DOMAIN:$P_OC" >"$OUT/links-external.txt"
  chmod 600 "$OUT/links-external.txt"
}

# sidecar_services writes and starts one unit per sidecar program.
sidecar_services() {
  unit() { # name description command
    cat >"/etc/systemd/system/albatross-$1.service" <<EOF
[Unit]
Description=Albatross server ($2)
After=network-online.target
[Service]
ExecStart=$3
Restart=always
RestartSec=3
LimitNOFILE=1048576
[Install]
WantedBy=multi-user.target
EOF
  }
  unit naive "NaiveProxy, Caddy" "/usr/local/bin/caddy-naive run --config $OUT/Caddyfile --adapter caddyfile"
  unit juicity "Juicity" "/usr/local/bin/juicity-server run -c $OUT/juicity.json"
  unit brook "Brook" "/usr/local/bin/brook wssserver --domainaddress $DOMAIN:$P_BROOK --cert $CERT --certkey $KEY --password $PASS"
  unit wstunnel "WStunnel" "/usr/local/bin/wstunnel server --restrict-http-upgrade-path-prefix $WST_PATH --tls-certificate $CERT --tls-private-key $KEY wss://[::]:$P_WST"
  systemctl daemon-reload
  systemctl enable --now albatross-naive albatross-juicity albatross-brook albatross-wstunnel awg-quick@awg0 ocserv >/dev/null 2>&1
  systemctl restart albatross-naive albatross-juicity albatross-brook albatross-wstunnel awg-quick@awg0 ocserv
}

# dns_links prints one DNS tunnel link per tunnel name.
dns_links() {
  [ -n "$DNS_TUNNEL" ] || return 0
  local n i=0
  for n in ${DNS_TUNNEL//,/ }; do
    i=$((i + 1))
    vmess_link "$1" 53 "" "Albatross-DNS$([ "$i" = 1 ] || echo "$i")" "dns:$n"
  done
}

urlenc() { python3 -c 'import sys,urllib.parse;print(urllib.parse.quote(sys.argv[1],safe=""))' "$1"; }

# vmess_link host port tls|"" name [icmp]. Without TLS the content is still
# encrypted end to end by VMess's AEAD; only the outer layer is plain HTTP
# (or, for icmp, mKCP inside ICMP echo packets, which the app's Xray reads
# through the fm field).
vmess_link() {
  python3 - "$UUID" "$1" "$2" "$WS_PATH" "$3" "$4" "${5:-}" "$KCP_SEED" <<'PY'
import base64, json, sys
uuid, host, port, path, tls, name, carrier, seed = sys.argv[1:]
v = {"v": "2", "ps": name, "add": host, "port": port, "id": uuid, "aid": "0",
     "scy": "aes-128-gcm", "net": "httpupgrade", "host": host, "path": path, "tls": tls}
if carrier == "icmp":
    v.update(net="kcp", type="none", path=seed, host="", fm={"udp": [{"type": "xicmp"}]})
elif carrier.startswith("dns:"):
    v.update(net="kcp", type="none", path=seed, host="",
             fm={"udp": [{"type": "xdns", "settings": {"domains": [{"names": [carrier[4:]], "edns0": 1232}]}}]})
if tls:
    v.update(sni=host, fp="chrome")
print("vmess://" + base64.b64encode(json.dumps(v, separators=(",", ":")).encode()).decode())
PY
}

# relay_rules writes the forwarding rules for the relay as a script that
# runs now and at every boot. The rules live in chains of their own, so
# the script can be run again with a new target (the watchdog does, when
# a server stops answering): it empties the chains and fills them for the
# target in $OUT/relay.target.
relay_rules() {
  local f="$OUT/relay.sh" p
  mkdir -p "$OUT"
  {
    echo '#!/bin/sh'
    echo "to=\$(cat $OUT/relay.target)"
    echo 'sysctl -qw net.ipv4.ip_forward=1'
    echo 'for t in "nat PREROUTING" "nat POSTROUTING" "filter FORWARD"; do'
    echo '  set -- $t'
    echo '  iptables -t "$1" -N "ALBATROSS_$2" 2>/dev/null || iptables -t "$1" -F "ALBATROSS_$2"'
    echo '  iptables -t "$1" -C "$2" -j "ALBATROSS_$2" 2>/dev/null || iptables -t "$1" -I "$2" -j "ALBATROSS_$2"'
    echo 'done'
    echo 'iptables -t nat -A ALBATROSS_POSTROUTING -d "$to" -j MASQUERADE'
    echo 'iptables -A ALBATROSS_FORWARD -m conntrack --ctstate RELATED,ESTABLISHED -j ACCEPT'
    # Every port the server's links use, the sidecar protocols' too; not
    # 22, which is this machine's own SSH (relay_links leaves that link out).
    for p in $P_REALITY $P_XHTTP $P_TROJAN $P_WS $P_GRPC $P_UPGRADE $P_ANYTLS $P_SS $P_STLS $P_TCP53 $P_HTTP \
      $P_OVPN $P_TGO $P_NAIVE $P_MIERU $P_BROOK $P_WST $P_OC; do
      echo "iptables -t nat -A ALBATROSS_PREROUTING -p tcp --dport $p -j DNAT --to-destination \"\$to\""
      echo "iptables -A ALBATROSS_FORWARD -p tcp -d \"\$to\" --dport $p -j ACCEPT"
    done
    for p in $P_SS $P_HY2 $P_TUIC $P_JUICITY $P_AWG $P_OC 53 $HOP_RANGE; do
      echo "iptables -t nat -A ALBATROSS_PREROUTING -p udp --dport $p -j DNAT --to-destination \"\$to\""
      echo "iptables -A ALBATROSS_FORWARD -p udp -d \"\$to\" --dport $p -j ACCEPT"
    done
    # The ICMP carrier: echo requests go on to the server, its replies come back.
    echo 'iptables -t nat -A ALBATROSS_PREROUTING -p icmp --icmp-type echo-request -j DNAT --to-destination "$to"'
    echo 'iptables -A ALBATROSS_FORWARD -p icmp -d "$to" -j ACCEPT'
  } > "$f"
  chmod 755 "$f"
  relay_watchdog
  if [ "$DRY_RUN" != 1 ]; then
    cat > /etc/systemd/system/albatross-relay.service <<EOF2
[Unit]
Description=Albatross relay: forwards every Albatross port to the server in $OUT/relay.target
After=network-online.target
Wants=network-online.target

[Service]
Type=oneshot
ExecStart=$f
RemainAfterExit=yes

[Install]
WantedBy=multi-user.target
EOF2
    cat > /etc/systemd/system/albatross-relay-watch.service <<EOF2
[Unit]
Description=Albatross relay watchdog: moves the forwarding to the server that answers

[Service]
Type=oneshot
ExecStart=$OUT/relay-watch.sh
EOF2
    cat > /etc/systemd/system/albatross-relay-watch.timer <<EOF2
[Unit]
Description=Albatross relay watchdog, every minute

[Timer]
OnBootSec=2min
OnUnitActiveSec=1min

[Install]
WantedBy=timers.target
EOF2
    systemctl daemon-reload
    systemctl enable --now albatross-relay.service
    systemctl restart albatross-relay.service
    systemctl enable --now albatross-relay-watch.timer
  fi
}

# relay_watchdog writes the script the timer runs: it checks that the
# current server answers on its Reality port (three misses in a row count)
# and, when it does not and another listed server does, points the rules
# at that one. The first server listed is preferred and taken back as soon
# as it answers again.
relay_watchdog() {
  cat > "$OUT/relay-watch.sh" <<EOF
#!/bin/bash
# Albatross relay watchdog (written by install.sh).
out="$OUT"
servers="$RELAY_TO${RELAY_TO2:+ $RELAY_TO2}"
port=$P_REALITY
EOF
  cat >> "$OUT/relay-watch.sh" <<'EOF'
cur=$(cat "$out/relay.target")
alive() { timeout 4 bash -c "exec 3<>/dev/tcp/$1/$port" 2>/dev/null; }
miss="$out/relay.miss"
if alive "$cur"; then
  echo 0 >"$miss"
else
  n=$(($(cat "$miss" 2>/dev/null || echo 0) + 1)); echo "$n" >"$miss"
fi
down=$([ "$(cat "$miss")" -ge 3 ] && echo 1 || echo 0)
for s in $servers; do
  if [ "$s" = "$cur" ]; then
    # The current server is the preferred one still answering, or the
    # best of the rest: stay.
    [ "$down" = 1 ] || exit 0
    continue
  fi
  if alive "$s"; then
    echo "$s" >"$out/relay.target"; echo 0 >"$miss"
    "$out/relay.sh" && logger -t albatross-relay "forwarding moved to $s"
    exit 0
  fi
done
EOF
  chmod 755 "$OUT/relay-watch.sh"
}

# relay_links rewrites the server's links to point at this machine: the
# address changes, the names used for TLS (sni, host) stay the server's.
# The SSH link is left out: port 22 here is this machine's own.
relay_links() {
  local here="$1" src="$2" script
  # The script is passed as an argument: the links come in on stdin.
  script="$(cat <<'PY'
import base64, json, re, sys
from urllib.parse import parse_qs, urlencode
here = sys.argv[1]
# Schemes whose TLS name is the link's host unless it names one (sni=).
tls_by_default = ("trojan", "trojan-go", "anytls", "hysteria2", "tuic", "juicity", "wstunnel", "naive+https", "naive+quic")
for line in sys.stdin:
    line = line.strip()
    if not line or line.startswith("ssh://"):
        continue
    if line.startswith("albatross://"):
        # A node as JSON (OpenVPN): its server is this machine.
        b = line[12:]
        v = json.loads(base64.urlsafe_b64decode(b + "=" * (-len(b) % 4)))
        v["server"] = here
        v["name"] = v.get("name", "") + "-relay"
        print("albatross://" + base64.urlsafe_b64encode(json.dumps(v, separators=(",", ":")).encode()).decode().rstrip("="))
        continue
    if line.startswith("brook://"):
        # Brook names its server inside a parameter; address= sends the
        # connection here and keeps the name for TLS.
        kind, _, qs = line[8:].partition("?")
        q = {k: v[0] for k, v in parse_qs(qs, keep_blank_values=True).items()}
        hp = re.match(r"^wss?://([^/]+)", q.get(kind, ""))
        if not hp or ":" not in hp.group(1):
            continue
        q["address"] = here + ":" + hp.group(1).rsplit(":", 1)[1]
        q["name"] = q.get("name", "") + "-relay"
        print("brook://" + kind + "?" + urlencode(q))
        continue
    if line.startswith("vmess://"):
        v = json.loads(base64.b64decode(line[8:] + "=" * (-len(line[8:]) % 4)))
        if v.get("tls") and not v.get("sni"):
            v["sni"] = v["add"]
        if not v.get("tls") and v.get("host") == v["add"]:
            # Plain HTTP carries the address as its Host header: a foreign
            # one would give the relay away.
            v["host"] = here
        v["add"] = here
        v["ps"] = v.get("ps", "") + "-relay"
        print("vmess://" + base64.b64encode(json.dumps(v, separators=(",", ":")).encode()).decode())
        continue
    m = re.match(r"^([a-z0-9+.-]+://(?:[^@/]*@)?)([^:/?#]+)(:\d+)(.*)$", line)
    if not m:
        continue
    head, host, port, rest = m.groups()
    scheme = head.split("://", 1)[0]
    if ("security=tls" in rest or scheme in tls_by_default) and "sni=" not in rest:
        if rest.startswith("?"):
            rest = "?sni=" + host + "&" + rest[1:]
        else:
            rest = "?sni=" + host + rest
    rest = re.sub(r"#(.*)$", lambda mm: "#" + mm.group(1) + "-relay", rest) if "#" in rest else rest + "#relay"
    print(head + here + port + rest)
PY
)"
  case "$src" in
    https://*) curl -fsSL "$src" ;;
    *) cat "$src" ;;
  esac | python3 -c "$script" "$here"
}

relay_main() {
  need_root
  IP="${IP:-$(public_ip)}"
  log "Relay $IP -> $RELAY_TO${RELAY_TO2:+ (then $RELAY_TO2)}"
  mkdir -p "$OUT"
  # A rerun keeps the server the watchdog settled on when it is still listed.
  if [ ! -s "$OUT/relay.target" ] || ! printf '%s\n%s\n' "$RELAY_TO" "${RELAY_TO2:-}" | grep -qx "$(cat "$OUT/relay.target")"; then
    echo "$RELAY_TO" >"$OUT/relay.target"
  fi
  relay_rules
  [ "$DRY_RUN" = 1 ] || "$OUT/relay.sh"
  if [ -n "${LINKS:-}" ]; then
    relay_links "$IP" "$LINKS" | tee "$OUT/links.txt"
    [ "$DRY_RUN" = 1 ] || cp "$OUT/links.txt" /root/albatross-links.txt
    log "Done. The relay's links are in $OUT/links.txt; add them to the app's own list next to the server's."
  else
    log "Done. Run again with LINKS=/root/albatross-links.txt (the server's links file) to get this relay's links."
  fi
}

# mirror_main sets up the mirror: nginx serving $OUT/mirror under a secret
# path, and a timer that refreshes the copies from the account service.
mirror_main() {
  need_root
  : "${MIRROR_SOURCE:?set MIRROR_SOURCE to the account service, e.g. https://accounts.example.com}"
  MIRROR_SOURCE="${MIRROR_SOURCE%/}"
  IP="${IP:-$(public_ip)}"
  mkdir -p "$OUT/mirror/list/servers" "$OUT/mirror/list/main" "$OUT/mirror/list/rules" "$OUT/mirror/dl"
  local tf="$OUT/mirror.token"
  [ -s "$tf" ] || { head -c 12 /dev/urandom | od -An -tx1 | tr -d ' \n' >"$tf"; chmod 600 "$tf"; }
  local token; token="$(cat "$tf")"
  cat >"$OUT/mirror-sync.sh" <<EOF
#!/bin/sh
# Albatross mirror refresh (written by install.sh): copies the lists, the
# update manifest and the installers it names. A file that cannot be
# fetched is kept as it was.
src="$MIRROR_SOURCE"; out="$OUT/mirror"
EOF
  cat >>"$OUT/mirror-sync.sh" <<'EOF'
get() { curl -fsSL --retry 2 --max-time 120 "$src/$1" -o "$out/$1.tmp" && mv "$out/$1.tmp" "$out/$1" || rm -f "$out/$1.tmp"; }
# The lists go out sealed (.sealed, opened with the keys signed-in apps
# get); the plain names are copied too while the collector still writes them.
for f in hot.txt new.txt fresh.txt all-1.txt all-2.txt all-3.txt all-4.txt all-5.txt all-6.txt all-7.txt all-8.txt \
         own-0.bin own-1.bin own-2.bin own-3.bin own-4.bin; do
  get "list/servers/$f.sealed"
  get "list/servers/$f"
done
for f in tor.json meta.json reach.json crowd.json iran.json; do
  get "list/servers/$f"
done
get list/rules/ads.srs
get list/main/update.json
# The installers the manifest names, by file name, fetched when their
# checksum changed.
python3 - "$out" <<'PY'
import hashlib, json, os, subprocess, sys
out = sys.argv[1]
try:
    m = json.load(open(os.path.join(out, "list/main/update.json")))
except Exception:
    sys.exit(0)
def digest(p):
    h = hashlib.sha256()
    with open(p, "rb") as f:
        for chunk in iter(lambda: f.read(1 << 20), b""):
            h.update(chunk)
    return h.hexdigest()
for f in (m.get("files") or {}).values():
    urls = f.get("urls") or []
    want = (f.get("sha256") or "").lower()
    if not urls or not want:
        continue
    name = os.path.basename(urls[0].split("?")[0])
    if not name or name.startswith("."):
        continue
    dst = os.path.join(out, "dl", name)
    if os.path.exists(dst) and digest(dst) == want:
        continue
    for u in urls:
        if subprocess.call(["curl", "-fsSL", "--retry", "2", "--max-time", "900", u, "-o", dst + ".tmp"]) == 0 and digest(dst + ".tmp") == want:
            os.replace(dst + ".tmp", dst)
            break
    try:
        os.remove(dst + ".tmp")
    except OSError:
        pass
PY
EOF
  chmod 755 "$OUT/mirror-sync.sh"
  cat >"$OUT/mirror-nginx.conf" <<EOF
server {
    listen $MIRROR_PORT default_server;
    server_name _;
    access_log off;
    # Everything but the secret path is nothing.
    location / { return 404; }
    location /$token/ {
        alias $OUT/mirror/;
        autoindex off;
        types { application/octet-stream bin srs apk exe; application/json json; text/plain txt; }
        default_type application/octet-stream;
        add_header Cache-Control "no-cache";
    }
}
EOF
  if [ "$DRY_RUN" != 1 ]; then
    apt-get update -qq
    DEBIAN_FRONTEND=noninteractive apt-get install -y -qq nginx curl python3 iptables-persistent >/dev/null
    rm -f /etc/nginx/sites-enabled/default
    cp "$OUT/mirror-nginx.conf" /etc/nginx/sites-enabled/albatross-mirror.conf
    nginx -t && systemctl enable --now nginx && systemctl reload nginx
    iptables -C INPUT -p tcp --dport "$MIRROR_PORT" -j ACCEPT 2>/dev/null || iptables -I INPUT -p tcp --dport "$MIRROR_PORT" -j ACCEPT
    netfilter-persistent save >/dev/null 2>&1 || true
    cat >/etc/systemd/system/albatross-mirror.service <<EOF2
[Unit]
Description=Albatross mirror refresh
After=network-online.target

[Service]
Type=oneshot
ExecStart=$OUT/mirror-sync.sh
EOF2
    cat >/etc/systemd/system/albatross-mirror.timer <<EOF2
[Unit]
Description=Albatross mirror refresh, every ten minutes

[Timer]
OnBootSec=1min
OnUnitActiveSec=10min

[Install]
WantedBy=timers.target
EOF2
    systemctl daemon-reload
    systemctl enable --now albatross-mirror.timer
    systemctl start albatross-mirror.service || true
  fi
  echo "http://$IP:$MIRROR_PORT/$token" | tee "$OUT/mirror.txt"
  log "Done. Mirror address above (also in $OUT/mirror.txt): put it in the repository variable DOMESTIC_MIRROR."
}

main() {
  if [ "${MIRROR:-0}" = 1 ]; then
    mirror_main
    [ -n "${RELAY_TO:-}" ] || return
  fi
  if [ -n "${RELAY_TO:-}" ]; then
    relay_main
    return
  fi
  if [ "$DRY_RUN" != 1 ]; then
    log "Installing sing-box $SINGBOX_VERSION and Xray $XRAY_VERSION"
    install_cores
  fi
  IP="${IP:-$(public_ip)}"
  IP6="${IP6:-$(public_ip6)}"
  DOMAIN="${DOMAIN:-${IP//./-}.sslip.io}"
  log "Server address: $IP ($DOMAIN)${IP6:+, IPv6 $IP6}${CDN_HOST:+, through Cloudflare as $CDN_HOST}"
  secrets
  certificate
  singbox_config
  xray_config
  "$SB" check -c "$OUT/sing-box.json"
  "$XRAY" run -test -c "$OUT/xray.json" >/dev/null
  if [ "$DRY_RUN" != 1 ]; then
    free_port53
    openvpn_setup
    ssh_setup
    decoy_setup
    if [ "$SIDECARS" = 1 ]; then
      log "Installing the sidecar protocols (NaiveProxy, Juicity, Mieru, Brook, WStunnel, AmneziaWG, OpenConnect)"
      sidecar_setup
    fi
    firewall
    services
  fi
  links "$IP" | tee "$OUT/links.txt"
  [ "$DRY_RUN" = 1 ] || cp "$OUT/links.txt" /root/albatross-links.txt
  log "Done. The links are in $OUT/links.txt; see deploy/server/README.md for the next step."
}

main "$@"
