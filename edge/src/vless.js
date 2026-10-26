// The parts of the edge that touch no socket: the request header, early
// data and the settings. Tested with `node --test` (edge/test).

export function relays(env) {
  return String(env.PROXY_IP || "").split(",").map((s) => s.trim()).filter(Boolean);
}

/**
 * Where a relay is reached for a destination port: a bare host takes
 * every port (the relay listens on the destination's port); "host:port"
 * is a TLS relay (it reads the destination off the ClientHello), so it
 * takes TLS destinations (443) only. Null when the relay does not apply.
 */
export function relayTarget(relay, port) {
  const m = /^\[?([^\]]+?)\]?(?::(\d+))?$/.exec(relay);
  if (!m) return null;
  if (!m[2]) return { hostname: m[1], port };
  return port === 443 ? { hostname: m[1], port: Number(m[2]) } : null;
}

/** Early data rides in the Sec-WebSocket-Protocol header, base64url. */
export function decodeEarly(h) {
  if (!h) return null;
  try {
    const s = h.replace(/-/g, "+").replace(/_/g, "/");
    const bin = atob(s);
    const out = new Uint8Array(bin.length);
    for (let i = 0; i < bin.length; i++) out[i] = bin.charCodeAt(i);
    return out;
  } catch {
    return null;
  }
}

/**
 * The VLESS request header: version, 16-byte id, addons, command (1 TCP,
 * 2 UDP), port, address type (1 IPv4, 2 name, 3 IPv6) and address.
 */
export function parseHeader(b, uuidHex) {
  if (b.length < 24) return { err: "short header" };
  const version = b[0];
  let id = "";
  for (let i = 1; i < 17; i++) id += b[i].toString(16).padStart(2, "0");
  if (id !== uuidHex) return { err: "unknown user" };
  let i = 18 + b[17];
  if (b.length < i + 4) return { err: "short header" };
  const cmd = b[i++];
  const port = (b[i] << 8) | b[i + 1];
  i += 2;
  const atyp = b[i++];
  let host;
  if (atyp === 1) {
    if (b.length < i + 4) return { err: "short header" };
    host = Array.from(b.subarray(i, i + 4)).join(".");
    i += 4;
  } else if (atyp === 2) {
    const n = b[i++];
    if (b.length < i + n) return { err: "short header" };
    host = new TextDecoder().decode(b.subarray(i, i + n));
    i += n;
  } else if (atyp === 3) {
    if (b.length < i + 16) return { err: "short header" };
    const parts = [];
    for (let k = 0; k < 8; k++) parts.push(((b[i + 2 * k] << 8) | b[i + 2 * k + 1]).toString(16));
    host = parts.join(":");
    i += 16;
  } else {
    return { err: "bad address type" };
  }
  if (cmd !== 1 && cmd !== 2) return { err: "unsupported command" };
  if (!port) return { err: "bad port" };
  return { version, cmd, port, host, rest: b.subarray(i) };
}

/** Destinations the edge never opens: the Worker's own neighbourhood. */
export function forbidden(host) {
  const h = host.toLowerCase();
  if (h === "localhost" || h.endsWith(".localhost") || h.endsWith(".internal")) return true;
  const v4 = /^(\d+)\.(\d+)\.(\d+)\.(\d+)$/.exec(h);
  if (v4) {
    const [a, b] = [Number(v4[1]), Number(v4[2])];
    return a === 0 || a === 10 || a === 127 || (a === 100 && b >= 64 && b <= 127) || (a === 169 && b === 254) ||
      (a === 172 && b >= 16 && b <= 31) || (a === 192 && b === 168) || a >= 224;
  }
  if (h.includes(":")) return h === "::" || h === "::1" || /^(fc|fd|fe[89ab])/.test(h);
  return false;
}

