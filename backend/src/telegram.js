// The Telegram support bot: the same assistant as the websites' chat
// (support.js), with a menu, guides for a connection that fails, and a team
// behind it. A person who needs more opens a ticket; the team (the owner's
// chat, paired with /pair) reads it in Telegram and answers by replying to its
// message. The bot's token is pasted in the administration panel, which sets
// the webhook; Telegram calls POST /api/v1/telegram with a secret the panel's
// service derives from its own, so nothing else needs to be set up.
//
// What is kept: a chat's messages until about half an hour after the last one
// (the schedule runs every minute), when a summary is mailed to the owner and
// they are deleted; a ticket's messages until it is closed, and a closed
// ticket keeps its number and nothing of the person. Rate limits remember a
// keyed hash of a person's id, never the id.
//
// The bot also sells data with Telegram Stars (the last section; what a
// payment does to an account is in sales.js).
import { DAY, TIERS, b64u, checkUsername, safeEqual, tag, tierOf } from "./lib.js";
import * as sales from "./sales.js";
import { CANNED, allowed, cleanMessages, mailOwner, oneLine, reply, stamp, today } from "./support.js";
import * as view from "./telegram-content.js";

export const SWEEP_CRON = "* * * * *";

const IDLE = 30 * 60; // a chat with no new message for this long is over
const TICKET_IDLE = 3 * DAY; // a ticket nobody writes to is closed
const SWEEP_MAX = 3; // chats ended per run: each may take a summary and a mail
const SWEEP_BUDGET = 20_000; // milliseconds after which a run starts nothing new
const CLAIM = 120; // seconds a ticket that is being opened holds its person's place
const MAX_STORED = 3000; // one message
const MAX_ROWS = 300; // messages of one chat that go into its summary mail
const AI_TIMEOUT = 18_000; // the platform leaves 30 seconds after the response for all of it
const TOKEN = /^\d{5,12}:[A-Za-z0-9_-]{30,64}$/;
const FIRST_TICKET = 1000;
// One connection: Telegram sends the next update only after the last was answered.
const HOOK = { allowed_updates: ["message", "callback_query", "pre_checkout_query"], max_connections: 1 };

const enc = new TextEncoder();
const now = () => Math.floor(Date.now() / 1000);
const sleep = (ms) => new Promise((resolve) => setTimeout(resolve, ms));
const log = (what) => (e) => console.error(`telegram ${what}:`, e && e.stack ? e.stack : e);

const COMMANDS = [
  { command: "start", description: "Start, and the main menu" },
  { command: "menu", description: "Show the main menu" },
  { command: "human", description: "Talk to our team (opens a ticket)" },
  { command: "close", description: "Close your ticket, or end this chat" },
  { command: "password", description: "A new password for an account you bought here" },
  { command: "help", description: "How this bot works" },
];
// While data is on sale the menu also has what Telegram requires of a bot that sells.
const STORE_COMMANDS = [
  { command: "buy", description: "Buy data with Telegram Stars" },
  { command: "terms", description: "Terms of buying data" },
  { command: "paysupport", description: "Help with a payment" },
];
const commandsFor = (selling) => (selling ? [...COMMANDS.slice(0, 3), ...STORE_COMMANDS, ...COMMANDS.slice(3)] : COMMANDS);
const TEAM_COMMANDS = [
  { command: "tickets", description: "The open tickets" },
  { command: "stats", description: "Today's numbers" },
  { command: "sales", description: "Sales, and the last payments" },
  { command: "refund", description: "Give a payment back" },
  { command: "teststars", description: "Test a payment of 1 Star" },
  { command: "close", description: "Close a ticket" },
  { command: "block", description: "Block a user" },
  { command: "unblock", description: "Let a user back" },
  { command: "help", description: "The team's commands" },
];
// What people see of the bot before they write: its menu of commands, a
// description (shown in an empty chat) and an "about" line (its profile, and
// the links people share).
const ABOUT = "Albatross VPN support. Get help installing, connecting and managing your account, and buy data with Telegram Stars. An AI assistant answers at once, and our team follows up on tickets. albatrossvpn.online";
const SHORT = "Albatross VPN support and data. albatrossvpn.online";
const TEXTS_RETRY = 10 * 60; // seconds a refused attempt to set them waits

// ---------- Settings and the Bot API ----------

const KEYS = ["telegram_bot_token", "telegram_bot_username", "telegram_owner_chat", "telegram_webhook_url"];

/** The bot's token, name, webhook address and the team's chat, in one read. */
async function config(env) {
  const { results } = await env.DB.prepare(`SELECT key, value FROM settings WHERE key IN (${KEYS.map((_, i) => `?${i + 1}`).join(", ")})`).bind(...KEYS).all();
  const c = Object.fromEntries(results.map((r) => [r.key, r.value]));
  return { token: c.telegram_bot_token || "", name: c.telegram_bot_username || "", team: Number(c.telegram_owner_chat) || 0, url: c.telegram_webhook_url || "" };
}

const setting = async (env, key) => ((await env.DB.prepare("SELECT value FROM settings WHERE key = ?1").bind(key).first()) || {}).value || "";
const save = (env, key, value) => env.DB.prepare("INSERT INTO settings (key, value) VALUES (?1, ?2) ON CONFLICT (key) DO UPDATE SET value = ?2").bind(key, value).run();
const drop = (env, ...keys) => env.DB.batch(keys.map((k) => env.DB.prepare("DELETE FROM settings WHERE key = ?1").bind(k)));
const forgetChats = (env) => env.DB.batch(["tg_users", "tg_messages", "tg_tickets", "tg_relay"].map((t) => env.DB.prepare(`DELETE FROM ${t}`)));

/** Text that fits in a message once made safe for Telegram's HTML; a cut is marked with an ellipsis and never splits a character. */
function fit(text, max = 3900) {
  let t = String(text);
  let cut = false;
  while (view.esc(t).length > (cut ? max - 1 : max)) {
    t = t.slice(0, Math.floor(t.length * 0.9));
    cut = true;
  }
  if (cut && /[\ud800-\udbff]$/.test(t)) t = t.slice(0, -1);
  return view.esc(t) + (cut ? "…" : "");
}

/** The Bot API for one token. Every call answers `{ ok, result, description }` and never throws; the token is never in what is logged. */
function bot(env, token) {
  const base = (env.TELEGRAM_API || "https://api.telegram.org").replace(/\/$/, "");
  async function once(method, payload) {
    const controller = new AbortController();
    const timer = setTimeout(() => controller.abort(), 10_000);
    try {
      const res = await fetch(`${base}/bot${token}/${method}`, { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify(payload), signal: controller.signal });
      return await res.json();
    } catch (e) {
      return { ok: false, description: e && e.name === "AbortError" ? "Telegram took too long to answer." : "Telegram could not be reached." };
    } finally {
      clearTimeout(timer);
    }
  }
  async function call(method, payload = {}) {
    const out = await once(method, payload);
    // Telegram asks to wait when a chat gets too much at once: a short wait is kept, a long one is not.
    const wait = out && out.error_code === 429 && out.parameters ? Number(out.parameters.retry_after) : 0;
    if (wait > 0 && wait <= 5) {
      await sleep(wait * 1000 + 100);
      return once(method, payload);
    }
    return out;
  }
  const html = { parse_mode: "HTML", link_preview_options: { is_disabled: true } };
  async function send(chat, text, markup, extra = {}) {
    const body = { chat_id: chat, text, ...html, ...(markup ? { reply_markup: markup } : {}), ...extra };
    const res = await call("sendMessage", body);
    // Text Telegram cannot read as HTML is sent as it is rather than lost.
    if (!res.ok && /parse entities/i.test(res.description || "")) return call("sendMessage", { ...body, text: text.replace(/<[^>]+>/g, ""), parse_mode: undefined });
    return res;
  }
  return {
    call,
    send,
    edit: (chat, id, text, markup) => call("editMessageText", { chat_id: chat, message_id: id, text, ...html, ...(markup ? { reply_markup: markup } : {}) }),
    keys: (chat, id, markup) => call("editMessageReplyMarkup", { chat_id: chat, message_id: id, reply_markup: markup || { inline_keyboard: [] } }),
    answer: (id, text = "", alert = false) => call("answerCallbackQuery", { callback_query_id: id, text, show_alert: alert }),
    typing: (chat) => call("sendChatAction", { chat_id: chat, action: "typing" }),
    react: (chat, id, emoji) => call("setMessageReaction", { chat_id: chat, message_id: id, reaction: [{ type: "emoji", emoji }] }),
    copy: (to, from, id, extra = {}) => call("copyMessage", { chat_id: to, from_chat_id: from, message_id: id, ...extra }),
  };
}

const sameCommands = (got, want) => Array.isArray(got) && got.length === want.length && got.every((c, i) => c.command === want[i].command && c.description === want[i].description);

