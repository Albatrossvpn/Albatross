// The account service: the apps sign in and report use here, users see and
// change their account on the website, the administrator manages everyone.
import {
  DAY, DEFAULT_TIER, DEVICE_ACTIVE, SUB_ACTIVE, TIERS, accountOf, checkPassword, checkUsername, edgeLinks, hashPassword, maxDevices, versionLess,
  randomPassword, rollPeriod, safeEqual, sign, tag, tierForQuota, tierOf, verify, verifyPassword,
} from "./lib.js";
import { LIST_PATH, MAX_LIST, pullFile, putList, serveFile, serveList } from "./files.js";
import { build as buildSub, countriesOf, edgeFor, headers as subHeaders, listUpdated, readSubToken, storedListKeys, subPool, subToken } from "./sub.js";

const SESSION = 30 * DAY; // an app or browser session
const ADMIN_SESSION = 8 * 3600;
const MAX_BODY = 8 * 1024;

const DEFAULTS = {
  signup_open: "0",
  default_quota_bytes: String(5 * 1024 ** 3),
  default_max_seconds: "0",
  default_valid_days: "0",
  default_reset_days: "0", // allowances renew only when the admin recharges them
  default_tier: "auto", // the plan sign-ups get: by their allowance, or a named plan
  download_windows: "",
  download_android: "",
};

class HttpError extends Error {
  constructor(status, message) {
    super(message);
    this.status = status;
  }
}

const json = (data, status = 200) =>
  new Response(JSON.stringify(data), {
    status,
    headers: { "Content-Type": "application/json; charset=utf-8", "Cache-Control": "no-store", "X-Content-Type-Options": "nosniff" },
  });

const now = () => Math.floor(Date.now() / 1000);

function randomUsernameSuffix() {
  const range = 1_000_000;
  const limit = Math.floor(0x1_0000_0000 / range) * range;
  const value = new Uint32Array(1);
  do {
    crypto.getRandomValues(value);
  } while (value[0] >= limit);
  return String(value[0] % range).padStart(6, "0");
}

export default {
  // Hourly: users' reports about servers, summed for the collector.
  async scheduled(event, env, ctx) {
    ctx.waitUntil(publishCrowdServers(env));
  },
  async fetch(request, env) {
    const url = new URL(request.url);
    if (url.pathname.startsWith("/list/")) return mirror(request, url, env);
    if (url.pathname.startsWith("/dl/")) return download(request, url, env);
    if (url.pathname.startsWith("/sub/")) return subscription(request, url, env);
    if (!url.pathname.startsWith("/api/")) return env.ASSETS.fetch(request);
    try {
      if (!env.TOKEN_SECRET || env.TOKEN_SECRET.length < 24) throw new HttpError(503, "The service is not configured yet.");
      return await route(request, env, url);
    } catch (e) {
      if (e instanceof HttpError) return json({ error: e.message }, e.status);
      console.error(e && e.stack ? e.stack : e);
      return json({ error: "Something went wrong. Try again." }, 500);
    }
  },
};

async function route(request, env, url) {
  const path = url.pathname.replace(/^\/api\/v1\/?/, "");
  const key = `${request.method} ${path}`;
  const admin = path.match(/^admin\/users\/(\d+)(?:\/([a-z-]+))?$/);

  switch (key) {
    case "GET public": return publicInfo(env);
    case "POST login": return login(request, env);
    case "POST signup": return signup(request, env);
    case "GET me": return me(request, env);
    case "GET edge": return edge(request, env);
    case "GET listkeys": return listKeys(request, env);
    case "GET devices": return devicesList(request, env);
    case "GET dns/google.mobileconfig": return dnsProfile("google");
    case "GET dns/cloudflare.mobileconfig": return dnsProfile("cloudflare");
    case "GET sub": return subInfo(request, env, url);
    case "POST usage": return usage(request, env);
    case "POST password": return changePassword(request, env);
    case "POST admin/login": return adminLogin(request, env);
    case "GET admin/stats": return adminStats(request, env);
    case "GET admin/users": return adminList(request, env, url);
    case "POST admin/users": return adminCreate(request, env);
    case "POST admin/users/bulk": return adminBulk(request, env);
    case "GET admin/listkeys": return adminListKeys(request, env);
    case "GET admin/settings": return adminSettings(request, env);
    case "PUT admin/settings": return adminSaveSettings(request, env);
    case "POST admin/files": return adminPullFile(request, env);
    case "POST stats": return crowdReport(request, env);
    case "POST admin/crowd/publish": return adminPublishCrowd(request, env);
    case "GET stats": return crowdSummary(request, env, url);
  }
  const list = path.match(/^admin\/list\/(.+)$/);
  if (list && request.method === "PUT") return adminPutList(request, env, list[1]);
  const dev = path.match(/^devices\/([a-z0-9]{1,64})$/);
  if (dev && request.method === "DELETE") return deviceRemove(request, env, dev[1]);
  if (admin) {
    const id = Number(admin[1]);
    if (request.method === "PATCH" && !admin[2]) return adminUpdate(request, env, id);
    if (request.method === "DELETE" && !admin[2]) return adminDelete(request, env, id);
  }
  throw new HttpError(404, "Not found.");
}

// ---------- Helpers ----------

async function body(request) {
  const text = await request.text();
  if (text.length > MAX_BODY) throw new HttpError(413, "Request too large.");
  try {
    const v = text ? JSON.parse(text) : {};
    if (v && typeof v === "object" && !Array.isArray(v)) return v;
  } catch {}
  throw new HttpError(400, "Bad request.");
}

async function settings(env) {
  const out = { ...DEFAULTS };
  const { results } = await env.DB.prepare("SELECT key, value FROM settings").all();
  for (const r of results) if (r.key in DEFAULTS) out[r.key] = r.value;
  return out;
}

// An IPv6 client is usually given a whole /64, so the limits count the
// prefix as one address; otherwise each few guesses could take a new one.
function clientAddress(request) {
  const ip = (request.headers.get("CF-Connecting-IP") || "unknown").toLowerCase();
  const mapped = /^::ffff:(\d+\.\d+\.\d+\.\d+)$/.exec(ip);
  if (mapped) return mapped[1];
  const halves = ip.split("::");
  if (!ip.includes(":") || halves.length > 2) return ip;
  const head = halves[0] ? halves[0].split(":") : [];
  const tail = halves[1] ? halves[1].split(":") : [];
  const groups = halves.length === 1 ? head : [...head, ...Array(Math.max(8 - head.length - tail.length, 0)).fill("0"), ...tail];
  const prefix = groups.slice(0, 4);
  if (groups.length !== 8 || !prefix.every((g) => /^[0-9a-f]{1,4}$/.test(g))) return ip;
  return prefix.map((g) => parseInt(g, 16).toString(16)).join(":") + "::/64";
}

