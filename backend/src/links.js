// Links as the app's engine reads them (internal/node/parse.go: the
// reading, normalize and Validate), for the protocols the subscriptions
// for Apple devices carry, and written back in the standard form. Public
// lists write links every which way (a vless link behind "ss://", "&amp;"
// for "&", "hy2://"); the engine reads them all, the connection apps on
// Apple devices often do not, so a subscription carries each server as
// the standard link the engine's reading of it makes.

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
  seed: "", earlyData: 0, security: "", sni: "", alpn: [], fp: "", insecure: false, publicKey: "", shortID: "", spiderX: "",
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
  n.spiderX = q.spx || "";
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

// What the engine's normalize does, for these protocols.
function normalize(n) {
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

const FLOWS = new Set(["xtls-rprx-vision", "xtls-rprx-vision-udp443"]);
const TRANSPORTS = new Set(["", "tcp", "ws", "grpc", "http", "httpupgrade", "xhttp", "kcp", "quic"]);

// One key of a plugin's options ("host=a;password=b;version=3").
function pluginOpt(n, key) {
  for (const kv of n.pluginOpts.split(";")) {
    const i = kv.indexOf("=");
    if (i >= 0 && kv.slice(0, i).trim() === key) return kv.slice(i + 1).trim();
  }
  return "";
}

// What the engine's Validate refuses, for these protocols.
function valid(n) {
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
      if (n.plugin && !["obfs-local", "v2ray-plugin", "shadow-tls"].includes(n.plugin)) return false;
      if (n.plugin === "shadow-tls" && !pluginOpt(n, "password")) return false;
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
    try {
      const m = JSON.parse(n.finalMask);
      if (!m || typeof m !== "object" || Array.isArray(m)) return false;
    } catch {
      return false;
    }
  }
  if (n.security === "reality" && (!key32(n.publicKey) || n.shortID.length > 16 || n.shortID.length % 2 || !/^[0-9a-fA-F]*$/.test(n.shortID))) return false;
  if (n.flow && !FLOWS.has(n.flow)) return false;
  if (!TRANSPORTS.has(n.transport)) return false;
  if (n.flow && n.transport && n.transport !== "tcp") return false;
  if (n.transport === "quic" && n.security !== "tls") return false;
  return true;
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

/** The node a link stands for, normalized; null when the engine would refuse it. */
export function parseNode(link) {
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
  normalize(n);
  return valid(n) ? n : null;
}

const enc = encodeURIComponent;
const b64 = (s) => {
  const bytes = new TextEncoder().encode(s);
  let bin = "";
  for (let i = 0; i < bytes.length; i += 0x8000) bin += String.fromCharCode(...bytes.subarray(i, i + 0x8000));
  return btoa(bin);
};
const b64url = (s) => b64(s).replace(/\+/g, "-").replace(/\//g, "_").replace(/=+$/, "");
const hostPort = (n) => (n.server.includes(":") ? `[${n.server}]` : n.server) + ":" + n.port;
const query = (pairs) => {
  const q = pairs.filter(([, v]) => v !== "" && v != null).map(([k, v]) => k + "=" + enc(String(v))).join("&");
  return q ? "?" + q : "";
};
// The path with Xray's early data size back in it ("/path?ed=2048").
const edPath = (n) => (n.earlyData > 0 ? n.path + (n.path.includes("?") ? "&" : "?") + "ed=" + n.earlyData : n.path);

// The Xray-standard TLS and transport parameters (v2rayN's share links).
function streamPairs(n) {
  const p = [["security", n.security || "none"]];
  if (n.security) p.push(["sni", n.sni], ["alpn", n.alpn.join(",")], ["fp", n.fp]);
  if (n.security === "reality") p.push(["pbk", n.publicKey], ["sid", n.shortID], ["spx", n.spiderX]);
  if (n.insecure) p.push(["allowInsecure", "1"]);
  if (n.security === "tls") p.push(["ech", n.ech], ["pcs", n.pinnedCert], ["vcn", n.verifyName]);
  p.push(["type", n.transport || "tcp"]);
  switch (n.transport) {
    case "tcp":
      if (n.headerType === "http") p.push(["headerType", "http"], ["host", n.host], ["path", n.path]);
      break;
    case "ws":
    case "httpupgrade":
      p.push(["host", n.host], ["path", edPath(n)]);
      break;
    case "http":
      p.push(["host", n.host], ["path", n.path]);
      break;
    case "xhttp":
      p.push(["host", n.host], ["path", n.path], ["mode", n.mode], ["extra", n.extra]);
      break;
    case "grpc":
      p.push(["serviceName", n.serviceName], ["authority", n.host], ["mode", n.mode]);
      break;
    case "kcp":
      p.push(["headerType", n.headerType], ["seed", n.seed]);
      break;
  }
  p.push(["fm", n.finalMask]);
  return p;
}

// v2rayN's VMess share form: base64 of a JSON object of strings.
function vmessLink(n, name) {
  const j = {
    v: "2", ps: name, add: n.server, port: String(n.port), id: n.uuid, aid: String(n.alterID), scy: n.method || "auto",
    net: n.transport || "tcp", type: "none", host: n.host, path: n.path, tls: n.security, sni: n.sni, alpn: n.alpn.join(","), fp: n.fp,
  };
  switch (n.transport) {
    case "tcp":
      if (n.headerType === "http") j.type = "http";
      break;
    case "ws":
    case "httpupgrade":
      j.path = edPath(n);
      break;
    case "grpc":
      j.path = n.serviceName;
      j.type = n.mode === "multi" ? "multi" : "gun";
      break;
    case "kcp":
      j.type = n.headerType || "none";
      j.path = n.seed;
      break;
    case "xhttp":
      if (n.mode) j.mode = n.mode;
      break;
  }
  if (n.security === "reality") Object.assign(j, { pbk: n.publicKey, sid: n.shortID, spx: n.spiderX });
  if (n.insecure) j.allowInsecure = "1";
  if (n.finalMask) j.fm = JSON.parse(n.finalMask);
  return "vmess://" + b64(JSON.stringify(j));
}

/** The standard share link for a node, named name. */
export function linkOf(n, name = "") {
  const frag = name ? "#" + enc(name) : "";
  switch (n.proto) {
    case "vmess":
      return vmessLink(n, name);
    case "vless":
      return `vless://${enc(n.uuid)}@${hostPort(n)}` + query([["encryption", n.encryption || "none"], ["flow", n.flow], ...streamPairs(n)]) + frag;
    case "trojan":
      return `trojan://${enc(n.password)}@${hostPort(n)}` + query(streamPairs(n)) + frag;
    case "shadowsocks": {
      // SIP002, and SIP022 for the 2022 ciphers: their keys in the clear.
      const user = n.method.startsWith("2022-") ? enc(n.method) + ":" + enc(n.password) : b64url(n.method + ":" + n.password);
      const plugin = n.plugin ? n.plugin + (n.pluginOpts ? ";" + n.pluginOpts : "") : "";
      return `ss://${user}@${hostPort(n)}` + query([["plugin", plugin]]) + frag;
    }
    case "hysteria2":
      return `hysteria2://${enc(n.password)}@${hostPort(n)}` + query([
        ["sni", n.sni], ["alpn", n.alpn.join(",")], ["insecure", n.insecure ? "1" : ""], ["obfs", n.obfsPassword ? "salamander" : ""],
        ["obfs-password", n.obfsPassword], ["mport", n.portHop], ["upmbps", n.upMbps || ""], ["downmbps", n.downMbps || ""],
      ]) + frag;
    case "tuic":
      return `tuic://${enc(n.uuid)}:${enc(n.password)}@${hostPort(n)}` + query([
        ["sni", n.sni], ["alpn", n.alpn.join(",")], ["congestion_control", n.congestionControl], ["udp_relay_mode", n.udpRelayMode],
        ["allow_insecure", n.insecure ? "1" : ""], ["disable_sni", n.disableSNI ? "1" : ""],
      ]) + frag;
    case "wireguard":
      return `wireguard://${enc(n.privateKey)}@${hostPort(n)}` + query([
        ["publickey", n.peerPublicKey], ["presharedkey", n.preSharedKey], ["address", n.localAddress.join(",")],
        ["reserved", n.reserved.join(",")], ["mtu", n.mtu || ""],
      ]) + frag;
  }
  return "";
}

/** A link in the standard form, named name; null when the engine would refuse it. */
export function cleanLink(link, name = "") {
  const n = parseNode(link);
  return n ? linkOf(n, name) : null;
}

export { UUID, atoi, isIP, splitList };