/** Makes the bot's menu and texts what this service wants: reads them, writes only what differs, and answers what Telegram refused (one phrase each, none when all is as wanted). */
async function applyTexts(tg, team, commands) {
  const [menu, about, short, mine] = await Promise.all([
    tg.call("getMyCommands"),
    tg.call("getMyDescription"),
    tg.call("getMyShortDescription"),
    team ? tg.call("getMyCommands", { scope: { type: "chat", chat_id: team } }) : null,
  ]);
  const todo = [
    [menu.ok && sameCommands(menu.result, commands), "menu", () => tg.call("setMyCommands", { commands })],
    [about.ok && (about.result || {}).description === ABOUT, "description", () => tg.call("setMyDescription", { description: ABOUT })],
    [short.ok && (short.result || {}).short_description === SHORT, "about line", () => tg.call("setMyShortDescription", { short_description: SHORT })],
    ...(team ? [[mine.ok && sameCommands(mine.result, TEAM_COMMANDS), "team menu", () => tg.call("setMyCommands", { commands: TEAM_COMMANDS, scope: { type: "chat", chat_id: team } })]] : []),
  ];
  const refused = [];
  for (const [fine, what, set] of todo) {
    if (fine) continue;
    const res = await set();
    if (!res.ok) refused.push(`${what}: ${oneLine(res.description, 100)}`);
  }
  return refused;
}

/**
 * Does `work` (it answers what was refused) when this version of the service
 * wants others than the last time (`version`) or the last try did not take;
 * a refused try waits ten minutes. `force` does it anyway. The outcome is kept
 * under `key`: `{ v, at, ok, note }`.
 */
async function versioned(env, key, version, force, work) {
  let last = {};
  try {
    last = JSON.parse(await setting(env, key)) || {};
  } catch {
    last = {};
  }
  if (!force && last.v === version && (last.ok || now() - last.at < TEXTS_RETRY)) return last;
  const refused = await work();
  const out = { v: version, at: now(), ok: refused.length === 0, note: refused.join("; ") };
  await save(env, key, JSON.stringify(out));
  return out;
}

/** The menu and texts (the menu has the store's commands while data is on sale). Answers `{ ok, note }`: what Telegram refused. */
async function refreshTexts(env, tg, team, force = false) {
  const commands = commandsFor((await sales.config(env)).enabled);
  const version = await tag("albatross telegram texts", JSON.stringify([commands, TEAM_COMMANDS, ABOUT, SHORT]));
  return versioned(env, "telegram_texts", version, force, () => applyTexts(tg, team, commands));
}

const hookVersion = (url) => tag("albatross telegram hook", JSON.stringify([HOOK, url]));

/** The webhook, set again (waiting updates kept) when this version of the service wants other updates than the last one set. */
async function refreshHook(env, tg, url, force = false) {
  if (!url) return { ok: true };
  return versioned(env, "telegram_hook", await hookVersion(url), force, async () => {
    const res = await tg.call("setWebhook", { url, secret_token: await hookSecret(env), ...HOOK });
    return res.ok ? [] : [`webhook: ${oneLine(res.description, 100)}`];
  });
}

/** The secret Telegram sends with every call, derived from the service's own so nothing else is stored. */
async function hookSecret(env) {
  const key = await crypto.subtle.importKey("raw", enc.encode(env.TOKEN_SECRET), { name: "HMAC", hash: "SHA-256" }, false, ["sign"]);
  return b64u.enc(new Uint8Array(await crypto.subtle.sign("HMAC", key, enc.encode("albatross telegram webhook"))));
}

// ---------- The administration panel connects and disconnects ----------

const ALPHABET = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789";
const newCode = () => Array.from(crypto.getRandomValues(new Uint8Array(8)), (b) => ALPHABET[b % ALPHABET.length]).join("");
const normalCode = (s) => String(s || "").toUpperCase().replace(/[^A-Z0-9]/g, "");

/** Connects a bot: checks the token, sets the webhook and the menu. Answers `{ username }` (with a `warning` when Telegram refused its menu or texts) or `{ error }`. */
export async function connect(env, request, token) {
  token = String(token || "").trim();
  if (!TOKEN.test(token)) return { error: "That does not look like a bot token. Copy it from @BotFather; it looks like 123456789:AAH-..." };
  const tg = bot(env, token);
  const me = await tg.call("getMe");
  if (!me.ok) return { error: `Telegram did not accept this token: ${oneLine(me.description, 120)}` };
  const url = `${new URL(request.url).origin}/api/v1/telegram`;
  const hook = await tg.call("setWebhook", { url, secret_token: await hookSecret(env), ...HOOK, drop_pending_updates: true });
  if (!hook.ok) return { error: `Telegram could not set the webhook: ${oneLine(hook.description, 120)}` };
  await save(env, "telegram_hook", JSON.stringify({ v: await hookVersion(url), at: now(), ok: true, note: "" }));
  // Another bot than before: the old one stops sending here, and what belonged to it is forgotten, the paired team chat included.
  const before = await config(env);
  if (before.token && before.token.split(":")[0] !== token.split(":")[0]) {
    await bot(env, before.token).call("deleteWebhook", { drop_pending_updates: true });
    await forgetChats(env);
    await drop(env, "telegram_owner_chat", "telegram_pair_code");
  }
  // The menu and the texts people see before they write. The bot works without them, so a refusal does not stop it, but it is told.
  const texts = await refreshTexts(env, tg, 0, true);
  const username = String(me.result.username || "");
  await save(env, "telegram_bot_token", token);
  await save(env, "telegram_bot_username", username);
  await save(env, "telegram_webhook_url", url);
  if (!(await setting(env, "telegram_owner_chat")) && !(await setting(env, "telegram_pair_code"))) await save(env, "telegram_pair_code", newCode());
  return { username, ...(texts.ok ? {} : { warning: `Telegram did not take the bot's ${texts.note}. "Check the bot" tries again.` }) };
}

/** Disconnects the bot and forgets every chat. */
export async function disconnect(env) {
  const { token } = await config(env);
  if (token) await bot(env, token).call("deleteWebhook", { drop_pending_updates: true });
  await drop(env, ...KEYS, "telegram_pair_code", "telegram_texts", "telegram_hook");
  await forgetChats(env);
}

/** A new pairing code; the chat paired until now stays until another is paired. */
export async function repair(env) {
  const code = newCode();
  await save(env, "telegram_pair_code", code);
  return code;
}

/** What Telegram says about the webhook, in a sentence, and the state of the bot's menu and texts (set again if they are not as wanted), for the panel's "Check the bot". */
export async function check(env) {
  const { token, name, team } = await config(env);
  if (!token) return "The bot is not connected.";
  const tg = bot(env, token);
  const info = await tg.call("getWebhookInfo");
  if (!info.ok) return `Telegram did not answer: ${oneLine(info.description, 120)}`;
  const hook = info.result || {};
  if (!hook.url) return `@${name} is reachable, but its webhook is not set. Save the token again.`;
  const when = hook.last_error_date ? new Date(hook.last_error_date * 1000).toISOString().slice(0, 16).replace("T", " ") : "";
  const last = hook.last_error_message ? ` Last error: ${oneLine(hook.last_error_message, 120)} (${when} UTC).` : " No errors.";
  const texts = await refreshTexts(env, tg, team, true);
  return `@${name} is reachable; its webhook is set and ${hook.pending_update_count || 0} update(s) are waiting.${last} ${texts.ok ? "Its menu and texts are set." : `Telegram refused its ${texts.note}.`}`;
}

/** Every hour: a webhook that is gone, or points elsewhere, is set again, and the menu and texts are looked at. */
export async function heal(env) {
  const { token, url, team } = await config(env);
  if (!token) return;
  const tg = bot(env, token);
  if (url) {
    const info = await tg.call("getWebhookInfo");
    if (info.ok && !(info.result && info.result.url === url)) {
      const res = await tg.call("setWebhook", { url, secret_token: await hookSecret(env), ...HOOK });
      console.warn("telegram: the webhook was not set; it was set again:", res.ok ? "ok" : oneLine(res.description, 100));
    }
  }
  await refreshTexts(env, tg, team, true);
}

// ---------- The webhook ----------

/** POST /telegram: an update from Telegram; answered at once, handled after. */
export async function webhook(request, env, kit, ctx) {
  // Whoever is not Telegram costs nothing more than this comparison.
  if (!(await safeEqual(request.headers.get("X-Telegram-Bot-Api-Secret-Token") || "", await hookSecret(env)))) return new Response("Not allowed.", { status: 401 });
  const c = await config(env);
  if (!c.token) return new Response("Not connected.", { status: 404 });
  let update;
  try {
    const text = await request.text();
    update = text.length <= 64 * 1024 ? JSON.parse(text) : null;
  } catch {
    update = null;
  }
  if (!update || typeof update !== "object" || !Number.isSafeInteger(update.update_id)) return new Response("Bad request.", { status: 400 });
  const run = { env, kit, tg: bot(env, c.token), team: c.team, name: c.name };
  const work = handle(run, update).catch((e) => failed(run, update, e));
  if (ctx && ctx.waitUntil) ctx.waitUntil(work);
  else await work;
  return new Response("ok");
}

