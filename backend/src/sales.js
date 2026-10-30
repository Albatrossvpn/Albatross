// Selling data with Telegram Stars (the Telegram bot's "Buy data", telegram.js):
// the packages and their prices, the orders and payments, and what a payment
// does to an account: a new one, or data added to one that exists. Telegram
// requires Stars for digital goods and services, so no payment provider and
// no key is involved: an invoice in XTR, paid inside Telegram.
import { DAY, TIERS, hashPassword, randomPassword, tierForQuota, tierOf } from "./lib.js";

const GB = 1024 ** 3;
const now = () => Math.floor(Date.now() / 1000);

/**
 * The ready-made packages, in GB a month: where the store starts. A buyer
 * can make any plan of their own from them (plan): more or less data, and
 * one to twelve months. A plan gives all its months' data at once and lasts
 * that many months; its total earns the plan (10 GB Silver, 100 GB Gold).
 */
export const PACKAGES = [5, 10, 25, 50, 100];

/** The lengths a plan can have, in months. */
export const MONTHS = [1, 3, 6, 12];

/** The data a plan can have a month: from MIN_GB to MAX_GB, in steps that grow with it (stepOf). */
export const MIN_GB = 5;
export const MAX_GB = 500;
export const stepOf = (gb) => (gb < 50 ? 5 : gb < 200 ? 10 : 50);

// The settings (the administration panel's), as the service starts: selling
// is off until the owner has tried a payment and turns it on.
export const SALES_DEFAULTS = {
  stars_enabled: "0",
  stars_price_toman: "10000", // what 1 GB costs, in toman
  stars_usd_toman: "270000", // what a dollar costs, in toman
  stars_usd_per_1000: "13", // what 1,000 Stars are worth to the seller, in dollars (Telegram pays out about 13)
  stars_valid_days: "30", // days a month of a plan lasts; 0: it never ends
  // Less per GB for more data a month, and for more months: "GB:percent" and
  // "months:percent", the largest step reached counts.
  stars_size_discounts: "10:5,25:10,50:15,100:20",
  stars_month_discounts: "3:10,6:20,12:30",
};
/** What the panel may set (inclusive). */
export const SALES_RANGE = {
  stars_price_toman: [1, 10_000_000],
  stars_usd_toman: [1000, 100_000_000],
  stars_usd_per_1000: [1, 100],
  stars_valid_days: [0, 3650],
};

/** Parses "10:5,25:10" into [[10, 5], [25, 10]], highest first; null when it is not that shape. */
export function parseSteps(v) {
  const s = String(v ?? "").trim();
  if (s === "") return [];
  if (!/^\d{1,4}:\d{1,2}(,\d{1,4}:\d{1,2}){0,9}$/.test(s)) return null;
  const steps = s.split(",").map((x) => x.split(":").map(Number));
  if (steps.some(([, pct]) => pct > 90)) return null;
  return steps.sort((a, b) => b[0] - a[0]);
}
const stepPct = (steps, n) => (steps.find(([at]) => n >= at) || [0, 0])[1];

/** The selling settings, as numbers. */
export async function config(env) {
  const keys = Object.keys(SALES_DEFAULTS);
  const { results } = await env.DB.prepare(`SELECT key, value FROM settings WHERE key IN (${keys.map((_, i) => `?${i + 1}`).join(", ")})`).bind(...keys).all();
  const s = { ...SALES_DEFAULTS, ...Object.fromEntries(results.map((r) => [r.key, r.value])) };
  return {
    enabled: s.stars_enabled === "1",
    priceToman: Number(s.stars_price_toman),
    usdToman: Number(s.stars_usd_toman),
    starsPer1000: Number(s.stars_usd_per_1000),
    days: Number(s.stars_valid_days),
    sizeSteps: parseSteps(s.stars_size_discounts) || parseSteps(SALES_DEFAULTS.stars_size_discounts),
    monthSteps: parseSteps(s.stars_month_discounts) || parseSteps(SALES_DEFAULTS.stars_month_discounts),
  };
}

/**
 * The Stars `gb` a month for `months` cost: what the data is worth in dollars
 * (price per GB over the dollar's price), less the discount for the data a
 * month and the one for the months, over what a Star is worth to the seller,
 * rounded up.
 */
export function starsFor(gb, cfg, months = 1) {
  const off = (1 - stepPct(cfg.sizeSteps || [], gb) / 100) * (1 - stepPct(cfg.monthSteps || [], months) / 100);
  const dollars = ((gb * months * cfg.priceToman) / cfg.usdToman) * off;
  return Math.max(1, Math.ceil(dollars / (cfg.starsPer1000 / 1000) - 1e-9));
}

/** Whether `gb` a month for `months` is a plan the store sells. */
export const validPlan = (gb, months) => Number.isInteger(gb) && gb >= MIN_GB && gb <= MAX_GB && gb % stepOf(gb) === 0 && MONTHS.includes(months);

