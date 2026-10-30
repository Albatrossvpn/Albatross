// The Telegram support bot (src/telegram.js) over a real SQL database (d1.mjs)
// with Telegram, the model and the mail stood in for: what a person sees, what
// the team sees, what is kept and what is forgotten.
import assert from "node:assert/strict";
import test, { afterEach } from "node:test";
import { d1 } from "./d1.mjs";
import { PACKS } from "../src/i18n/index.js";
import { check, connect, disconnect, heal, repair, sweep, webhook } from "../src/telegram.js";
import { ADMIN_REQUEST, HOOK_URL, SARA, SECRET, TEAM, TOKEN, datas, labels, media, nextMessage, nextUpdate, press, real, secretOf, telegram, text, toTeamWith, world } from "./tg-helpers.mjs";

afterEach(() => (globalThis.fetch = real));

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
  assert.deepEqual(hook.allowed_updates, ["message", "callback_query", "pre_checkout_query"], "payments are asked about too");
  assert.equal(hook.max_connections, 1, "updates arrive one after the other");
  assert.deepEqual(of("setMyCommands")[0].commands.map((c) => c.command), ["start", "menu", "human", "close", "password", "language", "help"]);
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
  await toTeamWith(w, SARA, "It does not connect");
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
  w.api.overrides.getWebhookInfo = { ok: true, result: { url: "" } };
  w.api.calls.length = 0;
  await heal(w.env);
  assert.equal(w.api.of("setWebhook").length, 1);
  assert.equal(w.api.of("setWebhook")[0].url, url);
  assert.equal(w.api.of("setWebhook")[0].drop_pending_updates, undefined, "waiting updates are kept");
  w.api.overrides.getWebhookInfo = { ok: true, result: { url, pending_update_count: 3 } };
  w.api.calls.length = 0;
  await heal(w.env);
  assert.equal(w.api.of("setWebhook").length, 0);
  assert.equal(await check(w.env), "@AlbatrossSupportBot is reachable; its webhook is set and 3 update(s) are waiting. No errors. Its menu and texts are set.");
  w.api.overrides.getWebhookInfo = { ok: true, result: { url, last_error_date: 1760000000, last_error_message: "Wrong response from the webhook: 500 Internal Server Error" } };
  assert.match(await check(w.env), /Last error: Wrong response from the webhook: 500/);
  // Texts typed in BotFather over the service's, and Telegram refusing to put them right: told, once, for all the languages.
  w.api.overrides.getWebhookInfo = { ok: true, result: { url } };
  w.api.overrides.setMyDescription = { ok: false, error_code: 400, description: "Bad Request: nope" };
  w.api.shown.description = "typed in BotFather";
  w.api.shown.local.clear();
  assert.match(await check(w.env), /Telegram refused its description \(default, \w\w, \w\w and \d+ more\): Bad Request: nope\.$/, "one reason for every language is one phrase");
  w.api.overrides.getWebhookInfo = { ok: true, result: { url: "" } };
  assert.match(await check(w.env), /webhook is not set/);
  await disconnect(w.env);
  assert.equal(await check(w.env), "The bot is not connected.");
});