const clientTag = (request, env) => tag(env.TOKEN_SECRET, clientAddress(request));

/** True when `key` has used up `max` attempts within the window. */
async function blocked(env, key, max, window) {
  const row = await env.DB.prepare("SELECT count, since FROM attempts WHERE key = ?1").bind(key).first();
  return !!row && now() - row.since <= window && row.count >= max;
}

async function count(env, key, window) {
  const t = now();
  await env.DB.batch([
    env.DB.prepare(
      `INSERT INTO attempts (key, count, since) VALUES (?1, 1, ?2)
       ON CONFLICT (key) DO UPDATE SET
         count = CASE WHEN ?2 - since > ?3 THEN 1 ELSE count + 1 END,
         since = CASE WHEN ?2 - since > ?3 THEN ?2 ELSE since END`,
    ).bind(key, t, window),
    // Old entries are dropped as new ones come in.
    env.DB.prepare("DELETE FROM attempts WHERE since < ?1").bind(t - DAY),
  ]);
}

const forget = (env, key) => env.DB.prepare("DELETE FROM attempts WHERE key = ?1").bind(key).run();

const tooMany = () => new HttpError(429, "Too many attempts. Wait a few minutes and try again.");

/** Rate-limit keys for checking the password of `name` from this request's address. */
async function passwordKeys(request, env, name) {
  const t = await clientTag(request, env);
  return { ip: "login:ip:" + t, who: "login:user:" + name, from: "login:from:" + t + ":" + name };
}

// Failures from all addresses lock a username outright only far above what
// typos make, or anyone who knows it could keep its owner out cheaply; that
// cap bounds a guesser with very many addresses. Below it, while many
// addresses guess at it, each one that already failed at it gets only a few
// tries; one that has not still signs in with the right password.
async function passwordBlocked(env, k) {
  if ((await blocked(env, k.ip, 40, 900)) || (await blocked(env, k.from, 8, 900)) || (await blocked(env, k.who, 100, 900))) return true;
  return (await blocked(env, k.who, 8, 900)) && (await blocked(env, k.from, 3, 900));
}

async function passwordFailed(env, k) {
  await count(env, k.ip, 900);
  await count(env, k.who, 900);
  await count(env, k.from, 900);
}

async function userByName(env, name) {
  return env.DB.prepare("SELECT * FROM users WHERE username = ?1").bind(String(name).trim().toLowerCase()).first();
}

const userById = (env, id) => env.DB.prepare("SELECT * FROM users WHERE id = ?1").bind(id).first();

const session = (env, user, ttl = SESSION) =>
  sign({ u: user.id, v: user.token_version, exp: now() + ttl }, env.TOKEN_SECRET);

async function authUser(request, env) {
  const m = /^Bearer (.+)$/.exec(request.headers.get("Authorization") || "");
  const p = m && (await verify(m[1], env.TOKEN_SECRET, now()));
  const user = p && p.u && (await userById(env, p.u));
  if (!user || user.token_version !== p.v) throw new HttpError(401, "Sign in again.");
  return { user, token: m[1], payload: p };
}

async function authAdmin(request, env) {
  const m = /^Bearer (.+)$/.exec(request.headers.get("Authorization") || "");
  const p = m && (await verify(m[1], env.TOKEN_SECRET, now()));
  if (!p || p.adm !== 1) throw new HttpError(401, "Sign in again.");
}

// A fixed hash to spend the same time on an unknown username as on a known one.
const DUMMY = "pbkdf2$100000$AAAAAAAAAAAAAAAAAAAAAA$AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA";

// ---------- Public ----------

async function publicInfo(env) {
  const s = await settings(env);
  return json({
    name: env.SITE_NAME || "Albatross",
    company: env.COMPANY_NAME || "",
    address: env.COMPANY_ADDRESS || "",
    contact: env.CONTACT_EMAIL || "",
    signup_open: s.signup_open === "1",
    turnstile_sitekey: env.TURNSTILE_SITEKEY || "",
    downloads: { windows: s.download_windows, android: s.download_android },
    tiers: TIERS,
  });
}

// ---------- Encrypted DNS for Apple devices ----------
//
// On some networks the resolver answers for this service's names with a
// lie, so the account page and the subscription never load there. A
// configuration profile that sets DNS over HTTPS for the whole device
// goes around that; Safari installs it from these addresses.

const DNS_PROVIDERS = {
  google: { name: "Google", url: "https://dns.google/dns-query", addrs: ["8.8.8.8", "8.8.4.4", "2001:4860:4860::8888", "2001:4860:4860::8844"], id: "google" },
  cloudflare: { name: "Cloudflare", url: "https://cloudflare-dns.com/dns-query", addrs: ["1.1.1.1", "1.0.0.1", "2606:4700:4700::1111", "2606:4700:4700::1001"], id: "cloudflare" },
};