/** Something broke while handling an update: it is logged, and the person is told rather than left waiting. */
async function failed(c, update, e) {
  log("update")(e);
  const q = update.callback_query;
  const m = update.message;
  if (q) await c.tg.answer(q.id, "Something went wrong. Please try again.");
  else if (m && m.chat && m.chat.type === "private" && m.from && !m.from.is_bot) await c.tg.send(m.chat.id, view.WENT_WRONG);
}

/** Who gets what: the team's chat, the people in private chats, nobody else. */
async function handle(c, update) {
  if (update.callback_query) return onCallback(c, update);
  if (update.pre_checkout_query) return onCheckout(c, update);
  const m = update.message;
  if (!m || !m.chat) return;
  // A group that became a supergroup has a new id: the team follows it.
  if (m.migrate_to_chat_id) return c.team && m.chat.id === c.team ? moveTeam(c, m.migrate_to_chat_id) : undefined;
  // What Telegram says of a payment: it was made, or it was given back.
  if (m.successful_payment && m.from && !m.from.is_bot) return onPaid(c, m);
  if (m.refunded_payment && m.from) return onRefunded(c, m);
  const person = !!m.from && !m.from.is_bot;
  // An administrator who writes anonymously in a group is the group (Telegram's own "GroupAnonymousBot").
  const anonymous = !!m.from && m.from.username === "GroupAnonymousBot" && m.chat.type !== "private";
  if (!person && !anonymous) return;
  const text = typeof m.text === "string" ? m.text : "";
  const pair = /^\/pair(?:@\w+)?(?:\s+([\s\S]*))?$/i.exec(text);
  if (pair) return onPair(c, m, pair[1]);
  if (c.team && m.chat.id === c.team && (await onTeam(c, m))) return;
  if (m.chat.type === "private" && person) return onUser(c, update, m);
}

/** A command, as `{ name, arg }`; null when the text is none, or is meant for another bot in the same group. */
function parseCommand(text, botName) {
  const m = /^\/([a-z_]+)(?:@(\w+))?(?:\s+([\s\S]*))?$/i.exec(text);
  if (!m || (m[2] && botName && m[2].toLowerCase() !== botName.toLowerCase())) return null;
  return { name: m[1].toLowerCase(), arg: (m[3] || "").trim() };
}

// ---------- Counting, limits, the stored conversation ----------

const bump = (env, key, n = 1) => env.DB.prepare("INSERT INTO tg_stats (day, key, n) VALUES (?1, ?2, ?3) ON CONFLICT (day, key) DO UPDATE SET n = n + ?3").bind(today(), key, n).run();

/** What the rate limits remember of a person: a keyed hash of the id. */
const idTag = (c, chat) => tag(c.env.TOKEN_SECRET, `telegram:${chat}`);

/** A notice of one kind at most once an hour to one person, however much they send. */
async function noticeOnce(c, t, kind, chat, text, markup) {
  const key = `tg:note:${kind}:${t}`;
  if (await c.kit.blocked(c.env, key, 1, 3600)) return;
  await c.kit.count(c.env, key, 3600);
  await c.tg.send(chat, text, markup);
}

/** Whether a person may go on, before anything of theirs is stored: so many messages an hour and a day, so many button presses an hour. */
async function pace(c, t, chat, button) {
  const rules = button
    ? [{ key: `tg:b:h:${t}`, max: 120, window: 3600 }]
    : [{ key: `tg:m:h:${t}`, max: 30, window: 3600 }, { key: `tg:m:d:${t}`, max: 120, window: DAY }];
  if (await allowed(c.env, c.kit, rules)) return true;
  if (!button) await noticeOnce(c, t, "pace", chat, view.SLOW_DOWN);
  return false;
}

/** Notes who writes; null when this update was just handled (a retry of Telegram's carries the same id). One statement, so a flood costs little. */
async function touch(env, chat, from, updateId) {
  const written = await env.DB.prepare(
    `INSERT INTO tg_users (chat_id, name, username, updated, last_update) VALUES (?1, ?2, ?3, ?4, ?5)
     ON CONFLICT (chat_id) DO UPDATE SET
       name = CASE WHEN blocked = 1 THEN '' ELSE ?2 END,
       username = CASE WHEN blocked = 1 THEN '' ELSE ?3 END,
       updated = ?4, last_update = ?5
     WHERE last_update != ?5`,
  ).bind(chat, oneLine(from.first_name, 40), String(from.username || "").slice(0, 40), now(), updateId).run();
  if (!written.meta.changes) return null;
  return env.DB.prepare("SELECT * FROM tg_users WHERE chat_id = ?1").bind(chat).first();
}

const remember = (env, chat, role, content) => env.DB.prepare("INSERT INTO tg_messages (chat_id, role, content, at) VALUES (?1, ?2, ?3, ?4)").bind(chat, role, String(content).slice(0, MAX_STORED), now()).run();

async function conversation(env, chat, limit = 24) {
  const { results } = await env.DB.prepare("SELECT id, role, content FROM tg_messages WHERE chat_id = ?1 ORDER BY id DESC LIMIT ?2").bind(chat, limit).all();
  return results.reverse();
}

const setMode = (env, chat, mode, device = "") => env.DB.prepare("UPDATE tg_users SET mode = ?2, device = ?3 WHERE chat_id = ?1").bind(chat, mode, device).run();

// A GIF is also a document, a venue also a location: the more specific comes first.
const KINDS = [["photo", "photo"], ["animation", "GIF"], ["document", "file"], ["voice", "voice message"], ["video", "video"], ["audio", "audio"], ["video_note", "video message"], ["sticker", "sticker"], ["venue", "place"], ["location", "location"], ["contact", "contact"], ["poll", "poll"]];
const kindOf = (m) => (KINDS.find(([k]) => m[k]) || [])[1] || "";
const words = (m) => (typeof m.text === "string" ? m.text : kindOf(m) ? `[${kindOf(m)}]${m.caption ? " " + m.caption : ""}` : "");

const who = (u) => `${u.name || "Someone"}${u.username ? ` (@${u.username})` : ""}`;

/** Mails the owner a conversation (all of it); true when it went. Never throws. */
async function mailChat(c, user, rows, { human = false, ticket = 0 } = {}) {
  const messages = rows.map((r) => ({ role: r.role, content: r.content }));
  try {
    return await mailOwner(c.env, { messages, contact: "", human, meta: { source: "telegram", telegram: { id: user.chat_id, name: user.name, username: user.username }, ...(ticket ? { ticket } : {}), time: stamp() } });
  } catch (e) {
    log("mail")(e && e.message ? e.message : e);
    return false;
  }
}

/** Whether another summary may be mailed for this person (a few a day) and in all (a hundred a day). */
async function mailAllowed(c, t) {
  return allowed(c.env, c.kit, [{ key: `tg:mail:d:${t}`, max: 8, window: DAY }, { key: `tg:mail:all:${today()}`, max: 100, window: DAY }]);
}

// ---------- A person writes ----------

async function onUser(c, update, m) {
  const { env, tg } = c;
  const chat = m.chat.id;
  const t = await idTag(c, chat);
  if (!(await pace(c, t, chat, false))) return;
  const user = await touch(env, chat, m.from, update.update_id);
  if (!user || user.blocked) return;
  const text = typeof m.text === "string" ? m.text.trim() : "";
  const command = parseCommand(text, ""); // in a private chat every command is for this bot
  if (command) return onCommand(c, user, command.name, command.arg);
  if (user.mode === "topup" && text) return onAccountName(c, user, text);
  if (user.mode === "ticket" && user.ticket_id > 0) return toTeam(c, user, m);
  if (!text) return tg.send(chat, view.CAN_ONLY_READ.text, view.CAN_ONLY_READ.markup);
  return ask(c, user, text);
}

async function onCommand(c, user, command, arg) {
  const { env, tg } = c;
  const chat = user.chat_id;
  const show = (s) => tg.send(chat, s.text, s.markup);
  // A command ends the wait for an account's username.
  if (user.mode === "topup") await setMode(env, chat, "");
  switch (command) {
    case "start": {
      if (user.ticket_id <= 0) await setMode(env, chat, "");
      const topic = arg.toLowerCase();
      if (topic === "human" || topic === "team") return openTicket(c, user);
      if (topic === "buy") return showStore(c, user);
      const shop = await shopOf(env);
      const screens = { connect: view.connectPicker(), install: view.installPicker(), plans: view.plans(shop), account: view.ACCOUNT_INFO, downloads: view.DOWNLOADS };
      return show(Object.hasOwn(screens, topic) ? screens[topic] : view.menu(user.name, !!shop));
    }
    case "menu":
      return show(view.menu(user.name, !!(await shopOf(env))));
    case "buy":
      return showStore(c, user);
    case "terms":
      return show(view.terms((await sales.config(env)).days));
    case "paysupport":
      return show(view.PAYSUPPORT);
    case "password":
      return forgotPassword(c, user);
    case "help":
      return show(view.help());
    case "human":
      return openTicket(c, user);
    case "close":
    case "end":
      return user.ticket_id > 0 ? closeTicket(c, user.ticket_id, "user") : endChat(c, user, true);
    default:
      return tg.send(chat, `I do not know that command.\n\n${view.help().text}`, view.help().markup);
  }
}

