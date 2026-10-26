// Passwords, session tokens and the rules that decide whether an account may
// connect. Pure functions: no database, no network.

const enc = new TextEncoder();
const dec = new TextDecoder();

export const DAY = 86400;

export const b64u = {
  enc(bytes) {
    let s = "";
    for (const b of bytes) s += String.fromCharCode(b);
    return btoa(s).replace(/\+/g, "-").replace(/\//g, "_").replace(/=+$/, "");
  },
  dec(str) {
    const s = atob(str.replace(/-/g, "+").replace(/_/g, "/"));
    return Uint8Array.from(s, (c) => c.charCodeAt(0));
  },
};

// ---------- Passwords ----------

// Workers allow at most 100,000 PBKDF2 iterations.
export const ITERATIONS = 100000;

async function derive(password, salt, iterations) {
  const key = await crypto.subtle.importKey("raw", enc.encode(password), "PBKDF2", false, ["deriveBits"]);
  const bits = await crypto.subtle.deriveBits({ name: "PBKDF2", hash: "SHA-256", salt, iterations }, key, 256);
  return new Uint8Array(bits);
}

export async function hashPassword(password) {
  const salt = crypto.getRandomValues(new Uint8Array(16));
  const hash = await derive(password, salt, ITERATIONS);
  return `pbkdf2$${ITERATIONS}$${b64u.enc(salt)}$${b64u.enc(hash)}`;
}

export async function verifyPassword(password, stored) {
  const [scheme, iter, salt, hash] = String(stored).split("$");
  if (scheme !== "pbkdf2") return false;
  const got = await derive(password, b64u.dec(salt), Number(iter));
  return equalBytes(got, b64u.dec(hash));
}

function equalBytes(a, b) {
  if (a.length !== b.length) return false;
  let d = 0;
  for (let i = 0; i < a.length; i++) d |= a[i] ^ b[i];
  return d === 0;
}

/** Compares two strings in constant time (by comparing their digests). */
export async function safeEqual(a, b) {
  const [x, y] = await Promise.all([a, b].map(async (s) => new Uint8Array(await crypto.subtle.digest("SHA-256", enc.encode(String(s))))));
  return equalBytes(x, y);
}

/** A short, non-reversible tag for rate-limit keys, so IP addresses are not stored. */
export async function tag(secret, value) {
  const d = new Uint8Array(await crypto.subtle.digest("SHA-256", enc.encode(secret + "|" + value)));
  return b64u.enc(d).slice(0, 22);
}

// ---------- Tokens ----------

const keys = new Map();
async function hmacKey(secret) {
  if (!keys.has(secret)) {
    keys.set(secret, await crypto.subtle.importKey("raw", enc.encode(secret), { name: "HMAC", hash: "SHA-256" }, false, ["sign", "verify"]));
  }
  return keys.get(secret);
}

/** Signs a payload; `exp` (unix seconds) is part of it. */
export async function sign(payload, secret) {
  const body = b64u.enc(enc.encode(JSON.stringify(payload)));
  const mac = new Uint8Array(await crypto.subtle.sign("HMAC", await hmacKey(secret), enc.encode(body)));
  return `${body}.${b64u.enc(mac)}`;
}

/** Returns the payload of a token that is genuine and not expired, else null. */
export async function verify(token, secret, now) {
  if (typeof token !== "string") return null;
  const [body, mac] = token.split(".");
  if (!body || !mac) return null;
  try {
    const ok = await crypto.subtle.verify("HMAC", await hmacKey(secret), b64u.dec(mac), enc.encode(body));
    if (!ok) return null;
    const p = JSON.parse(dec.decode(b64u.dec(body)));
    return p && p.exp > now ? p : null;
  } catch {
    return null;
  }
}

export function randomPassword(length = 14) {
  // No look-alike characters: these are read aloud and typed by hand.
  const alphabet = "abcdefghjkmnpqrstuvwxyzABCDEFGHJKLMNPQRSTUVWXYZ23456789";
  const bytes = crypto.getRandomValues(new Uint8Array(length));
  return Array.from(bytes, (b) => alphabet[b % alphabet.length]).join("");
}

// ---------- Limits ----------

/** The user's allowance period: when it has run out it starts again, with nothing used. */
export function rollPeriod(user, now) {
  if (user.reset_days > 0) {
    const len = user.reset_days * DAY;
    if (now >= user.period_start + len) {
      const periods = Math.floor((now - user.period_start) / len);
      return { period_start: user.period_start + periods * len, used_bytes: 0, used_seconds: 0, rolled: true };
    }
  }
  return { period_start: user.period_start, used_bytes: user.used_bytes, used_seconds: user.used_seconds, rolled: false };
}

// ---------- Plans ----------

/**
 * The plans an account can be on, and how many devices each may have
 * connected at the same time (0: as many as they like). An account's plan
 * is its `tier` column.
 */
export const TIERS = {
  bronze: { name: "Bronze", devices: 1 },
  silver: { name: "Silver", devices: 2 },
  gold: { name: "Gold", devices: 4 },
  vip: { name: "VIP", devices: 0 },
};
export const DEFAULT_TIER = "bronze";

/** The plan a stored tier names; an unknown one is the default plan. */
export const tierOf = (tier) => (Object.hasOwn(TIERS, tier) ? tier : DEFAULT_TIER);

/** How many devices an account may have connected at once; 0 means no limit. */
export const maxDevices = (user) => TIERS[tierOf(user.tier)].devices;

// A device holds one of its account's slots for this long after its last
// usage report (the apps report every minute while connected).
export const DEVICE_ACTIVE = 5 * 60;
// A subscription (iPhone, iPad, Mac: the service never hears from those
// while they are connected) holds a slot for this long after a fetch.
export const SUB_ACTIVE = 6 * 3600;

/**
 * What the app and the panels are told about an account. A device asking
 * whether it may connect passes `others`, how many other devices hold one
 * of the account's slots right now: it may not when they fill the plan. A
 * page passes `active`, how many devices hold a slot, to show it.
 */
export function accountOf(user, now, { others = -1, active = -1 } = {}) {
  const p = rollPeriod(user, now);
  const tier = tierOf(user.tier);
  const limit = TIERS[tier].devices;
  let reason = "";
  if (user.disabled) reason = "disabled";
  else if (user.expires_at > 0 && now >= user.expires_at) reason = "expired";
  else if (user.quota_bytes > 0 && p.used_bytes >= user.quota_bytes) reason = "quota";
  else if (user.max_seconds > 0 && p.used_seconds >= user.max_seconds) reason = "time";
  else if (limit > 0 && others >= limit) reason = "devices";
  let devices = Math.max(active, 0);
  if (others >= 0) devices = limit > 0 ? Math.min(others + (reason === "" ? 1 : 0), limit) : others + 1;
  return {
    username: user.username,
    allowed: reason === "",
    reason,
    tier,
    max_devices: limit,
    devices,
    quota_bytes: user.quota_bytes,
    used_bytes: p.used_bytes,
    max_seconds: user.max_seconds,
    used_seconds: p.used_seconds,
    expires_at: user.expires_at,
    resets_at: user.reset_days > 0 ? p.period_start + user.reset_days * DAY : 0,
  };
}

export const USERNAME = /^[a-z0-9][a-z0-9._-]{2,31}$/;

export function checkUsername(name) {
  return typeof name === "string" && USERNAME.test(name.trim().toLowerCase());
}

export function checkPassword(pw) {
  return typeof pw === "string" && pw.length >= 8 && pw.length <= 200;
}

/**
 * The edge's share links for a signed-in user: VLESS over WebSocket with
 * TLS, on the ports Cloudflare proxies that the app's edge scan also uses.
 * The path carries early data in the WebSocket upgrade (`?ed=`), which
 * saves a round trip. Empty when the edge is not deployed.
 */
/**
 * The edge's links: the edge's name on Cloudflare's TLS ports and, for
 * each clean Cloudflare address given (reach.json, found open from inside
 * the country), the same edge by address, which gets through where the
 * name's resolution is poisoned.
 */
export function edgeLinks(host, uuid, addrs = []) {
  if (!host || !/^[0-9a-f-]{36}$/i.test(uuid || "")) return [];
  const path = encodeURIComponent("/" + uuid.replace(/-/g, "").slice(0, 12) + "?ed=2560");
  const link = (addr, port, name) =>
    `vless://${uuid}@${addr}:${port}?encryption=none&security=tls&sni=${host}&fp=chrome&alpn=http%2F1.1&type=ws&host=${host}&path=${path}#${encodeURIComponent(name)}`;
  const out = [443, 2053, 8443].map((port) => link(host, port, `Albatross edge ${port}`));
  for (const a of addrs) if (/^[0-9.]+$/.test(a)) out.push(link(a, 443, `Albatross edge ${a}`));
  return out;
}

/** True when version a (x.y.z, anything after "-" ignored) is older than b. */
export function versionLess(a, b) {
  const parse = (v) => String(v || "").replace(/^v/, "").split("-")[0].split(".").map((n) => Number.parseInt(n, 10));
  const x = parse(a), y = parse(b);
  for (let i = 0; i < 3; i++) {
    const p = Number.isFinite(x[i]) ? x[i] : 0, q = Number.isFinite(y[i]) ? y[i] : 0;
    if (p !== q) return p < q;
  }
  return false;
}
