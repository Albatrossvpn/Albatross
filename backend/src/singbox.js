// The subscription as a sing-box config, for the sing-box apps on Apple
// devices, which load a whole config and not a list of links. Each link
// becomes an outbound the way the app's engine makes one (internal/node:
// links.js reads the link as parse.go does, this writes the outbound as
// singbox.go does); a link sing-box cannot run (Xray's transports, a mask
// only Xray applies) is left out, since a single outbound sing-box refuses
// stops the whole profile.
import { UUID, atoi, isIP, parseNode, splitList } from "./links.js";

// A UUID for an id that is not one, as Xray makes it: UUIDv5 with the
// all-zero namespace.
async function uuidV5(name) {
  const data = new Uint8Array([...new Uint8Array(16), ...new TextEncoder().encode(name)]);
  const b = new Uint8Array(await crypto.subtle.digest("SHA-1", data)).slice(0, 16);
  b[6] = (b[6] & 0x0f) | 0x50;
  b[8] = (b[8] & 0x3f) | 0x80;
  const hex = [...b].map((x) => x.toString(16).padStart(2, "0")).join("");
  return `${hex.slice(0, 8)}-${hex.slice(8, 12)}-${hex.slice(12, 16)}-${hex.slice(16, 20)}-${hex.slice(20)}`;
}

// What the engine hands to Xray because sing-box cannot run it, and
// ShadowTLS, which needs an outbound of its own to ride on.
function runsOnSingBox(n) {
  if (n.proto === "shadowsocks" && n.plugin === "shadow-tls") return false;
  if (n.finalMask) {
    // Every mask but the TLS fragment is Xray's.
    const m = JSON.parse(n.finalMask);
    for (const list of [m.tcp, m.udp]) if (Array.isArray(list) && list.some((e) => e?.type && e.type !== "fragment")) return false;
  }
  if (n.transport === "xhttp" || n.transport === "kcp" || n.flow === "xtls-rprx-vision-udp443") return false;
  if (n.proto === "vless" && n.encryption && n.encryption !== "none") return false;
  if (n.transport === "grpc" && n.mode === "multi") return false;
  // sing-box's HTTP transport over TLS is HTTP/2, not v2ray's header obfuscation.
  if (n.transport === "tcp" && n.headerType === "http" && n.security) return false;
  if (n.security === "tls" && (n.ech || n.verifyName || n.pinnedCert) && !n.insecure) return false;
  return true;
}

const firstHost = (h) => h.split(",")[0].trim();

// Xray's choice: the explicit SNI, else the HTTP host for HTTP-like
// transports, else the server's address when it is a name.
function serverName(n) {
  if (n.sni) return n.sni;
  if (["ws", "httpupgrade", "http", "xhttp", "grpc"].includes(n.transport)) {
    const h = firstHost(n.host);
    if (h && !isIP(h)) return h;
  }
  return isIP(n.server) ? "" : n.server;
}

// What a TCP transport can use: WebSocket and HTTPUpgrade need HTTP/1.1,
// and nothing over TCP can use h3.
function tcpALPN(n) {
  const wsLike = n.transport === "ws" || n.transport === "httpupgrade";
  const out = n.alpn.filter((a) => a !== "h3" && !(a === "h2" && wsLike));
  return !out.length && wsLike && n.alpn.length ? ["http/1.1"] : out;
}

function tlsOf(n) {
  if (n.security !== "tls" && n.security !== "reality") return null;
  const t = { enabled: true };
  const sni = serverName(n);
  if (sni) t.server_name = sni;
  if (n.insecure || (n.security === "tls" && !sni)) t.insecure = true;
  let alpn = tcpALPN(n);
  let fp = n.fp;
  if (!n.fp && n.security === "tls") {
    // Go's own ClientHello is easy to single out; look like a browser.
    fp = "chrome";
    if (!alpn.length && (n.transport === "ws" || n.transport === "httpupgrade")) alpn = ["http/1.1"];
  }
  if (alpn.length) t.alpn = alpn;
  if (fp) t.utls = { enabled: true, fingerprint: fp };
  if (n.security === "reality") {
    t.reality = { enabled: true, public_key: n.publicKey, short_id: n.shortID };
  } else if (n.fragment) {
    t.fragment = true;
  }
  return t;
}

