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
# MIRROR_PORT is where mirror mode serves; it is not among the relay's
# forwarded ports, so both can run on one machine.
MIRROR_PORT="${MIRROR_PORT:-8081}"

# Ports. Each protocol has its own so one being blocked leaves the rest.
P_REALITY=443        # VLESS + Reality + Vision (TCP)
P_XHTTP=8443         # VLESS + XHTTP + Reality (Xray, TCP)
P_TROJAN=2053        # Trojan + TLS
P_WS=2083            # VLESS + WebSocket + TLS
P_GRPC=2087          # VLESS + gRPC + TLS
P_UPGRADE=2096       # VMess + HTTPUpgrade + TLS
P_ANYTLS=8880        # AnyTLS
P_OVPN=2082          # OpenVPN over TCP (a certificate per client, tls-crypt)
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
  # Servers set up before the ICMP carrier get its seed added.
  grep -q "^KCP_SEED=" "$f" || echo "KCP_SEED=$("$SB" generate rand --hex 8)" >>"$f"
  grep -q "^STLS_PASS=" "$f" || echo "STLS_PASS=$("$SB" generate rand --hex 16)" >>"$f"
  grep -q "^SSH_PASS=" "$f" || echo "SSH_PASS=$("$SB" generate rand --hex 16)" >>"$f"
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
# TXT answers.
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
      "finalmask": {"udp": [{"type": "xdns", "settings": {"domains": [{"names": $(dns_names)}]}}]}
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
  for p in $P_REALITY $P_XHTTP $P_TROJAN $P_WS $P_GRPC $P_UPGRADE $P_ANYTLS $P_SS $P_STLS $P_TCP53 $P_OVPN 22 80; do
    iptables -C INPUT -p tcp --dport "$p" -j ACCEPT 2>/dev/null || iptables -I INPUT -p tcp --dport "$p" -j ACCEPT
  done
  for p in $P_SS $P_HY2 $P_TUIC $HOP_RANGE; do
    iptables -C INPUT -p udp --dport "$p" -j ACCEPT 2>/dev/null || iptables -I INPUT -p udp --dport "$p" -j ACCEPT
  done
  # Hysteria 2 port hopping: the whole range, and UDP 53, land on its one
  # port.
  local hy2ports="$HOP_RANGE $P_HY2_DNS"
  if [ -n "$DNS_TUNNEL" ]; then
    # UDP 53 serves the DNS tunnel; an older rule sending it to Hysteria goes.
    hy2ports="$HOP_RANGE"
    iptables -t nat -D PREROUTING -p udp --dport "$P_HY2_DNS" -j REDIRECT --to-ports "$P_HY2" 2>/dev/null || true
    iptables -C INPUT -p udp --dport 53 -j ACCEPT 2>/dev/null || iptables -I INPUT -p udp --dport 53 -j ACCEPT
  fi
  for r in $hy2ports; do
    iptables -C INPUT -p udp --dport "$r" -j ACCEPT 2>/dev/null || iptables -I INPUT -p udp --dport "$r" -j ACCEPT
    iptables -t nat -C PREROUTING -p udp --dport "$r" -j REDIRECT --to-ports "$P_HY2" 2>/dev/null ||
      iptables -t nat -A PREROUTING -p udp --dport "$r" -j REDIRECT --to-ports "$P_HY2"
  done
  # The ICMP carrier: echo requests must get in (the cloud's security
  # list too).
  iptables -C INPUT -p icmp --icmp-type echo-request -j ACCEPT 2>/dev/null || iptables -I INPUT -p icmp --icmp-type echo-request -j ACCEPT
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
EOF
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
             fm={"udp": [{"type": "xdns", "settings": {"domains": [{"names": [carrier[4:]]}]}}]})
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
    for p in $P_REALITY $P_XHTTP $P_TROJAN $P_WS $P_GRPC $P_UPGRADE $P_ANYTLS $P_SS $P_STLS $P_TCP53 $P_HTTP; do
      echo "iptables -t nat -A ALBATROSS_PREROUTING -p tcp --dport $p -j DNAT --to-destination \"\$to\""
      echo "iptables -A ALBATROSS_FORWARD -p tcp -d \"\$to\" --dport $p -j ACCEPT"
    done
    for p in $P_SS $P_HY2 $P_TUIC 53 $HOP_RANGE; do
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
relay_links() {
  local here="$1" src="$2" script
  # The script is passed as an argument: the links come in on stdin.
  script="$(cat <<'PY'
import base64, json, re, sys
here = sys.argv[1]
for line in sys.stdin:
    line = line.strip()
    if not line:
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
    if "security=tls" in rest and "sni=" not in rest:
        rest = re.sub(r"(\?|&)", lambda mm: mm.group(1) + "sni=" + host + "&", rest, count=1)
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
for f in hot.txt new.txt fresh.txt all-1.txt all-2.txt all-3.txt all-4.txt all-5.txt all-6.txt all-7.txt all-8.txt \
         tor.json meta.json crowd.json iran.json own-0.bin own-1.bin own-2.bin own-3.bin own-4.bin; do
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
  DOMAIN="${DOMAIN:-${IP//./-}.sslip.io}"
  log "Server address: $IP ($DOMAIN)"
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
    firewall
    services
  fi
  links "$IP" | tee "$OUT/links.txt"
  [ "$DRY_RUN" = 1 ] || cp "$OUT/links.txt" /root/albatross-links.txt
  log "Done. The links are in $OUT/links.txt; see deploy/server/README.md for the next step."
}

main "$@"
