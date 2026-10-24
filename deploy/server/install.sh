#!/usr/bin/env bash
# Sets up one Albatross server: every protocol the app runs, on one
# Ubuntu machine (22.04 or 24.04, x86-64 or ARM, for example an Oracle
# Cloud "Always Free" VM). Run as root:
#
#   curl -fsSL https://raw.githubusercontent.com/cezomagoanlace29861-jpg/Albatross/main/deploy/server/install.sh | sudo bash
#
# Optional environment:
#   DOMAIN   a name pointing at this machine; default <ip>.sslip.io
#   EMAIL    for Let's Encrypt; default none
#   DRY_RUN  1 = only write the configs into $OUT (for tests)
#
# At the end it prints one share link per protocol and saves them in
# /root/albatross-links.txt. See deploy/server/README.md for what to do
# with them.
set -euo pipefail

SINGBOX_VERSION="${SINGBOX_VERSION:-1.14.3}"
XRAY_VERSION="${XRAY_VERSION:-26.9.30}"
DRY_RUN="${DRY_RUN:-0}"
OUT="${OUT:-/etc/albatross}"
REALITY_DEST="${REALITY_DEST:-www.microsoft.com}"

# Ports. Each protocol has its own so one being blocked leaves the rest.
P_REALITY=443        # VLESS + Reality + Vision (TCP)
P_XHTTP=8443         # VLESS + XHTTP + Reality (Xray, TCP)
P_TROJAN=2053        # Trojan + TLS
P_WS=2083            # VLESS + WebSocket + TLS
P_GRPC=2087          # VLESS + gRPC + TLS
P_UPGRADE=2096       # VMess + HTTPUpgrade + TLS
P_ANYTLS=8880        # AnyTLS
P_SS=8388            # Shadowsocks 2022 (TCP and UDP)
P_HY2=443            # Hysteria 2 + Salamander (UDP), hops over HOP_RANGE
HOP_RANGE="20000:40000"
P_TUIC=8444          # TUIC v5 (UDP)
P_HTTP=80            # VMess (AEAD) + HTTPUpgrade, no TLS: looks like plain HTTP
# UDP 53 also lands on Hysteria 2: in the June 2025 shutdown it was the
# only UDP port that still got out.
P_HY2_DNS=53

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
  DEBIAN_FRONTEND=noninteractive apt-get install -y -qq curl unzip openssl certbot iptables-persistent >/dev/null
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
    } >"$f"
    chmod 600 "$f"
  fi
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
  local args=(certonly --standalone --non-interactive --agree-tos -d "$DOMAIN" --keep-until-expiring)
  if [ -n "${EMAIL:-}" ]; then args+=(-m "$EMAIL"); else args+=(--register-unsafely-without-email); fi
  iptables -C INPUT -p tcp --dport 80 -j ACCEPT 2>/dev/null || iptables -I INPUT -p tcp --dport 80 -j ACCEPT
  certbot "${args[@]}"
  ln -sf "/etc/letsencrypt/live/$DOMAIN/fullchain.pem" "$CERT"
  ln -sf "/etc/letsencrypt/live/$DOMAIN/privkey.pem" "$KEY"
  mkdir -p /etc/letsencrypt/renewal-hooks/deploy
  printf '#!/bin/sh\nsystemctl restart albatross-sing-box albatross-xray\n' >/etc/letsencrypt/renewal-hooks/deploy/albatross
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
     "users": [{"password": "$PASS"}], $tls},
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
    {"type": "hysteria2", "tag": "hy2", "listen": "::", "listen_port": $P_HY2,
     "users": [{"password": "$PASS"}],
     "obfs": {"type": "salamander", "password": "$OBFS"}, $tls},
    {"type": "vmess", "tag": "http", "listen": "::", "listen_port": $P_HTTP,
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
  }],
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

firewall() {
  # Oracle's Ubuntu images reject everything but SSH in iptables; the
  # cloud's own security list must allow the same ports (see README).
  for p in $P_REALITY $P_XHTTP $P_TROJAN $P_WS $P_GRPC $P_UPGRADE $P_ANYTLS $P_SS 80; do
    iptables -C INPUT -p tcp --dport "$p" -j ACCEPT 2>/dev/null || iptables -I INPUT -p tcp --dport "$p" -j ACCEPT
  done
  for p in $P_SS $P_HY2 $P_TUIC $HOP_RANGE; do
    iptables -C INPUT -p udp --dport "$p" -j ACCEPT 2>/dev/null || iptables -I INPUT -p udp --dport "$p" -j ACCEPT
  done
  # Hysteria 2 port hopping: the whole range, and UDP 53, land on its one
  # port.
  for r in "$HOP_RANGE" "$P_HY2_DNS"; do
    iptables -C INPUT -p udp --dport "$r" -j ACCEPT 2>/dev/null || iptables -I INPUT -p udp --dport "$r" -j ACCEPT
    iptables -t nat -C PREROUTING -p udp --dport "$r" -j REDIRECT --to-ports "$P_HY2" 2>/dev/null ||
      iptables -t nat -A PREROUTING -p udp --dport "$r" -j REDIRECT --to-ports "$P_HY2"
  done
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
anytls://$PASS@$DOMAIN:$P_ANYTLS?sni=$DOMAIN#$n-AnyTLS
ss://$(printf '%s' "2022-blake3-aes-256-gcm:$SS_KEY" | base64 -w0 | tr '+/' '-_' | tr -d '=')@$h:$P_SS#$n-SS2022
hysteria2://$PASS@$DOMAIN:$P_HY2?sni=$DOMAIN&obfs=salamander&obfs-password=$OBFS&mport=$P_HY2_DNS,$hop#$n-Hysteria2
tuic://$UUID:$PASS@$DOMAIN:$P_TUIC?sni=$DOMAIN&alpn=h3&congestion_control=bbr#$n-TUIC
EOF
}

urlenc() { python3 -c 'import sys,urllib.parse;print(urllib.parse.quote(sys.argv[1],safe=""))' "$1"; }

# vmess_link host port tls|"" name. Without TLS the content is still
# encrypted end to end by VMess's AEAD; only the outer layer is plain HTTP.
vmess_link() {
  python3 - "$UUID" "$1" "$2" "$WS_PATH" "$3" "$4" <<'PY'
import base64, json, sys
uuid, host, port, path, tls, name = sys.argv[1:]
v = {"v": "2", "ps": name, "add": host, "port": port, "id": uuid, "aid": "0",
     "scy": "aes-128-gcm", "net": "httpupgrade", "host": host, "path": path, "tls": tls}
if tls:
    v.update(sni=host, fp="chrome")
print("vmess://" + base64.b64encode(json.dumps(v, separators=(",", ":")).encode()).decode())
PY
}

main() {
  if [ "$DRY_RUN" != 1 ]; then
    log "Installing sing-box $SINGBOX_VERSION and Xray $XRAY_VERSION"
    install_cores
  fi
  IP="${IP:-$(public_ip)}"
  DOMAIN="${DOMAIN:-${IP//./-}.sslip.io}"
  log "Server address: $IP ($DOMAIN)"
  secrets
  certificate
  singbox_config
  xray_config
  "$SB" check -c "$OUT/sing-box.json"
  "$XRAY" run -test -c "$OUT/xray.json" >/dev/null
  if [ "$DRY_RUN" != 1 ]; then
    firewall
    services
  fi
  links "$IP" | tee "$OUT/links.txt"
  [ "$DRY_RUN" = 1 ] || cp "$OUT/links.txt" /root/albatross-links.txt
  log "Done. The links are in $OUT/links.txt; see deploy/server/README.md for the next step."
}

main "$@"