test("the bot's menu and texts are set when connecting, and what Telegram refuses is told instead of hidden", async () => {
  const env = { DB: d1(), TOKEN_SECRET: SECRET };
  const api = telegram({ setMyDescription: { ok: false, error_code: 400, description: "Bad Request: DESCRIPTION_TOO_LONG" } });
  const linked = await connect(env, ADMIN_REQUEST(), TOKEN);
  assert.equal(linked.username, "AlbatrossSupportBot");
  assert.match(linked.warning, /did not take the bot's description \(default, \w\w, \w\w and \d+ more\): Bad Request: DESCRIPTION_TOO_LONG\. "Check the bot" tries again\.$/);
  assert.equal(api.of("setMyShortDescription").length, PACKS.length, "what Telegram does take is set all the same, for every language");
  assert.deepEqual(api.shown.menus.get('"all"').map((c) => c.command), ["start", "menu", "human", "close", "password", "language", "help"]);
  assert.equal(JSON.parse((await env.DB.prepare("SELECT value FROM settings WHERE key = 'telegram_texts'").first()).value).ok, false);
  // All taken: no warning, and saving the token again writes nothing that is already set.
  const good = telegram();
  const kept = { DB: d1(), TOKEN_SECRET: SECRET };
  const again = await connect(kept, ADMIN_REQUEST(), TOKEN);
  assert.equal(again.warning, undefined);
  assert.ok(good.shown.description.startsWith("Albatross VPN support.") && good.shown.about === "Albatross VPN support and data. albatrossvpn.online" && good.shown.about.length <= 120 && good.shown.description.length <= 512 && good.shown.description.endsWith("albatrossvpn.online"));
  const writes = good.calls.filter((c) => c.method.startsWith("setMy")).length;
  await connect(kept, ADMIN_REQUEST(), TOKEN);
  assert.equal(good.calls.filter((c) => c.method.startsWith("setMy")).length, writes, "a bot whose texts are set is not written to again");
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
  assert.equal(lost.of("setMyDescription").length, PACKS.length, "one for each language");
  assert.equal(lost.of("setMyShortDescription").length, PACKS.length);
  assert.deepEqual(lost.of("setMyCommands").filter((c) => c.scope).map((c) => c.scope), [{ type: "chat", chat_id: TEAM }], "the team's own menu");
  assert.equal(lost.of("setMyCommands").filter((c) => !c.scope).length, PACKS.length, "and a menu for each language");
  await sweep(w.env, w.kit);
  assert.equal(lost.of("setMyDescription").length, PACKS.length, "set: not looked at again");
  // Telegram refuses: one try, then ten minutes of quiet, then another.
  await w.env.DB.prepare("UPDATE settings SET value = '{}' WHERE key = 'telegram_texts'").run();
  const refusing = telegram({ setMyShortDescription: { ok: false, error_code: 429, description: "Too Many Requests: retry after 600" } });
  await sweep(w.env, w.kit);
  await sweep(w.env, w.kit);
  assert.equal(refusing.of("setMyShortDescription").length, PACKS.length, "a refused try waits");
  const record = JSON.parse(await w.setting("telegram_texts"));
  assert.equal(record.ok, false);
  assert.match(record.note, /about line \(default, .*\): Too Many Requests/);
  record.at -= 11 * 60;
  await w.env.DB.prepare("UPDATE settings SET value = ?1 WHERE key = 'telegram_texts'").bind(JSON.stringify(record)).run();
  await sweep(w.env, w.kit);
  assert.equal(refusing.of("setMyShortDescription").length, 2 * PACKS.length);
});

test("every hour the menu and texts are looked at, and written only where Telegram shows something else", async () => {
  const w = await world();
  const bare = telegram({ getWebhookInfo: { ok: true, result: { url: HOOK_URL } } });
  await heal(w.env);
  assert.equal(bare.of("setMyDescription").length, 1, "the hourly look is at the English texts, which are what most people see");
  assert.ok(bare.of("setMyCommands").some((c) => c.scope && c.scope.chat_id === TEAM), "the team's menu too");
  await heal(w.env);
  assert.equal(bare.of("setMyDescription").length, 1, "right now: left alone");
  assert.equal(bare.of("setMyCommands").length, 2);
  assert.ok(bare.calls.length < 20, "and an hour's look is a handful of calls, not a call for each language");
  bare.shown.description = "something somebody typed in BotFather";
  await heal(w.env);
  assert.equal(bare.of("setMyDescription").length, 2, "a text that was changed behind the service's back is put right");
});

// ---------- A person: the menu and the guides ----------

test("/start shows the main menu with its six topics and the language button, and a link from the website opens the right guide", async () => {
  const w = await world();
  await w.send(text(SARA, "/start"));
  const menu = w.lastText(SARA);
  assert.ok(menu.text.includes("Albatross Support") && menu.text.includes("Hi Sara!"));
  assert.equal(menu.parse_mode, "HTML");
  assert.deepEqual(labels(menu.reply_markup), ["🔌 I can't connect", "📲 Install the app", "💎 Plans & upgrading", "👤 Account & data", "⬇️ Downloads", "🧑‍💼 Talk to our team", "🌐 Language"]);
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
  assert.ok(w.api.of("editMessageText")[3].text.includes("Never send your password here"));
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
  await w.send({ update_id: nextUpdate(), message: { message_id: 1, from: { id: 9, is_bot: true, first_name: "Bot" }, chat: { id: 9, type: "private" }, text: "hi" } });
  await w.send({ update_id: nextUpdate(), message: { message_id: 2, from: { id: SARA, first_name: "Sara" }, chat: { id: -100123, type: "supergroup" }, text: "hi all" } });
  await w.send({ update_id: nextUpdate(), channel_post: { message_id: 3, chat: { id: -100999, type: "channel" }, text: "news" } });
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
  // Before a word the button opens nothing: it asks what is wrong, and the assistant answers that first.
  await w.send(press(SARA, "h"));
  assert.ok(w.lastText(SARA).text.includes("Tell me what is wrong"));
  assert.equal((await w.row("SELECT COUNT(*) AS n FROM tg_tickets")).n, 0);
  await w.send(text(SARA, "It does not connect"));
  assert.deepEqual(labels(w.lastText(SARA).reply_markup), ["🧑‍💼 Talk to our team", "👍 Helpful", "👎 Not helpful"], "the way to the team stays under the answer");
  assert.equal((await w.row("SELECT COUNT(*) AS n FROM tg_tickets")).n, 0);
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
  assert.ok(w.lastText(SARA).text.includes("Tell me what is wrong"));
  assert.equal((await w.row("SELECT COUNT(*) AS n FROM tg_tickets")).n, 0, "no ticket before a word");
  w.api.calls.length = 0;
  w.env.answer = "Our team will answer you here.\n[[HUMAN]]";
  await w.send(text(SARA, "It says <no server>"));
  // The assistant could not help: the ticket opens with the person's words on its card.
  assert.ok(w.api.of("sendMessage").some((c) => c.chat_id === SARA && c.text === "Our team will answer you here."));
  assert.ok(w.lastText(SARA).text.includes("Ticket #1001 opened"));
  const card = w.lastText(TEAM);
  assert.ok(card.text.startsWith("🎫 <b>Ticket #1001</b> - new") && card.text.includes("<b>Visitor:</b> It says &lt;no server&gt;"), card.text);
  assert.deepEqual(datas(card.reply_markup), ["o:c:1001", "o:b:4242", "o:f:1001"]);
  // Later words come on their own, and the person sees they arrived.
  w.api.calls.length = 0;
  const asked = w.env.asked.length;
  const later = text(SARA, "Still <nothing>");
  await w.send(later);
  assert.equal(w.lastText(TEAM).text, "🎫 <b>#1001</b> - Sara (@sara_tg)\nStill &lt;nothing&gt;");
  assert.equal(w.api.of("setMessageReaction")[0].message_id, later.message.message_id);
  assert.equal(w.api.of("setMessageReaction")[0].reaction[0].emoji, "👀");
  // While the ticket is open the model answers nothing; the owner got one mail.
  assert.equal(w.env.sent.length, 1);
  assert.equal(w.env.asked.length, asked);
  assert.equal(w.api.of("sendMessage").filter((c) => c.chat_id === SARA).length, 0);
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
  assert.deepEqual((await w.rows("SELECT content FROM tg_messages WHERE role = 'user' ORDER BY id")).map((r) => r.content), ["It says <no server>", "Still <nothing>", "[photo] the screen", "[sticker]", "[GIF]", "[place]"]);
  assert.equal(w.env.asked.length, asked, "the model stays out of an open ticket");
});

test("the team answers a ticket by replying to its message; photos and files go too; closing tells the person and forgets the chat", async () => {
  const w = await world();
  await toTeamWith(w, SARA, "No server found");
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
  assert.equal((await w.row("SELECT COUNT(*) AS n FROM tg_relay")).n, 1);
  w.api.calls.length = 0;
  await w.send(text(TEAM, "one more thing", { reply_to_message: { message_id: noteId } }));
  assert.ok(w.lastText(TEAM).text.startsWith("Ticket #1001 is closed."));
  assert.equal(w.api.of("sendMessage").filter((c) => c.chat_id === SARA).length, 0, "the person is not written to");
});

test("a person who blocked the bot is not a silent failure for the team", async () => {
  const w = await world();
  await toTeamWith(w, SARA, "hello team");
  const noteId = (await w.rows("SELECT message_id FROM tg_relay ORDER BY message_id DESC"))[0].message_id;
  const api = telegram({ sendMessage: (p) => (p.chat_id === SARA ? { ok: false, error_code: 403, description: "Forbidden: bot was blocked by the user" } : { ok: true, result: { message_id: 900 } }) });
  await w.send(text(TEAM, "are you there?", { reply_to_message: { message_id: noteId } }));
  const warning = api.of("sendMessage").filter((c) => c.chat_id === TEAM).pop();
  assert.ok(warning.text.startsWith("⚠️ Could not deliver: Forbidden: bot was blocked by the user"));
});

test("the person can close their own ticket, with the button or /close, rate the help, and the team is told", async () => {
  const w = await world();
  await toTeamWith(w, SARA, "It does not connect");
  w.api.calls.length = 0;
  await w.send(press(SARA, "tc:1001"));
  assert.ok(w.lastText(SARA).text.includes("Glad it is sorted") && w.lastText(SARA).text.includes("How was our help?"));
  assert.deepEqual(labels(w.lastText(SARA).reply_markup), ["1⭐", "2⭐", "3⭐", "4⭐", "5⭐", "📋 Menu"]);
  assert.ok(w.lastText(TEAM).text.includes("Ticket #1001</b> (Sara (@sara_tg)) was closed by the person"), w.lastText(TEAM).text);
  await w.send(press(SARA, "r:4", 80));
  assert.ok(w.api.of("editMessageText").pop().text.includes("rated our help 4 out of 5"));
  await w.send(press(SARA, "r:9", 80));
  await w.send(text(TEAM, "/stats"));
  assert.ok(w.lastText(TEAM).text.includes("Rating of the help: 4.0 out of 5 (1)"), w.lastText(TEAM).text);
  await toTeamWith(w, SARA, "Another problem");
  assert.ok(w.lastText(SARA).text.includes("Ticket #1002 opened"), "numbers go on");
  await w.send(text(SARA, "/close"));
  assert.equal((await w.row("SELECT closed_by FROM tg_tickets WHERE id = 1002")).closed_by, "user");
  // Tickets are limited per person.
  for (let i = 0; i < 5; i++) {
    await toTeamWith(w, SARA, `Problem ${i}`);
    await w.send(text(SARA, "/close"));
  }
  assert.ok(w.api.of("sendMessage").some((c) => c.text.startsWith("You have opened several tickets today, so I cannot open another one right now")));
  assert.equal((await w.row("SELECT COUNT(*) AS n FROM tg_tickets")).n, 5, "five a day");
});

test("a button of an old ticket never closes a newer one, nor somebody else's", async () => {
  const w = await world();
  await toTeamWith(w, SARA, "Help");
  await w.send(press(SARA, "tc:1001"));
  await toTeamWith(w, SARA, "Help again");
  await toTeamWith(w, 11, "Help me too");
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
  await toTeamWith(w, SARA, "Help");
  // The ticket was closed behind the chat's back (as if the second statement of closing never ran).
  await w.env.DB.prepare("UPDATE tg_tickets SET status = 'closed', chat_id = 0 WHERE id = 1001").run();
  w.api.calls.length = 0;
  w.env.asked.length = 0;
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
  assert.equal(w.env.sent.length, 0, "nothing is sent before the person writes");
  api.calls.length = 0;
  w.env.answer = "Our team will answer you here.\n[[HUMAN]]";
  await w.send(text(SARA, "The error is 12"));
  await w.send(text(SARA, "Still the same"));
  assert.equal(api.of("setMessageReaction").length, 0, "nothing was delivered, so nothing is confirmed");
  assert.equal(w.env.sent.length, 2, "each message reaches the owner by mail");
  assert.ok(w.env.sent[0].text.includes("The error is 12"));
  assert.equal(api.of("sendMessage").filter((c) => c.chat_id === SARA && c.text.startsWith("I could not reach our team")).length, 1, "told once");
  // Neither the chat nor the mail: the person is not told someone knows.
  const lost = await world({ mail: false });
  const api2 = telegram({ sendMessage: (p) => (p.chat_id === TEAM ? { ok: false, error_code: 403, description: "Forbidden" } : { ok: true, result: { message_id: 900 } }) });
  await toTeamWith(lost, SARA, "Help");
  assert.ok(api2.of("sendMessage").some((c) => c.chat_id === SARA && c.text.startsWith("I could not reach our team")));
});

test("a team group that became a supergroup is followed, by the service message and by Telegram's refusal", async () => {
  const w = await world();
  await w.send({ update_id: nextUpdate(), message: { message_id: 1, chat: { id: TEAM, type: "group" }, migrate_to_chat_id: -100777 } });
  assert.equal(await w.setting("telegram_owner_chat"), "-100777");
  w.api.calls.length = 0;
  await toTeamWith(w, SARA, "Help");
  assert.equal(w.lastText(-100777).text.includes("Ticket #1001"), true, "the card goes to the new chat");
  assert.equal((await w.row("SELECT team_chat FROM tg_relay LIMIT 1")).team_chat, -100777);
  // Telegram's own refusal names the new chat as well.
  const v = await world();
  const api = telegram({ sendMessage: (p) => (p.chat_id === TEAM ? { ok: false, error_code: 400, description: "Bad Request: group chat was upgraded to a supergroup chat", parameters: { migrate_to_chat_id: -100888 } } : { ok: true, result: { message_id: 900 } }) });
  await toTeamWith(v, SARA, "Help");
  assert.equal(await v.setting("telegram_owner_chat"), "-100888");
  assert.ok(api.of("sendMessage").some((c) => c.chat_id === -100888 && c.text.includes("Ticket #1001")));
});

test("an administrator who writes anonymously in the team's group is the team: pairs it and answers tickets", async () => {
  const w = await world({ team: false });
  const GROUP = -100555;
  const anonymous = (words, extra = {}) => ({ update_id: nextUpdate(), message: { message_id: nextMessage(), from: { id: 1087968824, is_bot: true, first_name: "Group", username: "GroupAnonymousBot" }, sender_chat: { id: GROUP, type: "supergroup" }, chat: { id: GROUP, type: "supergroup" }, text: words, ...extra } });
  await w.send(anonymous(`/pair ${await w.setting("telegram_pair_code")}`));
  assert.equal(await w.setting("telegram_owner_chat"), String(GROUP));
  await toTeamWith(w, SARA, "Help");
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
  await w.send({ update_id: nextUpdate(), message: { message_id: 5, from: { id: 9, is_bot: true, username: "OtherBot" }, chat: { id: GROUP, type: "supergroup" }, text: "/stats" } });
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
  await toTeamWith(w, 13, "I need help");
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
  await w.send({ update_id: nextUpdate(), callback_query: { id: "x", from: { id: SARA }, data: "o:c:1001", message: { message_id: 1, chat: { id: SARA, type: "private" } } } });
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
  await toTeamWith(w, SARA, "Help");
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

test("an English chat is English from end to end: nothing in it is in Persian script", async () => {
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

// ---------- The bot's picture, and where people came from ----------

test("the bot gets its profile picture once for each bot, and a refusal is told", async () => {
  const asked = [];
  const assets = { fetch: async (request) => (asked.push(new URL(request.url).pathname), new Response(new Uint8Array([0xff, 0xd8, 0xff, 1, 2, 3]), { headers: { "Content-Type": "image/jpeg" } })) };
  const env = { DB: d1(), TOKEN_SECRET: SECRET, ASSETS: assets };
  const api = telegram();
  const quiet = { blocked: async () => false, count: async () => {} };
  const linked = await connect(env, ADMIN_REQUEST(), TOKEN);
  assert.equal(linked.warning, undefined);
  const set = api.of("setMyProfilePhoto");
  assert.equal(set.length, 1);
  assert.deepEqual(JSON.parse(set[0].photo), { type: "static", photo: "attach://picture" });
  assert.deepEqual(set[0].picture, { file: "bot.jpg", bytes: 6, type: "image/jpeg" });
  assert.deepEqual(asked, ["/bot.jpg"]);
  // Saved again, and the schedule every minute: the picture is not sent again.
  await connect(env, ADMIN_REQUEST(), TOKEN);
  await sweep(env, quiet);
  await sweep(env, quiet);
  assert.equal(api.of("setMyProfilePhoto").length, 1);
  // Another bot has none yet.
  await connect(env, ADMIN_REQUEST(), `987654321:${"B".repeat(35)}`);
  assert.equal(api.of("setMyProfilePhoto").length, 2);
  // Telegram refuses it: told when the token is saved, and by the panel's check, which tries again.
  const refusing = telegram({ setMyProfilePhoto: { ok: false, error_code: 400, description: "Bad Request: PHOTO_INVALID" }, getWebhookInfo: { ok: true, result: { url: HOOK_URL } } });
  const other = { DB: d1(), TOKEN_SECRET: SECRET, ASSETS: assets };
  const told = await connect(other, ADMIN_REQUEST(), TOKEN);
  assert.match(told.warning, /did not take the bot's picture: Bad Request: PHOTO_INVALID\. "Check the bot" tries again\.$/);
  assert.match(await check(other), /Telegram refused its picture: Bad Request: PHOTO_INVALID\.$/);
  assert.equal(refusing.of("setMyProfilePhoto").length, 2);
  // A build with no picture, or a service with no assets, sets none and says nothing.
  const bare = telegram();
  const none = await connect({ DB: d1(), TOKEN_SECRET: SECRET, ASSETS: { fetch: async () => new Response("<html>", { headers: { "Content-Type": "text/html" } }) } }, ADMIN_REQUEST(), TOKEN);
  assert.equal(none.warning, undefined);
  assert.equal(bare.of("setMyProfilePhoto").length, 0);
});

test("a link that names its source counts a new person once a day, and /stats lists the links of the week", async () => {
  const w = await world({ lang: false });
  await w.send(text(SARA, "/start ad_fa1"));
  await w.send(text(SARA, "/start ad_fa1"));
  await w.send(text(SARA + 1, "/start ad_fa1"));
  await w.send(text(SARA + 2, "/start AD_zh1"));
  // Not sources: a topic of a link, nothing, a word that is too long or has other characters.
  for (const [i, arg] of ["buy", "", "x".repeat(40), "bad!", "team"].entries()) await w.send(text(SARA + 3 + i, `/start ${arg}`.trim()));
  assert.deepEqual(await w.rows("SELECT key, n FROM tg_stats WHERE key LIKE 'from:%' ORDER BY key"), [{ key: "from:ad_fa1", n: 2 }, { key: "from:ad_zh1", n: 1 }], "once for each person, in lower case");
  // Someone who has a language already is not new.
  await w.speak(SARA + 20, "en");
  await w.send(text(SARA + 20, "/start ad_fa1"));
  assert.equal((await w.row("SELECT n FROM tg_stats WHERE key = 'from:ad_fa1'")).n, 2);
  await w.send(text(TEAM, "/stats"));
  assert.ok(w.lastText(TEAM).text.includes("New people by link, 7 days: ad_fa1 2 (today 2), ad_zh1 1 (today 1)"), w.lastText(TEAM).text);
  // Older days count in the week, and are not "today".
  await w.env.DB.prepare("UPDATE tg_stats SET day = date(day, '-3 days') WHERE key = 'from:ad_zh1'").run();
  await w.send(text(TEAM, "/stats"));
  assert.ok(w.lastText(TEAM).text.includes("ad_fa1 2 (today 2), ad_zh1 1"), w.lastText(TEAM).text);
  assert.ok(!w.lastText(TEAM).text.includes("ad_zh1 1 (today"));
});

// ---------- The assistant first ----------

test("asking for the team, the person says what is wrong first: the assistant answers it, and only what it cannot solve becomes a ticket", async () => {
  const w = await world({ answer: "Update the app, then choose the fastest location." });
  await w.send(press(SARA, "h"));
  assert.ok(w.lastText(SARA).text.includes("Tell me what is wrong"));
  assert.equal((await w.row("SELECT mode FROM tg_users WHERE chat_id = ?1", SARA)).mode, "ask");
  // A question the assistant can answer: answered, no ticket, and the team stays one tap away.
  await w.send(text(SARA, "How do I update the app?"));
  const answer = w.lastText(SARA);
  assert.equal(answer.text, "Update the app, then choose the fastest location.");
  assert.deepEqual(datas(answer.reply_markup), ["h", "fb:u:1", "fb:d:1"]);
  assert.equal((await w.row("SELECT COUNT(*) AS n FROM tg_tickets")).n, 0);
  assert.equal(w.api.of("sendMessage").filter((c) => c.chat_id === TEAM).length, 0, "the team is not bothered");
  assert.equal((await w.row("SELECT mode FROM tg_users WHERE chat_id = ?1", SARA)).mode, "", "the next question is an ordinary one");
  // Still stuck: one tap, and the team gets the whole story at once.
  await w.send(press(SARA, "h"));
  const card = w.lastText(TEAM);
  assert.ok(card.text.includes("Ticket #1001") && card.text.includes("How do I update the app?") && card.text.includes("<b>Assistant:</b> Update the app"), card.text);
});

test("what the assistant cannot answer, or a screenshot, opens the ticket at once; so does an assistant that is away", async () => {
  const w = await world({ answer: "Our team will answer you about the payment.\n[[HUMAN]]" });
  await w.send(text(SARA, "/human"));
  await w.send(text(SARA, "I paid but got no account"));
  assert.ok(w.api.of("sendMessage").some((c) => c.chat_id === SARA && c.text === "Our team will answer you about the payment."), "the assistant's words, without the marker");
  assert.ok(w.lastText(SARA).text.includes("Ticket #1001 opened"));
  assert.ok(w.lastText(TEAM).text.includes("I paid but got no account"));
  // A screenshot cannot be read by the assistant: it opens the ticket and goes to the team.
  const s = await world();
  await s.send(text(SARA, "/human"));
  await s.send(media(SARA, { photo: [{ file_id: "p" }], caption: "this error" }));
  assert.ok(s.api.of("sendMessage").some((c) => c.chat_id === SARA && c.text.includes("Ticket #1001 opened")));
  assert.ok(s.lastText(TEAM).text.includes("Ticket #1001"), "the card");
  assert.equal(s.api.of("copyMessage").filter((c) => c.chat_id === TEAM).length, 1, "and the screenshot");
  assert.equal(s.env.asked.length, 1, "the model only wrote the mail's summary");
  // The assistant is away: what the person wrote goes to the team as it is.
  const d = await world();
  await d.send(text(SARA, "/human"));
  d.env.aiDown = true;
  await d.send(text(SARA, "Nothing works"));
  assert.ok(d.lastText(SARA).text.includes("Ticket #1001 opened"));
  assert.ok(d.lastText(TEAM).text.includes("Nothing works"));
});

test("a ticket the team never heard of is closed quietly once its person goes quiet, and /tickets lists only real ones", async () => {
  const w = await world();
  await toTeamWith(w, SARA, "It does not connect");
  // One opened the old way, with nothing written in it.
  const t = Math.floor(Date.now() / 1000);
  await w.env.DB.prepare("INSERT INTO tg_tickets (id, chat_id, opened, updated) VALUES (1002, 11, ?1, ?1)").bind(t - 3600).run();
  await w.env.DB.prepare("INSERT INTO tg_users (chat_id, updated, mode, ticket_id) VALUES (11, ?1, 'ticket', 1002)").bind(t - 3600).run();
  await w.send(text(TEAM, "/tickets"));
  const list = w.lastText(TEAM).text;
  assert.ok(list.includes("Open tickets (1)") && list.includes("#1001") && !list.includes("#1002"), list);
  w.api.calls.length = 0;
  await sweep(w.env, w.kit);
  assert.equal((await w.row("SELECT status FROM tg_tickets WHERE id = 1002")).status, "closed");
  assert.equal((await w.row("SELECT status FROM tg_tickets WHERE id = 1001")).status, "open", "a real one stays");
  assert.equal(w.api.of("sendMessage").filter((c) => c.chat_id === 11 || c.chat_id === TEAM).length, 0, "nobody is told");
});

// ---------- A topic for each ticket ----------

/** A team group with Topics, paired; its members write as `member`, in a topic with `inTopic`. */
async function forumWorld() {
  const w = await world({ team: false });
  const GROUP = -1001234567;
  let next = 40;
  w.api.overrides.createForumTopic = () => ({ ok: true, result: { message_thread_id: next++, name: "t", icon_color: 0x6fb9f0 } });
  const chat = { id: GROUP, type: "supergroup", is_forum: true, title: "Albatross team" };
  const member = (words, extra = {}) => ({ update_id: nextUpdate(), message: { message_id: nextMessage(), from: { id: 5, first_name: "Owner" }, chat, ...(typeof words === "string" ? { text: words } : {}), ...extra } });
  const inTopic = (topic, words, extra = {}) => member(words, { is_topic_message: true, message_thread_id: topic, reply_to_message: { message_id: topic, forum_topic_created: { name: "t" } }, ...extra });
  await w.send(member(`/pair ${await w.setting("telegram_pair_code")}`));
  return { w, GROUP, member, inTopic };
}

test("in a team group with Topics each ticket gets its own topic: the person's messages go there, and writing there answers them", async () => {
  const { w, GROUP, member, inTopic } = await forumWorld();
  assert.equal(await w.setting("telegram_team_forum"), "1");
  assert.ok(w.lastText(GROUP).text.includes("Each ticket gets its own topic"));
  await toTeamWith(w, SARA, "It does not connect");
  const made = w.api.of("createForumTopic");
  assert.equal(made.length, 1);
  assert.deepEqual({ chat: made[0].chat_id, name: made[0].name }, { chat: GROUP, name: "#1001 · Sara (@sara_tg)" });
  const card = w.lastText(GROUP);
  assert.equal(card.message_thread_id, 40, "the card is in the ticket's topic");
  assert.ok(card.text.includes("Write in this topic to answer"));
  assert.equal((await w.row("SELECT topic FROM tg_tickets WHERE id = 1001")).topic, 40);
  // The person's next words and screenshots go to the same topic, which is made once.
  await w.send(text(SARA, "Still nothing"));
  assert.equal(w.lastText(GROUP).message_thread_id, 40);
  await w.send(media(SARA, { photo: [{ file_id: "p" }] }));
  assert.equal(w.api.of("copyMessage").pop().message_thread_id, 40);
  assert.equal(w.api.of("createForumTopic").length, 1);
  // A second ticket, a second topic.
  await toTeamWith(w, 11, "Help me too");
  assert.equal((await w.row("SELECT topic FROM tg_tickets WHERE id = 1002")).topic, 41);
  // The team writes in the topic, no reply needed: the person gets it.
  await w.send(inTopic(40, "Please update the app"));
  assert.equal(w.lastText(SARA).text, "💬 <b>Albatross team</b>\nPlease update the app");
  await w.send(inTopic(40, null, { photo: [{ file_id: "q" }] }));
  assert.equal(w.api.of("copyMessage").pop().chat_id, SARA);
  // A note about the topic itself answers nobody; nor does talk in the General chat.
  const before = w.api.of("sendMessage").filter((c) => c.chat_id === SARA).length;
  await w.send(inTopic(40, null, { forum_topic_edited: { name: "renamed" } }));
  await w.send(member("lunch?"));
  assert.equal(w.api.of("sendMessage").filter((c) => c.chat_id === SARA).length, before);
  // /tickets links each one to its topic.
  await w.send(member("/tickets"));
  assert.ok(w.lastText(GROUP).text.includes('<a href="https://t.me/c/1234567/40">#1001</a>'), w.lastText(GROUP).text);
  // /close in the topic closes that ticket, and its topic goes.
  await w.send(inTopic(40, "/close"));
  assert.equal((await w.row("SELECT status FROM tg_tickets WHERE id = 1001")).status, "closed");
  assert.deepEqual(w.api.of("deleteForumTopic").map((c) => c.message_thread_id), [40]);
  assert.ok(w.lastText(SARA).text.includes("Ticket #1001 is closed"));
  // The person closes theirs: the topic goes, and the General chat says so.
  await w.send(press(11, "tc:1002"));
  assert.deepEqual(w.api.of("deleteForumTopic").map((c) => c.message_thread_id), [40, 41]);
  const note = w.lastText(GROUP);
  assert.ok(note.text.includes("Ticket #1002") && note.text.includes("closed by the person") && !note.message_thread_id, note.text);
});

test("without the right to manage topics, tickets arrive in the General chat as before, and the team is told how to fix it once", async () => {
  const { w, GROUP } = await forumWorld();
  w.api.overrides.createForumTopic = () => ({ ok: false, error_code: 400, description: "Bad Request: not enough rights to manage topics" });
  await toTeamWith(w, SARA, "It does not connect");
  await toTeamWith(w, 11, "Help");
  const sent = w.api.of("sendMessage").filter((c) => c.chat_id === GROUP);
  assert.equal(sent.filter((c) => c.text.includes("right to <b>manage topics</b>")).length, 1, "told once");
  const cards = sent.filter((c) => c.text.includes("🎫 <b>Ticket #"));
  assert.equal(cards.length, 2);
  assert.ok(cards.every((c) => !c.message_thread_id), "in the General chat");
  // Replying to the card still answers.
  const cardId = (await w.row("SELECT message_id FROM tg_relay WHERE ticket_id = 1001")).message_id;
  await w.send({ update_id: nextUpdate(), message: { message_id: nextMessage(), from: { id: 5, first_name: "Owner" }, chat: { id: GROUP, type: "supergroup", is_forum: true }, text: "Try again", reply_to_message: { message_id: cardId } } });
  assert.ok(w.lastText(SARA).text.includes("Try again"));
  // A group that turns Topics off is followed: no more topics are asked for.
  await w.send({ update_id: nextUpdate(), message: { message_id: nextMessage(), from: { id: 5, first_name: "Owner" }, chat: { id: GROUP, type: "supergroup" }, text: "/stats" } });
  assert.equal(await w.setting("telegram_team_forum"), "");
});