/**
 * A plan of `gb` a month for `months`: its total data, days, Stars, the
 * saving against paying month by month at the full price, and the plan its
 * total earns. Null for a plan the store does not sell.
 */
export function plan(cfg, gb, months = 1) {
  gb = Number(gb);
  months = Number(months);
  if (!validPlan(gb, months)) return null;
  const total = gb * months;
  const tier = tierForQuota(total * GB);
  const stars = starsFor(gb, cfg, months);
  const full = Math.ceil(((gb * months * cfg.priceToman) / cfg.usdToman) / (cfg.starsPer1000 / 1000) - 1e-9);
  return {
    gb, months, total, stars, days: cfg.days > 0 ? cfg.days * months : 0,
    save: full > stars ? Math.round((1 - stars / full) * 100) : 0,
    tier, plan: TIERS[tier].name, devices: TIERS[tier].devices,
  };
}

/** The ready-made packages, for a month, with their prices and the plan each earns. */
export function catalog(cfg) {
  return PACKAGES.map((gb) => plan(cfg, gb, 1));
}

// ---------- Orders and payments ----------

export async function newOrder(env, { chat, kind, gb, stars, days, target = "" }) {
  const t = now();
  const made = await env.DB.prepare("INSERT INTO stars_orders (chat_id, kind, gb, stars, days, target, created, updated) VALUES (?1, ?2, ?3, ?4, ?5, ?6, ?7, ?7)").bind(chat, kind, gb, stars, days, target, t).run();
  return made.meta.last_row_id;
}

export const orderOf = (env, id) => env.DB.prepare("SELECT * FROM stars_orders WHERE id = ?1").bind(id).first();
export const paymentOf = (env, id) => env.DB.prepare("SELECT * FROM stars_payments WHERE id = ?1").bind(id).first();

/** Takes an order for payment: true for the first to ask (an order checked out and left for five minutes may be asked for again). */
export async function claimOrder(env, id, t) {
  const taken = await env.DB.prepare("UPDATE stars_orders SET status = 'checkout', updated = ?2 WHERE id = ?1 AND (status = 'new' OR (status = 'checkout' AND updated < ?2 - 300))").bind(id, t).run();
  return taken.meta.changes > 0;
}

/** Notes a payment Telegram says was made; `fresh` is false when it was noted before (Telegram sends an update twice). */
export async function recordPayment(env, { chargeId, chat, order, stars }) {
  const t = now();
  const made = await env.DB.prepare(
    `INSERT INTO stars_payments (charge_id, chat_id, order_id, kind, gb, stars, created, updated) VALUES (?1, ?2, ?3, ?4, ?5, ?6, ?7, ?7)
     ON CONFLICT (charge_id) DO NOTHING`,
  ).bind(chargeId, chat, order.id, order.kind, order.gb, stars, t).run();
  if (!made.meta.changes) return { fresh: false };
  await env.DB.prepare("UPDATE stars_orders SET status = 'paid', updated = ?2 WHERE id = ?1").bind(order.id, t).run();
  return { fresh: true, id: made.meta.last_row_id };
}

/** An account for a recharge: whether it exists, and whether it can be recharged (it has a data limit that does not renew by itself, and is not disabled). */
export async function accountFor(env, name) {
  const u = await env.DB.prepare("SELECT id, username, disabled, quota_bytes, reset_days FROM users WHERE username = ?1 COLLATE NOCASE").bind(String(name).trim().toLowerCase()).first();
  if (!u) return null;
  return { username: u.username, fits: !u.disabled && u.quota_bytes > 0 && u.reset_days === 0 };
}

const digits = (n) => Array.from(crypto.getRandomValues(new Uint8Array(n)), (b) => b % 10).join("");
const rank = (tier) => Object.keys(TIERS).indexOf(tierOf(tier));

/**
 * Does what a paid order is for, once: a new account (answers its username and
 * password), or data added to one (answers what it has now), and marks the
 * payment delivered in the same step. Throws when it cannot, and then nothing
 * was changed.
 */