/** The assistant answers what no screen covers. */
async function ask(c, user, text) {
  const { env, tg } = c;
  const chat = user.chat_id;
  // All the answers of Telegram in a day have their own budget, apart from the websites'.
  if (!(await allowed(env, c.kit, [{ key: `tg:chat:all:${today()}`, max: 600, window: DAY }]))) {
    return noticeOnce(c, await idTag(c, chat), "busy", chat, view.esc(CANNED.busy), view.TRY_AGAIN);
  }
  if (!(await env.DB.prepare("SELECT 1 AS found FROM tg_messages WHERE chat_id = ?1 LIMIT 1").bind(chat).first())) await bump(env, "chats");
  await remember(env, chat, "user", text);
  tg.typing(chat);
  const rows = (await conversation(env, chat)).filter((r) => r.role === "user" || r.role === "assistant");
  const messages = cleanMessages(rows);
  const context = [
    "The visitor writes to you in Telegram. Keep answers under 900 characters, in plain text.",
    'When the visitor needs a person, tell them to tap "Talk to our team" under your answer (or send /human), and end with the marker.',
    ...((await shopOf(env)) ? ["Data can be bought in this bot with Telegram Stars: the menu's \"Buy data\" button (or /buy) shows the packages, their prices and the plan each earns, and the account and password are sent in the chat right after paying. You do not know the prices: send the person there. The plan comes with the data: Bronze under 10 GB, Silver from 10 GB, Gold from 100 GB; VIP (no data limit) is given by the team."] : []),
    ...(user.mode === "problem" && user.device ? [`They are on ${user.device} and the standard checklist did not help: if they have not said, ask for the exact message they see, the network (Wi-Fi or mobile data) and the app version.`] : []),
  ].join("\n");
  const answer = messages && messages.length && messages[messages.length - 1].role === "user" ? await reply(env, messages, context, AI_TIMEOUT) : null;
  if (!answer || !answer.text) {
    await tg.send(chat, view.esc(CANNED.failed), view.TRY_AGAIN);
    return;
  }
  await remember(env, chat, "assistant", answer.text);
  if (user.mode === "problem") await setMode(env, chat, "");
  await bump(env, "answers");
  await tg.send(chat, fit(answer.text), view.feedback(answer.handoff));
}

// ---------- Tickets ----------

const ticketOf = (env, id) => env.DB.prepare("SELECT * FROM tg_tickets WHERE id = ?1").bind(id).first();

/** The team's chat is told of a ticket with who it is, what was said, and what it can do. */
function card(ticket, user, rows, fresh) {
  const last = rows.slice(-6).map((r) => `<b>${r.role === "user" ? "Visitor" : r.role === "team" ? "Team" : "Assistant"}:</b> ${fit(oneLine(r.content, 300), 400)}`).join("\n");
  return `🎫 <b>Ticket #${ticket}</b>${fresh ? " - new" : ""}\n👤 ${view.esc(who(user))} - id <code>${user.chat_id}</code>\n\n${last || "<i>No message yet. They were asked to describe the problem.</i>"}\n\n<i>Reply to this message to answer.</i>`;
}

const teamKeys = (ticket, chat) => ({ inline_keyboard: [[{ text: "✅ Close", callback_data: `o:c:${ticket}` }, { text: "🚫 Block", callback_data: `o:b:${chat}` }, { text: "📜 Full chat", callback_data: `o:f:${ticket}` }]] });

const remind = (c, id, message) => c.env.DB.prepare("INSERT OR REPLACE INTO tg_relay (team_chat, message_id, ticket_id, at) VALUES (?1, ?2, ?3, ?4)").bind(c.team, message, id, now()).run();

/** Follows a group that became a supergroup: the team's chat has a new id, and so do the cards in it. */
async function moveTeam(c, to) {
  const from = c.team;
  c.team = to;
  await save(c.env, "telegram_owner_chat", String(to));
  await c.env.DB.prepare("UPDATE tg_relay SET team_chat = ?2 WHERE team_chat = ?1").bind(from, to).run();
  await c.tg.call("setMyCommands", { commands: TEAM_COMMANDS, scope: { type: "chat", chat_id: to } });
}

/** Tells the team's chat something about a ticket and remembers the message, so that replying to it answers the person. Answers Telegram's answer, null when no chat is paired. */
async function tellTeam(c, ticket, text, markup) {
  if (!c.team) return null;
  let res = await c.tg.send(c.team, text, markup);
  const moved = !res.ok && res.parameters && res.parameters.migrate_to_chat_id;
  if (moved) {
    await moveTeam(c, moved);
    res = await c.tg.send(c.team, text, markup);
  }
  if (res.ok) await remind(c, ticket, res.result.message_id);
  return res;
}

/** The person asks for a person. */
async function openTicket(c, user) {
  const { env, tg } = c;
  const chat = user.chat_id;
  const have = user.ticket_id > 0 ? await ticketOf(env, user.ticket_id) : null;
  if (have && have.status === "open") return tg.send(chat, view.ticketOpen(have.id).text, view.ticketOpen(have.id).markup);
  const rows = await conversation(env, chat);
  const t = await idTag(c, chat);
  // Nobody reads tickets in Telegram until a chat is paired: the owner gets the chat by mail, and the person is told how things stand.
  if (!c.team) {
    tg.typing(chat);
    const mailed = rows.some((r) => r.role === "user") && (await mailAllowed(c, t)) ? await mailChat(c, user, rows, { human: true }) : false;
    return tg.send(chat, view.noTeam(mailed).text, view.noTeam(mailed).markup);
  }
  if (await c.kit.blocked(env, `tg:ticket:${t}`, 5, DAY)) return tg.send(chat, view.TICKET_LIMIT);
  if (await c.kit.blocked(env, `tg:tickets:all:${today()}`, 60, DAY)) return tg.send(chat, view.TEAM_BUSY);
  // The place is taken first, so two presses at once open one ticket. A claim that was never finished (the marker is minus the time it was made) is let go of after a while.
  const began = now();
  const claimed = await env.DB.prepare(
    `UPDATE tg_users SET ticket_id = ?2 WHERE chat_id = ?1 AND (
       ticket_id = 0 OR (ticket_id < 0 AND ticket_id > ?3)
       OR (ticket_id > 0 AND NOT EXISTS (SELECT 1 FROM tg_tickets WHERE id = tg_users.ticket_id AND status = 'open')))`,
  ).bind(chat, -began, -(began - CLAIM)).run();
  if (!claimed.meta.changes) return;
  let id;
  try {
    const made = await env.DB.prepare("INSERT INTO tg_tickets (id, chat_id, opened, updated) VALUES ((SELECT COALESCE(MAX(id), ?3) + 1 FROM tg_tickets), ?1, ?2, ?2)").bind(chat, began, FIRST_TICKET).run();
    id = made.meta.last_row_id;
    await env.DB.prepare("UPDATE tg_users SET mode = 'ticket', ticket_id = ?2, device = '' WHERE chat_id = ?1").bind(chat, id).run();
  } catch (e) {
    await env.DB.prepare("UPDATE tg_users SET ticket_id = 0 WHERE chat_id = ?1 AND ticket_id < 0").bind(chat).run();
    throw e;
  }
  await c.kit.count(env, `tg:ticket:${t}`, DAY);
  await c.kit.count(env, `tg:tickets:all:${today()}`, DAY);
  const opened = view.ticketOpened(id, rows.length > 0);
  await tg.send(chat, opened.text, opened.markup);
  await bump(env, "tickets");
  const told = await tellTeam(c, id, card(id, user, rows, true), teamKeys(id, chat));
  const mailed = await mailChat(c, user, rows, { human: true, ticket: id });
  // Neither the team's chat nor the mail took it: the person must not think someone knows.
  if (!(told && told.ok) && !mailed) await tg.send(chat, view.TEAM_UNREACHABLE);
}

