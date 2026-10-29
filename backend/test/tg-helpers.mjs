// What the tests of the Telegram bot share: a service with a connected bot, a
// Telegram that remembers what it was told, a model, a mailbox and a database
// (d1.mjs, the real SQL), and the updates people and the team send.
import assert from "node:assert/strict";
import { d1 } from "./d1.mjs";
import { connect, webhook } from "../src/telegram.js";

export const HOOK_URL = "https://accounts.albatrossvpn.online/api/v1/telegram";

export const TOKEN = "123456789:" + "A".repeat(35);
export const SECRET = "s".repeat(40);
export const TEAM = 777;
export const SARA = 4242;
export const ADMIN_REQUEST = () => new Request("https://accounts.albatrossvpn.online/api/v1/admin/settings");
export const real = globalThis.fetch;

const b64u = (bytes) => Buffer.from(bytes).toString("base64url");
export async function secretOf(tokenSecret) {
  const key = await crypto.subtle.importKey("raw", new TextEncoder().encode(tokenSecret), { name: "HMAC", hash: "SHA-256" }, false, ["sign"]);
  return b64u(new Uint8Array(await crypto.subtle.sign("HMAC", key, new TextEncoder().encode("albatross telegram webhook"))));
}

/** The Bot API: every call is kept (with the token it was made with) and answered the way Telegram does, unless a test says otherwise. */
export function telegram(overrides = {}) {
  const calls = [];
  let next = 500;
  // What the bot's menu and texts are, as a person would see them.
  const shown = { menus: new Map(), description: "", about: "" };
  const scoped = (p) => JSON.stringify(p.scope || "all");
  const remembered = {
    getMyCommands: (p) => ({ ok: true, result: shown.menus.get(scoped(p)) || [] }),
    setMyCommands: (p) => (shown.menus.set(scoped(p), p.commands), { ok: true, result: true }),
    getMyDescription: () => ({ ok: true, result: { description: shown.description } }),
    setMyDescription: (p) => ((shown.description = p.description), { ok: true, result: true }),
    getMyShortDescription: () => ({ ok: true, result: { short_description: shown.about } }),
    setMyShortDescription: (p) => ((shown.about = p.short_description), { ok: true, result: true }),
  };
  globalThis.fetch = async (url, init) => {
    const [, token, method] = /\/bot([^/]+)\/(\w+)$/.exec(String(url));
    const payload = JSON.parse(init.body);
    calls.push({ method, token, ...payload });
    const o = overrides[method];
    const given = typeof o === "function" ? o(payload) : o;
    const body = given ?? (remembered[method] ? remembered[method](payload) : method === "getMe" ? { ok: true, result: { username: "AlbatrossSupportBot" } } : method === "sendMessage" || method === "copyMessage" ? { ok: true, result: { message_id: next++ } } : { ok: true, result: true });
    return new Response(JSON.stringify(body), { status: body.ok ? 200 : body.error_code || 400 });
  };
  return { calls, shown, of: (method) => calls.filter((c) => c.method === method) };
}

export const labels = (markup) => (markup ? markup.inline_keyboard.flat().map((b) => b.text) : []);
export const datas = (markup) => (markup ? markup.inline_keyboard.flat().map((b) => b.callback_data || b.url) : []);

let uid = 1000;
let mid = 10;
/** The next update id and message id: they only go up, as Telegram's do. */
export const nextUpdate = () => ++uid;
export const nextMessage = () => ++mid;
export const text = (chat, words, extra = {}) => ({ update_id: ++uid, message: { message_id: ++mid, from: { id: chat, first_name: "Sara", username: "sara_tg" }, chat: { id: chat, type: "private" }, text: words, ...extra } });
export const media = (chat, extra) => ({ update_id: ++uid, message: { message_id: ++mid, from: { id: chat, first_name: "Sara", username: "sara_tg" }, chat: { id: chat, type: "private" }, ...extra } });
export const press = (chat, data, message_id = 55) => ({ update_id: ++uid, callback_query: { id: `cb${uid}`, from: { id: chat, first_name: "Sara", username: "sara_tg" }, data, message: { message_id, chat: { id: chat, type: "private" } } } });

/** A service with a connected bot, a paired team chat (unless `team` is false), a model that answers `answer`, and a mailbox (unless `mail` is false). */
export async function world({ answer = "Update the app first.", team = true, mail = true } = {}) {
  const api = telegram();
  const env = { DB: d1(), TOKEN_SECRET: SECRET, CONTACT_EMAIL: "support@albatrossvpn.online", sent: [], asked: [], answer, aiDown: false };
  env.AI = { run: async (model, input) => { env.asked.push(input); if (env.aiDown) throw new Error("the model is away"); return { response: env.answer }; } };
  env.EMAIL = { send: async (m) => void env.sent.push(m) };
  if (mail) await env.DB.prepare("INSERT INTO settings (key, value) VALUES ('support_notify_to', 'owner@example.com')").run();
  const linked = await connect(env, ADMIN_REQUEST(), TOKEN);
  assert.deepEqual(linked, { username: "AlbatrossSupportBot" });
  const kit = {
    blocked: async (_e, key, max, window) => {
      const row = await env.DB.prepare("SELECT count, since FROM attempts WHERE key = ?1").bind(key).first();
      return !!row && Math.floor(Date.now() / 1000) - row.since <= window && row.count >= max;
    },
    count: (_e, key, window) =>
      env.DB.prepare(
        `INSERT INTO attempts (key, count, since) VALUES (?1, 1, ?2)
         ON CONFLICT (key) DO UPDATE SET count = CASE WHEN ?2 - since > ?3 THEN 1 ELSE count + 1 END, since = CASE WHEN ?2 - since > ?3 THEN ?2 ELSE since END`,
      ).bind(key, Math.floor(Date.now() / 1000), window).run(),
  };
  const w = {
    env, api, kit,
    async send(update, headers = {}) {
      const res = await webhook(new Request("https://accounts.albatrossvpn.online/api/v1/telegram", { method: "POST", headers: { "X-Telegram-Bot-Api-Secret-Token": await secretOf(SECRET), ...headers }, body: JSON.stringify(update) }), env, kit, undefined);
      return res;
    },
    row: (sql, ...args) => env.DB.prepare(sql).bind(...args).first(),
    rows: async (sql, ...args) => (await env.DB.prepare(sql).bind(...args).all()).results,
    lastText: (chat) => api.of("sendMessage").filter((c) => c.chat_id === chat).pop(),
    setting: async (key) => ((await env.DB.prepare("SELECT value FROM settings WHERE key = ?1").bind(key).first()) || {}).value ?? null,
  };
  if (team) {
    const code = (await w.row("SELECT value FROM settings WHERE key = 'telegram_pair_code'")).value;
    await w.send(text(TEAM, `/pair ${code}`));
    assert.equal(await w.setting("telegram_owner_chat"), String(TEAM));
  }
  w.boot = api.calls.slice();
  api.calls.length = 0;
  return w;
}

