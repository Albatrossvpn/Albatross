// The Telegram support bot: the same assistant as the websites' chat
// (support.js), with a menu, guides for a connection that fails, and a team
// behind it. A person who needs more opens a ticket; the team (the owner's
// chat, paired with /pair) reads it in Telegram and answers by replying to its
// message. The bot's token is pasted in the administration panel, which sets
// the webhook; Telegram calls POST /api/v1/telegram with a secret the panel's
// service derives from its own, so nothing else needs to be set up.
//
// What is kept: a chat's messages until half an hour after the last one, when
// a summary is mailed to the owner and they are deleted; a ticket's messages
// until it is closed.
import { DAY, b64u, safeEqual } from "./lib.js";
import { CANNED, allowed, cleanMessages, mailOwner, oneLine, reply, stamp, today } from "./support.js";
import * as view from "./telegram-content.js";

export const SWEEP_CRON = "*/10 * * * *";

const IDLE = 30 * 60; // a chat with no new message for this long is over
const TICKET_IDLE = 3 * DAY; // a ticket nobody writes to is closed
const SWEEP_MAX = 8; // chats ended per run
const MAX_STORED = 3000; // one message
const AI_TIMEOUT = 18_000; // the platform leaves 30 seconds after the response for all of it
const TOKEN = /^\d{5,12}:[A-Za-z0-9_-]{30,64}$/;
const FIRST_TICKET = 1000;

const enc = new TextEncoder();
const now = () => Math.floor(Date.now() / 1000);

const COMMANDS = [
  { command: "start", description: "Start, and the main menu" },
  { command: "menu", description: "Show the main menu" },
  { command: "human", description: "Talk to our team (opens a ticket)" },
  { command: "close", description: "Close your ticket, or end this chat" },
  { command: "help", description: "How this bot works" },
];
const TEAM_COMMANDS = [
  { command: "tickets", description: "The open tickets" },
  { command: "stats", description: "Today's numbers" },
  { command: "close", description: "Close a ticket" },
  { command: "block", description: "Block a user" },
  { command: "unblock", description: "Let a user back" },
  { command: "help", description: "The team's commands" },
];
const ABOUT = "Albatross VPN support. Get help installing, connecting and managing your account. An AI assistant answers at once, and our team follows up on tickets.";
const SHORT = "Albatross VPN support: instant answers, and a team behind them.";

// ---------- Settings and the Bot API ----------

const KEYS = ["telegram_bot_token", "telegram_bot_username", "telegram_owner_chat"];

/** The bot's token, name and the team's chat, in one read. */
async function config(env) {
  const { results } = await env.DB.prepare(`SELECT key, value FROM settings WHERE key IN (${KEYS.map((_, i) => `?${i + 1}`).join(", ")})`).bind(...KEYS).all();
  const c = Object.fromEntries(results.map((r) => [r.key, r.value]));
  return { token: c.telegram_bot_token || "", name: c.telegram_bot_username || "", team: Number(c.telegram_owner_chat) || 0 };
}

const setting = async (env, key) => ((await env.DB.prepare("SELECT value FROM settings WHERE key = ?1").bind(key).first()) || {}).value || "";
const save = (env, key, value) => env.DB.prepare("INSERT INTO settings (key, value) VALUES (?1, ?2) ON CONFLICT (key) DO UPDATE SET value = ?2").bind(key, value).run();
const drop = (env, ...keys) => env.DB.batch(keys.map((k) => env.DB.prepare("DELETE FROM settings WHERE key = ?1").bind(k)));

/** Text that fits in a message once made safe for Telegram's HTML. */
function fit(text, max = 3900) {
  let t = String(text);
  while (view.esc(t).length > max) t = t.slice(0, Math.floor(t.length * 0.8));
  return view.esc(t);
}