export async function deliver(env, order, paymentId) {
  const t = now();
  const bytes = order.gb * GB;
  const delivered = (username) => env.DB.prepare("UPDATE stars_payments SET status = 'delivered', username = ?2, grant_bytes = ?3, updated = ?4 WHERE id = ?1 AND changes() > 0").bind(paymentId, username, bytes, t);
  if (order.kind === "test") {
    await env.DB.prepare("UPDATE stars_payments SET status = 'delivered', updated = ?2 WHERE id = ?1").bind(paymentId, t).run();
    return { kind: "test" };
  }
  if (order.kind === "new") {
    const password = randomPassword();
    const hash = await hashPassword(password);
    const expires = order.days > 0 ? t + order.days * DAY : 0;
    const tier = tierForQuota(bytes);
    for (let tries = 0; tries < 8; tries++) {
      const username = `tg${digits(7)}`;
      try {
        await env.DB.batch([
          env.DB.prepare(
            `INSERT INTO users (username, pass_hash, created_at, quota_bytes, max_seconds, expires_at, reset_days, period_start, note, tier, tg_owner)
             VALUES (?1, ?2, ?3, ?4, 0, ?5, 0, ?3, ?6, ?7, ?8)`,
          ).bind(username, hash, t, bytes, expires, `Stars P${paymentId}`, tier, order.chat_id),
          delivered(username),
        ]);
        return { kind: "new", username, password, quota_bytes: bytes, expires_at: expires, tier };
      } catch (e) {
        if (!/UNIQUE/i.test(String(e))) throw e;
      }
    }
    throw new Error("No free username.");
  }
  // Data added to an account: what is left of the period's data (if the account has not ended) and the new data, for the days of a package.
  const [added] = await env.DB.batch([
    env.DB.prepare(
      `UPDATE users SET
         quota_bytes = CASE WHEN expires_at = 0 OR expires_at > ?2 THEN MAX(quota_bytes - used_bytes, 0) ELSE 0 END + ?3,
         used_bytes = 0, used_seconds = 0, period_start = ?2,
         expires_at = CASE WHEN ?4 > 0 THEN MAX(expires_at, ?2) + ?4 * 86400 ELSE 0 END
       WHERE username = ?1 COLLATE NOCASE AND disabled = 0 AND quota_bytes > 0 AND reset_days = 0`,
    ).bind(order.target, t, bytes, order.days),
    delivered(order.target),
  ]);
  if (!added.meta.changes) throw new Error("The account cannot be recharged.");
  const u = await env.DB.prepare("SELECT username, quota_bytes, expires_at, tier FROM users WHERE username = ?1 COLLATE NOCASE").bind(order.target).first();
  // A plan an account has is not taken away by a small recharge.
  const tier = rank(tierForQuota(u.quota_bytes)) > rank(u.tier) ? tierForQuota(u.quota_bytes) : tierOf(u.tier);
  if (tier !== u.tier) await env.DB.prepare("UPDATE users SET tier = ?2 WHERE username = ?1 COLLATE NOCASE").bind(order.target, tier).run();
  return { kind: "topup", username: u.username, quota_bytes: u.quota_bytes, expires_at: u.expires_at, tier };
}

/** The accounts this Telegram account bought (new ones, not recharges; switched-off ones are not), newest first: the ones whose password it may ask for anew. */
export async function ownedAccounts(env, chat) {
  const { results } = await env.DB.prepare("SELECT username FROM users WHERE tg_owner = ?1 AND disabled = 0 ORDER BY id DESC LIMIT 5").bind(chat).all();
  return results.map((r) => r.username);
}

/** A new password for an account this Telegram account bought; every device is signed out. Answers the password, or null when the account is not this person's. */
export async function resetPassword(env, chat, username) {
  const u = await env.DB.prepare("SELECT id FROM users WHERE username = ?1 COLLATE NOCASE AND tg_owner = ?2 AND disabled = 0").bind(String(username).trim().toLowerCase(), chat).first();
  if (!u) return null;
  const password = randomPassword();
  const hash = await hashPassword(password);
  await env.DB.batch([
    env.DB.prepare("UPDATE users SET pass_hash = ?2, token_version = token_version + 1 WHERE id = ?1").bind(u.id, hash),
    env.DB.prepare("DELETE FROM devices WHERE user_id = ?1").bind(u.id),
  ]);
  return password;
}

/** Takes back what a refunded payment gave: a new account is disabled, data added is taken off again (never below what was used). */
export async function reverse(env, payment) {
  if (!payment.username) return;
  if (payment.kind === "new") await env.DB.prepare("UPDATE users SET disabled = 1, token_version = token_version + 1 WHERE username = ?1 COLLATE NOCASE").bind(payment.username).run();
  else if (payment.kind === "topup" && payment.grant_bytes > 0) await env.DB.prepare("UPDATE users SET quota_bytes = MAX(used_bytes, quota_bytes - ?2) WHERE username = ?1 COLLATE NOCASE AND quota_bytes > 0").bind(payment.username, payment.grant_bytes).run();
}

/** Orders nobody paid and payments long settled are forgotten: orders after a day, records of payments after two years. */
export async function purge(env, t) {
  await env.DB.batch([
    env.DB.prepare("DELETE FROM stars_orders WHERE status IN ('new', 'checkout') AND created < ?1").bind(t - DAY),
    env.DB.prepare("DELETE FROM stars_payments WHERE created < ?1").bind(t - 730 * DAY),
    env.DB.prepare("DELETE FROM stars_orders WHERE created < ?1").bind(t - 730 * DAY),
  ]);
}
