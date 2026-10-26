// Subscriptions for devices without the app (iPhone, iPad, Mac): the
// account's servers as any client app reads them, a base64 list of links,
// by country, every server named after the service, refreshed by the app
// itself. The address carries a token of its own (a year, void when the
// password changes), so the apps need no sign-in.
import { DAY, accountOf, edgeLinks, sign, verify } from "./lib.js";
import { COUNTRIES, flag, hint } from "./countries.js";

export const SUB_TTL = 365 * DAY;

/** The token in a subscription address. */
export const subToken = (env, user, now) => sign({ u: user.id, v: user.token_version, sub: 1, exp: now + SUB_TTL }, env.TOKEN_SECRET);

/** The payload of a subscription token, or null. */
export async function readSubToken(env, token, now) {
  const p = await verify(token, env.TOKEN_SECRET, now);
  return p && p.sub === 1 && p.u ? p : null;
}

const b64 = (s) => btoa(String.fromCharCode(...new TextEncoder().encode(s)));
const unb64 = (s) => new TextDecoder().decode(Uint8Array.from(atob(s.replace(/-/g, "+").replace(/_/g, "/")), (c) => c.charCodeAt(0)));

/** The name a link carries (its fragment, or a vmess link's "ps"). */
export function nameOf(link) {
  if (link.startsWith("vmess://")) {
    try {
      return String(JSON.parse(unb64(link.slice(8))).ps || "");
    } catch {
      return "";
    }
  }
  const i = link.indexOf("#");
  if (i < 0) return "";
  try {
    return decodeURIComponent(link.slice(i + 1));
  } catch {
    return link.slice(i + 1);
  }
}

/** The link with its name replaced. */
export function rename(link, name) {
  if (link.startsWith("vmess://")) {
    try {
      const j = JSON.parse(unb64(link.slice(8)));
      j.ps = name;
      return "vmess://" + b64(JSON.stringify(j));
    } catch {
      return link;
    }
  }
  const i = link.indexOf("#");
  return (i < 0 ? link : link.slice(0, i)) + "#" + encodeURIComponent(name);
}