/** A person writes while their ticket is open: the team gets it, with who it is from. */
async function toTeam(c, user, m) {
  const { env, tg } = c;
  const chat = user.chat_id;
  const ticket = user.ticket_id;
  const open = await env.DB.prepare("UPDATE tg_tickets SET updated = ?2 WHERE id = ?1 AND status = 'open'").bind(ticket, now()).run();
  if (!open.meta.changes) {
    // The ticket was closed behind this chat's back: it goes on as an ordinary chat.
    await env.DB.prepare("UPDATE tg_users SET mode = '', ticket_id = 0 WHERE chat_id = ?1 AND ticket_id = ?2").bind(chat, ticket).run();
    const text = typeof m.text === "string" ? m.text.trim() : "";
    return text ? ask(c, { ...user, mode: "", ticket_id: 0 }, text) : tg.send(chat, view.CAN_ONLY_READ.text, view.CAN_ONLY_READ.markup);
  }
  await remember(env, chat, "user", words(m));
  if (!c.team) return;
  const head = `🎫 <b>#${ticket}</b> - ${view.esc(who(user))}`;
  let sent;
  if (typeof m.text === "string") {
    sent = await tellTeam(c, ticket, `${head}\n${fit(m.text, 3600)}`);
  } else {
    // A picture, a file or a voice note is copied as it is, with the ticket in its caption where Telegram allows one.
    const caption = ["photo", "document", "video", "audio", "voice", "animation"].some((k) => m[k]);
    if (caption) sent = await c.tg.copy(c.team, m.chat.id, m.message_id, { caption: `🎫 #${ticket} - ${who(user)}${m.caption ? `\n${m.caption}` : ""}`.slice(0, 1000), parse_mode: undefined });
    else {
      await tellTeam(c, ticket, head);
      sent = await c.tg.copy(c.team, m.chat.id, m.message_id);
    }
    if (sent && sent.ok && sent.result) await remind(c, ticket, sent.result.message_id);
  }
  if (sent && sent.ok) {
    // The person is told it arrived without another message in their chat.
    await tg.react(chat, m.message_id, "👀");
    return;
  }
  // The team's chat did not take it (the bot was removed, the chat is gone): the owner gets the words by mail, and the person is told, once an hour.
  const t = await idTag(c, chat);
  if (await mailAllowed(c, t)) await mailChat(c, user, [{ role: "user", content: words(m) }], { human: true, ticket });
  await noticeOnce(c, t, "team", chat, view.TEAM_UNREACHABLE);
}

/** Closes a ticket: the person's chat is forgotten and the ticket keeps nothing of them. `tell: false` closes it without a word to the person (a blocked one). */
async function closeTicket(c, id, by, { tell = true } = {}) {
  const { env, tg } = c;
  const t = await ticketOf(env, id);
  if (!t || t.status !== "open") return false;
  const person = t.chat_id;
  const user = person ? await env.DB.prepare("SELECT name, username FROM tg_users WHERE chat_id = ?1").bind(person).first() : null;
  // Only the one who closes it first goes on: a second press does nothing.
  const closed = await env.DB.prepare("UPDATE tg_tickets SET status = 'closed', closed_by = ?2, updated = ?3, chat_id = 0 WHERE id = ?1 AND status = 'open'").bind(id, by, now()).run();
  if (!closed.meta.changes) return false;
  await env.DB.batch([
    // The person's state is reset only if it is still this ticket's: a newer ticket is not touched.
    env.DB.prepare("UPDATE tg_users SET mode = '', ticket_id = 0, device = '' WHERE chat_id = ?1 AND ticket_id = ?2").bind(person, id),
    env.DB.prepare("DELETE FROM tg_messages WHERE chat_id = ?1").bind(person),
  ]);
  await bump(env, "closed");
  if (tell && person) {
    const said = by === "team" ? view.ticketClosedByTeam(id) : by === "idle" ? view.ticketClosedIdle(id) : view.ticketClosedByUser(id);
    await tg.send(person, said.text, said.markup);
  }
  if (by !== "team") await tellTeam(c, id, `✅ <b>Ticket #${id}</b> was closed ${by === "user" ? `by ${view.esc(who(user || {}))}` : "after three quiet days"}.`);
  return true;
}

// ---------- A chat ends ----------

/** Ends a chat: its summary goes to the owner and its messages go. `told`: the person asked for it and is answered. */
async function endChat(c, user, told = false) {
  const { env, tg } = c;
  const chat = user.chat_id;
  const rows = await conversation(env, chat, MAX_ROWS);
  if (told && !rows.length) return tg.send(chat, view.NOTHING_TO_END.text, view.NOTHING_TO_END.markup);
  const said = rows.some((r) => r.role === "user" && r.content.length >= 3);
  let mailed = false;
  if (said && (await mailAllowed(c, await idTag(c, chat)))) mailed = await mailChat(c, user, rows);
  const last = rows.length ? rows[rows.length - 1].id : 0;
  await env.DB.batch([
    env.DB.prepare("DELETE FROM tg_messages WHERE chat_id = ?1 AND id <= ?2").bind(chat, last),
    // The person stays known only while they might write again: a chat that wrote in the meantime stays.
    env.DB.prepare("DELETE FROM tg_users WHERE chat_id = ?1 AND updated = ?2 AND mode != 'ticket' AND blocked = 0").bind(chat, user.updated),
  ]);
  if (told) await tg.send(chat, view.ended(mailed).text, view.ended(mailed).markup);
}

/** Every minute: a few chats that went quiet are summarized and forgotten, tickets nobody writes to are closed, old rows go. */
export async function sweep(env, kit) {
  const began = Date.now();
  const { token, name, team, url } = await config(env);
  if (!token) return;
  const c = { env, tg: bot(env, token), team, name, kit };
  const t = now();
  const over = () => Date.now() - began > SWEEP_BUDGET;
  await refreshHook(env, c.tg, url).catch(log("hook"));
  await refreshTexts(env, c.tg, team).catch(log("texts"));
  // Stars Telegram took that were not delivered within minutes are given back.
  const stuck = await env.DB.prepare("SELECT id FROM stars_payments WHERE status = 'paid' AND updated < ?1 LIMIT 5").bind(t - 180).all();
  for (const row of stuck.results) {
    if (over()) break;
    await giveBack(c, row.id, view.DELIVERY_FAILED).catch(log("sweep"));
  }
  await sales.purge(env, t).catch(log("sweep"));
  const quiet = await env.DB.prepare("SELECT * FROM tg_users WHERE mode != 'ticket' AND blocked = 0 AND updated < ?1 ORDER BY updated LIMIT ?2").bind(t - IDLE, SWEEP_MAX).all();
  for (const user of quiet.results) {
    if (over()) break;
    await endChat(c, user).catch(log("sweep"));
  }
  const stale = await env.DB.prepare("SELECT id FROM tg_tickets WHERE status = 'open' AND updated < ?1 LIMIT 5").bind(t - TICKET_IDLE).all();
  for (const row of stale.results) {
    if (over()) break;
    await closeTicket(c, row.id, "idle").catch(log("sweep"));
  }
  // A ticket's message ids are kept a while after it is closed, so that an answer to an old card says it is closed.
  await env.DB.batch([
    env.DB.prepare("DELETE FROM tg_relay WHERE at < ?1").bind(t - 14 * DAY),
    env.DB.prepare("DELETE FROM tg_stats WHERE day < ?1").bind(new Date((t - 90 * DAY) * 1000).toISOString().slice(0, 10)),
  ]);
}

// ---------- Buttons ----------

async function onCallback(c, update) {
  const { env, tg } = c;
  const q = update.callback_query;
  const data = String(q.data || "");
  const msg = q.message;
  if (!msg || !msg.chat) return tg.answer(q.id);
  if (data.startsWith("o:")) return onTeamButton(c, q, data, msg);
  if (msg.chat.type !== "private" || !q.from || q.from.is_bot) return tg.answer(q.id);
  const chat = msg.chat.id;
  const t = await idTag(c, chat);
  if (!(await pace(c, t, chat, true))) return tg.answer(q.id, "Slow down a little.");
  const user = await touch(env, chat, q.from, update.update_id);
  if (!user || user.blocked) return tg.answer(q.id);
  const [kind, arg, extra] = data.split(":");
  const extraOf = (d) => d.slice(d.indexOf(":") + 1);
  // Leaving the guide for a connection that fails ends its "describe the problem" step, and leaving the store ends the wait for an account's username.
  if (((user.mode === "problem" && !["no", "fb", "h", "tc", "r"].includes(kind)) || user.mode === "topup") && !["sr", "fb"].includes(kind)) {
    await setMode(env, chat, "");
    user.mode = "";
  }
  // A screen of the menu changes in place; an answer or a closing note stays and a new message follows it.
  const edit = async (s) => {
    const res = await tg.edit(chat, msg.message_id, s.text, s.markup);
    if (!res.ok && !/not modified/i.test(res.description || "")) await tg.send(chat, s.text, s.markup);
  };
  const show = (s) => tg.send(chat, s.text, s.markup);
  switch (kind) {
    case "menu":
      await tg.answer(q.id);
      return edit(view.menu(user.name, !!(await shopOf(env))));
    case "mn":
      await tg.answer(q.id);
      return show(view.menu(user.name, !!(await shopOf(env))));
    case "c":
      await tg.answer(q.id);
      return edit(arg ? view.connectGuide(arg) : view.connectPicker());
    case "cn":
      await tg.answer(q.id);
      return show(view.connectPicker());
    case "ok":
      await tg.answer(q.id, "Great!");
      await bump(env, "fixed");
      return edit(view.FIXED);
    case "no":
      await tg.answer(q.id);
      if (Object.hasOwn(view.DEVICES, arg) && user.ticket_id <= 0) await setMode(env, chat, "problem", view.DEVICES[arg].name);
      return edit(view.stillBroken());
    case "i":
      await tg.answer(q.id);
      return edit(arg ? view.installGuide(arg) : view.installPicker());
    case "p":
      await tg.answer(q.id);
      return edit(view.plans(await shopOf(env)));
    case "s":
    case "sn":
    case "sr":
    case "sp":
    case "st":
      await tg.answer(q.id);
      return onStoreButton(c, user, kind, arg, edit, show);
    case "pw":
    case "py":
      await tg.answer(q.id);
      return onPasswordButton(c, user, kind, extraOf(data), edit, show);
    case "a":
      await tg.answer(q.id);
      return edit(view.ACCOUNT_INFO);
    case "d":
      await tg.answer(q.id);
      return edit(view.DOWNLOADS);
    case "h":
      await tg.answer(q.id);
      return openTicket(c, user);
    case "tc": {
      await tg.answer(q.id);
      const id = Number(arg) || (user.ticket_id > 0 ? user.ticket_id : 0);
      const ticket = id ? await ticketOf(env, id) : null;
      // Only a person's own open ticket: an old button never closes a newer one, nor somebody else's.
      if (ticket && ticket.status === "open" && ticket.chat_id === chat) return closeTicket(c, id, "user");
      return show(view.TICKET_GONE);
    }
    case "r": {
      const n = Number(arg);
      await tg.answer(q.id, "Thank you!");
      if (!Number.isInteger(n) || n < 1 || n > 5) return;
      await bump(env, `rate${n}`);
      return edit(view.rated(n));
    }
    case "fb": {
      const up = arg === "u";
      await bump(env, up ? "up" : "down");
      await tg.answer(q.id, up ? "Thank you!" : "Sorry about that.");
      return tg.keys(chat, msg.message_id, up ? view.thanks(extra === "1") : view.SORRY);
    }
    default:
      return tg.answer(q.id);
  }
}

