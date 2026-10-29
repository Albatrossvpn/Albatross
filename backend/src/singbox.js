// The subscription as a sing-box config, for the sing-box apps on Apple
// devices, which load a whole config and not a list of links. Each link
// becomes an outbound the way the app's engine makes one (internal/node:
// parse.go reads the link, singbox.go writes the outbound); a link
// sing-box cannot run (Xray's transports, a malformed key) is left out,
// since a single outbound sing-box refuses stops the whole profile.

const unesc = (s) => {
  if (!s.includes("%")) return s;
  try {
    return decodeURIComponent(s);
  } catch {
    return s;
  }
};

function parseQuery(q) {
  const out = Object.create(null);
  for (const kv of q.split("&")) {
    if (!kv) continue;
    const i = kv.indexOf("=");
    let k = unesc(i < 0 ? kv : kv.slice(0, i)).trim().toLowerCase();
    while (k.startsWith("amp;")) k = k.slice(4);
    if (!k || k in out) continue;
    out[k] = unesc(i < 0 ? "" : kv.slice(i + 1)).trim();
  }
  return out;
}

function atoi(s) {
  s = String(s ?? "").trim();
  const i = s.search(/[,\-/ ]/);
  if (i > 0) s = s.slice(0, i);
  return /^[+-]?\d+$/.test(s) ? Number(s) : 0;
}

const truthy = (s) => ["1", "true", "yes", "on"].includes(String(s ?? "").trim().toLowerCase());
const splitList = (s) => String(s ?? "").split(",").map((p) => p.trim()).filter(Boolean);
const firstNonEmpty = (...v) => v.find((s) => s) || "";

const IPV4 = /^(25[0-5]|2[0-4]\d|1\d\d|[1-9]?\d)(\.(25[0-5]|2[0-4]\d|1\d\d|[1-9]?\d)){3}$/;
const isIPv6 = (s) => {
  if (!s.includes(":") || !/^[0-9a-fA-F:.]+$/.test(s)) return false;
  try {
    new URL(`http://[${s}]/`);
    return true;
  } catch {
    return false;
  }
};
const isIP = (s) => IPV4.test(s) || isIPv6(s);

function usableIP(s) {
  if (IPV4.test(s)) {
    const a = Number(s.split(".")[0]);
    return s !== "0.0.0.0" && a !== 127 && !(a >= 224 && a <= 239);
  }
  const h = new URL(`http://[${s}]/`).hostname.toLowerCase();
  return h !== "[::]" && h !== "[::1]" && !h.startsWith("[ff");
}

function validHostname(h) {
  if (h.length > 253 || h.startsWith(".") || h.startsWith("-")) return false;
  h = h.replace(/\.$/, "");
  if (!h.includes(".")) return false;
  return h.split(".").every((l) => l.length > 0 && l.length <= 63 && /^[-_0-9a-zA-Z]+$/.test(l));
}

function splitHostPort(s) {
  if (s.startsWith("[")) {
    const end = s.indexOf("]");
    if (end < 0) return null;
    return [s.slice(1, end), s.slice(end + 1).replace(/^:/, "")];
  }
  const i = s.lastIndexOf(":");
  if (i < 0) return [s, ""];
  if (s.split(":").length > 2 && isIP(s)) return [s, ""];
  return [s.slice(0, i), s.slice(i + 1)];
}

