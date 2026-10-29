// The Telegram support bot (src/telegram.js) over a real SQL database (d1.mjs)
// with Telegram, the model and the mail stood in for: what a person sees, what
// the team sees, what is kept and what is forgotten.
import assert from "node:assert/strict";
import test, { afterEach } from "node:test";
import { d1 } from "./d1.mjs";
import { connect, disconnect, repair, sweep, webhook } from "../src/telegram.js";

const TOKEN = "123456789:" + "A".repeat(35);
const SECRET = "s".repeat(40);
const TEAM = 777;
const SARA = 4242;
const real = globalThis.fetch;
afterEach(() => (globalThis.fetch = real));

const b64u = (bytes) => Buffer.from(bytes).toString("base64url");
async function secretOf(tokenSecret) {
  const key = await crypto.subtle.importKey("raw", new TextEncoder().encode(tokenSecret), { name: "HMAC", hash: "SHA-256" }, false, ["sign"]);
  return b64u(new Uint8Array(await crypto.subtle.sign("HMAC", key, new TextEncoder().encode("albatross telegram webhook"))));
}

/** The Bot API: every call is kept and answered the way Telegram does, unless a test says otherwise. */
function telegram(overrides = {}) {
  const calls = [];
  let next = 500;
  globalThis.fetch = async (url, init) => {
    const method = String(url).split("/").pop();
    const payload = JSON.parse(init.body);
    calls.push({ method, ...payload });
    const o = overrides[method];
    const given = typeof o === "function" ? o(payload) : o;
    const body = given ?? (method === "getMe" ? { ok: true, result: { username: "AlbatrossSupportBot" } } : method === "sendMessage" || method === "copyMessage" ? { ok: true, result: { message_id: next++ } } : { ok: true, result: true });
    return new Response(JSON.stringify(body), { status: body.ok ? 200 : body.error_code || 400 });
  };
  return { calls, of: (method) => calls.filter((c) => c.method === method) };
}

const labels = (markup) => (markup ? markup.inline_keyboard.flat().map((b) => b.text) : []);
const datas = (markup) => (markup ? markup.inline_keyboard.flat().map((b) => b.callback_data || b.url) : []);

let uid = 1000;
let mid = 10;
const text = (chat, words, extra = {}) => ({ update_id: ++uid, message: { message_id: ++mid, from: { id: chat, first_name: "Sara", username: "sara_tg" }, chat: { id: chat, type: "private" }, text: words, ...extra } });
const media = (chat, extra) => ({ update_id: ++uid, message: { message_id: ++mid, from: { id: chat, first_name: "Sara", username: "sara_tg" }, chat: { id: chat, type: "private" }, ...extra } });
const press = (chat, data, message_id = 55) => ({ update_id: ++uid, callback_query: { id: `cb${uid}`, from: { id: chat, first_name: "Sara", username: "sara_tg" }, data, message: { message_id, chat: { id: chat, type: "private" } } } });

/** A service with a connected bot, a paired team chat (unless `team` is false), a model that answers `answer`, and a mailbox. */
async function world({ answer = "Update the app first.", team = true } = {}) {
  const api = telegram();
  const env = { DB: d1(), TOKEN_SECRET: SECRET, CONTACT_EMAIL: "support@albatrossvpn.online", sent: [], asked: [], answer, aiDown: false };
  env.AI = { run: async (model, input) => { env.asked.push(input); if (env.aiDown) throw new Error("the model is away"); return { response: env.answer }; } };
  env.EMAIL = { send: async (m) => void env.sent.push(m) };
  await env.DB.prepare("INSERT INTO settings (key, value) VALUES ('support_notify_to', 'owner@example.com')").run();
  const linked = await connect(env, new Request("https://accounts.albatrossvpn.online/api/v1/admin/settings"), TOKEN);
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
  };
  if (team) {
    const code = (await w.row("SELECT value FROM settings WHERE key = 'telegram_pair_code'")).value;
    await w.send(text(TEAM, `/pair ${code}`));
    assert.equal((await w.row("SELECT value FROM settings WHERE key = 'telegram_owner_chat'")).value, String(TEAM));
  }
  w.boot = api.calls.slice();
  api.calls.length = 0;
  return w;
}