function quicTLS(n, defaultALPN) {
  const t = { enabled: true };
  const sni = serverName(n);
  if (sni) t.server_name = sni;
  if (n.insecure || !sni) t.insecure = true;
  if (n.disableSNI) t.disable_sni = true;
  const alpn = n.alpn.length ? n.alpn : defaultALPN;
  if (alpn.length) t.alpn = alpn;
  return t;
}

function transportOf(n) {
  switch (n.transport) {
    case "ws": {
      const t = { type: "ws" };
      if (n.path) t.path = n.path;
      let host = firstHost(n.host);
      if (!host && n.sni && !isIP(n.sni)) host = n.sni;
      if (host) t.headers = { Host: host };
      if (n.earlyData > 0) {
        t.max_early_data = n.earlyData;
        t.early_data_header_name = "Sec-WebSocket-Protocol";
      }
      return t;
    }
    case "httpupgrade": {
      const t = { type: "httpupgrade" };
      const host = firstHost(n.host) || (n.sni && !isIP(n.sni) ? n.sni : "");
      if (host) t.host = host;
      if (n.path) t.path = n.path;
      return t;
    }
    case "grpc":
      return n.serviceName ? { type: "grpc", service_name: n.serviceName } : { type: "grpc" };
    case "http": {
      const t = { type: "http" };
      const hosts = splitList(n.host);
      if (hosts.length) t.host = hosts;
      if (n.path) t.path = n.path;
      return t;
    }
    case "quic":
      return { type: "quic" };
    case "tcp": {
      if (n.headerType !== "http") return null;
      const t = { type: "http", method: "GET" };
      const hosts = splitList(n.host);
      if (hosts.length) t.host = hosts;
      const paths = splitList(n.path);
      if (paths.length) t.path = paths[0];
      return t;
    }
  }
  return null;
}

function stream(out, n) {
  const t = tlsOf(n);
  if (t) out.tls = t;
  const tr = transportOf(n);
  if (tr) out.transport = tr;
}

/** The sing-box outbound (or, for WireGuard, endpoint) for a node. */
function singBox(n, tag) {
  const out = { tag, type: n.proto, server: n.server, server_port: n.port };
  switch (n.proto) {
    case "vmess":
      out.uuid = n.uuid;
      out.security = n.method;
      if (n.alterID > 0) out.alter_id = n.alterID;
      stream(out, n);
      break;
    case "vless":
      out.uuid = n.uuid;
      if (n.flow) out.flow = "xtls-rprx-vision";
      out.packet_encoding = "xudp";
      stream(out, n);
      break;
    case "trojan":
      out.password = n.password;
      stream(out, n);
      break;
    case "shadowsocks":
      out.method = n.method;
      out.password = n.password;
      if (n.plugin) {
        out.plugin = n.plugin;
        if (n.pluginOpts) out.plugin_opts = n.pluginOpts;
      }
      break;
    case "hysteria2": {
      const ranges = [];
      for (const p of splitList(n.portHop)) {
        const [lo, hi = lo] = p.split("-");
        if (atoi(lo) > 0 && atoi(hi) >= atoi(lo)) ranges.push(lo.trim() + ":" + hi.trim());
      }
      if (ranges.length) {
        delete out.server_port;
        out.server_ports = ranges;
        out.hop_interval = "30s";
      }
      out.password = n.password;
      if (n.obfsPassword) out.obfs = { type: "salamander", password: n.obfsPassword };
      if (n.upMbps > 0) out.up_mbps = n.upMbps;
      if (n.downMbps > 0) out.down_mbps = n.downMbps;
      out.tls = quicTLS(n, []);
      break;
    }
    case "tuic":
      out.uuid = n.uuid;
      out.password = n.password;
      if (["bbr", "cubic", "new_reno"].includes(n.congestionControl)) out.congestion_control = n.congestionControl;
      if (n.udpRelayMode === "quic") out.udp_relay_mode = "quic";
      out.tls = quicTLS(n, ["h3"]);
      break;
    case "wireguard": {
      const peer = { address: n.server, port: n.port, public_key: n.peerPublicKey, allowed_ips: ["0.0.0.0/0", "::/0"] };
      if (n.preSharedKey) peer.pre_shared_key = n.preSharedKey;
      if (n.reserved.length === 3) peer.reserved = n.reserved;
      return { tag, type: "wireguard", mtu: n.mtu > 0 ? n.mtu : 1280, address: n.localAddress, private_key: n.privateKey, peers: [peer] };
    }
  }
  return out;
}

