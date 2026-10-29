// The Telegram support bot (src/telegram.js) over a real SQL database (d1.mjs)
// with Telegram, the model and the mail stood in for: what a person sees, what
// the team sees, what is kept and what is forgotten.
import assert from "node:assert/strict";
import test, { afterEach } from "node:test";
import { d1 } from "./d1.mjs";
import { check, connect, disconnect, heal, repair, sweep, webhook } from "../src/telegram.js";

const HOOK_URL = "https://accounts.albatrossvpn.online/api/v1/telegram";

const TOKEN = "123456789:" + "A".repeat(35);
const SECRET = "s".repeat(40);
const TEAM = 777;
const SARA = 4242;
const ADMIN_REQUEST = () => new Request("https://accounts.albatrossvpn.online/api/v1/admin/settings");
const real = globalThis.fetch;
afterEach(() => (globalThis.fetch = real));

const b64u = (bytes) => Buffer.from(bytes).toString("base64url");
async function secretOf(tokenSecret) {
  const key = await crypto.subtle.importKey("raw", new TextEncoder().encode(tokenSecret), { name: "HMAC", hash: "SHA-256" }, false, ["sign"]);
  return b64u(new Uint8Array(await crypto.subtle.sign("HMAC", key, new TextEncoder().encode("albatross telegram webhook"))));
}