// ---------- Connecting ----------

test("a token that is not a token, or that Telegram refuses, connects nothing and says why", async () => {
  const env = { DB: d1(), TOKEN_SECRET: SECRET };
  const req = new Request("https://accounts.albatrossvpn.online/api/v1/admin/settings");
  assert.match((await connect(env, req, "hello")).error, /does not look like a bot token/);
  const api = telegram({ getMe: { ok: false, error_code: 401, description: "Unauthorized" } });
  assert.match((await connect(env, req, TOKEN)).error, /did not accept this token: Unauthorized/);
  assert.equal(api.of("setWebhook").length, 0);
  telegram({ setWebhook: { ok: false, error_code: 400, description: "Bad webhook: HTTPS URL must be provided" } });
  assert.match((await connect(env, req, TOKEN)).error, /could not set the webhook: Bad webhook/);
  assert.equal(await env.DB.prepare("SELECT value FROM settings WHERE key = 'telegram_bot_token'").first(), null, "a bot that was not connected is not stored");
});

test("connecting sets the webhook with the derived secret, the menu and the texts, and keeps a pairing code until a chat is paired", async () => {
  const w = await world({ team: false });
  const env = w.env;
  const of = (method) => w.boot.filter((c) => c.method === method);
  const hook = of("setWebhook")[0];
  assert.equal(hook.url, "https://accounts.albatrossvpn.online/api/v1/telegram");
  assert.equal(hook.secret_token, await secretOf(SECRET));
  assert.match(hook.secret_token, /^[A-Za-z0-9_-]{1,256}$/);
  assert.deepEqual(hook.allowed_updates, ["message", "callback_query"]);
  assert.deepEqual(of("setMyCommands")[0].commands.map((c) => c.command), ["start", "menu", "human", "close", "help"]);
  assert.ok(of("setMyDescription")[0].description.includes("Albatross"));
  assert.equal((await w.row("SELECT value FROM settings WHERE key = 'telegram_bot_username'")).value, "AlbatrossSupportBot");
  const code = (await w.row("SELECT value FROM settings WHERE key = 'telegram_pair_code'")).value;
  assert.match(code, /^[A-HJ-NP-Z2-9]{8}$/);
  assert.notEqual(await repair(env), code, "a new code on request");
});

test("the webhook answers only Telegram: a secret, a connected bot, a real update", async () => {
  const w = await world();
  assert.equal((await w.send(text(SARA, "hi"), { "X-Telegram-Bot-Api-Secret-Token": "wrong" })).status, 401);
  assert.equal((await w.send(text(SARA, "hi"), { "X-Telegram-Bot-Api-Secret-Token": "" })).status, 401);
  assert.equal(w.api.calls.length, 0);
  assert.equal((await w.send({ nothing: true })).status, 400);
  assert.equal((await w.send(text(SARA, "/start"))).status, 200);
  const bare = { DB: d1(), TOKEN_SECRET: SECRET };
  const res = await webhook(new Request("https://x/api/v1/telegram", { method: "POST", body: "{}" }), bare, w.kit, undefined);
  assert.equal(res.status, 404, "no bot, nothing to answer");
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
  assert.ok(phone.text.includes("connection app") && phone.text.includes("Add the servers again"));
  assert.ok(datas(phone.reply_markup).includes("https://accounts.albatrossvpn.online/account"));
  await w.send(press(SARA, "ok:win"));
  assert.ok(w.api.of("editMessageText")[3].text.includes("all set"));
  assert.equal((await w.row("SELECT n FROM tg_stats WHERE key = 'fixed'")).n, 1);
  // Still broken: the next words are the problem, and the assistant is told which device.
  await w.send(press(SARA, "no:win"));
  assert.ok(w.api.of("editMessageText")[4].text.includes("Type the exact message"));
  assert.equal((await w.row("SELECT mode, device FROM tg_users WHERE chat_id = ?1", SARA)).mode, "problem");
  await w.send(text(SARA, "It says no working server"));
  assert.ok(w.env.asked[0].messages[0].content.includes("They are on Windows and the standard checklist did not help"));
  assert.equal((await w.row("SELECT mode FROM tg_users WHERE chat_id = ?1", SARA)).mode, "", "the problem was described");
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
  assert.equal(w.env.asked.length, 0);
});