/** The sing-box object for a link, tagged; null when sing-box cannot run it. */
export async function outboundOf(link, tag) {
  const n = parseNode(link);
  if (!n || !runsOnSingBox(n)) return null;
  // sing-box takes a UUID; Xray makes one of any other id.
  if (["vmess", "vless", "tuic"].includes(n.proto) && !UUID.test(n.uuid)) n.uuid = await uuidV5(n.uuid);
  return singBox(n, tag);
}

const TEST_URL = "https://www.gstatic.com/generate_204";

/**
 * A whole sing-box config (1.12 and later) for servers ({link, name}):
 * every server in a selector, which starts on the fastest (urltest); the
 * device's traffic through a TUN, DNS through the selected server,
 * private addresses direct. Null when no link is one sing-box runs.
 */
export async function singBoxConfig(servers) {
  const outbounds = [], endpoints = [], tags = [], seen = new Set();
  for (const [i, { link, name }] of servers.entries()) {
    const base = String(name || "").trim() || `Server ${i + 1}`;
    let tag = base;
    for (let n = 2; seen.has(tag); n++) tag = `${base} (${n})`;
    const o = await outboundOf(link, tag);
    if (!o) continue;
    seen.add(tag);
    tags.push(tag);
    (o.type === "wireguard" ? endpoints : outbounds).push(o);
  }
  if (!tags.length) return null;
  const config = {
    log: { level: "warn" },
    dns: {
      servers: [
        { type: "https", tag: "remote", server: "1.1.1.1", detour: "select" },
        { type: "local", tag: "local" },
      ],
      final: "remote",
      strategy: "ipv4_only",
    },
    inbounds: [{ type: "tun", tag: "tun-in", address: ["172.19.0.1/30", "fdfe:dcba:9876::1/126"], auto_route: true, strict_route: true }],
    outbounds: [
      { type: "selector", tag: "select", outbounds: ["auto", ...tags], default: "auto", interrupt_exist_connections: true },
      { type: "urltest", tag: "auto", outbounds: tags, url: TEST_URL, interval: "10m", tolerance: 100, interrupt_exist_connections: true },
      ...outbounds,
      { type: "direct", tag: "direct" },
    ],
    route: {
      rules: [{ action: "sniff" }, { protocol: "dns", action: "hijack-dns" }, { ip_is_private: true, outbound: "direct" }],
      final: "select",
      default_domain_resolver: "local",
    },
    experimental: { cache_file: { enabled: true } },
  };
  if (endpoints.length) config.endpoints = endpoints;
  return config;
}

/**
 * Whether a request comes from a sing-box app that wants a config: the
 * address says so (f=singbox, as the account page writes it for them),
 * or the official apps' user agent (SFI, SFA, SFM, SFT, sing-box). Apps
 * that run sing-box underneath but read lists of links themselves
 * (Hiddify, Karing and the like) keep the list.
 */
export function wantsSingBox(request, url) {
  if (String(url.searchParams.get("f") || "").toLowerCase() === "singbox") return true;
  const ua = request.headers.get("User-Agent") || "";
  return /^(SF[IAMT]|sing-box)\b/i.test(ua) && !/hiddify|karing|clash|nekobox|v2box|streisand/i.test(ua);
}