/** The Bot API: every call is kept (with the token it was made with) and answered the way Telegram does, unless a test says otherwise. */
function telegram(overrides = {}) {
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

const labels = (markup) => (markup ? markup.inline_keyboard.flat().map((b) => b.text) : []);
const datas = (markup) => (markup ? markup.inline_keyboard.flat().map((b) => b.callback_data || b.url) : []);

let uid = 1000;
let mid = 10;
const text = (chat, words, extra = {}) => ({ update_id: ++uid, message: { message_id: ++mid, from: { id: chat, first_name: "Sara", username: "sara_tg" }, chat: { id: chat, type: "private" }, text: words, ...extra } });
const media = (chat, extra) => ({ update_id: ++uid, message: { message_id: ++mid, from: { id: chat, first_name: "Sara", username: "sara_tg" }, chat: { id: chat, type: "private" }, ...extra } });
const press = (chat, data, message_id = 55) => ({ update_id: ++uid, callback_query: { id: `cb${uid}`, from: { id: chat, first_name: "Sara", username: "sara_tg" }, data, message: { message_id, chat: { id: chat, type: "private" } } } });

/** A service with a connected bot, a paired team chat (unless `team` is false), a model that answers `answer`, and a mailbox (unless `mail` is false). */
async function world({ answer = "Update the app first.", team = true, mail = true } = {}) {
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

// ---------- Connecting ----------

test("a token that is not a token, or that Telegram refuses, connects nothing and says why", async () => {
  const env = { DB: d1(), TOKEN_SECRET: SECRET };
  const req = ADMIN_REQUEST();
  assert.match((await connect(env, req, "hello")).error, /does not look like a bot token/);
  const api = telegram({ getMe: { ok: false, error_code: 401, description: "Unauthorized" } });
  assert.match((await connect(env, req, TOKEN)).error, /did not accept this token: Unauthorized/);
  assert.equal(api.of("setWebhook").length, 0);
  telegram({ setWebhook: { ok: false, error_code: 400, description: "Bad webhook: HTTPS URL must be provided" } });
  assert.match((await connect(env, req, TOKEN)).error, /could not set the webhook: Bad webhook/);
  assert.equal(await env.DB.prepare("SELECT value FROM settings WHERE key = 'telegram_bot_token'").first(), null, "a bot that was not connected is not stored");
});

test("connecting sets the webhook with the derived secret, one connection, the menu and the texts, and keeps a pairing code until a chat is paired", async () => {
  const w = await world({ team: false });
  const env = w.env;
  const of = (method) => w.boot.filter((c) => c.method === method);
  const hook = of("setWebhook")[0];
  assert.equal(hook.url, "https://accounts.albatrossvpn.online/api/v1/telegram");
  assert.equal(hook.secret_token, await secretOf(SECRET));
  assert.match(hook.secret_token, /^[A-Za-z0-9_-]{1,256}$/);
  assert.deepEqual(hook.allowed_updates, ["message", "callback_query"]);
  assert.equal(hook.max_connections, 1, "updates arrive one after the other");
  assert.deepEqual(of("setMyCommands")[0].commands.map((c) => c.command), ["start", "menu", "human", "close", "help"]);
  assert.ok(of("setMyDescription")[0].description.includes("Albatross"));
  assert.equal(await w.setting("telegram_bot_username"), "AlbatrossSupportBot");
  assert.equal(await w.setting("telegram_webhook_url"), "https://accounts.albatrossvpn.online/api/v1/telegram");
  const code = await w.setting("telegram_pair_code");
  assert.match(code, /^[A-HJ-NP-Z2-9]{8}$/);
  assert.notEqual(await repair(env), code, "a new code on request");
});

test("the webhook answers only Telegram: the secret first (nothing is read without it), then a connected bot, then a real update", async () => {
  const w = await world();
  assert.equal((await w.send(text(SARA, "hi"), { "X-Telegram-Bot-Api-Secret-Token": "wrong" })).status, 401);
  assert.equal((await w.send(text(SARA, "hi"), { "X-Telegram-Bot-Api-Secret-Token": "" })).status, 401);
  assert.equal(w.api.calls.length, 0);
  let reads = 0;
  const spy = { ...w.env, DB: { prepare: (sql) => (reads++, w.env.DB.prepare(sql)), batch: w.env.DB.batch } };
  assert.equal((await webhook(new Request("https://x/api/v1/telegram", { method: "POST", body: "{}" }), spy, w.kit, undefined)).status, 401);
  assert.equal(reads, 0, "a stranger costs the database nothing");
  assert.equal((await w.send({ nothing: true })).status, 400);
  assert.equal((await w.send(text(SARA, "/start"))).status, 200);
  const bare = { DB: d1(), TOKEN_SECRET: SECRET };
  const res = await webhook(new Request("https://x/api/v1/telegram", { method: "POST", headers: { "X-Telegram-Bot-Api-Secret-Token": await secretOf(SECRET) }, body: "{}" }), bare, w.kit, undefined);
  assert.equal(res.status, 404, "no bot, nothing to answer");
});

test("a token of another bot replaces the old one and forgets what belonged to it; a new token of the same bot keeps everything", async () => {
  const w = await world();
  await w.send(text(SARA, "/human"));
  // The same bot with a token made anew in BotFather: nothing is lost, and the old token is not used.
  const same = telegram();
  assert.deepEqual(await connect(w.env, ADMIN_REQUEST(), "123456789:" + "B".repeat(35)), { username: "AlbatrossSupportBot" });
  assert.equal(same.of("deleteWebhook").length, 0);
  assert.equal(await w.setting("telegram_owner_chat"), String(TEAM));
  assert.equal((await w.row("SELECT COUNT(*) AS n FROM tg_tickets")).n, 1);
  // Another bot: its webhook is taken from the old token, its chats and its team are gone, and a new code waits.
  const other = telegram({ getMe: { ok: true, result: { username: "OtherBot" } } });
  assert.deepEqual(await connect(w.env, ADMIN_REQUEST(), "987654321:" + "C".repeat(35)), { username: "OtherBot" });
  assert.equal(other.of("deleteWebhook").length, 1);
  assert.equal(other.of("deleteWebhook")[0].token, "123456789:" + "B".repeat(35));
  for (const t of ["tg_users", "tg_messages", "tg_tickets", "tg_relay"]) assert.equal((await w.row(`SELECT COUNT(*) AS n FROM ${t}`)).n, 0, t);
  assert.equal(await w.setting("telegram_owner_chat"), null);
  assert.match(await w.setting("telegram_pair_code"), /^[A-HJ-NP-Z2-9]{8}$/);
});

test("every hour a webhook that is gone is set again and one that is there is left alone; the panel can ask how the bot is", async () => {
  const w = await world();
  const url = "https://accounts.albatrossvpn.online/api/v1/telegram";
  const gone = telegram({ getWebhookInfo: { ok: true, result: { url: "" } } });
  await heal(w.env);
  assert.equal(gone.of("setWebhook").length, 1);
  assert.equal(gone.of("setWebhook")[0].url, url);
  assert.equal(gone.of("setWebhook")[0].drop_pending_updates, undefined, "waiting updates are kept");
  const fine = telegram({ getWebhookInfo: { ok: true, result: { url, pending_update_count: 3 } } });
  await heal(w.env);
  assert.equal(fine.of("setWebhook").length, 0);
  assert.equal(await check(w.env), "@AlbatrossSupportBot is reachable; its webhook is set and 3 update(s) are waiting. No errors. Its menu and texts are set.");
  telegram({ getWebhookInfo: { ok: true, result: { url, last_error_date: 1760000000, last_error_message: "Wrong response from the webhook: 500 Internal Server Error" } } });
  assert.match(await check(w.env), /Last error: Wrong response from the webhook: 500/);
  telegram({ getWebhookInfo: { ok: true, result: { url } }, setMyDescription: { ok: false, error_code: 400, description: "Bad Request: nope" } });
  assert.match(await check(w.env), /Telegram refused its description: Bad Request: nope\.$/);
  telegram({ getWebhookInfo: { ok: true, result: { url: "" } } });
  assert.match(await check(w.env), /webhook is not set/);
  await disconnect(w.env);
  assert.equal(await check(w.env), "The bot is not connected.");
});

test("the bot's menu and texts are set when connecting, and what Telegram refuses is told instead of hidden", async () => {
  const env = { DB: d1(), TOKEN_SECRET: SECRET };
  const api = telegram({ setMyDescription: { ok: false, error_code: 400, description: "Bad Request: DESCRIPTION_TOO_LONG" } });
  const linked = await connect(env, ADMIN_REQUEST(), TOKEN);
  assert.equal(linked.username, "AlbatrossSupportBot");
  assert.match(linked.warning, /did not take the bot's description: Bad Request: DESCRIPTION_TOO_LONG\. "Check the bot" tries again\.$/);
  assert.equal(api.of("setMyShortDescription").length, 1, "what Telegram does take is set all the same");
  assert.deepEqual(api.shown.menus.get('"all"').map((c) => c.command), ["start", "menu", "human", "close", "help"]);
  assert.equal(JSON.parse((await env.DB.prepare("SELECT value FROM settings WHERE key = 'telegram_texts'").first()).value).ok, false);
  // All taken: no warning, and a second connecting writes nothing that is already right.
  const good = telegram();
  const again = await connect({ DB: d1(), TOKEN_SECRET: SECRET }, ADMIN_REQUEST(), TOKEN);
  assert.equal(again.warning, undefined);
  assert.ok(good.shown.description.startsWith("Albatross VPN support.") && good.shown.about.startsWith("Albatross VPN support:"));
  const writes = good.calls.filter((c) => c.method.startsWith("setMy")).length;
  await connect({ DB: d1(), TOKEN_SECRET: SECRET }, ADMIN_REQUEST(), TOKEN);
  assert.equal(good.calls.filter((c) => c.method.startsWith("setMy")).length, writes, "a bot whose texts are right is not written to again");
});

test("the schedule sets the menu and texts when they are not what the service wants, and a refused try waits ten minutes", async () => {
  const w = await world();
  w.api.calls.length = 0;
  await sweep(w.env, w.kit);
  assert.equal(w.api.calls.length, 0, "all is as wanted: a run asks Telegram nothing");
  // A version of the service that wants other texts (as after a deploy): the next run sets them.
  await w.env.DB.prepare("UPDATE settings SET value = '{}' WHERE key = 'telegram_texts'").run();
  const lost = telegram();
  await sweep(w.env, w.kit);
  assert.equal(lost.of("setMyDescription").length, 1);
  assert.equal(lost.of("setMyShortDescription").length, 1);
  assert.deepEqual(lost.of("setMyCommands").map((c) => c.scope), [undefined, { type: "chat", chat_id: TEAM }], "the menu, and the team's own");
  await sweep(w.env, w.kit);
  assert.equal(lost.of("setMyDescription").length, 1, "set: not looked at again");
  // Telegram refuses: one try, then ten minutes of quiet, then another.
  await w.env.DB.prepare("UPDATE settings SET value = '{}' WHERE key = 'telegram_texts'").run();
  const refusing = telegram({ setMyShortDescription: { ok: false, error_code: 429, description: "Too Many Requests: retry after 600" } });
  await sweep(w.env, w.kit);
  await sweep(w.env, w.kit);
  assert.equal(refusing.of("setMyShortDescription").length, 1, "a refused try waits");
  const record = JSON.parse(await w.setting("telegram_texts"));
  assert.equal(record.ok, false);
  assert.match(record.note, /about line: Too Many Requests/);
  record.at -= 11 * 60;
  await w.env.DB.prepare("UPDATE settings SET value = ?1 WHERE key = 'telegram_texts'").bind(JSON.stringify(record)).run();
  await sweep(w.env, w.kit);
  assert.equal(refusing.of("setMyShortDescription").length, 2);
});

test("every hour the menu and texts are looked at, and written only where Telegram shows something else", async () => {
  const w = await world();
  const bare = telegram({ getWebhookInfo: { ok: true, result: { url: HOOK_URL } } });
  await heal(w.env);
  assert.equal(bare.of("setMyDescription").length, 1);
  assert.ok(bare.of("setMyCommands").some((c) => c.scope && c.scope.chat_id === TEAM), "the team's menu too");
  await heal(w.env);
  assert.equal(bare.of("setMyDescription").length, 1, "right now: left alone");
  assert.equal(bare.of("setMyCommands").length, 2);
  bare.shown.description = "something somebody typed in BotFather";
  await heal(w.env);
  assert.equal(bare.of("setMyDescription").length, 2, "a text that was changed behind the service's back is put right");
});

// ---------- A person: the menu and the guides ----------

test("/start shows the main menu with its six buttons, and a link from the website opens the right guide", async () => {
  const w = await world();
  await w.send(text(SARA, "/start"));
  const menu = w.lastText(SARA);
  assert.ok(menu.text.includes("Albatross Support") && menu.text.includes("Hi Sara!"));
  assert.equal(menu.parse_mode, "HTML");
  assert.deepEqual(labels(menu.reply_markup), ["🔌 I can't connect", "📲 Install the app", "💎 Plans & upgrading", "👤 Account & data", "⬇️ Downloads", "🧑‍💼 Talk to our team"]);
  await w.send(text(SARA, "/start@AlbatrossSupportBot connect"));
  assert.ok(w.lastText(SARA).text.includes("Which device are you on?"));
  await w.send(text(SARA, "/start plans"));
  assert.ok(w.lastText(SARA).text.includes("Bronze") && w.lastText(SARA).text.includes("VIP"));
  // A topic that is not one - not even a word every object has - is the menu.
  for (const topic of ["constructor", "__proto__", "toString", "nonsense"]) {
    await w.send(text(SARA, `/start ${topic}`));
    assert.ok(w.lastText(SARA).text.includes("Pick a topic"), topic);
  }
  await w.send(text(SARA, "/help"));
  assert.ok(w.lastText(SARA).text.includes("/human"));
  await w.send(text(SARA, "/nonsense"));
  assert.ok(w.lastText(SARA).text.startsWith("I do not know that command."));
  assert.equal(w.env.asked.length, 0, "commands and menus never cost the model");
});

test("the guide for a connection that fails goes by device, edits the message in place, and a thumbs down leads on to a person", async () => {
  const w = await world();
  await w.send(press(SARA, "c"));
  assert.equal(w.api.of("answerCallbackQuery").length, 1);
  const picker = w.api.of("editMessageText")[0];
  assert.equal(picker.message_id, 55);
  assert.deepEqual(datas(picker.reply_markup).slice(0, 4), ["c:win", "c:and", "c:ios", "c:mac"]);
  await w.send(press(SARA, "c:win"));
  const guide = w.api.of("editMessageText")[1];
  assert.ok(guide.text.includes("Windows") && guide.text.includes("1. <b>Update Albatross.</b>") && guide.text.includes("Did it work?"));
  assert.deepEqual(labels(guide.reply_markup).slice(0, 2), ["✅ It works now", "❌ Still not connecting"]);
  await w.send(press(SARA, "c:ios"));
  const phone = w.api.of("editMessageText")[2];
  assert.ok(phone.text.includes("iPhone and iPad") && phone.text.includes("Add the servers again"));
  assert.ok(datas(phone.reply_markup).includes("https://accounts.albatrossvpn.online/account"));
  await w.send(press(SARA, "c:mac"));
  const mac = w.api.of("editMessageText")[3];
  assert.ok(mac.text.includes("On a Mac the servers live in a connection app") && !/iPhone|iPad/.test(mac.text), "a Mac is not told about iPhones");
  await w.send(press(SARA, "ok:win"));
  assert.ok(w.api.of("editMessageText")[4].text.includes("all set"));
  assert.equal((await w.row("SELECT n FROM tg_stats WHERE key = 'fixed'")).n, 1);
  // Still broken: the next words are the problem, and the assistant is told which device.
  await w.send(press(SARA, "no:win"));
  assert.ok(w.api.of("editMessageText")[5].text.includes("Type the exact message"));
  assert.equal((await w.row("SELECT mode, device FROM tg_users WHERE chat_id = ?1", SARA)).mode, "problem");
  await w.send(text(SARA, "It says no working server"));
  assert.ok(w.env.asked[0].messages[0].content.includes("They are on Windows and the standard checklist did not help"));
  assert.equal((await w.row("SELECT mode FROM tg_users WHERE chat_id = ?1", SARA)).mode, "", "the problem was described");
  // Leaving the guide by the menu ends the step too, so the next question is not read as that problem.
  await w.send(press(SARA, "no:and"));
  assert.equal((await w.row("SELECT mode FROM tg_users WHERE chat_id = ?1", SARA)).mode, "problem");
  await w.send(press(SARA, "menu"));
  assert.equal((await w.row("SELECT mode FROM tg_users WHERE chat_id = ?1", SARA)).mode, "");
  await w.send(text(SARA, "What does Gold cost?"));
  assert.ok(!w.env.asked[1].messages[0].content.includes("standard checklist"));
});

test("install, plans, account and downloads are screens with links, not answers of the model", async () => {
  const w = await world();
  await w.send(press(SARA, "i:ios"));
  assert.ok(w.api.of("editMessageText")[0].text.includes("Add to Home Screen"));
  await w.send(press(SARA, "i:win"));
  assert.ok(w.api.of("editMessageText")[1].text.includes("Windows protected your PC"));
  assert.ok(datas(w.api.of("editMessageText")[1].reply_markup).includes("https://albatrossvpn.online/#download"));
  await w.send(press(SARA, "p"));
  assert.ok(w.api.of("editMessageText")[2].text.includes("unlimited devices"));
  await w.send(press(SARA, "a"));
  assert.ok(w.api.of("editMessageText")[3].text.includes("Never send yours here"));
  await w.send(press(SARA, "d"));
  assert.ok(w.api.of("editMessageText")[4].text.includes("SHA-256"));
  await w.send(press(SARA, "menu"));
  assert.ok(w.api.of("editMessageText")[5].text.includes("Pick a topic"));
  await w.send(press(SARA, "i:mac"));
  const mac = w.api.of("editMessageText")[6];
  assert.ok(mac.text.includes("Install on Mac") && !/Home Screen|iPhone|iOS/.test(mac.text), "a Mac has its own steps");
  assert.equal(w.env.asked.length, 0);
});

// ---------- A person: the assistant ----------

test("a question is answered by the assistant with the conversation so far, and the answer can be rated without losing what it said", async () => {
  const w = await world({ answer: "Open the app and tap Update." });
  await w.send(text(SARA, "How do I update the app?"));
  assert.equal(w.api.of("sendChatAction")[0].action, "typing");
  const input = w.env.asked[0];
  assert.equal(input.messages[0].role, "system");
  assert.ok(input.messages[0].content.includes("CONTEXT") && input.messages[0].content.includes("Telegram"));
  assert.deepEqual(input.messages.slice(1), [{ role: "user", content: "How do I update the app?" }]);
  const answer = w.lastText(SARA);
  assert.equal(answer.text, "Open the app and tap Update.");
  assert.deepEqual(labels(answer.reply_markup), ["👍 Helpful", "👎 Not helpful"]);
  await w.send(text(SARA, "And on Windows?"));
  assert.deepEqual(w.env.asked[1].messages.slice(1).map((m) => m.role), ["user", "assistant", "user"], "the second question carries the first exchange");
  assert.equal((await w.row("SELECT n FROM tg_stats WHERE key = 'chats'")).n, 1);
  assert.equal((await w.row("SELECT n FROM tg_stats WHERE key = 'answers'")).n, 2);
  // Thumbs: counted, and the buttons change under the answer.
  await w.send(press(SARA, "fb:u", 60));
  assert.equal(w.api.of("editMessageReplyMarkup")[0].message_id, 60);
  assert.deepEqual(labels(w.api.of("editMessageReplyMarkup")[0].reply_markup), ["📋 Menu"]);
  await w.send(press(SARA, "fb:d", 61));
  assert.deepEqual(labels(w.api.of("editMessageReplyMarkup")[1].reply_markup), ["🧑‍💼 Talk to our team", "🔌 Fix a connection", "📋 Menu"]);
  assert.equal((await w.row("SELECT n FROM tg_stats WHERE key = 'up'")).n, 1);
  assert.equal((await w.row("SELECT n FROM tg_stats WHERE key = 'down'")).n, 1);
  // The buttons under an answer send new messages: the answer above them stays.
  w.api.calls.length = 0;
  await w.send(press(SARA, "mn", 61));
  await w.send(press(SARA, "cn", 61));
  assert.equal(w.api.of("editMessageText").length, 0);
  assert.ok(w.api.of("sendMessage")[0].text.includes("Pick a topic") && w.api.of("sendMessage")[1].text.includes("Which device are you on?"));
});

test("the answer is made safe for Telegram: the model's markup is text, a hand-over brings the team button and keeps it after a thumbs up", async () => {
  const w = await world({ answer: "Use <b>bold</b> & <script>alert(1)</script>\n[[HUMAN]]" });
  await w.send(text(SARA, "I want to pay"));
  const a = w.lastText(SARA);
  assert.equal(a.text, "Use &lt;b&gt;bold&lt;/b&gt; &amp; &lt;script&gt;alert(1)&lt;/script&gt;");
  assert.deepEqual(labels(a.reply_markup), ["🧑‍💼 Talk to our team", "👍 Helpful", "👎 Not helpful"]);
  assert.deepEqual(datas(a.reply_markup), ["h", "fb:u:1", "fb:d:1"]);
  await w.send(press(SARA, "fb:u:1", 60));
  assert.deepEqual(labels(w.api.of("editMessageReplyMarkup")[0].reply_markup), ["🧑‍💼 Talk to our team", "📋 Menu"], "the way to a person is still there");
});

test("a long answer is cut to fit with a mark, and never in the middle of a character", async () => {
  const w = await world({ answer: "<".repeat(1500) });
  await w.send(text(SARA, "again"));
  const a = w.lastText(SARA).text;
  assert.ok(a.length <= 4096 && a.startsWith("&lt;") && a.endsWith("…"));
  // An emoji is two units long: wherever the cut falls (with these lengths it falls inside one, twice), no half of one is left.
  for (let n = 0; n < 12; n++) {
    w.env.answer = "<<😀".repeat(440 + n * 5);
    await w.send(text(100 + n, "and again"));
    const b = w.lastText(100 + n).text;
    assert.ok(b.length <= 4096 && b.endsWith("…") && b.isWellFormed(), `answer ${n}`);
  }
  w.env.answer = "short";
  await w.send(text(SARA, "and once more"));
  assert.equal(w.lastText(SARA).text, "short", "a short answer has no mark");
});

test("a model that is away, a picture, and a flood of messages are each met with a plain answer", async () => {
  const w = await world();
  w.env.aiDown = true;
  await w.send(text(SARA, "hello"));
  assert.ok(w.lastText(SARA).text.startsWith("I could not answer just now"));
  assert.deepEqual(labels(w.lastText(SARA).reply_markup), ["🧑‍💼 Talk to our team", "📋 Menu"]);
  w.env.aiDown = false;
  await w.send(media(SARA, { photo: [{ file_id: "x" }] }));
  assert.ok(w.lastText(SARA).text.startsWith("I can only read text here"));
  // Thirty messages an hour per person; the notice comes once, and is not about the model.
  for (let i = 0; i < 45; i++) await w.send(text(SARA, `message ${i}`));
  assert.equal(w.api.of("sendMessage").filter((c) => c.text.startsWith("You are sending messages very fast")).length, 1, "one notice, then quiet");
  assert.equal(w.env.asked.length, 1 + 28, "the failed hello, then what was left of the thirty an hour");
  // Buttons have their own, wider allowance.
  const flooded = await world();
  for (let i = 0; i < 130; i++) await flooded.send(press(SARA, "menu"));
  assert.equal(flooded.api.of("editMessageText").length, 120);
  assert.equal(flooded.api.of("answerCallbackQuery").filter((c) => c.text === "Slow down a little.").length, 10);
});

test("the day's budget of answers is the bot's own, and a person is told so once an hour, differently from being too fast", async () => {
  const w = await world();
  const t = Math.floor(Date.now() / 1000);
  await w.env.DB.prepare("INSERT INTO attempts (key, count, since) VALUES (?1, 600, ?2)").bind(`tg:chat:all:${new Date().toISOString().slice(0, 10)}`, t).run();
  await w.send(text(SARA, "How do I update?"));
  await w.send(text(SARA, "Hello?"));
  const notices = w.api.of("sendMessage").filter((c) => c.text.startsWith("I am getting a lot of messages"));
  assert.equal(notices.length, 1);
  assert.deepEqual(labels(notices[0].reply_markup), ["🧑‍💼 Talk to our team", "📋 Menu"]);
  assert.equal(w.env.asked.length, 0);
  // The websites' own budget is a separate one.
  assert.equal((await w.row("SELECT COUNT(*) AS n FROM tg_messages")).n, 0, "nothing was stored of what was not answered");
});

test("rate limits remember a keyed hash of the person, never the id", async () => {
  const w = await world();
  await w.send(text(SARA, "How do I update?"));
  await w.send(press(SARA, "menu"));
  await w.send(text(SARA, "/human"));
  const keys = (await w.rows("SELECT key FROM attempts")).map((r) => r.key);
  assert.ok(keys.length >= 4, keys.join());
  assert.ok(keys.every((k) => !k.includes(String(SARA))), keys.join());
});

test("an update Telegram sends twice is handled once, and bots, groups and channel posts are not answered", async () => {
  const w = await world();
  const u = text(SARA, "How do I install?");
  await w.send(u);
  await w.send(u);
  assert.equal(w.env.asked.length, 1);
  assert.equal(w.api.of("sendMessage").length, 1);
  await w.send({ update_id: ++uid, message: { message_id: 1, from: { id: 9, is_bot: true, first_name: "Bot" }, chat: { id: 9, type: "private" }, text: "hi" } });
  await w.send({ update_id: ++uid, message: { message_id: 2, from: { id: SARA, first_name: "Sara" }, chat: { id: -100123, type: "supergroup" }, text: "hi all" } });
  await w.send({ update_id: ++uid, channel_post: { message_id: 3, chat: { id: -100999, type: "channel" }, text: "news" } });
  assert.equal(w.api.of("sendMessage").length, 1);
});

test("when handling breaks the person is told rather than left waiting, and a button press is answered", async () => {
  const w = await world();
  w.kit.blocked = async () => { throw new Error("the database is away"); };
  const error = console.error;
  console.error = () => {};
  try {
    await w.send(text(SARA, "hello"));
    await w.send(press(SARA, "menu"));
  } finally {
    console.error = error;
  }
  assert.ok(w.lastText(SARA).text.startsWith("Something went wrong on our side"));
  assert.equal(w.api.of("answerCallbackQuery").pop().text, "Something went wrong. Please try again.");
});

test("a flood wait Telegram asks for is waited out, once", async () => {
  const w = await world();
  let n = 0;
  const api = telegram({ sendMessage: () => (n++ === 0 ? { ok: false, error_code: 429, description: "Too Many Requests: retry after 1", parameters: { retry_after: 1 } } : { ok: true, result: { message_id: 900 } }) });
  await w.send(text(SARA, "/menu"));
  assert.equal(api.of("sendMessage").length, 2);
});

// ---------- Tickets ----------

test("talking to the team opens a numbered ticket: the person is told, the team's chat gets a card, the owner gets a mail", async () => {
  const w = await world({ answer: "Summary text.\nStatus: NEEDS FOLLOW-UP" });
  await w.send(text(SARA, "I can't connect on Windows"));
  w.api.calls.length = 0;
  await w.send(press(SARA, "h"));
  const mine = w.lastText(SARA);
  assert.ok(mine.text.includes("Ticket #1001 opened") && mine.text.includes("has your chat so far"));
  assert.deepEqual(labels(mine.reply_markup), ["✅ My problem is solved"]);
  assert.deepEqual(datas(mine.reply_markup), ["tc:1001"], "the button names its ticket");
  const card = w.lastText(TEAM);
  assert.ok(card.text.includes("Ticket #1001") && card.text.includes("Sara (@sara_tg)") && card.text.includes(`<code>${SARA}</code>`));
  assert.ok(card.text.includes("I can't connect on Windows"));
  assert.deepEqual(datas(card.reply_markup), ["o:c:1001", "o:b:4242", "o:f:1001"]);
  const relay = await w.row("SELECT ticket_id FROM tg_relay WHERE team_chat = ?1", TEAM);
  assert.equal(relay.ticket_id, 1001);
  assert.equal((await w.row("SELECT mode, ticket_id FROM tg_users WHERE chat_id = ?1", SARA)).mode, "ticket");
  assert.equal(w.env.sent.length, 1);
  const mail = w.env.sent[0];
  assert.ok(mail.subject.startsWith("[Albatross Telegram] ticket #1001: I can't connect on Windows"), mail.subject);
  assert.ok(mail.text.includes("Channel: Telegram   From: Sara (@sara_tg), id 4242") && mail.text.includes("Visitor: I can't connect on Windows") && mail.text.includes("Status: NEEDS FOLLOW-UP"));
  // Asking again shows the open ticket instead of a second one.
  await w.send(text(SARA, "/human"));
  assert.ok(w.lastText(SARA).text.includes("already have <b>ticket #1001</b>"));
  assert.equal((await w.row("SELECT COUNT(*) AS n FROM tg_tickets")).n, 1);
});

test("two presses of the button at once open one ticket, and a claim that never finished lets go", async () => {
  const w = await world();
  await Promise.all([w.send(press(SARA, "h")), w.send(press(SARA, "h"))]);
  assert.equal((await w.row("SELECT COUNT(*) AS n FROM tg_tickets")).n, 1);
  assert.equal(w.api.of("sendMessage").filter((c) => c.chat_id === TEAM).length, 1, "one card");
  // A person whose ticket is being opened (a claim made a moment ago) opens nothing else.
  const t = Math.floor(Date.now() / 1000);
  await w.send(text(11, "hello"));
  await w.env.DB.prepare("UPDATE tg_users SET ticket_id = ?1 WHERE chat_id = 11").bind(-(t - 10)).run();
  await w.send(text(11, "/human"));
  assert.equal((await w.row("SELECT COUNT(*) AS n FROM tg_tickets")).n, 1);
  // One made long ago was never finished: it lets go.
  await w.env.DB.prepare("UPDATE tg_users SET ticket_id = ?1 WHERE chat_id = 11").bind(-(t - 500)).run();
  await w.send(text(11, "/human"));
  assert.equal((await w.row("SELECT COUNT(*) AS n FROM tg_tickets")).n, 2);
  assert.equal((await w.row("SELECT ticket_id FROM tg_users WHERE chat_id = 11")).ticket_id, 1002);
});

test("while a ticket is open the person's words, pictures and files reach the team, with the ticket on them; the model stays out of it", async () => {
  const w = await world();
  await w.send(text(SARA, "/human"));
  assert.ok(w.lastText(SARA).text.includes("Write your problem here"));
  w.api.calls.length = 0;
  const first = text(SARA, "It says <no server>");
  await w.send(first);
  const forwarded = w.lastText(TEAM);
  assert.equal(forwarded.text, "🎫 <b>#1001</b> - Sara (@sara_tg)\nIt says &lt;no server&gt;");
  assert.equal(w.api.of("setMessageReaction")[0].message_id, first.message.message_id);
  assert.equal(w.api.of("setMessageReaction")[0].reaction[0].emoji, "👀");
  assert.equal(w.env.asked.length, 0);
  await w.send(media(SARA, { photo: [{ file_id: "p" }], caption: "the screen" }));
  const copy = w.api.of("copyMessage")[0];
  assert.equal(copy.chat_id, TEAM);
  assert.equal(copy.from_chat_id, SARA);
  assert.ok(copy.caption.startsWith("🎫 #1001 - Sara (@sara_tg)") && copy.caption.includes("the screen"));
  await w.send(media(SARA, { sticker: { file_id: "s" } }));
  assert.equal(w.api.of("copyMessage").length, 2);
  assert.ok(w.lastText(TEAM).text.includes("#1001"), "a sticker has no caption: a line says whose it is");
  // A GIF is also a document, and a place also a location: the more exact kind is named.
  await w.send(media(SARA, { animation: { file_id: "a" }, document: { file_id: "a" } }));
  await w.send(media(SARA, { venue: { title: "Cafe" }, location: { latitude: 1, longitude: 2 } }));
  assert.deepEqual((await w.rows("SELECT content FROM tg_messages ORDER BY id")).map((r) => r.content), ["It says <no server>", "[photo] the screen", "[sticker]", "[GIF]", "[place]"]);
});

test("the team answers a ticket by replying to its message; photos and files go too; closing tells the person and forgets the chat", async () => {
  const w = await world();
  await w.send(text(SARA, "/human"));
  await w.send(text(SARA, "No server found"));
  const noteId = (await w.rows("SELECT message_id FROM tg_relay ORDER BY at DESC, message_id DESC"))[0].message_id;
  w.api.calls.length = 0;
  const reply = text(TEAM, "Please update to 1.1.0 & tell me if it helps", { reply_to_message: { message_id: noteId } });
  await w.send(reply);
  const toSara = w.lastText(SARA);
  assert.equal(toSara.text, "💬 <b>Albatross team</b>\nPlease update to 1.1.0 &amp; tell me if it helps");
  assert.deepEqual(labels(toSara.reply_markup), ["✅ My problem is solved"]);
  assert.equal(w.api.of("setMessageReaction")[0].chat_id, TEAM);
  assert.equal((await w.row("SELECT role FROM tg_messages ORDER BY id DESC LIMIT 1")).role, "team");
  await w.send(media(TEAM, { photo: [{ file_id: "q" }], reply_to_message: { message_id: noteId } }));
  assert.equal(w.api.of("copyMessage")[0].chat_id, SARA);
  assert.deepEqual(datas(w.api.of("copyMessage")[0].reply_markup), ["tc:1001"], "a photo from the team carries the button too");
  // A long answer is cut to fit rather than refused.
  await w.send(text(TEAM, "&".repeat(4000), { reply_to_message: { message_id: noteId } }));
  assert.ok(w.lastText(SARA).text.length <= 4096 && w.lastText(SARA).text.endsWith("…"));
  // Something said in the team's chat that answers nothing is not sent anywhere.
  w.api.calls.length = 0;
  await w.send(text(TEAM, "lunch?", { chat: { id: TEAM, type: "group" } }));
  assert.equal(w.api.calls.length, 0);
  // Closing with /close as a reply.
  await w.send(text(TEAM, "/close", { reply_to_message: { message_id: noteId } }));
  assert.ok(w.lastText(SARA).text.includes("Ticket #1001 is closed"));
  assert.ok(w.lastText(TEAM).text.includes("Ticket #1001 closed."));
  assert.equal((await w.row("SELECT mode, ticket_id FROM tg_users WHERE chat_id = ?1", SARA)).ticket_id, 0);
  assert.equal((await w.row("SELECT COUNT(*) AS n FROM tg_messages")).n, 0, "nothing of the chat is kept");
  const closed = await w.row("SELECT status, closed_by, chat_id FROM tg_tickets WHERE id = 1001");
  assert.deepEqual({ ...closed }, { status: "closed", closed_by: "team", chat_id: 0 }, "a closed ticket keeps its number and nothing of the person");
  // The message ids stay a while, so a reply to an old card says it is closed instead of vanishing.
  assert.equal((await w.row("SELECT COUNT(*) AS n FROM tg_relay")).n, 2);
  w.api.calls.length = 0;
  await w.send(text(TEAM, "one more thing", { reply_to_message: { message_id: noteId } }));
  assert.ok(w.lastText(TEAM).text.startsWith("Ticket #1001 is closed."));
  assert.equal(w.api.of("sendMessage").filter((c) => c.chat_id === SARA).length, 0, "the person is not written to");
});

test("a person who blocked the bot is not a silent failure for the team", async () => {
  const w = await world();
  await w.send(text(SARA, "/human"));
  await w.send(text(SARA, "hello team"));
  const noteId = (await w.rows("SELECT message_id FROM tg_relay ORDER BY message_id DESC"))[0].message_id;
  const api = telegram({ sendMessage: (p) => (p.chat_id === SARA ? { ok: false, error_code: 403, description: "Forbidden: bot was blocked by the user" } : { ok: true, result: { message_id: 900 } }) });
  await w.send(text(TEAM, "are you there?", { reply_to_message: { message_id: noteId } }));
  const warning = api.of("sendMessage").filter((c) => c.chat_id === TEAM).pop();
  assert.ok(warning.text.startsWith("⚠️ Could not deliver: Forbidden: bot was blocked by the user"));
});

test("the person can close their own ticket, with the button or /close, rate the help, and the team is told", async () => {
  const w = await world();
  await w.send(text(SARA, "/human"));
  w.api.calls.length = 0;
  await w.send(press(SARA, "tc:1001"));
  assert.ok(w.lastText(SARA).text.includes("Glad it is sorted") && w.lastText(SARA).text.includes("How was our help?"));
  assert.deepEqual(labels(w.lastText(SARA).reply_markup), ["1⭐", "2⭐", "3⭐", "4⭐", "5⭐", "📋 Menu"]);
  assert.ok(w.lastText(TEAM).text.includes("Ticket #1001") && w.lastText(TEAM).text.includes("closed by Sara (@sara_tg)"));
  await w.send(press(SARA, "r:4", 80));
  assert.ok(w.api.of("editMessageText").pop().text.includes("rated our help 4 out of 5"));
  await w.send(press(SARA, "r:9", 80));
  await w.send(text(TEAM, "/stats"));
  assert.ok(w.lastText(TEAM).text.includes("Rating of the help: 4.0 out of 5 (1)"), w.lastText(TEAM).text);
  await w.send(text(SARA, "/human"));
  assert.ok(w.lastText(SARA).text.includes("Ticket #1002 opened"), "numbers go on");
  await w.send(text(SARA, "/close"));
  assert.equal((await w.row("SELECT closed_by FROM tg_tickets WHERE id = 1002")).closed_by, "user");
  // Tickets are limited per person.
  for (let i = 0; i < 5; i++) {
    await w.send(text(SARA, "/human"));
    await w.send(text(SARA, "/close"));
  }
  assert.ok(w.api.of("sendMessage").some((c) => c.text.startsWith("You have opened several tickets today, so I cannot open another one right now")));
  assert.equal((await w.row("SELECT COUNT(*) AS n FROM tg_tickets")).n, 5, "five a day");
});

test("a button of an old ticket never closes a newer one, nor somebody else's", async () => {
  const w = await world();
  await w.send(text(SARA, "/human"));
  await w.send(press(SARA, "tc:1001"));
  await w.send(text(SARA, "/human"));
  await w.send(text(11, "/human"));
  w.api.calls.length = 0;
  await w.send(press(SARA, "tc:1001"));
  assert.ok(w.lastText(SARA).text.includes("already closed"));
  assert.equal((await w.row("SELECT status FROM tg_tickets WHERE id = 1002")).status, "open");
  await w.send(press(SARA, "tc:1003"));
  assert.equal((await w.row("SELECT status FROM tg_tickets WHERE id = 1003")).status, "open", "somebody else's ticket is not hers to close");
  assert.equal((await w.row("SELECT mode FROM tg_users WHERE chat_id = ?1", SARA)).mode, "ticket");
});

test("a ticket the team closes while its person still has the old state goes on as an ordinary chat", async () => {
  const w = await world();
  await w.send(text(SARA, "/human"));
  // The ticket was closed behind the chat's back (as if the second statement of closing never ran).
  await w.env.DB.prepare("UPDATE tg_tickets SET status = 'closed', chat_id = 0 WHERE id = 1001").run();
  w.api.calls.length = 0;
  await w.send(text(SARA, "Is anybody there?"));
  assert.equal(w.env.asked.length, 1, "the assistant answers");
  assert.equal((await w.row("SELECT mode, ticket_id FROM tg_users WHERE chat_id = ?1", SARA)).ticket_id, 0);
  assert.equal(w.api.of("sendMessage").filter((c) => c.chat_id === TEAM).length, 0);
});

test("without a paired team chat nobody is promised a ticket: the owner gets the chat by mail and the person is told how things stand", async () => {
  const w = await world({ team: false, answer: "Problem: connection.\nStatus: NEEDS FOLLOW-UP" });
  await w.send(text(SARA, "I cannot connect on Windows"));
  w.env.sent.length = 0;
  await w.send(text(SARA, "/human"));
  const said = w.lastText(SARA);
  assert.ok(said.text.includes("not connected to this chat yet") && said.text.includes("sent them this chat by email") && said.text.includes("support@albatrossvpn.online"), said.text);
  assert.ok(!said.text.includes("Ticket #"));
  assert.equal((await w.row("SELECT COUNT(*) AS n FROM tg_tickets")).n, 0);
  assert.equal(w.env.sent.length, 1);
  assert.ok(w.env.sent[0].subject.includes("needs follow-up") && w.env.sent[0].text.includes("I cannot connect on Windows"));
  assert.equal((await w.row("SELECT mode FROM tg_users WHERE chat_id = ?1", SARA)).mode, "", "the person stays with the assistant");
  // With no mail set up either, it does not say one was sent.
  const bare = await world({ team: false, mail: false });
  await bare.send(text(SARA, "I cannot connect on Windows"));
  await bare.send(text(SARA, "/human"));
  assert.ok(bare.lastText(SARA).text.includes("not connected to this chat yet") && !bare.lastText(SARA).text.includes("by email, but"), bare.lastText(SARA).text);
  assert.equal(bare.env.sent.length, 0);
});

test("a team chat that cannot be reached is not a silent loss: the owner is mailed, and the person is told once", async () => {
  const w = await world();
  const api = telegram({ sendMessage: (p) => (p.chat_id === TEAM ? { ok: false, error_code: 403, description: "Forbidden: bot was kicked from the group chat" } : { ok: true, result: { message_id: 900 } }) });
  await w.send(text(SARA, "/human"));
  assert.equal(w.env.sent.length, 1, "the owner got the ticket by mail");
  assert.ok(!api.of("sendMessage").some((c) => c.text === "I could not reach our team's chat just now, so they may be slow to see your message. If it is urgent, email support@albatrossvpn.online."), "the mail took it, so nothing is hidden");
  w.env.sent.length = 0;
  api.calls.length = 0;
  await w.send(text(SARA, "The error is 12"));
  await w.send(text(SARA, "Still the same"));
  assert.equal(api.of("setMessageReaction").length, 0, "nothing was delivered, so nothing is confirmed");
  assert.equal(w.env.sent.length, 2, "each message reaches the owner by mail");
  assert.ok(w.env.sent[0].text.includes("The error is 12"));
  assert.equal(api.of("sendMessage").filter((c) => c.chat_id === SARA && c.text.startsWith("I could not reach our team")).length, 1, "told once");
  // Neither the chat nor the mail: the person is not told someone knows.
  const lost = await world({ mail: false });
  const api2 = telegram({ sendMessage: (p) => (p.chat_id === TEAM ? { ok: false, error_code: 403, description: "Forbidden" } : { ok: true, result: { message_id: 900 } }) });
  await lost.send(text(SARA, "/human"));
  assert.ok(api2.of("sendMessage").some((c) => c.chat_id === SARA && c.text.startsWith("I could not reach our team")));
});

test("a team group that became a supergroup is followed, by the service message and by Telegram's refusal", async () => {
  const w = await world();
  await w.send({ update_id: ++uid, message: { message_id: 1, chat: { id: TEAM, type: "group" }, migrate_to_chat_id: -100777 } });
  assert.equal(await w.setting("telegram_owner_chat"), "-100777");
  w.api.calls.length = 0;
  await w.send(text(SARA, "/human"));
  assert.equal(w.lastText(-100777).text.includes("Ticket #1001"), true, "the card goes to the new chat");
  assert.equal((await w.row("SELECT team_chat FROM tg_relay LIMIT 1")).team_chat, -100777);
  // Telegram's own refusal names the new chat as well.
  const v = await world();
  const api = telegram({ sendMessage: (p) => (p.chat_id === TEAM ? { ok: false, error_code: 400, description: "Bad Request: group chat was upgraded to a supergroup chat", parameters: { migrate_to_chat_id: -100888 } } : { ok: true, result: { message_id: 900 } }) });
  await v.send(text(SARA, "/human"));
  assert.equal(await v.setting("telegram_owner_chat"), "-100888");
  assert.ok(api.of("sendMessage").some((c) => c.chat_id === -100888 && c.text.includes("Ticket #1001")));
});

test("an administrator who writes anonymously in the team's group is the team: pairs it and answers tickets", async () => {
  const w = await world({ team: false });
  const GROUP = -100555;
  const anonymous = (words, extra = {}) => ({ update_id: ++uid, message: { message_id: ++mid, from: { id: 1087968824, is_bot: true, first_name: "Group", username: "GroupAnonymousBot" }, sender_chat: { id: GROUP, type: "supergroup" }, chat: { id: GROUP, type: "supergroup" }, text: words, ...extra } });
  await w.send(anonymous(`/pair ${await w.setting("telegram_pair_code")}`));
  assert.equal(await w.setting("telegram_owner_chat"), String(GROUP));
  await w.send(text(SARA, "/human"));
  const card = w.lastText(GROUP);
  assert.ok(card.text.includes("Ticket #1001"));
  const noteId = (await w.row("SELECT message_id FROM tg_relay")).message_id;
  await w.send(anonymous("Try again now", { reply_to_message: { message_id: noteId } }));
  assert.ok(w.lastText(SARA).text.includes("Try again now"));
  // A command meant for another bot in the same group is not ours.
  w.api.calls.length = 0;
  await w.send(anonymous("/stats@SomeOtherBot"));
  assert.equal(w.api.calls.length, 0);
  await w.send(anonymous("/stats@AlbatrossSupportBot"));
  assert.ok(w.lastText(GROUP).text.includes("Today (UTC)"));
  // A real bot is still not a person.
  await w.send({ update_id: ++uid, message: { message_id: 5, from: { id: 9, is_bot: true, username: "OtherBot" }, chat: { id: GROUP, type: "supergroup" }, text: "/stats" } });
  assert.equal(w.api.of("sendMessage").filter((c) => c.chat_id === GROUP).length, 1);
});

// ---------- A chat ends ----------

test("/end sends the owner a summary of the chat, forgets it and thanks the person; an empty chat has nothing to end", async () => {
  const w = await world({ answer: "Problem: update.\nStatus: RESOLVED" });
  await w.send(text(SARA, "/end"));
  assert.ok(w.lastText(SARA).text.includes("nothing to close"));
  await w.send(text(SARA, "How do I update?"));
  await w.send(text(SARA, "/end"));
  assert.ok(w.lastText(SARA).text.startsWith("Thank you! A short summary of this chat has gone to our team"));
  assert.equal(w.env.sent.length, 1);
  assert.ok(w.env.sent[0].subject.startsWith("[Albatross Telegram] resolved: How do I update?"));
  assert.equal((await w.row("SELECT COUNT(*) AS n FROM tg_messages")).n, 0);
  assert.equal(await w.row("SELECT * FROM tg_users WHERE chat_id = ?1", SARA), null, "nobody is kept who is not talking");
});

test("a person is told a summary went to the team only when it did", async () => {
  const none = await world({ mail: false });
  await none.send(text(SARA, "How do I update?"));
  await none.send(text(SARA, "/end"));
  assert.equal(none.lastText(SARA).text, "This chat is closed and deleted. Write any time to start again.");
  assert.equal((await none.row("SELECT COUNT(*) AS n FROM tg_messages")).n, 0, "it is deleted all the same");
  const broken = await world();
  broken.env.EMAIL = { send: async () => { throw new Error("the mail service is away"); } };
  await broken.send(text(SARA, "How do I update?"));
  const error = console.error;
  console.error = () => {};
  try {
    await broken.send(text(SARA, "/end"));
  } finally {
    console.error = error;
  }
  assert.equal(broken.lastText(SARA).text, "This chat is closed and deleted. Write any time to start again.");
  assert.match(await broken.setting("support_mail_status"), /^Failed, .*the mail service is away/);
});

test("a hiccup of the mail service is tried once more", async () => {
  const w = await world({ answer: "Problem: update.\nStatus: RESOLVED" });
  let n = 0;
  w.env.EMAIL = { send: async (m) => { if (n++ === 0) throw new Error("hiccup"); w.env.sent.push(m); } };
  await w.send(text(SARA, "How do I update?"));
  await w.send(text(SARA, "/end"));
  assert.equal(n, 2);
  assert.equal(w.env.sent.length, 1);
  assert.ok(w.lastText(SARA).text.startsWith("Thank you! A short summary"));
  assert.match(await w.setting("support_mail_status"), /^Sent, /);
});

test("the mail carries the whole chat, while the model reads its start and its end", async () => {
  const w = await world({ answer: "Problem: many.\nStatus: NEEDS FOLLOW-UP" });
  const long = "x".repeat(700);
  for (let i = 0; i < 20; i++) await w.send(text(SARA, `question ${i} ${long}`));
  w.env.asked.length = 0;
  await w.send(text(SARA, "/end"));
  const summaryCall = w.env.asked.find((a) => a.messages[0].content.startsWith("You write short internal notes"));
  assert.ok(summaryCall.messages[1].content.length < 9500 && summaryCall.messages[1].content.includes("left out"), "the model is not given more than it can read");
  assert.ok(summaryCall.messages[1].content.includes("question 0") && summaryCall.messages[1].content.includes("question 19"));
  const mail = w.env.sent[0].text;
  for (let i = 0; i < 20; i++) assert.ok(mail.includes(`Visitor: question ${i} `), `question ${i} is in the mail`);
  assert.ok(mail.includes("Assistant: Problem: many."), "the answers are in it too");
});

test("every minute a few of the chats that went quiet are summarized and forgotten, and tickets nobody writes to are closed", async () => {
  const w = await world({ answer: "Problem: update.\nStatus: NEEDS FOLLOW-UP" });
  await w.send(text(SARA, "How do I update?"));
  await w.send(text(11, "Where do I download?"));
  await w.send(text(12, "Only looking at the menu"));
  await w.send(text(13, "/human"));
  await w.send(text(13, "I need help"));
  const old = Math.floor(Date.now() / 1000) - 2 * 3600;
  await w.env.DB.prepare("UPDATE tg_users SET updated = ?1 WHERE chat_id IN (?2, ?3, ?4, ?5)").bind(old, SARA, 11, 13, 12).run();
  await w.env.DB.prepare("DELETE FROM tg_messages WHERE chat_id = 12").run();
  w.env.sent.length = 0;
  await sweep(w.env, w.kit);
  assert.equal(w.env.sent.length, 2, "one summary for each chat that said something");
  assert.equal(await w.row("SELECT * FROM tg_users WHERE chat_id = ?1", SARA), null);
  assert.equal(await w.row("SELECT * FROM tg_users WHERE chat_id = 12"), null);
  assert.equal((await w.row("SELECT mode FROM tg_users WHERE chat_id = 13")).mode, "ticket", "a ticket's chat stays while the ticket is open");
  // A ticket that nobody writes to for three days is closed, and both sides are told.
  w.api.calls.length = 0;
  await w.env.DB.prepare("UPDATE tg_tickets SET updated = ?1").bind(Math.floor(Date.now() / 1000) - 4 * 86400).run();
  await sweep(w.env, w.kit);
  assert.equal((await w.row("SELECT closed_by FROM tg_tickets WHERE id = 1001")).closed_by, "idle");
  assert.ok(w.lastText(13).text.includes("closed after three quiet days"));
  assert.ok(w.lastText(TEAM).text.includes("after three quiet days"));
});

test("a run of the schedule takes only a few chats, so that each gets its summary in the time there is", async () => {
  const w = await world({ answer: "Problem: update.\nStatus: RESOLVED" });
  for (let i = 0; i < 5; i++) await w.send(text(20 + i, `Question ${i}`));
  await w.env.DB.prepare("UPDATE tg_users SET updated = ?1").bind(Math.floor(Date.now() / 1000) - 2 * 3600).run();
  w.env.sent.length = 0;
  await sweep(w.env, w.kit);
  assert.equal(w.env.sent.length, 3);
  assert.equal((await w.row("SELECT COUNT(*) AS n FROM tg_users")).n, 2);
  await sweep(w.env, w.kit);
  assert.equal(w.env.sent.length, 5);
  assert.equal((await w.row("SELECT COUNT(*) AS n FROM tg_users")).n, 0);
});

test("old message ids and counts are dropped by the schedule, recent ones are not", async () => {
  const w = await world();
  const t = Math.floor(Date.now() / 1000);
  await w.env.DB.prepare("INSERT INTO tg_relay (team_chat, message_id, ticket_id, at) VALUES (?1, 1, 7, ?2), (?1, 2, 8, ?3)").bind(TEAM, t - 20 * 86400, t - 86400).run();
  await w.env.DB.prepare("INSERT INTO tg_stats (day, key, n) VALUES ('2020-01-01', 'chats', 5)").run();
  await sweep(w.env, w.kit);
  assert.deepEqual((await w.rows("SELECT message_id FROM tg_relay")).map((r) => r.message_id), [2]);
  assert.equal(await w.row("SELECT * FROM tg_stats WHERE day = '2020-01-01'"), null);
});

test("a chat that wrote while it was being ended is not lost", async () => {
  const w = await world({ answer: "Problem: update.\nStatus: NEEDS FOLLOW-UP" });
  await w.send(text(SARA, "How do I update?"));
  const stale = await w.row("SELECT * FROM tg_users WHERE chat_id = ?1", SARA);
  await w.send(text(SARA, "One more question"));
  await w.env.DB.prepare("UPDATE tg_users SET updated = ?1 WHERE chat_id = ?2").bind(stale.updated - 5000, SARA).run();
  const before = (await w.row("SELECT COUNT(*) AS n FROM tg_messages")).n;
  assert.equal(before, 4);
  await w.send(text(SARA, "Third question"));
  await sweep(w.env, w.kit);
  assert.equal((await w.row("SELECT COUNT(*) AS n FROM tg_messages")).n, 6, "a chat that is talking is not quiet");
});

// ---------- The team's chat ----------

test("only the right code pairs the team's chat, five tries an hour; the team gets its own menu of commands", async () => {
  const w = await world({ team: false });
  const code = await w.setting("telegram_pair_code");
  await w.send(text(900, "/pair WRONGCODE"));
  assert.ok(w.lastText(900).text.startsWith("That code is not right"));
  assert.equal(await w.setting("telegram_owner_chat"), null);
  await w.send(text(900, `/pair ${code.slice(0, 4).toLowerCase()}-${code.slice(4)}`));
  assert.ok(w.lastText(900).text.startsWith("✅ <b>Paired.</b>"));
  assert.equal(await w.setting("telegram_owner_chat"), "900");
  assert.equal(await w.setting("telegram_pair_code"), null, "a code works once");
  const team = w.api.of("setMyCommands").pop();
  assert.deepEqual(team.scope, { type: "chat", chat_id: 900 });
  assert.ok(team.commands.some((c) => c.command === "tickets"));
  // Guessing is limited.
  const g = await world({ team: false });
  for (let i = 0; i < 7; i++) await g.send(text(901, `/pair GUESS${i}XX`));
  assert.equal(g.api.of("sendMessage").filter((c) => c.chat_id === 901).length, 5);
});

test("the team lists tickets, reads the day's numbers, shows a ticket's chat and blocks a person, who is then forgotten and not answered", async () => {
  const w = await world();
  await w.send(text(SARA, "How do I update?"));
  await w.send(text(SARA, "/human"));
  await w.send(text(SARA, "It says <no server>"));
  w.api.calls.length = 0;
  await w.send(text(TEAM, "/tickets"));
  const list = w.lastText(TEAM).text;
  assert.ok(list.includes("Open tickets (1)") && list.includes("#1001") && list.includes("Sara (@sara_tg)") && list.includes("It says &lt;no server&gt;"), list);
  await w.send(text(TEAM, "/stats"));
  assert.match(w.lastText(TEAM).text, /Chats: 1 - answers: 1[\s\S]*Tickets: 1 opened - 0 closed - 1 open now[\s\S]*@AlbatrossSupportBot/);
  await w.send(press(TEAM, "o:f:1001", 70));
  assert.ok(w.lastText(TEAM).text.includes("Ticket #1001") && w.lastText(TEAM).text.includes("How do I update?"));
  // Buttons are the team's: a person cannot press them.
  await w.send({ update_id: ++uid, callback_query: { id: "x", from: { id: SARA }, data: "o:c:1001", message: { message_id: 1, chat: { id: SARA, type: "private" } } } });
  assert.equal(w.api.of("answerCallbackQuery").pop().text, "Not for you.");
  assert.equal((await w.row("SELECT status FROM tg_tickets WHERE id = 1001")).status, "open");
  await w.send(press(TEAM, "o:b:" + SARA, 70));
  const blocked = await w.row("SELECT name, username, blocked, mode FROM tg_users WHERE chat_id = ?1", SARA);
  assert.deepEqual({ ...blocked }, { name: "", username: "", blocked: 1, mode: "" }, "a blocked person keeps only the id that the block is");
  assert.equal((await w.row("SELECT status FROM tg_tickets WHERE id = 1001")).status, "closed", "blocking closes their ticket");
  assert.equal((await w.row("SELECT COUNT(*) AS n FROM tg_messages")).n, 0);
  // The closed ticket's full chat is gone by design.
  await w.send(press(TEAM, "o:f:1001", 70));
  assert.ok(w.lastText(TEAM).text.includes("is closed, and its chat was deleted"));
  w.env.asked.length = 0;
  w.api.calls.length = 0;
  await w.send(text(SARA, "please answer"));
  await w.send(text(SARA, "/start"));
  assert.equal(w.api.of("sendMessage").length, 0);
  assert.equal(w.env.asked.length, 0);
  assert.equal((await w.row("SELECT name FROM tg_users WHERE chat_id = ?1", SARA)).name, "", "writing again does not bring the name back");
  await w.send(text(TEAM, `/unblock ${SARA}`));
  await w.send(text(SARA, "/start"));
  assert.ok(w.lastText(SARA).text.includes("Albatross Support"));
  await w.send(text(TEAM, "/block nobody"));
  assert.ok(w.lastText(TEAM).text.startsWith("Say whom"));
  await w.send(text(TEAM, "/close 4242"));
  assert.ok(w.lastText(TEAM).text.includes("is not open"));
});

test("the team can block by replying to a ticket's message", async () => {
  const w = await world();
  await w.send(text(SARA, "/human"));
  const noteId = (await w.row("SELECT message_id FROM tg_relay")).message_id;
  await w.send(text(TEAM, "/block", { reply_to_message: { message_id: noteId } }));
  assert.equal((await w.row("SELECT blocked FROM tg_users WHERE chat_id = ?1", SARA)).blocked, 1);
});

test("the owner can try the bot from the team's own private chat: what is not for the team goes on as a person's", async () => {
  const w = await world();
  await w.send(text(TEAM, "/start"));
  assert.ok(w.lastText(TEAM).text.includes("Albatross Support"));
  await w.send(text(TEAM, "How do I install?"));
  assert.equal(w.env.asked.length, 1);
  await w.send(text(TEAM, "/help"));
  assert.ok(w.lastText(TEAM).text.includes("Team commands"));
});

// ---------- Disconnecting ----------

test("disconnecting removes the webhook, the token and every chat", async () => {
  const w = await world();
  await w.send(text(SARA, "How do I update?"));
  await w.send(text(SARA, "/human"));
  w.api.calls.length = 0;
  await disconnect(w.env);
  assert.equal(w.api.of("deleteWebhook").length, 1);
  for (const key of ["telegram_bot_token", "telegram_bot_username", "telegram_owner_chat", "telegram_webhook_url", "telegram_pair_code"]) assert.equal(await w.setting(key), null, key);
  for (const t of ["tg_users", "tg_messages", "tg_tickets", "tg_relay"]) assert.equal((await w.row(`SELECT COUNT(*) AS n FROM ${t}`)).n, 0, t);
  assert.equal((await w.send(text(SARA, "hi"))).status, 404);
});

test("nothing the bot says is Persian", async () => {
  const w = await world();
  await w.send(text(SARA, "/start"));
  for (const d of ["c", "c:win", "c:and", "c:ios", "c:mac", "no:win", "i", "i:win", "i:and", "i:ios", "i:mac", "p", "a", "d", "menu", "mn", "cn", "ok:win"]) await w.send(press(SARA, d));
  await w.send(text(SARA, "/human"));
  await w.send(text(SARA, "/help"));
  await w.send(press(SARA, "tc:1001"));
  await w.send(press(SARA, "r:5"));
  const said = JSON.stringify(w.api.calls);
  assert.ok(!/[؀-ۿ]/.test(said));
});