// ---------- The team ----------

/** The team's chat is paired with a code that only the administration panel shows. */
async function onPair(c, m, arg) {
  const { env, tg } = c;
  const chat = m.chat.id;
  if (await c.kit.blocked(env, `tg:pair:${await idTag(c, chat)}`, 5, 3600)) return;
  await c.kit.count(env, `tg:pair:${await idTag(c, chat)}`, 3600);
  const code = await setting(env, "telegram_pair_code");
  if (!code || !(await safeEqual(normalCode(arg), code))) return tg.send(chat, "That code is not right. Open the administration panel's Settings for the current one.");
  await save(env, "telegram_owner_chat", String(chat));
  await drop(env, "telegram_pair_code");
  c.team = chat;
  await tg.call("setMyCommands", { commands: TEAM_COMMANDS, scope: { type: "chat", chat_id: chat } });
  return tg.send(chat, view.PAIRED);
}

/** Commands and replies in the team's chat. True when it was for the team; false lets a private chat go on as a person's (so the owner can try the bot). */
async function onTeam(c, m) {
  const { env, tg } = c;
  const chat = m.chat.id;
  const text = typeof m.text === "string" ? m.text.trim() : "";
  const command = parseCommand(text, c.name);
  const replied = m.reply_to_message ? await env.DB.prepare("SELECT ticket_id FROM tg_relay WHERE team_chat = ?1 AND message_id = ?2").bind(chat, m.reply_to_message.message_id).first() : null;
  const name = command ? command.name : "";
  const arg = command ? command.arg : "";
  const done = async (work) => {
    await work;
    return true;
  };
  if (name === "help") return done(tg.send(chat, view.OWNER_HELP));
  if (name === "tickets") return done(listTickets(c));
  if (name === "stats") return done(stats(c));
  if (name === "sales") return done(salesReport(c));
  if (name === "refund") return done(refundByTeam(c, arg));
  if (name === "teststars") return done(testStars(c, m.from.id));
  if (name === "block" || name === "unblock") {
    // Replying to a ticket's message says whom, too.
    const target = arg || (replied ? ((await ticketOf(env, replied.ticket_id)) || {}).chat_id || "" : "");
    return done(blockUser(c, name === "block", target));
  }
  if (name === "close" && (arg || replied)) {
    const id = Number(arg) || (replied && replied.ticket_id);
    return done(closeTicket(c, id, "team").then((closed) => tg.send(chat, closed ? `✅ Ticket #${id} closed.` : `Ticket #${view.esc(arg || id)} is not open.`)));
  }
  if (replied) return done(answerPerson(c, m, replied.ticket_id));
  return m.chat.type !== "private"; // a group's other talk is not for the bot; a private chat can try it
}

async function answerPerson(c, m, id) {
  const { env, tg } = c;
  const t = await ticketOf(env, id);
  if (!t || t.status !== "open") {
    await tg.send(m.chat.id, `Ticket #${id} is closed. The person can open a new one with /human.`);
    return;
  }
  const res = typeof m.text === "string" ? await tg.send(t.chat_id, view.teamSays(fit(m.text, 3800)), view.ticketKeys(id)) : await tg.copy(t.chat_id, m.chat.id, m.message_id, { reply_markup: view.ticketKeys(id) });
  if (!res.ok) {
    await tg.send(m.chat.id, `⚠️ Could not deliver: ${view.esc(oneLine(res.description, 120))}`);
    return;
  }
  await remember(env, t.chat_id, "team", words(m));
  await env.DB.prepare("UPDATE tg_tickets SET updated = ?2 WHERE id = ?1").bind(id, now()).run();
  await tg.react(m.chat.id, m.message_id, "👍");
}

async function listTickets(c) {
  const { results } = await c.env.DB.prepare(
    `SELECT t.id, t.updated, u.name, u.username,
            (SELECT content FROM tg_messages WHERE chat_id = t.chat_id AND role = 'user' ORDER BY id DESC LIMIT 1) AS last
     FROM tg_tickets t LEFT JOIN tg_users u ON u.chat_id = t.chat_id
     WHERE t.status = 'open' ORDER BY t.updated DESC LIMIT 15`,
  ).all();
  if (!results.length) return c.tg.send(c.team, "No open tickets. 🎉");
  const ago = (s) => (now() - s < 3600 ? `${Math.max(1, Math.round((now() - s) / 60))} min` : `${Math.round((now() - s) / 3600)} h`);
  const lines = results.map((r) => `<b>#${r.id}</b> - ${view.esc(who(r))} - ${ago(r.updated)} ago${r.last ? `\n<i>${fit(oneLine(r.last, 90), 200)}</i>` : ""}`);
  return c.tg.send(c.team, `🎫 <b>Open tickets (${results.length})</b>\n\n${lines.join("\n\n")}\n\n<i>Reply to a ticket's message to answer. /close 1042 closes one.</i>`);
}

async function stats(c) {
  const { results } = await c.env.DB.prepare("SELECT key, n FROM tg_stats WHERE day = ?1").bind(today()).all();
  const n = Object.fromEntries(results.map((r) => [r.key, r.n]));
  const open = await c.env.DB.prepare("SELECT COUNT(*) AS n FROM tg_tickets WHERE status = 'open'").first();
  const votes = [1, 2, 3, 4, 5].reduce((sum, k) => sum + (n[`rate${k}`] || 0), 0);
  const average = votes ? ([1, 2, 3, 4, 5].reduce((sum, k) => sum + k * (n[`rate${k}`] || 0), 0) / votes).toFixed(1) : "";
  return c.tg.send(
    c.team,
    `📊 <b>Today (UTC)</b>\nChats: ${n.chats || 0} - answers: ${n.answers || 0}\nHelpful: 👍 ${n.up || 0} - 👎 ${n.down || 0} - guides that fixed it: ${n.fixed || 0}\nTickets: ${n.tickets || 0} opened - ${n.closed || 0} closed - ${open.n} open now${votes ? `\nRating of the help: ${average} out of 5 (${votes})` : ""}${c.name ? `\n\n@${view.esc(c.name)}` : ""}`,
  );
}

async function blockUser(c, on, arg) {
  const { env } = c;
  const id = Number(arg);
  if (!Number.isSafeInteger(id) || !id) return c.tg.send(c.team, `Say whom: /${on ? "block" : "unblock"} 123456789 (the id is on the ticket), or reply with it to a ticket's message.`);
  await env.DB.prepare("INSERT INTO tg_users (chat_id, updated, blocked) VALUES (?1, ?2, ?3) ON CONFLICT (chat_id) DO UPDATE SET blocked = ?3").bind(id, now(), on ? 1 : 0).run();
  if (on) {
    // A blocked person keeps only their id, which is what the block is: no name, no messages, no open ticket.
    const open = await env.DB.prepare("SELECT id FROM tg_tickets WHERE chat_id = ?1 AND status = 'open'").bind(id).first();
    if (open) await closeTicket(c, open.id, "team", { tell: false });
    await env.DB.batch([
      env.DB.prepare("DELETE FROM tg_messages WHERE chat_id = ?1").bind(id),
      env.DB.prepare("UPDATE tg_users SET name = '', username = '', mode = '', ticket_id = 0, device = '' WHERE chat_id = ?1").bind(id),
    ]);
  }
  return c.tg.send(c.team, on ? `🚫 ${id} is blocked. /unblock ${id} lets them back.` : `✅ ${id} is unblocked.`);
}