/** The links of a list file, each with the country its name says. */
export function parseList(text) {
  const out = [];
  for (const l of String(text || "").split(/\r?\n/)) {
    const link = l.trim();
    if (!link || link.startsWith("#") || !/^[a-z0-9+.-]+:\/\//i.test(link)) continue;
    out.push({ link, country: hint(nameOf(link)) });
  }
  return out;
}

// The schemes the connection apps on Apple devices (V2Box, Streisand,
// Shadowrocket, Hiddify, sing-box) take. The app's own protocols and
// plain proxies are left out of a subscription.
const APPLE_SCHEMES = new Set(["vmess", "vless", "trojan", "ss", "hysteria2", "hy2", "tuic", "wireguard", "wg"]);

/** Whether a link is one the Apple apps can use. */
export const forApple = (e) => APPLE_SCHEMES.has(e.link.slice(0, e.link.indexOf("://")).toLowerCase());

// How many servers a subscription carries at least, and how far into the
// fresh list the top-up looks.
export const SUB_MIN = 40;
const TOPUP_FROM = 400;

/**
 * The pool a subscription is built from: the hot list (tested through for
 * real, in order) and, when it is short, a top-up from the fresh list
 * (newly seen, their address answering), a different sample each time so
 * that refreshing the subscription brings other servers to try. Servers
 * inside the country and ones the Apple apps cannot use are left out.
 */
export function pool(hot, fresh, min = SUB_MIN, pick = Math.random) {
  const seen = new Set();
  const out = [];
  for (const e of parseList(hot)) {
    if (e.country !== "IR" && forApple(e) && !seen.has(e.link)) {
      seen.add(e.link);
      out.push(e);
    }
  }
  if (out.length >= min) return tcpFirst(out);
  const cands = [];
  for (const e of parseList(fresh)) {
    if (cands.length >= TOPUP_FROM) break;
    if (e.country !== "IR" && forApple(e) && !seen.has(e.link)) cands.push(e);
  }
  // A random sample of the candidates, in their order.
  const want = Math.min(min - out.length, cands.length);
  const chosen = new Set();
  while (chosen.size < want) chosen.add(Math.floor(pick() * cands.length) % cands.length);
  for (const [i, e] of cands.entries()) if (chosen.has(i)) out.push(e);
  return tcpFirst(out);
}

// UDP is throttled on many networks here: servers over it go after the rest.
const UDP_SCHEMES = new Set(["hysteria2", "hy2", "tuic"]);
const overUDP = (e) => UDP_SCHEMES.has(e.link.slice(0, e.link.indexOf("://")).toLowerCase());
export const tcpFirst = (entries) => [...entries.filter((e) => !overUDP(e)), ...entries.filter(overUDP)];

/** The countries in a list, most servers first. */
export function countriesOf(entries) {
  const n = new Map();
  for (const e of entries) if (e.country && e.country !== "IR") n.set(e.country, (n.get(e.country) || 0) + 1);
  return [...n].sort((a, b) => b[1] - a[1] || a[0].localeCompare(b[0]))
    .map(([code, count]) => ({ code, name: COUNTRIES[code] || code, flag: flag(code), count }));
}

/**
 * The subscription for a choice: "" every server, a country code that
 * country's, "edge" the project's own edge. Each server is named after the
 * service; the servers' own names and sources never show.
 */
export function build(entries, choice, brand, edge) {
  let list = entries;
  let label = "All servers";
  if (choice === "edge") {
    list = [];
  } else if (choice) {
    list = entries.filter((e) => e.country === choice);
    label = COUNTRIES[choice] || choice;
  } else {
    list = entries.filter((e) => e.country !== "IR");
  }
  const links = [];
  if (!choice || choice === "edge") for (const [i, l] of edge.entries()) links.push(rename(l, `${brand} Edge ${i + 1}`));
  for (const [i, e] of list.entries()) {
    const where = e.country ? `${flag(e.country)} ${COUNTRIES[e.country] || e.country}` : "Server";
    links.push(rename(e.link, `${brand} ${where} ${i + 1}`));
  }
  return { title: `${brand} ${label}`, body: b64(links.join("\n") + "\n"), count: links.length };
}

/** The headers client apps read: the allowance and the name. */
export function headers(title, account, interval = 1) {
  const h = new Headers({
    "Content-Type": "text/plain; charset=utf-8",
    "Cache-Control": "no-store",
    "Content-Disposition": `attachment; filename="${title.replace(/[^\w .-]+/g, "")}.txt"`,
    "Profile-Title": "base64:" + b64(title),
    "Profile-Update-Interval": String(interval),
    "X-Content-Type-Options": "nosniff",
  });
  const info = [`upload=0`, `download=${account.used_bytes || 0}`];
  if (account.quota_bytes > 0) info.push(`total=${account.quota_bytes}`);
  if (account.expires_at > 0) info.push(`expire=${account.expires_at}`);
  h.set("Subscription-Userinfo", info.join("; "));
  return h;
}

/** A server list as the service has it: its own storage, or GitHub where there is none. */
export async function listFile(env, name) {
  if (env.FILES) {
    const o = await env.FILES.get("list/servers/" + name);
    return o ? o.text() : "";
  }
  const up = await fetch("https://raw.githubusercontent.com/Albatrossvpn/Albatross/servers/" + name, { cf: { cacheTtl: 600, cacheEverything: true } });
  return up.ok ? up.text() : "";
}

export const hotList = (env) => listFile(env, "hot.txt");

/** The servers a subscription offers right now (pool). */
export async function subPool(env) {
  const hot = await listFile(env, "hot.txt");
  const entries = pool(hot, "");
  if (entries.length >= SUB_MIN) return entries;
  return pool(hot, await listFile(env, "fresh.txt"));
}

/** The edge's links for an account that may connect. */
export const edgeFor = (env, account) => (account.allowed ? edgeLinks(env.EDGE_HOST, env.EDGE_UUID) : []);

export { accountOf };