/** The Bot API for one token. Every call answers `{ ok, result, description }` and never throws; the token is never in what is logged. */
function bot(env, token) {
  const base = (env.TELEGRAM_API || "https://api.telegram.org").replace(/\/$/, "");
  async function call(method, payload = {}) {
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

/** The secret Telegram sends with every call, derived from the service's own so nothing else is stored. */
async function hookSecret(env) {
  const key = await crypto.subtle.importKey("raw", enc.encode(env.TOKEN_SECRET), { name: "HMAC", hash: "SHA-256" }, false, ["sign"]);
  return b64u.enc(new Uint8Array(await crypto.subtle.sign("HMAC", key, enc.encode("albatross telegram webhook"))));
}

// ---------- The administration panel connects and disconnects ----------

const ALPHABET = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789";
const newCode = () => Array.from(crypto.getRandomValues(new Uint8Array(8)), (b) => ALPHABET[b % ALPHABET.length]).join("");
const normalCode = (s) => String(s || "").toUpperCase().replace(/[^A-Z0-9]/g, "");

/** Connects a bot: checks the token, sets the webhook and the menu. Answers `{ username }` or `{ error }`. */
export async function connect(env, request, token) {
  token = String(token || "").trim();
  if (!TOKEN.test(token)) return { error: "That does not look like a bot token. Copy it from @BotFather; it looks like 123456789:AAH-..." };
  const tg = bot(env, token);
  const me = await tg.call("getMe");
  if (!me.ok) return { error: `Telegram did not accept this token: ${oneLine(me.description, 120)}` };
  const hook = await tg.call("setWebhook", {
    url: `${new URL(request.url).origin}/api/v1/telegram`,
    secret_token: await hookSecret(env),
    allowed_updates: ["message", "callback_query"],
    drop_pending_updates: true,
    max_connections: 20,
  });
  if (!hook.ok) return { error: `Telegram could not set the webhook: ${oneLine(hook.description, 120)}` };
  // The menu and the texts people see before they write; failing to set them does not stop the bot.
  await Promise.all([tg.call("setMyCommands", { commands: COMMANDS }), tg.call("setMyDescription", { description: ABOUT }), tg.call("setMyShortDescription", { short_description: SHORT })]);
  const username = String(me.result.username || "");
  await save(env, "telegram_bot_token", token);
  await save(env, "telegram_bot_username", username);
  if (!(await setting(env, "telegram_owner_chat")) && !(await setting(env, "telegram_pair_code"))) await save(env, "telegram_pair_code", newCode());
  return { username };
}

/** Disconnects the bot and forgets every chat. */
export async function disconnect(env) {
  const { token } = await config(env);
  if (token) await bot(env, token).call("deleteWebhook", { drop_pending_updates: true });
  await drop(env, "telegram_bot_token", "telegram_bot_username", "telegram_owner_chat", "telegram_pair_code");
  await env.DB.batch(["tg_users", "tg_messages", "tg_tickets", "tg_relay"].map((t) => env.DB.prepare(`DELETE FROM ${t}`)));
}

/** A new pairing code; the chat paired until now stays until another is paired. */
export async function repair(env) {
  const code = newCode();
  await save(env, "telegram_pair_code", code);
  return code;
}

// ---------- The webhook ----------

/** POST /telegram: an update from Telegram; answered at once, handled after. */
export async function webhook(request, env, kit, ctx) {
  const c = await config(env);
  if (!c.token) return new Response("Not connected.", { status: 404 });
  if (!(await safeEqual(request.headers.get("X-Telegram-Bot-Api-Secret-Token") || "", await hookSecret(env)))) return new Response("Not allowed.", { status: 401 });
  let update;
  try {
    const text = await request.text();
    update = text.length <= 64 * 1024 ? JSON.parse(text) : null;
  } catch {
    update = null;
  }
  if (!update || typeof update !== "object" || !Number.isSafeInteger(update.update_id)) return new Response("Bad request.", { status: 400 });
  const work = handle({ env, kit, tg: bot(env, c.token), team: c.team, name: c.name }, update).catch((e) => console.error("telegram:", e && e.stack ? e.stack : e));
  if (ctx && ctx.waitUntil) ctx.waitUntil(work);
  else await work;
  return new Response("ok");
}

/** Who gets what: the team's chat, the people in private chats, nobody else. */
async function handle(c, update) {
  if (update.callback_query) return onCallback(c, update);
  const m = update.message;
  if (!m || !m.chat || !m.from || m.from.is_bot) return;
  const text = typeof m.text === "string" ? m.text : "";
  const pair = /^\/pair(?:@\w+)?(?:\s+(.*))?$/is.exec(text);
  if (pair) return onPair(c, m, pair[1]);
  if (c.team && m.chat.id === c.team && (await onTeam(c, m))) return;
  if (m.chat.type === "private") return onUser(c, update, m);
}

// ---------- Counting, limits, the stored conversation ----------

const bump = (env, key) => env.DB.prepare("INSERT INTO tg_stats (day, key, n) VALUES (?1, ?2, 1) ON CONFLICT (day, key) DO UPDATE SET n = n + 1").bind(today(), key).run();

/** Whether this chat may go on; at most one notice an hour when it may not. */
async function permitted(c, chat, ai = false) {
  const t = String(chat);
  const rules = [
    { key: `tg:h:${t}`, max: 40, window: 3600 },
    { key: `tg:d:${t}`, max: 200, window: DAY },
    ...(ai ? [{ key: `support:chat:all:${today()}`, max: 900, window: DAY }] : []),
  ];
  if (await allowed(c.env, c.kit, rules)) return true;
  const note = `tg:note:${t}`;
  if (!(await c.kit.blocked(c.env, note, 1, 3600))) {
    await c.kit.count(c.env, note, 3600);
    await c.tg.send(chat, view.esc(ai ? CANNED.busy : view.SLOW_DOWN));
  }
  return false;
}

async function touch(env, chat, from, updateId) {
  await env.DB.prepare(
    `INSERT INTO tg_users (chat_id, name, username, updated) VALUES (?1, ?2, ?3, ?4)
     ON CONFLICT (chat_id) DO UPDATE SET name = ?2, username = ?3, updated = ?4`,
  ).bind(chat, oneLine(from.first_name, 40), String(from.username || "").slice(0, 40), now()).run();
  // Telegram may send an update twice: only the first one is handled.
  const first = await env.DB.prepare("UPDATE tg_users SET last_update = ?2 WHERE chat_id = ?1 AND last_update < ?2").bind(chat, updateId).run();
  if (!first.meta.changes) return null;
  return env.DB.prepare("SELECT * FROM tg_users WHERE chat_id = ?1").bind(chat).first();
}

const remember = (env, chat, role, content) => env.DB.prepare("INSERT INTO tg_messages (chat_id, role, content, at) VALUES (?1, ?2, ?3, ?4)").bind(chat, role, String(content).slice(0, MAX_STORED), now()).run();

async function conversation(env, chat, limit = 24) {
  const { results } = await env.DB.prepare("SELECT id, role, content FROM tg_messages WHERE chat_id = ?1 ORDER BY id DESC LIMIT ?2").bind(chat, limit).all();
  return results.reverse();
}

const setMode = (env, chat, mode, device = "") => env.DB.prepare("UPDATE tg_users SET mode = ?2, device = ?3 WHERE chat_id = ?1").bind(chat, mode, device).run();

const KINDS = [["photo", "photo"], ["document", "file"], ["voice", "voice message"], ["video", "video"], ["audio", "audio"], ["video_note", "video message"], ["animation", "GIF"], ["sticker", "sticker"], ["location", "location"], ["contact", "contact"], ["poll", "poll"]];
const kindOf = (m) => (KINDS.find(([k]) => m[k]) || [])[1] || "";
const words = (m) => (typeof m.text === "string" ? m.text : kindOf(m) ? `[${kindOf(m)}]${m.caption ? " " + m.caption : ""}` : "");

const who = (u) => `${u.name || "Someone"}${u.username ? ` (@${u.username})` : ""}`;

// ---------- A person writes ----------

async function onUser(c, update, m) {
  const { env, tg } = c;
  const chat = m.chat.id;
  const user = await touch(env, chat, m.from, update.update_id);
  if (!user || user.blocked) return;
  const text = typeof m.text === "string" ? m.text.trim() : "";
  const command = /^\/([a-z_]+)(?:@\w+)?(?:\s+(.*))?$/is.exec(text);
  if (command) {
    if (!(await permitted(c, chat))) return;
    return onCommand(c, user, command[1].toLowerCase(), (command[2] || "").trim());
  }
  if (user.mode === "ticket") return toTeam(c, user, m);
  if (!text) {
    if (!(await permitted(c, chat))) return;
    return tg.send(chat, view.CAN_ONLY_READ.text, view.CAN_ONLY_READ.markup);
  }
  return ask(c, user, text);
}

async function onCommand(c, user, command, arg) {
  const { env, tg } = c;
  const chat = user.chat_id;
  const show = (s) => tg.send(chat, s.text, s.markup);
  switch (command) {
    case "start": {
      if (!user.ticket_id) await setMode(env, chat, "");
      const topic = arg.toLowerCase();
      if (topic === "human" || topic === "team") return openTicket(c, user);
      const screens = { connect: view.connectPicker(), install: view.installPicker(), plans: view.PLANS, account: view.ACCOUNT_INFO, downloads: view.DOWNLOADS };
      return show(screens[topic] || view.menu(user.name));
    }
    case "menu":
      return show(view.menu(user.name));
    case "help":
      return show(view.help());
    case "human":
      return openTicket(c, user);
    case "close":
    case "end":
      return user.ticket_id ? closeTicket(c, user.ticket_id, "user") : endChat(c, user, true);
    default:
      return tg.send(chat, `I do not know that command.\n\n${view.help().text}`, view.help().markup);
  }
}

/** The assistant answers what no screen covers. */
async function ask(c, user, text) {
  const { env, tg } = c;
  const chat = user.chat_id;
  if (!(await permitted(c, chat, true))) return;
  if (!(await env.DB.prepare("SELECT 1 AS found FROM tg_messages WHERE chat_id = ?1 LIMIT 1").bind(chat).first())) await bump(env, "chats");
  await remember(env, chat, "user", text);
  tg.typing(chat);
  const rows = (await conversation(env, chat)).filter((r) => r.role === "user" || r.role === "assistant");
  const messages = cleanMessages(rows);
  const context = [
    "The visitor writes to you in Telegram. Keep answers under 900 characters, in plain text.",
    'When the visitor needs a person, tell them to tap "Talk to our team" under your answer (or send /human), and end with the marker.',
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

/** Tells the team's chat something about a ticket and remembers the message, so that replying to it answers the person. */
async function tellTeam(c, ticket, text, markup) {
  if (!c.team) return null;
  const res = await c.tg.send(c.team, text, markup);
  if (res.ok) await c.env.DB.prepare("INSERT OR REPLACE INTO tg_relay (team_chat, message_id, ticket_id, at) VALUES (?1, ?2, ?3, ?4)").bind(c.team, res.result.message_id, ticket, now()).run();
  return res;
}

async function openTicket(c, user) {
  const { env, tg } = c;
  const chat = user.chat_id;
  if (user.ticket_id) {
    const open = await ticketOf(env, user.ticket_id);
    if (open && open.status === "open") return tg.send(chat, view.ticketOpen(open.id).text, view.ticketOpen(open.id).markup);
  }
  if (await c.kit.blocked(env, `tg:ticket:${chat}`, 5, DAY)) return tg.send(chat, view.esc(`You have opened several tickets today. Please write in the one that is open, or email ${view.MAIL}.`));
  await c.kit.count(env, `tg:ticket:${chat}`, DAY);
  const t = now();
  const made = await env.DB.prepare("INSERT INTO tg_tickets (id, chat_id, opened, updated) VALUES ((SELECT COALESCE(MAX(id), ?3) + 1 FROM tg_tickets), ?1, ?2, ?2)").bind(chat, t, FIRST_TICKET).run();
  const id = made.meta.last_row_id;
  await env.DB.prepare("UPDATE tg_users SET mode = 'ticket', ticket_id = ?2, device = '' WHERE chat_id = ?1").bind(chat, id).run();
  const rows = await conversation(env, chat);
  const opened = view.ticketOpened(id, rows.length > 0);
  await tg.send(chat, opened.text, opened.markup);
  await bump(env, "tickets");
  await tellTeam(c, id, card(id, user, rows, true), teamKeys(id, chat));
  const messages = rows.map((r) => ({ role: r.role, content: r.content }));
  await mailOwner(env, { messages, contact: "", human: true, meta: { source: "telegram", telegram: { id: chat, name: user.name, username: user.username }, ticket: id, time: stamp() } }).catch((e) => console.error("telegram mail:", e && e.message ? e.message : e));
}

/** A person writes while their ticket is open: the team gets it, with who it is from. */
async function toTeam(c, user, m) {
  const { env, tg } = c;
  const chat = user.chat_id;
  if (!(await permitted(c, chat))) return;
  const ticket = user.ticket_id;
  await remember(env, chat, "user", words(m));
  await env.DB.prepare("UPDATE tg_tickets SET updated = ?2 WHERE id = ?1").bind(ticket, now()).run();
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
    if (sent && sent.ok && sent.result) await env.DB.prepare("INSERT OR REPLACE INTO tg_relay (team_chat, message_id, ticket_id, at) VALUES (?1, ?2, ?3, ?4)").bind(c.team, sent.result.message_id, ticket, now()).run();
  }
  // The person is told it arrived without another message in their chat.
  await tg.react(chat, m.message_id, "👀");
}

async function closeTicket(c, id, by) {
  const { env, tg } = c;
  const t = await ticketOf(env, id);
  if (!t || t.status !== "open") return false;
  const user = await env.DB.prepare("SELECT * FROM tg_users WHERE chat_id = ?1").bind(t.chat_id).first();
  await env.DB.batch([
    env.DB.prepare("UPDATE tg_tickets SET status = 'closed', closed_by = ?2, updated = ?3 WHERE id = ?1").bind(id, by, now()),
    env.DB.prepare("UPDATE tg_users SET mode = '', ticket_id = 0, device = '' WHERE chat_id = ?1").bind(t.chat_id),
    env.DB.prepare("DELETE FROM tg_messages WHERE chat_id = ?1").bind(t.chat_id),
    env.DB.prepare("DELETE FROM tg_relay WHERE ticket_id = ?1").bind(id),
  ]);
  await bump(env, "closed");
  const said = by === "team" ? view.ticketClosedByTeam(id) : by === "idle" ? view.ticketClosedIdle(id) : view.ticketClosedByUser(id);
  await tg.send(t.chat_id, said.text, said.markup);
  if (by !== "team") await tellTeam(c, id, `✅ <b>Ticket #${id}</b> was closed ${by === "user" ? `by ${view.esc(who(user || {}))}` : "after three quiet days"}.`);
  return true;
}

// ---------- A chat ends ----------

/** Ends a chat: its summary goes to the owner and its messages go. `told`: the person asked for it and is answered. */
async function endChat(c, user, told = false) {
  const { env, tg } = c;
  const chat = user.chat_id;
  const rows = await conversation(env, chat, 200);
  const said = rows.filter((r) => r.role === "user" && r.content.length >= 3);
  if (told && !rows.length) return tg.send(chat, view.NOTHING_TO_END.text, view.NOTHING_TO_END.markup);
  if (said.length && (await allowed(env, c.kit, [{ key: `support:mail:d:tg:${chat}`, max: 8, window: DAY }, { key: `support:mail:all:${today()}`, max: 150, window: DAY }]))) {
    const messages = cleanMessages(rows.filter((r) => r.role === "user" || r.role === "assistant")) || [];
    await mailOwner(env, { messages, contact: "", meta: { source: "telegram", telegram: { id: chat, name: user.name, username: user.username }, time: stamp() } }).catch((e) => console.error("telegram mail:", e && e.message ? e.message : e));
  }
  const last = rows.length ? rows[rows.length - 1].id : 0;
  await env.DB.batch([
    env.DB.prepare("DELETE FROM tg_messages WHERE chat_id = ?1 AND id <= ?2").bind(chat, last),
    // The person stays known only while they might write again: a chat that wrote in the meantime stays.
    env.DB.prepare("DELETE FROM tg_users WHERE chat_id = ?1 AND updated = ?2 AND mode != 'ticket' AND blocked = 0").bind(chat, user.updated),
  ]);
  if (told) await tg.send(chat, view.ENDED.text, view.ENDED.markup);
}

/** Every ten minutes: chats that went quiet are summarized and forgotten, tickets nobody writes to are closed, old rows go. */
export async function sweep(env, kit) {
  const { token, name, team } = await config(env);
  if (!token) return;
  const c = { env, tg: bot(env, token), team, name, kit };
  const t = now();
  const quiet = await env.DB.prepare("SELECT * FROM tg_users WHERE mode != 'ticket' AND blocked = 0 AND updated < ?1 ORDER BY updated LIMIT ?2").bind(t - IDLE, SWEEP_MAX).all();
  for (const user of quiet.results) await endChat(c, user).catch((e) => console.error("telegram sweep:", e && e.message ? e.message : e));
  const stale = await env.DB.prepare("SELECT id FROM tg_tickets WHERE status = 'open' AND updated < ?1 LIMIT 10").bind(t - TICKET_IDLE).all();
  for (const row of stale.results) await closeTicket(c, row.id, "idle").catch((e) => console.error("telegram sweep:", e && e.message ? e.message : e));
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
  if (msg.chat.type !== "private") return tg.answer(q.id);
  const chat = msg.chat.id;
  const user = await touch(env, chat, q.from, update.update_id);
  if (!user || user.blocked) return tg.answer(q.id);
  if (!(await c.kit.blocked(env, `tg:cb:${chat}`, 150, 3600))) await c.kit.count(env, `tg:cb:${chat}`, 3600);
  else return tg.answer(q.id, "Slow down a little.");
  const edit = async (s) => {
    const res = await tg.edit(chat, msg.message_id, s.text, s.markup);
    if (!res.ok && !/not modified/i.test(res.description || "")) await tg.send(chat, s.text, s.markup);
  };
  const [kind, arg] = data.split(":");
  switch (kind) {
    case "menu":
      await tg.answer(q.id);
      return edit(view.menu(user.name));
    case "c":
      await tg.answer(q.id);
      return edit(arg ? view.connectGuide(arg) : view.connectPicker());
    case "ok":
      await tg.answer(q.id, "Great!");
      await bump(env, "fixed");
      return edit(view.FIXED);
    case "no":
      await tg.answer(q.id);
      if (view.DEVICES[arg] && !user.ticket_id) await setMode(env, chat, "problem", view.DEVICES[arg].name);
      return edit(view.stillBroken());
    case "i":
      await tg.answer(q.id);
      return edit(arg ? view.installGuide(arg) : view.installPicker());
    case "p":
      await tg.answer(q.id);
      return edit(view.PLANS);
    case "a":
      await tg.answer(q.id);
      return edit(view.ACCOUNT_INFO);
    case "d":
      await tg.answer(q.id);
      return edit(view.DOWNLOADS);
    case "h":
      await tg.answer(q.id);
      return openTicket(c, user);
    case "tc":
      await tg.answer(q.id);
      return user.ticket_id ? closeTicket(c, user.ticket_id, "user") : undefined;
    case "fb":
      await bump(env, arg === "u" ? "up" : "down");
      await tg.answer(q.id, arg === "u" ? "Thank you!" : "Sorry about that.");
      return tg.keys(chat, msg.message_id, arg === "u" ? view.THANKS : view.SORRY);
    default:
      return tg.answer(q.id);
  }
}

// ---------- The team ----------

/** The team's chat is paired with a code that only the administration panel shows. */
async function onPair(c, m, arg) {
  const { env, tg } = c;
  const chat = m.chat.id;
  if (await c.kit.blocked(env, `tg:pair:${chat}`, 5, 3600)) return;
  await c.kit.count(env, `tg:pair:${chat}`, 3600);
  const code = await setting(env, "telegram_pair_code");
  if (!code || !(await safeEqual(normalCode(arg), code))) return tg.send(chat, "That code is not right. Open the administration panel's Settings for the current one.");
  await save(env, "telegram_owner_chat", String(chat));
  await drop(env, "telegram_pair_code");
  await tg.call("setMyCommands", { commands: TEAM_COMMANDS, scope: { type: "chat", chat_id: chat } });
  return tg.send(chat, view.PAIRED);
}

/** Commands and replies in the team's chat. True when it was for the team; false lets a private chat go on as a person's (so the owner can try the bot). */
async function onTeam(c, m) {
  const { env, tg } = c;
  const chat = m.chat.id;
  const text = typeof m.text === "string" ? m.text.trim() : "";
  const command = /^\/([a-z_]+)(?:@\w+)?(?:\s+(.*))?$/is.exec(text);
  const replied = m.reply_to_message ? await env.DB.prepare("SELECT ticket_id FROM tg_relay WHERE team_chat = ?1 AND message_id = ?2").bind(chat, m.reply_to_message.message_id).first() : null;
  const name = command ? command[1].toLowerCase() : "";
  const arg = command ? (command[2] || "").trim() : "";
  const done = async (work) => {
    await work;
    return true;
  };
  if (name === "help") return done(tg.send(chat, view.OWNER_HELP));
  if (name === "tickets") return done(listTickets(c));
  if (name === "stats") return done(stats(c));
  if (name === "block" || name === "unblock") return done(blockUser(c, name === "block", arg));
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
  const res = typeof m.text === "string" ? await tg.send(t.chat_id, view.teamSays(m.text), view.ticketKeys) : await tg.copy(t.chat_id, m.chat.id, m.message_id);
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
  return c.tg.send(
    c.team,
    `📊 <b>Today (UTC)</b>\nChats: ${n.chats || 0} - answers: ${n.answers || 0}\nHelpful: 👍 ${n.up || 0} - 👎 ${n.down || 0} - guides that fixed it: ${n.fixed || 0}\nTickets: ${n.tickets || 0} opened - ${n.closed || 0} closed - ${open.n} open now${c.name ? `\n\n@${view.esc(c.name)}` : ""}`,
  );
}

async function blockUser(c, on, arg) {
  const id = Number(arg);
  if (!Number.isSafeInteger(id) || !id) return c.tg.send(c.team, `Say whom: /${on ? "block" : "unblock"} 123456789 (the id is on the ticket).`);
  await c.env.DB.prepare("INSERT INTO tg_users (chat_id, updated, blocked) VALUES (?1, ?2, ?3) ON CONFLICT (chat_id) DO UPDATE SET blocked = ?3").bind(id, now(), on ? 1 : 0).run();
  if (on) {
    const open = await c.env.DB.prepare("SELECT id FROM tg_tickets WHERE chat_id = ?1 AND status = 'open'").bind(id).first();
    if (open) await closeTicketQuietly(c, open.id);
  }
  return c.tg.send(c.team, on ? `🚫 ${id} is blocked. /unblock ${id} lets them back.` : `✅ ${id} is unblocked.`);
}

/** Closes a ticket of someone who was blocked, without telling them. */
async function closeTicketQuietly(c, id) {
  const t = await ticketOf(c.env, id);
  if (!t) return;
  await c.env.DB.batch([
    c.env.DB.prepare("UPDATE tg_tickets SET status = 'closed', closed_by = 'team', updated = ?2 WHERE id = ?1").bind(id, now()),
    c.env.DB.prepare("UPDATE tg_users SET mode = '', ticket_id = 0 WHERE chat_id = ?1").bind(t.chat_id),
    c.env.DB.prepare("DELETE FROM tg_messages WHERE chat_id = ?1").bind(t.chat_id),
    c.env.DB.prepare("DELETE FROM tg_relay WHERE ticket_id = ?1").bind(id),
  ]);
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
    if (!t) return tg.send(c.team, `Ticket #${id} is gone.`);
    const rows = await conversation(env, t.chat_id, 60);
    const lines = rows.map((r) => `<b>${r.role === "user" ? "Visitor" : r.role === "team" ? "Team" : "Assistant"}:</b> ${fit(r.content, 600)}`);
    let out = "";
    for (const l of lines.reverse()) if ((out + l).length < 3600) out = `${l}\n\n${out}`;
    return tg.send(c.team, `📜 <b>Ticket #${id}</b>\n\n${out || "<i>Nothing kept.</i>"}`);
  }
  return tg.answer(q.id);
}