/** The buttons on a ticket in the team's chat. */
async function onTeamButton(c, q, data, msg) {
  const { env, tg } = c;
  if (!c.team || msg.chat.id !== c.team) return tg.answer(q.id, "Not for you.", true);
  const [, kind, arg] = data.split(":");
  const id = Number(arg);
  if (kind === "c") {
    const done = await closeTicket(c, id, "team");
    return tg.answer(q.id, done ? `Ticket #${id} closed.` : "It is not open.");
  }
  if (kind === "b") {
    await tg.answer(q.id, "Blocked.");
    return blockUser(c, true, arg);
  }
  if (kind === "f") {
    await tg.answer(q.id);
    const t = await ticketOf(env, id);
    // A closed ticket's chat is gone, by design.
    if (!t || t.status !== "open") return tg.send(c.team, `Ticket #${id} is closed, and its chat was deleted with it.`);
    const rows = await conversation(env, t.chat_id, 60);
    const lines = rows.map((r) => `<b>${r.role === "user" ? "Visitor" : r.role === "team" ? "Team" : "Assistant"}:</b> ${fit(r.content, 600)}`);
    let out = "";
    for (const l of lines.reverse()) if ((out + l).length < 3600) out = `${l}\n\n${out}`;
    return tg.send(c.team, `📜 <b>Ticket #${id}</b>\n\n${out || "<i>Nothing kept.</i>"}`);
  }
  return tg.answer(q.id);
}

// ---------- Selling data with Telegram Stars ----------
//
// A person picks a package (a new account, or data for one they have), reads
// and agrees to the terms, and gets an invoice in Stars. Telegram asks, and
// waits ten seconds, whether the order can be fulfilled (onCheckout); then
// says it was paid (onPaid), and the account is made or recharged at once and
// its username and password are sent in the same chat. A payment that cannot
// be delivered is given back.

/** The packages on sale now, with the settings; null while selling is off. */
async function shopOf(env) {
  const cfg = await sales.config(env);
  return cfg.enabled ? { cfg, catalog: sales.catalog(cfg), days: cfg.days } : null;
}

async function showStore(c, user) {
  const shop = await shopOf(c.env);
  const s = shop ? view.store(shop.catalog, shop.days) : view.STORE_OFF;
  return c.tg.send(user.chat_id, s.text, s.markup);
}

/** Orders a person may make: ten an hour, three hundred a day in all. */
const mayOrder = (c, t) => allowed(c.env, c.kit, [{ key: `tg:order:${t}`, max: 10, window: 3600 }, { key: `tg:orders:all:${today()}`, max: 300, window: DAY }]);

async function makeOrder(c, user, shop, p, kind, target = "") {
  const order = { chat_id: user.chat_id, kind, gb: p.gb, stars: p.stars, days: shop.days, target };
  return { ...order, id: await sales.newOrder(c.env, { chat: order.chat_id, kind, gb: p.gb, stars: p.stars, days: shop.days, target }) };
}

/** The store's buttons: the packages, a package's choice, a new account, one to recharge, the order's payment, the terms. */
async function onStoreButton(c, user, kind, arg, edit, show) {
  const { env, tg } = c;
  const chat = user.chat_id;
  const shop = await shopOf(env);
  if (!shop) return edit(view.STORE_OFF);
  const p = arg && kind !== "sp" ? shop.catalog.find((x) => x.gb === Number(arg)) : null;
  switch (kind) {
    case "s":
      return edit(p ? view.storeChoice(p, shop.days) : view.store(shop.catalog, shop.days));
    case "st":
      return show(view.terms(shop.days));
    case "sn": {
      if (!p) return edit(view.store(shop.catalog, shop.days));
      if (!(await mayOrder(c, await idTag(c, chat)))) return tg.send(chat, view.SLOW_DOWN);
      return edit(view.checkout(await makeOrder(c, user, shop, p, "new"), p, shop.days));
    }
    case "sr":
      if (!p) return edit(view.store(shop.catalog, shop.days));
      await setMode(env, chat, "topup", String(p.gb));
      return edit(view.askAccount(p));
    default: // "sp": the person agreed, and the invoice is sent
      return sendInvoice(c, user, Number(arg));
  }
}

/** A person writes the username of the account to recharge. */
async function onAccountName(c, user, text) {
  const { env, tg } = c;
  const chat = user.chat_id;
  const send = (s) => tg.send(chat, s.text, s.markup);
  const shop = await shopOf(env);
  const p = shop && shop.catalog.find((x) => x.gb === Number(user.device));
  if (!p) {
    await setMode(env, chat, "");
    return send(shop ? view.store(shop.catalog, shop.days) : view.STORE_OFF);
  }
  const name = text.trim().toLowerCase();
  if (!checkUsername(name)) return send(view.NOT_A_USERNAME);
  const t = await idTag(c, chat);
  // Whether a username exists is told to whoever asks, a few times an hour: nobody can list the accounts.
  if (!(await allowed(env, c.kit, [{ key: `tg:lookup:${t}`, max: 10, window: 3600 }, { key: `tg:lookups:all:${today()}`, max: 500, window: DAY }]))) return send(view.TOO_MANY_LOOKUPS);
  const account = await sales.accountFor(env, name);
  if (!account) return send(view.NO_SUCH_ACCOUNT);
  if (!account.fits) return send(view.CANNOT_RECHARGE);
  if (!(await mayOrder(c, t))) return tg.send(chat, view.SLOW_DOWN);
  await setMode(env, chat, "");
  return send(view.checkout(await makeOrder(c, user, shop, p, "topup", account.username), p, shop.days));
}

/** The order's invoice, in Stars (no provider and no key: Telegram's own currency, XTR). */
async function sendInvoice(c, user, orderId) {
  const { env, tg } = c;
  const chat = user.chat_id;
  const order = await sales.orderOf(env, orderId);
  if (!order || order.chat_id !== chat || order.status !== "new") return tg.send(chat, view.ORDER_GONE.text, view.ORDER_GONE.markup);
  if (order.kind !== "test" && !(await shopOf(env))) return tg.send(chat, view.STORE_OFF.text, view.STORE_OFF.markup);
  const test = order.kind === "test";
  const valid = order.days > 0 ? `valid ${order.days} days` : "no end date";
  const res = await tg.call("sendInvoice", {
    chat_id: chat,
    title: test ? "Test payment" : `${order.gb} GB of data`,
    description: (test ? "A test payment of 1 Star, to see that buying works. It can be given back." : `Albatross VPN data: ${order.gb} GB, ${valid}. ${order.kind === "topup" ? `Added to the account ${order.target}.` : "A new account."}`).slice(0, 255),
    payload: `o${order.id}`,
    provider_token: "",
    currency: "XTR",
    prices: [{ label: test ? "Test" : `${order.gb} GB`, amount: order.stars }],
  });
  if (!res.ok) await tg.send(chat, `I could not send the invoice: ${view.esc(oneLine(res.description, 120))}. Please try again, or tell our team.`);
}

/** Telegram asks, once the person pressed Pay, whether the order can be fulfilled: answered within its ten seconds. */
async function onCheckout(c, update) {
  const { env, tg } = c;
  const q = update.pre_checkout_query;
  const answer = (ok, why = "") => tg.call("answerPreCheckoutQuery", { pre_checkout_query_id: q.id, ok, ...(ok ? {} : { error_message: why }) });
  const found = /^o(\d+)$/.exec(String(q.invoice_payload || ""));
  const order = found ? await sales.orderOf(env, Number(found[1])) : null;
  if (!order || !q.from || order.chat_id !== q.from.id || q.currency !== "XTR" || q.total_amount !== order.stars) return answer(false, "This invoice is not valid. Pick a package again with /buy.");
  const blockedRow = await env.DB.prepare("SELECT blocked FROM tg_users WHERE chat_id = ?1").bind(q.from.id).first();
  if (blockedRow && blockedRow.blocked) return answer(false, "You cannot buy here.");
  if (order.kind !== "test" && !(await shopOf(env))) return answer(false, "Selling is paused. Please try again later.");
  if (now() - order.created > 3600) return answer(false, "This invoice has expired. Pick a package again with /buy.");
  if (order.kind === "topup") {
    const account = await sales.accountFor(env, order.target);
    if (!account || !account.fits) return answer(false, "That account cannot be recharged any more. Ask our team.");
  }
  // One payment per order: a second invoice of the same order, or a second press, is turned away.
  if (!(await sales.claimOrder(env, order.id, now()))) return answer(false, "This invoice was already paid, or is being paid. Pick a package again with /buy.");
  return answer(true);
}