// A lenient split of scheme://userinfo@host:port/path?query#fragment.
function splitURI(link) {
  let rest = link.slice(link.indexOf("://") + 3);
  const u = { user: "", host: "", port: "", path: "", query: Object.create(null), fragment: "" };
  let i = rest.indexOf("#");
  if (i >= 0) {
    u.fragment = unesc(rest.slice(i + 1));
    rest = rest.slice(0, i);
  }
  i = rest.indexOf("?");
  if (i >= 0) {
    u.query = parseQuery(rest.slice(i + 1));
    rest = rest.slice(0, i);
  }
  i = rest.lastIndexOf("@");
  if (i >= 0) {
    // The last "@" can be in the path (pw@host:443/@channel): then the
    // host follows the last one before the path.
    const j = rest.indexOf("@");
    if (j < i) {
      const slash = rest.slice(j + 1).indexOf("/");
      if (slash >= 0 && j + 1 + slash < i) {
        const k = rest.slice(0, j + 1 + slash).lastIndexOf("@");
        if (k >= 0) i = k;
      }
    }
    u.user = rest.slice(0, i);
    rest = rest.slice(i + 1);
  }
  i = rest.indexOf("/");
  if (i >= 0) {
    u.path = rest.slice(i);
    rest = rest.slice(0, i);
  }
  const hp = splitHostPort(rest);
  if (!hp) return null;
  [u.host, u.port] = hp;
  return u;
}

const B64CHAR = /[A-Za-z0-9+/\-_]/;

function trimB64(s) {
  s = s.trim();
  let end = 0;
  while (end < s.length && B64CHAR.test(s[end])) end++;
  while (end < s.length && s[end] === "=") end++;
  return s.slice(0, end);
}

// Standard or URL alphabet, padded or not; null when it is neither.
function decodeB64(s) {
  s = s.trim().replace(/=+$/, "");
  const url = /[-_]/.test(s);
  if (!/^[A-Za-z0-9+/\-_]*$/.test(s) || (url && /[+/]/.test(s)) || s.length % 4 === 1) return null;
  const std = url ? s.replace(/-/g, "+").replace(/_/g, "/") : s;
  try {
    return Uint8Array.from(atob(std + "=".repeat((4 - (std.length % 4)) % 4)), (c) => c.charCodeAt(0));
  } catch {
    return null;
  }
}

// As much of s as is base64, in whole groups of four characters.
function decodeB64Prefix(s) {
  let end = 0;
  while (end < s.length && B64CHAR.test(s[end])) end++;
  s = s.slice(0, end - (end % 4));
  const url = /[-_]/.test(s);
  return decodeB64(url ? s.replace(/[+/]/g, "") : s.replace(/[-_]/g, "")) || new Uint8Array();
}

const text = (bytes) => new TextDecoder().decode(bytes);
function utf8(bytes) {
  try {
    return new TextDecoder("utf-8", { fatal: true }).decode(bytes);
  } catch {
    return null;
  }
}

// The first JSON object in s; what follows its end is not part of it.
function firstJSON(s) {
  s = s.replace(/^\s+/, "");
  if (!s.startsWith("{")) return null;
  let depth = 0, inStr = false;
  for (let i = 0; i < s.length; i++) {
    const c = s[i];
    if (inStr) {
      if (c === "\\") i++;
      else if (c === '"') inStr = false;
    } else if (c === '"') inStr = true;
    else if (c === "{" || c === "[") depth++;
    else if (c === "}" || c === "]") {
      if (--depth === 0) {
        try {
          return JSON.parse(s.slice(0, i + 1));
        } catch {
          return null;
        }
      }
    }
  }
  return null;
}

const str = (v) => (v == null ? "" : typeof v === "string" ? v.trim() : String(v).trim());

const UUID = /^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$/;

// A node as the app's engine has one (internal/node.Node), for the
// protocols the subscriptions carry.
const node = (fields) => ({
  proto: "", server: "", port: 0, portHop: "", uuid: "", password: "", method: "", alterID: 0, flow: "",
  encryption: "", transport: "", headerType: "", host: "", path: "", serviceName: "", mode: "", extra: "",
  seed: "", earlyData: 0, security: "", sni: "", alpn: [], fp: "", insecure: false, publicKey: "", shortID: "",
  ech: "", pinnedCert: "", verifyName: "", fragment: false, finalMask: "", plugin: "", pluginOpts: "",
  obfs: "", obfsPassword: "", upMbps: 0, downMbps: 0, congestionControl: "", udpRelayMode: "",
  disableSNI: false, privateKey: "", peerPublicKey: "", preSharedKey: "", localAddress: [], reserved: [], mtu: 0,
  ...fields,
});