function dnsProfile(which) {
  const p = DNS_PROVIDERS[which];
  if (!p) throw new HttpError(404, "Not found.");
  const xml = (s) => String(s).replace(/[&<>]/g, (c) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;" }[c]));
  const body = `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>PayloadContent</key>
  <array>
    <dict>
      <key>DNSSettings</key>
      <dict>
        <key>DNSProtocol</key>
        <string>HTTPS</string>
        <key>ServerURL</key>
        <string>${xml(p.url)}</string>
        <key>ServerAddresses</key>
        <array>
${p.addrs.map((a) => `          <string>${a}</string>`).join("\n")}
        </array>
      </dict>
      <key>PayloadDescription</key>
      <string>Encrypted DNS (${xml(p.name)}) for every network</string>
      <key>PayloadDisplayName</key>
      <string>Encrypted DNS: ${xml(p.name)}</string>
      <key>PayloadIdentifier</key>
      <string>online.albatrossvpn.dns.${p.id}.settings</string>
      <key>PayloadType</key>
      <string>com.apple.dnsSettings.managed</string>
      <key>PayloadUUID</key>
      <string>a1b2c3d4-0000-4000-8000-${p.id === "google" ? "000000000001" : "000000000002"}</string>
      <key>PayloadVersion</key>
      <integer>1</integer>
      <key>ProhibitDisablement</key>
      <false/>
    </dict>
  </array>
  <key>PayloadDescription</key>
  <string>Looks names up over an encrypted connection to ${xml(p.name)}, so a network's resolver cannot answer with lies. Remove it any time in Settings, General, VPN &amp; Device Management.</string>
  <key>PayloadDisplayName</key>
  <string>Encrypted DNS (${xml(p.name)})</string>
  <key>PayloadIdentifier</key>
  <string>online.albatrossvpn.dns.${p.id}</string>
  <key>PayloadRemovalDisallowed</key>
  <false/>
  <key>PayloadType</key>
  <string>Configuration</string>
  <key>PayloadUUID</key>
  <string>a1b2c3d4-0000-4000-8000-${p.id === "google" ? "000000000011" : "000000000012"}</string>
  <key>PayloadVersion</key>
  <integer>1</integer>
</dict>
</plist>
`;
  return new Response(body, {
    status: 200,
    headers: {
      "Content-Type": "application/x-apple-aspen-config; charset=utf-8",
      "Content-Disposition": `attachment; filename="encrypted-dns-${p.id}.mobileconfig"`,
      "Cache-Control": "public, max-age=86400",
      "X-Content-Type-Options": "nosniff",
    },
  });
}

// ---------- Devices ----------
//
// An account's plan says how many devices may be connected at once. Each
// copy of the app names itself with a random number kept on the device
// (X-Albatross-Device) and a word for what it is (X-Albatross-Device-Name).
// A usage report, sent every minute while connected, claims one of the
// account's slots for DEVICE_ACTIVE; a report that finds the other slots
// taken is answered "devices" and the app disconnects. Signing in and
// looking at the account claim nothing.

/** The device an app's call names, or null for the website. */
function deviceOf(request) {
  if (request.headers.get("X-Albatross-Client") === "web") return null;
  const id = (request.headers.get("X-Albatross-Device") || "").trim().toLowerCase();
  const name = (request.headers.get("X-Albatross-Device-Name") || "").replace(/[^\x20-\x7e]/g, "").trim().slice(0, 40);
  // A copy from before devices were counted (1.0.0) is one shared device.
  if (!/^[0-9a-f]{16,64}$/.test(id)) return { id: "app", name: "" };
  return { id, name };
}

// The names shown for the devices that have none of their own.
const DEVICE_NAMES = { app: "Albatross app (older version)", sub: "iPhone, iPad and Mac (subscription)" };
const deviceName = (d) => d.name || DEVICE_NAMES[d.device_id ?? d.id] || "Device";

/** How many of the account's devices other than `id` hold a slot now. */
async function busyDevices(env, user, id, t) {
  const r = await env.DB.prepare("SELECT COUNT(*) AS n FROM devices WHERE user_id = ?1 AND device_id != ?2 AND active_until > ?3")
    .bind(user.id, id, t).first();
  return r.n;
}

const upsertDevice = (env, user, d, t, until) => env.DB.prepare(
  `INSERT INTO devices (user_id, device_id, name, first_seen, last_seen, active_until) VALUES (?1, ?2, ?3, ?4, ?4, ?5)
   ON CONFLICT (user_id, device_id) DO UPDATE SET last_seen = ?4, name = CASE WHEN ?3 = '' THEN name ELSE ?3 END,
     active_until = CASE WHEN ?5 < 0 THEN active_until ELSE ?5 END`,
).bind(user.id, d.id, d.name, t, until).run();

/**
 * The account as a caller sees it. A device is told whether a slot is
 * free for it (and remembered, holding nothing); the website is told how
 * many devices hold one.
 */
async function viewOf(env, user, d, t) {
  if (!d) return accountOf(user, t, { active: await busyDevices(env, user, "", t) });
  await upsertDevice(env, user, d, t, -1);
  return accountOf(user, t, { others: await busyDevices(env, user, d.id, t) });
}

/**
 * A device asks to hold one of its account's slots for `hold` seconds. It
 * may while fewer than the plan's number of other devices hold one; a
 * device that may not connect gives up the slot it held, so it never
 * counts the devices that stay out.
 */
async function claimSlot(env, user, d, t, hold = DEVICE_ACTIVE) {
  const a = accountOf(user, t, { others: await busyDevices(env, user, d.id, t) });
  await upsertDevice(env, user, d, t, a.allowed ? t + hold : 0);
  return a;
}

/** The devices this account has been used on, those holding a slot first. */
async function devicesList(request, env) {
  const { user } = await authUser(request, env);
  const t = now();
  const { results } = await env.DB.prepare(
    "SELECT device_id, name, first_seen, last_seen, active_until FROM devices WHERE user_id = ?1 ORDER BY (active_until > ?2) DESC, last_seen DESC LIMIT 100",
  ).bind(user.id, t).all();
  return json({
    tier: tierOf(user.tier),
    max_devices: maxDevices(user),
    devices: results.map((d) => ({ id: d.device_id, name: deviceName(d), first_seen: d.first_seen, last_seen: d.last_seen, active: d.active_until > t })),
  });
}

/** Forgets a device; the slot it held is free at once. */
async function deviceRemove(request, env, id) {
  const { user } = await authUser(request, env);
  const r = await env.DB.prepare("DELETE FROM devices WHERE user_id = ?1 AND device_id = ?2").bind(user.id, id).run();
  if (!r.meta.changes) throw new HttpError(404, "No such device.");
  return json({ ok: true });
}

const forgetDevices = (env, id) => env.DB.prepare("DELETE FROM devices WHERE user_id = ?1").bind(id).run();

// ---------- Users ----------

async function login(request, env) {
  await requireCurrentClient(request, env);
  const { username, password } = await body(request);
  if (typeof username !== "string" || typeof password !== "string" || password.length > 200 || username.length > 64) {
    throw new HttpError(400, "Enter your username and password.");
  }
  const name = username.trim().toLowerCase();
  const k = await passwordKeys(request, env, name);
  if (await passwordBlocked(env, k)) throw tooMany();

  const user = await userByName(env, name);
  const ok = await verifyPassword(password, user ? user.pass_hash : DUMMY);
  if (!user || !ok) {
    await passwordFailed(env, k);
    throw new HttpError(401, "Wrong username or password.");
  }
  // The username's count stays, so the other addresses guessing at it stay slowed.
  await forget(env, k.from);
  return json({ token: await session(env, user), account: await viewOf(env, user, deviceOf(request), now()) });
}

async function verifyTurnstile(request, env, token) {
  if (!env.TURNSTILE_SECRET) return;
  const form = new FormData();
  form.append("secret", env.TURNSTILE_SECRET);
  form.append("response", String(token || ""));
  const ip = request.headers.get("CF-Connecting-IP");
  if (ip) form.append("remoteip", ip);
  const res = await fetch("https://challenges.cloudflare.com/turnstile/v0/siteverify", { method: "POST", body: form });
  const out = await res.json().catch(() => ({}));
  if (!out.success) throw new HttpError(400, "Verification failed. Reload the page and try again.");
}

async function signup(request, env) {
  const s = await settings(env);
  if (s.signup_open !== "1") throw new HttpError(403, "Sign-ups are closed.");
  const { username, password, turnstile } = await body(request);
  const ip = "signup:ip:" + (await clientTag(request, env));
  if (await blocked(env, ip, 5, 3600)) throw tooMany();
  await verifyTurnstile(request, env, turnstile);
  if (!checkUsername(username)) throw new HttpError(400, "Choose a username of 3 to 32 letters, digits, dots, dashes or underscores.");
  if (!checkPassword(password)) throw new HttpError(400, "Choose a password of at least 8 characters.");

  // Every attempt counts, the ones turned away as taken too: otherwise the
  // answers could be used to learn which usernames exist, without limit.
  await count(env, ip, 3600);
  const t = now();
  const days = Number(s.default_valid_days);
  try {
    await env.DB.prepare(
      `INSERT INTO users (username, pass_hash, created_at, quota_bytes, max_seconds, expires_at, reset_days, period_start, tier)
       VALUES (?1, ?2, ?3, ?4, ?5, ?6, ?7, ?3, ?8)`,
    ).bind(username.trim().toLowerCase(), await hashPassword(password), t, Number(s.default_quota_bytes), Number(s.default_max_seconds),
      days > 0 ? t + days * DAY : 0, Number(s.default_reset_days),
      s.default_tier === "auto" || !s.default_tier ? tierForQuota(Number(s.default_quota_bytes)) : tierOf(s.default_tier)).run();
  } catch (e) {
    if (/UNIQUE/i.test(String(e))) throw new HttpError(409, "That username is taken.");
    throw e;
  }
  const user = await userByName(env, username);
  return json({ token: await session(env, user), account: accountOf(user, t) }, 201);
}

async function me(request, env) {
  await requireCurrentClient(request, env);
  const { user } = await authUser(request, env);
  const account = await viewOf(env, user, deviceOf(request), now());
  return json({ account, created_at: user.created_at, total_bytes: user.total_bytes, total_seconds: user.total_seconds });
}

/** The edge's links for the signed-in user (edge/README.md); none while the account may not connect. */
async function edge(request, env) {
  await requireCurrentClient(request, env);
  const { user } = await authUser(request, env);
  const a = await viewOf(env, user, deviceOf(request), now());
  return json({ links: a.allowed ? edgeLinks(env.EDGE_HOST, env.EDGE_UUID) : [], reason: a.reason });
}

// How many of the collector's keys a signed-in app gets: enough for the
// lists a cache may still serve (a collection every twenty minutes).
const LIST_KEYS_GIVEN = 12;

/**
 * The keys that open the collector's sealed lists (cmd/collect -keys-out),
 * for a signed-in user who may connect: the newest first. A copy of the
 * app with the sign-in cut out gets none, and reads no list.
 */
async function listKeys(request, env) {
  await requireCurrentClient(request, env);
  const { user } = await authUser(request, env);
  const a = await viewOf(env, user, deviceOf(request), now());
  if (!a.allowed) return json({ keys: [], reason: a.reason });
  return json({ keys: await recentListKeys(env), reason: "" });
}

/**
 * The same keys for the administrator: the tester inside the country
 * (deploy/tester) opens the sealed lists with them and seals the hot.txt it
 * publishes with the newest.
 */
async function adminListKeys(request, env) {
  await authAdmin(request, env);
  return json({ keys: await recentListKeys(env), reason: "" });
}

async function recentListKeys(env) {
  return (await storedListKeys(env))
    .filter((k) => Number.isSafeInteger(k.id) && typeof k.key === "string" && /^[0-9a-f]{64}$/.test(k.key))
    .sort((x, y) => y.id - x.id)
    .slice(0, LIST_KEYS_GIVEN);
}

/**
 * What the account page needs for other devices (iPhone, iPad, Mac):
 * the subscription address and the countries on offer.
 */
async function subInfo(request, env, url) {
  const { user } = await authUser(request, env);
  const t = now();
  const a = await viewOf(env, user, null, t);
  const entries = await subPool(env);
  return json({
    url: `${url.origin}/sub/${await subToken(env, user, t)}`,
    countries: countriesOf(entries),
    servers: entries.length,
    edge: (await edgeFor(env, a)).length,
    brand: env.SITE_NAME || "Albatross",
  });
}

/** GET /sub/<token>?c=<country|edge|empty>: the subscription a client app downloads. */
async function subscription(request, url, env) {
  if (request.method !== "GET" && request.method !== "HEAD") return new Response("Method not allowed", { status: 405 });
  const t = now();
  const p = await readSubToken(env, url.pathname.slice("/sub/".length), t);
  const user = p && (await userById(env, p.u));
  if (!user || user.token_version !== p.v) return new Response("This subscription address is no longer valid. Sign in to your account for a new one.", { status: 401 });
  // The service never hears from these devices while they are connected,
  // so the subscription holds one slot for a while after each fetch.
  const a = await claimSlot(env, user, { id: "sub", name: "" }, t, SUB_ACTIVE);
  if (a.reason === "devices") {
    return new Response(`All ${a.max_devices} devices of this account are in use. Disconnect one, or remove it on your account page.`, { status: 403 });
  }
  if (!a.allowed) return new Response("This account cannot connect right now (" + a.reason + ").", { status: 403 });
  const choice = String(url.searchParams.get("c") || "").toUpperCase().replace("EDGE", "edge");
  if (choice && choice !== "edge" && !/^[A-Z]{2}$/.test(choice)) return new Response("Not found", { status: 404 });
  const [entries, updated, edge] = await Promise.all([subPool(env), listUpdated(env), edgeFor(env, a)]);
  const sub = buildSub(entries, choice, env.SITE_NAME || "Albatross", edge, updated);
  const h = subHeaders(sub.title, a);
  return new Response(request.method === "HEAD" ? null : sub.body, { status: 200, headers: h });
}

const MAX_BYTES_PER_REPORT = 100 * 1024 ** 3;
const MAX_SECONDS_PER_REPORT = 7 * DAY;

/** The app reports data and time used since its last report and learns whether the user may go on. */
async function usage(request, env) {
  await requireCurrentClient(request, env);
  const { user, token, payload } = await authUser(request, env);
  const b = await body(request);
  const bytes = b.bytes ?? 0;
  const seconds = b.seconds ?? 0;
  if (!Number.isSafeInteger(bytes) || !Number.isSafeInteger(seconds) || bytes < 0 || seconds < 0 ||
      bytes > MAX_BYTES_PER_REPORT || seconds > MAX_SECONDS_PER_REPORT) {
    throw new HttpError(400, "Bad usage report.");
  }
  const t = now();
  const p = rollPeriod(user, t);
  if (p.rolled) {
    // A new period: only one of several devices reporting at once wins this update.
    await env.DB.prepare("UPDATE users SET period_start = ?1, used_bytes = 0, used_seconds = 0 WHERE id = ?2 AND period_start = ?3")
      .bind(p.period_start, user.id, user.period_start).run();
  }
  // A report the app sends again (its answer was lost on the way) is
  // counted once: its id, when it carries one, is remembered for a few days.
  let apply = true;
  const id = typeof b.id === "string" && /^[0-9a-f]{8,64}$/.test(b.id) ? user.id + ":" + b.id : "";
  if (id) {
    const day = Math.floor(t / DAY);
    const r = await env.DB.prepare("INSERT OR IGNORE INTO usage_ids (id, day) VALUES (?1, ?2)").bind(id, day).run();
    apply = r.meta.changes > 0;
    if (apply && Math.random() < 0.01) await env.DB.prepare("DELETE FROM usage_ids WHERE day < ?1").bind(day - 7).run();
  }
  if (apply) {
    await env.DB.prepare(
      `UPDATE users SET used_bytes = used_bytes + ?1, used_seconds = used_seconds + ?2,
         total_bytes = total_bytes + ?1, total_seconds = total_seconds + ?2, last_seen = ?3 WHERE id = ?4`,
    ).bind(bytes, seconds, t, user.id).run();
  }
  const fresh = await userById(env, user.id);
  // Sessions are renewed while they are in use.
  const renewed = payload.exp - t < SESSION / 2 ? await session(env, fresh) : token;
  const d = deviceOf(request);
  return json({ token: renewed, account: d ? await claimSlot(env, fresh, d, t) : accountOf(fresh, t) });
}

async function changePassword(request, env) {
  await requireCurrentClient(request, env);
  const { user } = await authUser(request, env);
  const { old_password, new_password } = await body(request);
  const k = await passwordKeys(request, env, user.username.toLowerCase());
  if (await passwordBlocked(env, k)) throw tooMany();
  if (typeof old_password !== "string" || !(await verifyPassword(old_password, user.pass_hash))) {
    await passwordFailed(env, k);
    throw new HttpError(403, "The current password is wrong.");
  }
  if (!checkPassword(new_password)) throw new HttpError(400, "Choose a password of at least 8 characters.");
  // Raising the version signs out every other device.
  await env.DB.prepare("UPDATE users SET pass_hash = ?1, token_version = token_version + 1 WHERE id = ?2")
    .bind(await hashPassword(new_password), user.id).run();
  await forgetDevices(env, user.id);
  const fresh = await userById(env, user.id);
  return json({ token: await session(env, fresh), account: await viewOf(env, fresh, deviceOf(request), now()) });
}

// ---------- Administrator ----------

async function adminLogin(request, env) {
  if (!env.ADMIN_USER || !env.ADMIN_PASSWORD) throw new HttpError(503, "The administrator account is not set up.");
  const { username, password } = await body(request);
  const ip = "admin:ip:" + (await clientTag(request, env));
  if (await blocked(env, ip, 10, 900)) throw tooMany();
  // Both checks always run, so the answer does not say which one was wrong.
  const [a, b] = await Promise.all([safeEqual(username ?? "", env.ADMIN_USER), safeEqual(password ?? "", env.ADMIN_PASSWORD)]);
  if (!(a && b)) {
    await count(env, ip, 900);
    throw new HttpError(401, "Wrong username or password.");
  }
  await forget(env, ip);
  return json({ token: await sign({ adm: 1, exp: now() + ADMIN_SESSION }, env.TOKEN_SECRET) });
}

/** The administrator (the release workflow) stores a release file from GitHub in the project's own storage. */
async function adminPullFile(request, env) {
  await authAdmin(request, env);
  if (!env.FILES) throw new HttpError(503, "No file storage is bound.");
  const { name, url, force } = await body(request);
  try {
    return json(await pullFile(env.FILES, String(name || ""), String(url || ""), { force: !!force }));
  } catch (e) {
    throw new HttpError(400, e.message);
  }
}

// Copies of the app send their version (X-Albatross-Version); those before
// 1.0.0 send none, so a request without it is taken to come from 0.4.0.
const OLD_CLIENT = "0.4.0";
let minClientCache = null; // { at, version }

/** The oldest version that may still connect: min_version of the published update manifest. */
async function minClient(env) {
  if (minClientCache && now() - minClientCache.at < 60) return minClientCache.version;
  let version = "";
  try {
    const obj = env.FILES && (await env.FILES.get("list/main/update.json"));
    if (obj) version = String((await obj.json()).min_version || "");
  } catch {}
  minClientCache = { at: now(), version };
  return version;
}

/**
 * Refuses copies of the app older than the update manifest's min_version,
 * so a version cut off by a forced update cannot sign in or go on even if
 * it ignores the manifest. The website's own pages (X-Albatross-Client:
 * web) are not apps and are always served.
 */
async function requireCurrentClient(request, env) {
  if (request.headers.get("X-Albatross-Client") === "web") return;
  const min = await minClient(env);
  if (!min) return;
  const version = request.headers.get("X-Albatross-Version") || OLD_CLIENT;
  if (versionLess(version, min)) {
    throw new HttpError(426, "This version of Albatross no longer works. Install the new version from https://albatrossvpn.online");
  }
}

/** The administrator (CI) publishes one of the app's own files into the project's own storage. */
async function adminPutList(request, env, path) {
  await authAdmin(request, env);
  if (!env.FILES) throw new HttpError(503, "No file storage is bound.");
  if (!LIST_PATH.test(path)) throw new HttpError(404, "Not one of the app's files.");
  if (Number(request.headers.get("Content-Length") || 0) > MAX_LIST) throw new HttpError(413, "File too large.");
  try {
    const stored = await putList(env.FILES, path, await request.arrayBuffer());
    if (path === "main/update.json") minClientCache = null;
    return json(stored);
  } catch (e) {
    throw new HttpError(400, e.message);
  }
}

/** A user as the administrator sees them. */
function adminView(u, t, active = 0) {
  const a = accountOf(u, t, { active });
  return {
    id: u.id, username: u.username, created_at: u.created_at, disabled: !!u.disabled, note: u.note,
    tier: a.tier, max_devices: a.max_devices, devices: a.devices,
    quota_bytes: u.quota_bytes, max_seconds: u.max_seconds, expires_at: u.expires_at, reset_days: u.reset_days,
    used_bytes: a.used_bytes, used_seconds: a.used_seconds, resets_at: a.resets_at, status: a.allowed ? "active" : a.reason,
    total_bytes: u.total_bytes, total_seconds: u.total_seconds, last_seen: u.last_seen,
  };
}

async function adminStats(request, env) {
  await authAdmin(request, env);
  const t = now();
  const row = await env.DB.prepare(
    `SELECT COUNT(*) AS users, COALESCE(SUM(disabled), 0) AS disabled,
       COALESCE(SUM(CASE WHEN last_seen > ?1 THEN 1 ELSE 0 END), 0) AS active_day,
       COALESCE(SUM(CASE WHEN last_seen > ?2 THEN 1 ELSE 0 END), 0) AS active_month,
       COALESCE(SUM(total_bytes), 0) AS total_bytes, COALESCE(SUM(total_seconds), 0) AS total_seconds FROM users`,
  ).bind(t - DAY, t - 30 * DAY).first();
  return json(row);
}

async function adminList(request, env, url) {
  await authAdmin(request, env);
  const q = (url.searchParams.get("q") || "").trim().toLowerCase().replace(/[\\%_]/g, "\\$&");
  const limit = Math.min(Math.max(Number(url.searchParams.get("limit")) || 25, 1), 100);
  const offset = Math.max(Number(url.searchParams.get("offset")) || 0, 0);
  const like = `%${q}%`;
  const total = await env.DB.prepare("SELECT COUNT(*) AS n FROM users WHERE username LIKE ?1 ESCAPE '\\' OR note LIKE ?1 ESCAPE '\\'").bind(like).first();
  const { results } = await env.DB.prepare(
    "SELECT * FROM users WHERE username LIKE ?1 ESCAPE '\\' OR note LIKE ?1 ESCAPE '\\' ORDER BY id DESC LIMIT ?2 OFFSET ?3",
  ).bind(like, limit, offset).all();
  const t = now();
  const active = new Map();
  if (results.length) {
    const ids = results.map((u) => u.id);
    const rows = await env.DB.prepare(
      `SELECT user_id, COUNT(*) AS n FROM devices WHERE active_until > ?1 AND user_id IN (${ids.map((_, i) => `?${i + 2}`).join(",")}) GROUP BY user_id`,
    ).bind(t, ...ids).all();
    for (const r of rows.results) active.set(r.user_id, r.n);
  }
  return json({ total: total.n, users: results.map((u) => adminView(u, t, active.get(u.id) || 0)) });
}

/** Limits from a request, as the values stored. */
function limitsFrom(b, base = {}) {
  const out = {};
  const num = (k, max) => {
    if (b[k] === undefined) return;
    const v = Number(b[k]);
    if (!Number.isSafeInteger(v) || v < 0 || v > max) throw new HttpError(400, `Bad value for ${k}.`);
    out[k] = v;
  };
  num("quota_bytes", 1024 ** 5);
  num("max_seconds", 10 * 365 * DAY);
  num("expires_at", 4102444800);
  num("reset_days", 3650);
  if (b.note !== undefined) out.note = String(b.note).slice(0, 200);
  // The plan: as chosen, or ("" / auto / none given) the one the
  // allowance earns whenever the allowance is set.
  if (b.tier !== undefined && b.tier !== "" && b.tier !== "auto") {
    if (!Object.hasOwn(TIERS, b.tier)) throw new HttpError(400, `The plan is one of ${Object.keys(TIERS).join(", ")}.`);
    out.tier = b.tier;
  } else if (out.quota_bytes !== undefined) {
    out.tier = tierForQuota(out.quota_bytes);
  }
  return { ...base, ...out };
}

async function createUser(env, name, password, limits, t) {
  if (!checkUsername(name)) throw new HttpError(400, "Usernames have 3 to 32 letters, digits, dots, dashes or underscores.");
  try {
    await env.DB.prepare(
      `INSERT INTO users (username, pass_hash, created_at, quota_bytes, max_seconds, expires_at, reset_days, period_start, note, tier)
       VALUES (?1, ?2, ?3, ?4, ?5, ?6, ?7, ?3, ?8, ?9)`,
    ).bind(name.trim().toLowerCase(), await hashPassword(password), t, limits.quota_bytes ?? 0, limits.max_seconds ?? 0,
      limits.expires_at ?? 0, limits.reset_days ?? 0, limits.note ?? "", limits.tier ?? tierForQuota(limits.quota_bytes ?? 0)).run();
  } catch (e) {
    if (/UNIQUE/i.test(String(e))) throw new HttpError(409, `The username "${name}" is taken.`);
    throw e;
  }
}

async function adminCreate(request, env) {
  await authAdmin(request, env);
  const b = await body(request);
  const password = b.password ? String(b.password) : randomPassword();
  if (!checkPassword(password)) throw new HttpError(400, "A password has at least 8 characters.");
  await createUser(env, String(b.username || ""), password, limitsFrom(b), now());
  const user = await userByName(env, b.username);
  return json({ user: adminView(user, now()), password }, 201);
}

async function adminBulk(request, env) {
  await authAdmin(request, env);
  const b = await body(request);
  const n = Number(b.count);
  const prefix = String(b.prefix || "user").toLowerCase();
  if (!Number.isInteger(n) || n < 1 || n > 200) throw new HttpError(400, "Create between 1 and 200 users at a time.");
  if (!/^[a-z0-9][a-z0-9._-]{0,20}$/.test(prefix)) throw new HttpError(400, "The prefix has up to 21 letters, digits, dots, dashes or underscores.");
  const limits = limitsFrom(b);
  const made = [];
  for (let i = 0; i < n; i++) {
    for (let tries = 0; tries < 5; tries++) {
      const name = `${prefix}${randomUsernameSuffix()}`;
      const password = randomPassword();
      try {
        await createUser(env, name, password, limits, now());
        made.push({ username: name, password });
        break;
      } catch (e) {
        if (!(e instanceof HttpError && e.status === 409)) throw e;
      }
    }
  }
  return json({ users: made }, 201);
}

async function adminUpdate(request, env, id) {
  await authAdmin(request, env);
  const user = await userById(env, id);
  if (!user) throw new HttpError(404, "No such user.");
  const b = await body(request);
  const limits = limitsFrom(b);
  const sets = [];
  const vals = [];
  const set = (col, v) => { vals.push(v); sets.push(`${col} = ?${vals.length}`); };
  for (const [k, v] of Object.entries(limits)) set(k, v);
  if (b.disabled !== undefined) set("disabled", b.disabled ? 1 : 0);
  if (b.password !== undefined) {
    if (!checkPassword(b.password)) throw new HttpError(400, "A password has at least 8 characters.");
    set("pass_hash", await hashPassword(String(b.password)));
    sets.push("token_version = token_version + 1");
  }
  if (b.reset_usage) {
    set("used_bytes", 0);
    set("used_seconds", 0);
    set("period_start", now());
  }
  if (b.sign_out) sets.push("token_version = token_version + 1");
  if (!sets.length) throw new HttpError(400, "Nothing to change.");
  vals.push(id);
  await env.DB.prepare(`UPDATE users SET ${sets.join(", ")} WHERE id = ?${vals.length}`).bind(...vals).run();
  if (b.sign_out || b.password !== undefined) await forgetDevices(env, id);
  const t = now();
  return json({ user: adminView(await userById(env, id), t, await busyDevices(env, { id }, "", t)) });
}

async function adminDelete(request, env, id) {
  await authAdmin(request, env);
  const r = await env.DB.prepare("DELETE FROM users WHERE id = ?1").bind(id).run();
  if (!r.meta.changes) throw new HttpError(404, "No such user.");
  await forgetDevices(env, id);
  return json({ ok: true });
}

async function adminSettings(request, env) {
  await authAdmin(request, env);
  return json(await settings(env));
}

async function adminSaveSettings(request, env) {
  await authAdmin(request, env);
  const b = await body(request);
  const stmts = [];
  for (const k of Object.keys(DEFAULTS)) {
    if (b[k] === undefined) continue;
    let v = String(b[k]).slice(0, 500);
    if (k === "signup_open") v = b[k] === true || v === "1" ? "1" : "0";
    else if (k === "default_tier") {
      if (v !== "auto" && !Object.hasOwn(TIERS, v)) throw new HttpError(400, `The plan is auto or one of ${Object.keys(TIERS).join(", ")}.`);
    } else if (k.startsWith("download_")) {
      if (v && !/^https:\/\//.test(v)) throw new HttpError(400, "Download links must start with https://");
    } else if (!/^\d{1,16}$/.test(v)) throw new HttpError(400, `Bad value for ${k}.`);
    stmts.push(env.DB.prepare("INSERT INTO settings (key, value) VALUES (?1, ?2) ON CONFLICT (key) DO UPDATE SET value = ?2").bind(k, v));
  }
  if (stmts.length) await env.DB.batch(stmts);
  return json(await settings(env));
}

// ---------- Crowd learning ----------
//
// Apps whose users turned on "Share anonymous connection stats" report, a
// few times a day, how many tests of each kind of connection passed on their
// operator's network. A new install on that operator then starts with the
// order that works there. Reports carry no device or server details, and no
// address is stored: the rate limit keeps only a keyed hash of it, for a day.

const CROWD_KINDS = { proto: 16, style: 32, hello: 16, port: 8 };
// Per-server reports: at most this many servers per report, kept this
// many days, and summed over the last few for the collector.
const CROWD_SERVERS = 300;
const CROWD_SERVER_DAYS = 7;
const CROWD_SERVER_WINDOW = 3;

/** Sums users' reports about servers over the last days into crowd.json for the collector (cmd/collect -crowd). */
export function crowdServersSummary(rows, today) {
  const servers = {};
  for (const r of rows) servers[r.key] = [r.ok, r.n];
  return { updated: new Date(today * DAY * 1000).toISOString(), days: CROWD_SERVER_WINDOW, servers };
}

async function publishCrowdServers(env) {
  if (!env.FILES) return null;
  const today = Math.floor(now() / DAY);
  const { results } = await env.DB.prepare(
    "SELECT key, SUM(ok) AS ok, SUM(n) AS n FROM crowd_servers WHERE day >= ?1 GROUP BY key HAVING SUM(n) >= 2",
  ).bind(today - CROWD_SERVER_WINDOW).all();
  const summary = crowdServersSummary(results, today);
  await putList(env.FILES, "servers/crowd.json", new TextEncoder().encode(JSON.stringify(summary)).buffer);
  return summary;
}

/** The administrator (or a test) publishes the summary now instead of at the hour. */
async function adminPublishCrowd(request, env) {
  await authAdmin(request, env);
  const summary = await publishCrowdServers(env);
  if (!summary) throw new HttpError(503, "No file storage is bound.");
  return json({ servers: Object.keys(summary.servers).length });
}
const CROWD_DAYS = 14;
// Reports from one address in a day. The app reports only through the VPN,
// so a server's users all share its exit address (the project's own
// servers many of them): the cap must hold that crowd, and still keep one
// address from drowning the rest.
const CROWD_REPORTS_PER_ADDRESS = 60;
// An operator's numbers are published only once this many reports came in,
// so no one report can be told apart.
const CROWD_MIN_REPORTS = 3;

async function crowdReport(request, env) {
  const b = await body(request);
  const asn = b.asn;
  if (!Number.isInteger(asn) || asn < 1 || asn > 4294967295) throw new HttpError(400, "Bad request.");
  const rows = [];
  for (const [kind, size] of Object.entries(CROWD_KINDS)) {
    const list = b[kind] ?? [];
    if (!Array.isArray(list) || list.length > size) throw new HttpError(400, "Bad request.");
    for (const r of list) {
      if (!Array.isArray(r) || r.length !== 3 || !r.every(Number.isInteger)) throw new HttpError(400, "Bad request.");
      const [code, ok, n] = r;
      if (code < 0 || code >= size || n < 1 || ok < 0 || ok > n) throw new HttpError(400, "Bad request.");
      // One report counts for at most 500 tests of a kind.
      const scale = Math.min(1, 500 / n);
      rows.push([kind, code, Math.round(ok * scale), Math.round(n * scale)]);
    }
  }
  // Per server: [key, ok, n], the key a 64-bit hash in hex (no address).
  const servers = b.servers ?? [];
  if (!Array.isArray(servers) || servers.length > CROWD_SERVERS) throw new HttpError(400, "Bad request.");
  const srows = [];
  for (const r of servers) {
    if (!Array.isArray(r) || r.length !== 3 || typeof r[0] !== "string" || !/^[0-9a-f]{1,16}$/.test(r[0])) throw new HttpError(400, "Bad request.");
    const [key, ok, n] = r;
    if (!Number.isInteger(ok) || !Number.isInteger(n) || n < 1 || ok < 0 || ok > n) throw new HttpError(400, "Bad request.");
    // One report counts for at most 20 tests of a server.
    const scale = Math.min(1, 20 / n);
    srows.push([key, Math.round(ok * scale), Math.round(n * scale)]);
  }
  const limit = "stats:" + (await clientTag(request, env));
  if (await blocked(env, limit, CROWD_REPORTS_PER_ADDRESS, DAY)) throw new HttpError(429, "Too many reports.");
  await count(env, limit, DAY);
  const day = Math.floor(now() / DAY);
  const stmts = rows.map(([kind, code, ok, n]) =>
    env.DB.prepare(
      `INSERT INTO crowd (asn, kind, code, day, ok, n) VALUES (?1, ?2, ?3, ?4, ?5, ?6)
       ON CONFLICT (asn, kind, code, day) DO UPDATE SET ok = ok + ?5, n = n + ?6`,
    ).bind(asn, kind, code, day, ok, n));
  for (const [key, ok, n] of srows) {
    stmts.push(env.DB.prepare(
      `INSERT INTO crowd_servers (key, day, ok, n) VALUES (?1, ?2, ?3, ?4)
       ON CONFLICT (key, day) DO UPDATE SET ok = ok + ?3, n = n + ?4`,
    ).bind(key, day, ok, n));
  }
  stmts.push(env.DB.prepare("DELETE FROM crowd_servers WHERE day < ?1").bind(day - CROWD_SERVER_DAYS));
  stmts.push(
    env.DB.prepare(
      `INSERT INTO crowd_reports (asn, day, reports) VALUES (?1, ?2, 1)
       ON CONFLICT (asn, day) DO UPDATE SET reports = reports + 1`,
    ).bind(asn, day),
    env.DB.prepare("DELETE FROM crowd WHERE day < ?1").bind(day - CROWD_DAYS),
    env.DB.prepare("DELETE FROM crowd_reports WHERE day < ?1").bind(day - CROWD_DAYS),
  );
  await env.DB.batch(stmts);
  return json({ ok: true });
}

// GET stats lists what passes on each network (by ASN) that enough
// installs reported. With ?asn=self it gives only the asker's own network,
// and says which one that is (an app cannot tell its ASN by itself).
async function crowdSummary(request, env, url) {
  const self = request.cf?.asn ?? 0;
  const only = url.searchParams.get("asn") === "self" ? self : 0;
  const since = Math.floor(now() / DAY) - CROWD_DAYS;
  const { results: enough } = await env.DB.prepare(
    "SELECT asn FROM crowd_reports WHERE day >= ?1 GROUP BY asn HAVING SUM(reports) >= ?2",
  ).bind(since, CROWD_MIN_REPORTS).all();
  const keep = new Set(enough.map((r) => r.asn));
  const { results } = await env.DB.prepare(
    "SELECT asn, kind, code, SUM(ok) AS ok, SUM(n) AS n FROM crowd WHERE day >= ?1 GROUP BY asn, kind, code",
  ).bind(since).all();
  const out = {};
  for (const r of results) {
    if (!keep.has(r.asn) || (only && r.asn !== only)) continue;
    const a = (out[r.asn] ??= {});
    (a[r.kind] ??= []).push([r.code, r.ok, r.n]);
  }
  return json({ asn: self, asns: out });
}

// download serves the release files (the APK and the Windows installer)
// from GitHub's releases through Cloudflare, for networks where GitHub's
// downloads break off or are filtered: /dl/v0.4.1/Albatross-0.4.1-android.apk.
// Only those files; ranges pass through so interrupted downloads resume,
// and the edge keeps a copy for a day.
const DOWNLOADS = /^\/dl\/(v\d+\.\d+\.\d+)\/(Albatross-\d+\.\d+\.\d+-android\.apk|Albatross-Setup-\d+\.\d+\.\d+\.exe)$/;
async function download(request, url, env) {
  const m = DOWNLOADS.exec(url.pathname);
  if (!m) return new Response("Not found", { status: 404 });
  if (env.FILES) {
    const r = await serveFile(request, env.FILES, m[2]);
    if (r) return r;
  }
  const headers = {};
  const range = request.headers.get("Range");
  if (range) headers.Range = range;
  const up = await fetch(`https://github.com/Albatrossvpn/Albatross/releases/download/${m[1]}/${m[2]}`, {
    headers,
    redirect: "follow",
    cf: { cacheTtl: 86400, cacheEverything: true },
  });
  if (!up.ok && up.status !== 206) return new Response("Not found", { status: 404 });
  const out = new Headers();
  for (const h of ["Content-Length", "Content-Range", "Accept-Ranges", "ETag", "Last-Modified"]) {
    const v = up.headers.get(h);
    if (v) out.set(h, v);
  }
  out.set("Content-Type", m[2].endsWith(".apk") ? "application/vnd.android.package-archive" : "application/octet-stream");
  out.set("Content-Disposition", `attachment; filename="${m[2]}"`);
  out.set("Cache-Control", "public, max-age=86400");
  out.set("X-Content-Type-Options", "nosniff");
  return new Response(up.body, { status: up.status, headers: out });
}

// mirror serves the app's own files (lists, update manifest, ad list) from
// the project's own storage, which CI fills (deploy/files/publish-lists.sh),
// or from GitHub while a file is not there yet and the repository is public.
// Only those files.
async function mirror(request, url, env) {
  const path = url.pathname.slice("/list/".length);
  if (!LIST_PATH.test(path)) return new Response("Not found", { status: 404 });
  if (env.FILES) {
    const r = await serveList(request, env.FILES, path);
    if (r) return r;
  }
  const up = await fetch("https://raw.githubusercontent.com/Albatrossvpn/Albatross/" + path, { cf: { cacheTtl: 600, cacheEverything: true } });
  if (!up.ok) return new Response("Not found", { status: 404 });
  return new Response(up.body, {
    status: 200,
    headers: { "Content-Type": "text/plain; charset=utf-8", "Cache-Control": "public, max-age=600" },
  });
}