// ---------- A person: the assistant ----------

test("a question is answered by the assistant with the conversation so far, and the answer can be rated", async () => {
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
  assert.deepEqual(labels(w.api.of("editMessageReplyMarkup")[1].reply_markup), ["🧑‍💼 Talk to our team", "🔌 Fix a connection", "⬅ Menu"]);
  assert.equal((await w.row("SELECT n FROM tg_stats WHERE key = 'up'")).n, 1);
  assert.equal((await w.row("SELECT n FROM tg_stats WHERE key = 'down'")).n, 1);
});

test("the answer is made safe for Telegram: the model's markup is text, a long answer is cut to fit, a hand-over brings the team button", async () => {
  const w = await world({ answer: "Use <b>bold</b> & <script>alert(1)</script>\n[[HUMAN]]" });
  await w.send(text(SARA, "I want to pay"));
  const a = w.lastText(SARA);
  assert.equal(a.text, "Use &lt;b&gt;bold&lt;/b&gt; &amp; &lt;script&gt;alert(1)&lt;/script&gt;");
  assert.deepEqual(labels(a.reply_markup), ["🧑‍💼 Talk to our team", "👍 Helpful", "👎 Not helpful"]);
  w.env.answer = "<".repeat(1500);
  await w.send(text(SARA, "again"));
  assert.ok(w.lastText(SARA).text.length <= 4096 && w.lastText(SARA).text.startsWith("&lt;"));
});