// The Xray-standard transport and TLS parameters of vless, trojan and
// URL-form vmess links.
function applyStreamParams(n, q) {
  n.transport = q.type || "";
  n.headerType = q.headertype || "";
  n.host = q.host || "";
  n.path = q.path || "";
  n.serviceName = q.servicename || "";
  n.mode = q.mode || "";
  n.extra = q.extra || "";
  n.seed = q.seed || "";
  if (n.transport === "grpc" && !n.serviceName) n.serviceName = n.path.replace(/^\//, "");
  if (n.transport === "grpc" && !n.host) n.host = q.authority || "";
  if (atoi(q.ed) > 0) n.earlyData = atoi(q.ed);
  n.security = q.security || "";
  n.sni = firstNonEmpty(q.sni, q.peer, q.servername);
  n.alpn = splitList(q.alpn);
  n.fp = q.fp || "";
  n.insecure = truthy(q.allowinsecure) || truthy(q.insecure) || truthy(q.allow_insecure) || truthy(q["skip-cert-verify"]);
  n.publicKey = firstNonEmpty(q.pbk, q.publickey);
  n.shortID = q.sid || "";
  n.ech = q.ech || "";
  n.pinnedCert = q.pcs || "";
  n.verifyName = q.vcn || "";
  n.flow = q.flow || "";
  const fm = q.fm || "";
  if (fm) {
    if (fm.trim().startsWith("{")) n.finalMask = fm;
    if (fm.includes("fragment") || fm.includes("tlshello")) n.fragment = true;
  }
  if (q.fragment) n.fragment = true;
}

function parseStandard(link, scheme) {
  const u = splitURI(link);
  if (!u) return null;
  const n = node({ server: u.host, port: atoi(u.port) });
  applyStreamParams(n, u.query);
  if (scheme === "vless") {
    n.proto = "vless";
    n.uuid = unesc(u.user);
    n.encryption = u.query.encryption || "";
  } else {
    n.proto = "trojan";
    n.password = unesc(u.user);
    if (!n.security) n.security = "tls";
  }
  return n;
}

function parseVMess(link) {
  let body = link.slice("vmess://".length);
  const h = body.indexOf("#");
  if (h >= 0) body = body.slice(0, h);
  if (body.includes("@")) {
    // Xray's URL form: vmess://uuid@host:port?type=ws&security=tls...
    const n = parseStandard("vless://" + body, "vless");
    if (!n) return null;
    n.proto = "vmess";
    n.encryption = "";
    n.method = "auto";
    return n;
  }
  const q = body.indexOf("?");
  if (q > 0) {
    // Shadowrocket's form: base64(security:uuid@host:port)?obfs=websocket...
    const dec = decodeB64(trimB64(body.slice(0, q)));
    const s = dec && utf8(dec);
    if (s && s.includes("@") && !s.startsWith("{")) return parseShadowrocketVMess(s, parseQuery(body.slice(q + 1)));
  }
  let raw = decodeB64(trimB64(body));
  if (!raw) {
    raw = decodeB64Prefix(trimB64(body));
    if (!raw.length) return null;
  }
  const j = firstJSON(text(raw));
  if (!j || typeof j !== "object" || Array.isArray(j)) return null;
  // Go's JSON fields match their keys whatever their case.
  const lower = new Map();
  for (const [k, v] of Object.entries(j)) if (!lower.has(k.toLowerCase())) lower.set(k.toLowerCase(), v);
  const get = (k) => (k in j ? j[k] : lower.get(k.toLowerCase()));
  const n = node({
    proto: "vmess", server: str(get("add")), port: atoi(str(get("port"))), uuid: str(get("id")),
    alterID: atoi(str(get("aid"))), method: str(get("scy")), transport: str(get("net")).toLowerCase(),
    headerType: str(get("type")), host: str(get("host")), path: str(get("path")), sni: str(get("sni")),
    alpn: splitList(str(get("alpn"))), fp: str(get("fp")),
    insecure: truthy(str(get("insecure"))) || truthy(str(get("allowInsecure"))) || truthy(str(get("skip-cert-verify"))),
    mode: str(get("mode")), publicKey: str(get("pbk")), shortID: str(get("sid")),
  });
  const tls = str(get("tls")).toLowerCase();
  if (["tls", "true", "1"].includes(tls)) n.security = "tls";
  else if (tls === "reality") n.security = "reality";
  if (n.transport === "grpc") n.serviceName = firstNonEmpty(str(get("serviceName")), n.path.replace(/^\//, ""));
  if (n.transport === "kcp" || n.transport === "mkcp") n.seed = n.path;
  const fm = get("fm");
  if (fm && typeof fm === "object" && !Array.isArray(fm)) n.finalMask = JSON.stringify(fm);
  return n;
}

function parseShadowrocketVMess(dec, q) {
  const at = dec.lastIndexOf("@");
  const cred = dec.slice(0, at);
  const c = cred.indexOf(":");
  const hp = splitHostPort(dec.slice(at + 1));
  if (!hp) return null;
  const n = node({
    proto: "vmess", server: hp[0], port: atoi(hp[1]), uuid: c < 0 ? "" : cred.slice(c + 1),
    method: c < 0 ? cred : cred.slice(0, c), alterID: atoi(q.alterid), path: q.path || "", host: q.obfsparam || "", sni: q.peer || "",
  });
  switch (q.obfs) {
    case "websocket":
    case "ws":
      n.transport = "ws";
      break;
    case "http":
      n.transport = "tcp";
      n.headerType = "http";
      break;
    case "grpc":
      n.transport = "grpc";
      n.serviceName = q.path || "";
      break;
    default:
      n.transport = "tcp";
  }
  if (truthy(q.tls)) {
    n.security = "tls";
    n.insecure = truthy(q.allowinsecure);
  }
  return n;
}

const SS_METHODS = new Set([
  "2022-blake3-aes-128-gcm", "2022-blake3-aes-256-gcm", "2022-blake3-chacha20-poly1305", "none", "aes-128-gcm",
  "aes-192-gcm", "aes-256-gcm", "chacha20-ietf-poly1305", "xchacha20-ietf-poly1305", "aes-128-ctr", "aes-192-ctr",
  "aes-256-ctr", "aes-128-cfb", "aes-192-cfb", "aes-256-cfb", "rc4-md5", "chacha20-ietf", "xchacha20",
]);

// Whether a link's userinfo is a Shadowsocks "method:password", plain or base64.
function ssCredential(userinfo) {
  let cred = unesc(userinfo);
  const dec = decodeB64(cred);
  const s = dec && utf8(dec);
  if (s && s.includes(":")) cred = s;
  const c = cred.indexOf(":");
  if (c < 0) return false;
  let method = cred.slice(0, c).toLowerCase();
  if (method === "chacha20-poly1305") method = "chacha20-ietf-poly1305";
  return SS_METHODS.has(method);
}

function parseSS(link) {
  let body = link.slice("ss://".length);
  const h = body.indexOf("#");
  if (h >= 0) body = body.slice(0, h);
  let query = Object.create(null);
  const q = body.indexOf("?");
  if (q >= 0) {
    query = parseQuery(body.slice(q + 1));
    body = body.slice(0, q);
  }
  body = body.replace(/\/$/, "");
  // Mislabelled links: a v2rayN vmess JSON or a vless URL behind "ss://".
  const at = body.lastIndexOf("@");
  if (at < 0) {
    const dec = decodeB64(trimB64(body));
    if (dec && text(dec).trim().startsWith("{")) return parseVMess("vmess://" + trimB64(body));
  } else if (query.encryption || (UUID.test(body.slice(0, at)) && (query.type || query.security))) {
    return parseStandard("vless://" + link.slice("ss://".length), "vless");
  } else if ((query.type || query.security) && !ssCredential(body.slice(0, at))) {
    // Panels add type= to genuine Shadowsocks links, so only a userinfo
    // that is not "method:password" means Trojan.
    return parseStandard("trojan://" + link.slice("ss://".length), "trojan");
  }
  let method, password, hostport;
  const cut = (s) => {
    const c = s.indexOf(":");
    return c < 0 ? [s, ""] : [s.slice(0, c), s.slice(c + 1)];
  };
  if (at >= 0) {
    const userinfo = body.slice(0, at);
    hostport = body.slice(at + 1).replace(/\/$/, "");
    const dec = decodeB64(unesc(userinfo));
    const s = dec && utf8(dec);
    [method, password] = s && s.includes(":") ? cut(s) : cut(unesc(userinfo));
  } else {
    const dec = decodeB64(unesc(body));
    if (!dec) return null;
    const s = text(dec);
    const a = s.lastIndexOf("@");
    if (a < 0) return null;
    [method, password] = cut(s.slice(0, a));
    hostport = s.slice(a + 1);
  }
  const hp = splitHostPort(hostport);
  if (!hp) return null;
  const n = node({ proto: "shadowsocks", server: hp[0], port: atoi(hp[1]), method: method.trim().toLowerCase(), password });
  if (query.plugin) {
    const s = query.plugin.indexOf(";");
    n.plugin = s < 0 ? query.plugin : query.plugin.slice(0, s);
    if (s >= 0) n.pluginOpts = query.plugin.slice(s + 1);
  }
  return n;
}

// "443", "20000-30000" and "443,20000-30000".
function setPorts(n, port, mport) {
  const spec = mport || port;
  if (/[,-]/.test(spec)) {
    n.portHop = spec;
    n.port = atoi(port) > 0 ? atoi(port) : atoi(spec);
    return;
  }
  n.port = atoi(port);
}

function parseHy2(link) {
  const u = splitURI(link);
  if (!u) return null;
  const q = u.query;
  const n = node({
    proto: "hysteria2", server: u.host, password: unesc(u.user), security: "tls", sni: firstNonEmpty(q.sni, q.peer),
    insecure: truthy(q.insecure) || truthy(q.allowinsecure) || truthy(q.allow_insecure) || truthy(q["skip-cert-verify"]),
    alpn: splitList(q.alpn), obfs: q.obfs || "", obfsPassword: q["obfs-password"] || "",
    upMbps: atoi(firstNonEmpty(q.upmbps, q["up-mbps"], q.up)), downMbps: atoi(firstNonEmpty(q.downmbps, q["down-mbps"], q.down)),
  });
  if (n.obfs === "none") n.obfs = "";
  // sing-box cannot pin a leaf certificate's hash: verification is skipped.
  if (q.pinsha256) n.insecure = true;
  setPorts(n, u.port, q.mport || "");
  return n;
}

function parseTUIC(link) {
  const u = splitURI(link);
  if (!u) return null;
  const q = u.query;
  const user = unesc(u.user);
  const c = user.indexOf(":");
  return node({
    proto: "tuic", server: u.host, port: atoi(u.port), uuid: c < 0 ? user : user.slice(0, c), password: c < 0 ? "" : user.slice(c + 1),
    security: "tls", sni: q.sni || "", insecure: truthy(q.allow_insecure) || truthy(q.insecure) || truthy(q.allowinsecure),
    alpn: splitList(q.alpn), congestionControl: firstNonEmpty(q.congestion_control, q.congestion_controller),
    udpRelayMode: q.udp_relay_mode || "", disableSNI: truthy(q.disable_sni),
  });
}

function parseReserved(s) {
  if (s.includes(",")) {
    const out = splitList(s).map((p) => (/^\d+$/.test(p) ? Number(p) : -1));
    return out.length === 3 && out.every((v) => v >= 0 && v <= 255) ? out : [];
  }
  const b = decodeB64(s);
  return b && b.length === 3 ? [...b] : [];
}

function parseWireGuard(link) {
  const u = splitURI(link);
  if (!u) return null;
  const q = u.query;
  const n = node({
    proto: "wireguard", server: u.host, port: atoi(u.port),
    privateKey: firstNonEmpty(unesc(u.user), q.privatekey, q.secretkey),
    peerPublicKey: firstNonEmpty(q.publickey, q.peerpublickey, q.public_key),
    preSharedKey: firstNonEmpty(q.presharedkey, q.psk), mtu: atoi(q.mtu),
    localAddress: splitList(firstNonEmpty(q.address, q.ip, q.addresses)),
  });
  if (q.reserved) n.reserved = parseReserved(q.reserved);
  return n;
}

const FINGERPRINTS = new Set(["chrome", "firefox", "edge", "safari", "360", "qq", "ios", "android", "random", "randomized"]);
const BAD_ESCAPE = /%(?:[^0-9A-Fa-f]|[0-9A-Fa-f][^0-9A-Fa-f]|[0-9A-Fa-f]?$)/g;

// Xray's "?ed=2048" path suffix, moved into the early data size.
function splitEarlyData(n) {
  const i = n.path.indexOf("?");
  if (i < 0) return;
  const q = parseQuery(n.path.slice(i + 1));
  const ed = atoi(q.ed);
  if (ed <= 0) return;
  n.earlyData = ed;
  delete q.ed;
  const rest = Object.entries(q).map(([k, v]) => k + "=" + v).sort();
  n.path = n.path.slice(0, i) + (rest.length ? "?" + rest.join("&") : "");
}

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

// What the engine's normalize does, for these protocols.
async function normalize(n) {
  n.server = n.server.trim().replace(/^[[\]]+|[[\]]+$/g, "");
  for (const k of ["privateKey", "peerPublicKey", "preSharedKey", "publicKey"]) n[k] = n[k].trim().replace(/ /g, "+");
  n.transport = n.transport.trim().toLowerCase();
  n.transport = { raw: "tcp", none: "tcp", h2: "http", splithttp: "xhttp", mkcp: "kcp", gun: "grpc" }[n.transport] || n.transport;
  if (["vless", "vmess", "trojan"].includes(n.proto)) {
    if (!n.transport) n.transport = "tcp";
  } else if (n.proto !== "shadowsocks") n.transport = "";
  n.headerType = n.headerType.toLowerCase();
  if (n.headerType !== "http" && n.transport !== "kcp") n.headerType = "";
  n.security = n.security.toLowerCase();
  if (n.security === "xtls") n.security = "tls";
  if (n.security !== "tls" && n.security !== "reality") n.security = "";
  if (!n.security) {
    Object.assign(n, { sni: "", alpn: [], fp: "", publicKey: "", shortID: "", insecure: false });
  }
  n.fp = n.fp.toLowerCase();
  if (n.fp && !FINGERPRINTS.has(n.fp)) n.fp = "chrome";
  if (n.security === "reality" && !n.fp) n.fp = "chrome";
  n.flow = n.flow.toLowerCase();
  if (n.flow === "none") n.flow = "";
  if (n.flow && (n.transport !== "tcp" || !n.security)) n.flow = "";
  n.encryption = n.encryption.trim();
  if (n.proto === "vless" && !n.encryption.startsWith("mlkem768x25519plus.")) n.encryption = "none";
  if (n.proto === "vmess") {
    n.method = n.method.toLowerCase();
    if (!["auto", "none", "zero", "aes-128-gcm", "chacha20-poly1305"].includes(n.method)) n.method = "auto";
    if (n.alterID < 0 || n.alterID > 65535) n.alterID = 0;
  }
  if (n.proto === "shadowsocks") {
    if (n.plugin === "simple-obfs" || n.plugin === "obfs") n.plugin = "obfs-local";
    if (n.method === "chacha20-poly1305") n.method = "chacha20-ietf-poly1305";
  }
  if (n.path && !n.path.startsWith("/") && ["ws", "httpupgrade", "http", "xhttp"].includes(n.transport)) n.path = "/" + n.path;
  if (n.transport === "ws" || n.transport === "httpupgrade") splitEarlyData(n);
  if (n.path.includes("%")) n.path = n.path.replace(BAD_ESCAPE, (m) => "%25" + m.slice(1));
  if (["vmess", "vless", "tuic"].includes(n.proto) && n.uuid && !UUID.test(n.uuid)) n.uuid = await uuidV5(n.uuid);
  if (n.proto === "wireguard") n.localAddress = n.localAddress.map((a) => (a.includes("/") ? a : a + (a.includes(":") ? "/128" : "/32")));
}

const key32 = (k) => {
  const b = decodeB64(k);
  return !!b && b.length === 32;
};

function validSS2022Key(method, password) {
  const want = method === "2022-blake3-aes-128-gcm" ? 16 : 32;
  return password.split(":").every((p) => {
    const b = decodeB64(p);
    return !!b && b.length === want;
  });
}

// What the engine's Validate refuses, and what it hands to Xray because
// sing-box cannot run it: either way, nothing for a sing-box config.
function runsOnSingBox(n) {
  if (!n.server || /[ \t/\\?#@,"']/.test(n.server)) return false;
  if (n.port <= 0 || n.port > 65535) return false;
  if (isIP(n.server) ? !usableIP(n.server) : !validHostname(n.server)) return false;
  switch (n.proto) {
    case "vmess":
    case "vless":
      if (!n.uuid) return false;
      break;
    case "trojan":
    case "hysteria2":
      if (!n.password) return false;
      if (n.proto === "hysteria2" && n.obfs && !n.obfsPassword) return false;
      break;
    case "shadowsocks":
      if (!n.password && n.method !== "none") return false;
      if (!SS_METHODS.has(n.method)) return false;
      if (n.method.startsWith("2022-") && !validSS2022Key(n.method, n.password)) return false;
      // ShadowTLS needs an outbound of its own to ride on: not offered here.
      if (n.plugin && n.plugin !== "obfs-local" && n.plugin !== "v2ray-plugin") return false;
      break;
    case "tuic":
      if (!n.uuid || !n.password) return false;
      break;
    case "wireguard":
      if (!key32(n.privateKey) || !key32(n.peerPublicKey)) return false;
      if (n.preSharedKey && !key32(n.preSharedKey)) return false;
      if (!n.localAddress.length) return false;
      break;
    default:
      return false;
  }
  if (n.finalMask) {
    let m;
    try {
      m = JSON.parse(n.finalMask);
    } catch {
      return false;
    }
    // Every mask but the TLS fragment is Xray's.
    for (const list of [m?.tcp, m?.udp]) if (Array.isArray(list) && list.some((e) => e?.type && e.type !== "fragment")) return false;
  }
  if (n.security === "reality" && (!key32(n.publicKey) || n.shortID.length > 16 || n.shortID.length % 2 || !/^[0-9a-fA-F]*$/.test(n.shortID))) return false;
  if (n.flow && n.flow !== "xtls-rprx-vision") return false;
  if (!["", "tcp", "ws", "grpc", "http", "httpupgrade", "quic"].includes(n.transport)) return false;
  if (n.flow && n.transport && n.transport !== "tcp") return false;
  if (n.transport === "quic" && n.security !== "tls") return false;
  if (n.proto === "vless" && n.encryption && n.encryption !== "none") return false;
  if (n.transport === "grpc" && n.mode === "multi") return false;
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

const PARSERS = {
  vmess: parseVMess,
  vless: (l) => parseStandard(l, "vless"),
  trojan: (l) => parseStandard(l, "trojan"),
  ss: parseSS,
  hysteria2: parseHy2,
  hy2: parseHy2,
  tuic: parseTUIC,
  wireguard: parseWireGuard,
  wg: parseWireGuard,
};

/** The sing-box object for a link, tagged; null when sing-box cannot run it. */
export async function outboundOf(link, tag) {
  link = String(link || "").trim();
  const i = link.indexOf("://");
  const parse = i > 0 && PARSERS[link.slice(0, i).toLowerCase()];
  if (!parse) return null;
  let n;
  try {
    n = parse(link);
  } catch {
    return null;
  }
  if (!n) return null;
  await normalize(n);
  return runsOnSingBox(n) ? singBox(n, tag) : null;
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