/** Telegram says a payment was made: the data is delivered, once, and the person gets the account and its password. */
async function onPaid(c, m) {
  const { env, tg } = c;
  const paid = m.successful_payment;
  const payer = m.from.id;
  const charge = String(paid.telegram_payment_charge_id || "");
  if (!charge) return;
  const found = /^o(\d+)$/.exec(String(paid.invoice_payload || ""));
  const order = found ? await sales.orderOf(env, Number(found[1])) : null;
  if (!order || order.chat_id !== payer || paid.currency !== "XTR" || paid.total_amount !== order.stars) {
    // Paid for what no order matches: the Stars go back, and the team is told.
    const res = await tg.call("refundStarPayment", { user_id: payer, telegram_payment_charge_id: charge });
    await tellTeam(c, 0, `⚠️ A payment of ⭐${Number(paid.total_amount) || 0} matched no order, ${res.ok ? "and was refunded" : `and could NOT be refunded (${view.esc(oneLine(res.description, 100))})`}. Charge <code>${view.esc(charge)}</code>.`, undefined, false);
    return;
  }
  const rec = await sales.recordPayment(env, { chargeId: charge, chat: payer, order, stars: paid.total_amount });
  if (!rec.fresh) return; // Telegram sent it twice
  let out;
  try {
    out = await sales.deliver(env, order, rec.id);
  } catch (e) {
    log("delivery")(e && e.message ? e.message : e);
    return giveBack(c, rec.id, view.DELIVERY_FAILED);
  }
  await bump(env, "sales");
  await bump(env, "stars", order.stars);
  if (out.kind === "test") {
    await tg.send(payer, view.receiptTest(rec.id));
    return;
  }
  const plan = TIERS[tierOf(out.tier)];
  const receipt = out.kind === "new"
    ? view.receiptNew({ id: rec.id, gb: order.gb, stars: order.stars, username: out.username, password: out.password, plan: plan.name, devices: plan.devices, expires: out.expires_at })
    : view.receiptTopup({ id: rec.id, gb: order.gb, stars: order.stars, username: out.username, quota_bytes: out.quota_bytes, plan: plan.name, devices: plan.devices, expires: out.expires_at });
  let sent = await tg.send(payer, receipt.text, receipt.markup);
  if (!sent.ok) sent = await tg.send(payer, receipt.text, receipt.markup);
  const who = `<code>${view.esc(out.username)}</code> (${plan.name})`;
  await tellTeam(c, 0, `💰 <b>Sale P${rec.id}</b> - ${order.gb} GB for ⭐${order.stars} - ${out.kind === "new" ? "new account" : "recharge of"} ${who}${sent.ok ? "" : `\n⚠️ The receipt could NOT be sent (${view.esc(oneLine(sent.description, 100))}): reset the account's password in the panel and tell the buyer (id <code>${payer}</code>).`}`, undefined, false);
}

/** Gives a payment back (Telegram's refund), switches off what it gave, and tells the buyer and the team. False when it could not. */
async function giveBack(c, id, why) {
  const { env, tg } = c;
  const pay = await sales.paymentOf(env, id);
  if (!pay || pay.status === "refunded") return false;
  const res = await tg.call("refundStarPayment", { user_id: pay.chat_id, telegram_payment_charge_id: pay.charge_id });
  if (!res.ok) {
    await env.DB.prepare("UPDATE stars_payments SET status = 'failed', updated = ?2 WHERE id = ?1").bind(id, now()).run();
    await tellTeam(c, 0, `⚠️ <b>Payment P${id}</b> (⭐${pay.stars}) could not be delivered and could NOT be refunded: ${view.esc(oneLine(res.description, 120))}. Refund it by hand with /refund P${id}.`, undefined, false);
    return false;
  }
  await env.DB.prepare("UPDATE stars_payments SET status = 'refunded', updated = ?2 WHERE id = ?1").bind(id, now()).run();
  await sales.reverse(env, pay);
  await bump(env, "refunds");
  const said = view.GAVE_BACK(id, pay.stars, why);
  await tg.send(pay.chat_id, said.text, said.markup);
  if (why !== view.BY_TEAM) await tellTeam(c, 0, `↩ <b>Payment P${id}</b> (⭐${pay.stars}) was refunded: ${view.esc(why)}`, undefined, false);
  return true;
}

/** Telegram says a payment was given back (not by this service): what it gave is taken back too. */
async function onRefunded(c, m) {
  const { env } = c;
  const charge = String((m.refunded_payment || {}).telegram_payment_charge_id || "");
  const pay = charge ? await env.DB.prepare("SELECT * FROM stars_payments WHERE charge_id = ?1").bind(charge).first() : null;
  if (!pay || pay.status === "refunded") return;
  await env.DB.prepare("UPDATE stars_payments SET status = 'refunded', updated = ?2 WHERE id = ?1").bind(pay.id, now()).run();
  await sales.reverse(env, pay);
  await tellTeam(c, 0, `↩ Telegram says <b>payment P${pay.id}</b> (⭐${pay.stars}) was refunded. ${pay.username ? `The account <code>${view.esc(pay.username)}</code> was ${pay.kind === "new" ? "switched off" : "reduced by the data it got"}.` : ""}`, undefined, false);
}

/** /refund P12 in the team's chat. */
async function refundByTeam(c, arg) {
  const { env, tg } = c;
  const id = Number(String(arg).trim().replace(/^p/i, ""));
  if (!Number.isSafeInteger(id) || id <= 0) return tg.send(c.team, "Say which payment: /refund P12 (the number is in /sales).");
  const pay = await sales.paymentOf(env, id);
  if (!pay) return tg.send(c.team, `There is no payment P${id}.`);
  if (pay.status === "refunded") return tg.send(c.team, `P${id} was already refunded.`);
  const done = await giveBack(c, id, view.BY_TEAM);
  if (done) await tg.send(c.team, `↩ P${id} refunded (⭐${pay.stars}).${pay.username ? ` ${pay.kind === "new" ? "The account" : "The data on"} <code>${view.esc(pay.username)}</code> ${pay.kind === "new" ? "is switched off" : "was taken off again"}.` : ""}`);
}

/** /sales: today's numbers and the last payments. */
async function salesReport(c) {
  const { env } = c;
  const { results } = await env.DB.prepare("SELECT key, n FROM tg_stats WHERE day = ?1 AND key IN ('sales', 'stars', 'refunds')").bind(today()).all();
  const n = Object.fromEntries(results.map((r) => [r.key, r.n]));
  const last = await env.DB.prepare("SELECT id, kind, gb, stars, username, status FROM stars_payments ORDER BY id DESC LIMIT 8").all();
  const lines = last.results.map((r) => `<b>P${r.id}</b> - ${r.kind === "test" ? "test" : `${r.gb} GB`} ⭐${r.stars}${r.username ? ` - <code>${view.esc(r.username)}</code>` : ""} - ${r.status}`);
  const shop = await shopOf(env);
  return c.tg.send(c.team, `💰 <b>Sales</b> (${shop ? "on sale" : "selling is off"})\nToday (UTC): ${n.sales || 0} sold for ⭐${n.stars || 0}, ${n.refunds || 0} refunded\n\n${lines.join("\n") || "<i>No payments yet.</i>"}\n\n<i>/refund P12 gives one back.</i>`);
}

/** /teststars: an invoice of 1 Star to the person who asked, to see that selling works end to end (it can be refunded). */
async function testStars(c, person) {
  const id = await sales.newOrder(c.env, { chat: person, kind: "test", gb: 0, stars: 1, days: 0 });
  return sendInvoice(c, { chat_id: person }, id);
}

// ---------- A new password for an account bought here ----------
//
// Only the Telegram account that bought a new account (the number its
// purchase remembered, sales.js) can ask for its password anew: the person
// has no other proof, and none is asked. All the account's devices are signed
// out and the new password is shown once, in this chat.

async function forgotPassword(c, user) {
  const names = await sales.ownedAccounts(c.env, user.chat_id);
  const s = !names.length ? view.NO_OWN_ACCOUNT : names.length === 1 ? view.confirmReset(names[0]) : view.pickAccount(names);
  return c.tg.send(user.chat_id, s.text, s.markup);
}

async function onPasswordButton(c, user, kind, name, edit, show) {
  const { env, tg } = c;
  const chat = user.chat_id;
  const names = await sales.ownedAccounts(env, chat);
  if (kind === "pw") {
    if (!name || name === "pw") return edit(!names.length ? view.NO_OWN_ACCOUNT : names.length === 1 ? view.confirmReset(names[0]) : view.pickAccount(names));
    return edit(names.includes(name) ? view.confirmReset(name) : view.NO_OWN_ACCOUNT);
  }
  // "py": done, if the account is still this person's and not too often.
  if (!names.includes(name)) return edit(view.NO_OWN_ACCOUNT);
  if (!(await allowed(env, c.kit, [{ key: `tg:reset:${await idTag(c, chat)}`, max: 3, window: DAY }]))) return show(view.TOO_MANY_RESETS);
  const password = await sales.resetPassword(env, chat, name);
  if (!password) return edit(view.NO_OWN_ACCOUNT);
  await bump(env, "passwords");
  return edit(view.newPassword(name, password));
}