test("a model that is away, a picture, and a flood of messages are each met with a plain answer", async () => {
  const w = await world();
  w.env.aiDown = true;
  await w.send(text(SARA, "hello"));
  assert.ok(w.lastText(SARA).text.startsWith("I could not answer just now"));
  assert.deepEqual(labels(w.lastText(SARA).reply_markup), ["🧑‍💼 Talk to our team", "⬅ Menu"]);
  w.env.aiDown = false;
  await w.send(media(SARA, { photo: [{ file_id: "x" }] }));
  assert.ok(w.lastText(SARA).text.startsWith("I can only read text here"));
  // Forty messages an hour per person; the notice comes once.
  for (let i = 0; i < 45; i++) await w.send(text(SARA, `message ${i}`));
  assert.equal(w.api.of("sendMessage").filter((c) => c.text.startsWith("I am getting a lot of messages")).length, 1, "one notice, then quiet");
  assert.equal(w.env.asked.length, 1 + 38, "the failed hello, then what was left of the forty an hour");
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

// ---------- Tickets ----------

test("talking to the team opens a numbered ticket: the person is told, the team's chat gets a card, the owner gets a mail", async () => {
  const w = await world({ answer: "Summary text.\nStatus: NEEDS FOLLOW-UP" });
  await w.send(text(SARA, "I can't connect on Windows"));
  w.api.calls.length = 0;
  await w.send(press(SARA, "h"));
  const mine = w.lastText(SARA);
  assert.ok(mine.text.includes("Ticket #1001 opened") && mine.text.includes("has your chat so far"));
  assert.deepEqual(labels(mine.reply_markup), ["✅ My problem is solved"]);
  const card = w.lastText(TEAM);
  assert.ok(card.text.includes("Ticket #1001") && card.text.includes("Sara (@sara_tg)") && card.text.includes(`<code>${SARA}</code>`) && card.text.includes("I can&#39;t connect") === false);
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
  assert.deepEqual((await w.rows("SELECT role, content FROM tg_messages ORDER BY id")).map((r) => r.content), ["It says <no server>", "[photo] the screen", "[sticker]"]);
});

test("the team answers a ticket by replying to its message; photos and files go too; closing tells the person and forgets the chat", async () => {
  const w = await world();
  await w.send(text(SARA, "/human"));
  await w.send(text(SARA, "No server found"));
  const note = w.lastText(TEAM);
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
  assert.equal((await w.row("SELECT COUNT(*) AS n FROM tg_relay")).n, 0);
  assert.equal((await w.row("SELECT status, closed_by FROM tg_tickets WHERE id = 1001")).closed_by, "team");
  // A reply to a closed ticket says so instead of vanishing.
  await w.send(text(TEAM, "one more thing", { reply_to_message: { message_id: noteId } }));
  assert.equal(w.api.of("sendMessage").filter((c) => c.chat_id === SARA).length, 1, "the person got only the closing note");
  void note;
});

test("a person who blocked the bot is not a silent failure for the team", async () => {
  const w = await world();
  await w.send(text(SARA, "/human"));
  await w.send(text(SARA, "hello team"));
  const noteId = (await w.rows("SELECT message_id FROM tg_relay ORDER BY message_id DESC"))[0].message_id;
  telegram({ sendMessage: (p) => (p.chat_id === SARA ? { ok: false, error_code: 403, description: "Forbidden: bot was blocked by the user" } : { ok: true, result: { message_id: 900 } }) });
  const api = telegram({ sendMessage: (p) => (p.chat_id === SARA ? { ok: false, error_code: 403, description: "Forbidden: bot was blocked by the user" } : { ok: true, result: { message_id: 900 } }) });
  await w.send(text(TEAM, "are you there?", { reply_to_message: { message_id: noteId } }));
  const warning = api.of("sendMessage").filter((c) => c.chat_id === TEAM).pop();
  assert.ok(warning.text.startsWith("⚠️ Could not deliver: Forbidden: bot was blocked by the user"));
});

test("the person can close their own ticket, with the button or /close, and the team is told", async () => {
  const w = await world();
  await w.send(text(SARA, "/human"));
  w.api.calls.length = 0;
  await w.send(press(SARA, "tc"));
  assert.ok(w.lastText(SARA).text.includes("Glad it is sorted"));
  assert.ok(w.lastText(TEAM).text.includes("Ticket #1001") && w.lastText(TEAM).text.includes("closed by Sara (@sara_tg)"));
  await w.send(text(SARA, "/human"));
  assert.ok(w.lastText(SARA).text.includes("Ticket #1002 opened"), "numbers go on");
  await w.send(text(SARA, "/close"));
  assert.equal((await w.row("SELECT closed_by FROM tg_tickets WHERE id = 1002")).closed_by, "user");
  // Tickets are limited per person.
  for (let i = 0; i < 5; i++) {
    await w.send(text(SARA, "/human"));
    await w.send(text(SARA, "/close"));
  }
  assert.ok(w.api.of("sendMessage").some((c) => c.text.startsWith("You have opened several tickets today")));
  assert.equal((await w.row("SELECT COUNT(*) AS n FROM tg_tickets")).n, 5, "five a day");
});

test("without a paired team chat a ticket still reaches the owner by mail, and says nothing it cannot keep", async () => {
  const w = await world({ team: false });
  await w.send(text(SARA, "/human"));
  await w.send(text(SARA, "No server found"));
  assert.equal(w.api.of("sendMessage").filter((c) => c.chat_id === TEAM).length, 0);
  assert.equal(w.env.sent.length, 1);
  assert.ok(w.env.sent[0].subject.includes("ticket #1001"));
});

// ---------- A chat ends ----------

test("/end sends the owner a summary of the chat, forgets it and thanks the person; an empty chat has nothing to end", async () => {
  const w = await world({ answer: "Problem: update.\nStatus: RESOLVED" });
  await w.send(text(SARA, "/end"));
  assert.ok(w.lastText(SARA).text.includes("nothing to close"));
  await w.send(text(SARA, "How do I update?"));
  await w.send(text(SARA, "/end"));
  assert.ok(w.lastText(SARA).text.startsWith("Thank you!"));
  assert.equal(w.env.sent.length, 1);
  assert.ok(w.env.sent[0].subject.startsWith("[Albatross Telegram] resolved: How do I update?"));
  assert.equal((await w.row("SELECT COUNT(*) AS n FROM tg_messages")).n, 0);
  assert.equal(await w.row("SELECT * FROM tg_users WHERE chat_id = ?1", SARA), null, "nobody is kept who is not talking");
});

test("every ten minutes the chats that went quiet are summarized and forgotten, and tickets nobody writes to are closed", async () => {
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
  assert.equal((await w.row("SELECT closed_by FROM tg_tickets WHERE chat_id = 13")).closed_by, "idle");
  assert.ok(w.lastText(13).text.includes("closed after three quiet days"));
  assert.ok(w.lastText(TEAM).text.includes("after three quiet days"));
});

test("a chat that wrote while it was being ended is not lost", async () => {
  const w = await world({ answer: "Problem: update.\nStatus: NEEDS FOLLOW-UP" });
  await w.send(text(SARA, "How do I update?"));
  const stale = await w.row("SELECT * FROM tg_users WHERE chat_id = ?1", SARA);
  await w.send(text(SARA, "One more question"));
  // The sweep read the person before the second message: it must not delete what came since.
  const c = { env: w.env, kit: w.kit };
  void c;
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
  const code = (await w.row("SELECT value FROM settings WHERE key = 'telegram_pair_code'")).value;
  await w.send(text(900, "/pair WRONGCODE"));
  assert.ok(w.lastText(900).text.startsWith("That code is not right"));
  assert.equal(await w.row("SELECT value FROM settings WHERE key = 'telegram_owner_chat'"), null);
  await w.send(text(900, `/pair ${code.slice(0, 4).toLowerCase()}-${code.slice(4)}`));
  assert.ok(w.lastText(900).text.startsWith("✅ <b>Paired.</b>"));
  assert.equal((await w.row("SELECT value FROM settings WHERE key = 'telegram_owner_chat'")).value, "900");
  assert.equal(await w.row("SELECT value FROM settings WHERE key = 'telegram_pair_code'"), null, "a code works once");
  const team = w.api.of("setMyCommands").pop();
  assert.deepEqual(team.scope, { type: "chat", chat_id: 900 });
  assert.ok(team.commands.some((c) => c.command === "tickets"));
  // Guessing is limited.
  const g = await world({ team: false });
  for (let i = 0; i < 7; i++) await g.send(text(901, `/pair GUESS${i}XX`));
  assert.equal(g.api.of("sendMessage").filter((c) => c.chat_id === 901).length, 5);
});

test("the team lists tickets, reads the day's numbers, shows a ticket's chat and blocks a person, who is then not answered", async () => {
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
  assert.equal((await w.row("SELECT blocked FROM tg_users WHERE chat_id = ?1", SARA)).blocked, 1);
  assert.equal((await w.row("SELECT status FROM tg_tickets WHERE id = 1001")).status, "closed", "blocking closes their ticket");
  w.env.asked.length = 0;
  w.api.calls.length = 0;
  await w.send(text(SARA, "please answer"));
  await w.send(text(SARA, "/start"));
  assert.equal(w.api.of("sendMessage").length, 0);
  assert.equal(w.env.asked.length, 0);
  await w.send(text(TEAM, `/unblock ${SARA}`));
  await w.send(text(SARA, "/start"));
  assert.ok(w.lastText(SARA).text.includes("Albatross Support"));
  await w.send(text(TEAM, "/block nobody"));
  assert.ok(w.lastText(TEAM).text.startsWith("Say whom"));
  await w.send(text(TEAM, "/close 4242"));
  assert.ok(w.lastText(TEAM).text.includes("is not open"));
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
  assert.equal(await w.row("SELECT value FROM settings WHERE key = 'telegram_bot_token'"), null);
  for (const t of ["tg_users", "tg_messages", "tg_tickets", "tg_relay"]) assert.equal((await w.row(`SELECT COUNT(*) AS n FROM ${t}`)).n, 0, t);
  assert.equal((await w.send(text(SARA, "hi"))).status, 404);
});

test("nothing the bot says is Persian", async () => {
  const w = await world();
  await w.send(text(SARA, "/start"));
  for (const d of ["c", "c:win", "c:and", "c:ios", "c:mac", "no:win", "i", "i:win", "i:and", "i:ios", "i:mac", "p", "a", "d", "menu", "ok:win"]) await w.send(press(SARA, d));
  await w.send(text(SARA, "/human"));
  await w.send(text(SARA, "/help"));
  const said = JSON.stringify(w.api.calls);
  assert.ok(!/[؀-ۿ]/.test(said));
});
