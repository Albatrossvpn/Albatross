// The Telegram bot in many languages: every pack is complete and sound, every
// screen of every language is something Telegram takes, and the bot asks for a
// language first, keeps the choice, and speaks it everywhere (menus, answers,
// tickets, payments).
import assert from "node:assert/strict";
import { readdirSync } from "node:fs";
import test, { afterEach } from "node:test";
import { check, htmlProblems } from "./i18n-check.mjs";
import en from "../src/i18n/en.js";
import { DEFAULT, PACKS, fill, has, pack, translator } from "../src/i18n/index.js";
import { connect, disconnect, sweep } from "../src/telegram.js";
import { languagePicker, screens } from "../src/telegram-content.js";
import { d1 } from "./d1.mjs";
import { ADMIN_REQUEST, SARA, TEAM, TOKEN, datas, labels, nextUpdate, press, real, telegram, text, world } from "./tg-helpers.mjs";

afterEach(() => (globalThis.fetch = real));

// ---------- The packs ----------

test("every language file is a pack on the picker, and every pack is complete and sound", async () => {
  const files = readdirSync(new URL("../src/i18n/", import.meta.url)).filter((f) => f.endsWith(".js") && f !== "index.js").map((f) => f.slice(0, -3));
  assert.deepEqual(PACKS.map((p) => p.code).sort(), files.sort(), "a pack that is not in index.js is never offered, and one that is not a file cannot load");
  assert.ok(PACKS.some((p) => p.code === DEFAULT), "English, the master and the fallback, is a pack");
  for (const p of PACKS) {
    const problems = check(p.code, p);
    assert.deepEqual(problems, [], `${p.code}.js: ${problems.slice(0, 5).join(" | ")}`);
  }
  assert.equal(new Set(PACKS.map((p) => p.flag + p.name)).size, PACKS.length, "each language has its own name and flag");
});

test("a string is filled from its placeholders, a missing key is read from English, and a key nobody has is a mistake", () => {
  assert.equal(fill("Hi {name}, {n} left{gone}", { name: "Sara", n: 3 }), "Hi Sara, 3 left");
  assert.equal(fill("{a}{a}", { a: "$&" }), "$&$&", "a value is put in as it is");
  assert.equal(fill("{name}", { name: "{password}", password: "x" }), "{password}", "a value is not read as a placeholder again");
  const t = translator("fr");
  assert.equal(t("btn_pay", { stars: 29 }).includes("29"), true);
  assert.ok(Array.isArray(t("guide_common")) && t("guide_common").length === en.s.guide_common.length);
  assert.equal(translator("klingon")("btn_menu"), en.s.btn_menu, "an unknown language is English");
  assert.throws(() => t("no_such_key"), /No string/);
  assert.equal(has("fr"), true);
  assert.equal(has("klingon"), false);
  assert.equal(pack("klingon"), en);
});

// ---------- What Telegram takes ----------

/** Every screen the bot can show, with sample arguments: [name, { text, markup }]. */
function everyScreen(v) {
  const shop = { catalog: [{ gb: 5, stars: 15, plan: "Bronze", devices: 1 }, { gb: 10, stars: 29, plan: "Silver", devices: 2 }, { gb: 100, stars: 285, plan: "Gold", devices: 4 }, { gb: 1000, stars: 2850, plan: "VIP", devices: 0 }], days: 30 };
  const p = shop.catalog[1];
  const receipt = { id: 7, gb: 10, stars: 29, username: "tg1234567", password: "Ab3dE6gH9jK2mN", quota_bytes: 10 * 1024 ** 3, plan: "Silver", devices: 2, expires: 1900000000 };
  const out = [];
  const add = (name, s) => out.push([name, s]);
  for (const selling of [false, true]) {
    add(`menu(${selling})`, v.menu("Sara <b>&", selling));
    add(`menu anon(${selling})`, v.menu("", selling));
    add(`help(${selling})`, v.help(selling));
    add(`unknownCommand(${selling})`, v.unknownCommand(selling));
  }
  add("connectPicker", v.connectPicker());
  for (const d of ["win", "and", "ios", "mac", "nonsense"]) {
    add(`connectGuide(${d})`, v.connectGuide(d));
    add(`installGuide(${d})`, v.installGuide(d));
  }
  add("installPicker", v.installPicker());
  add("FIXED", v.FIXED);
  add("stillBroken", v.stillBroken());
  add("plans", v.plans());
  add("plans(shop)", v.plans(shop));
  add("ACCOUNT_INFO", v.ACCOUNT_INFO);
  add("DOWNLOADS", v.DOWNLOADS);
  add("CAN_ONLY_READ", v.CAN_ONLY_READ);
  add("busy", v.busy());
  add("failed", v.failed());
  for (const hadChat of [true, false]) add(`ticketOpened(${hadChat})`, v.ticketOpened(1042, hadChat));
  add("ticketOpen", v.ticketOpen(1042));
  add("ticketClosedByUser", v.ticketClosedByUser(1042));
  add("ticketClosedByTeam", v.ticketClosedByTeam(1042));
  add("ticketClosedIdle", v.ticketClosedIdle(1042));
  add("rated", v.rated(4));
  add("TICKET_GONE", v.TICKET_GONE);
  add("ended(true)", v.ended(true));
  add("ended(false)", v.ended(false));
  add("NOTHING_TO_END", v.NOTHING_TO_END);
  for (const mailed of [true, false]) add(`noTeam(${mailed})`, v.noTeam(mailed));
  add("store", v.store(shop.catalog, 30));
  add("store no end", v.store(shop.catalog, 0));
  add("storeChoice", v.storeChoice(p, 30));
  add("askAccount", v.askAccount(p));
  for (const k of ["NOT_A_USERNAME", "NO_SUCH_ACCOUNT", "CANNOT_RECHARGE", "TOO_MANY_LOOKUPS", "PAYSUPPORT", "STORE_OFF", "ORDER_GONE", "NO_OWN_ACCOUNT", "TOO_MANY_RESETS"]) add(k, v[k]);
  add("checkout new", v.checkout({ id: 5, kind: "new", gb: 10, stars: 29, target: "" }, p, 30));
  add("checkout topup", v.checkout({ id: 5, kind: "topup", gb: 10, stars: 29, target: "sara_1" }, p, 0));
  add("terms", v.terms(30));
  add("terms no end", v.terms(0));
  add("pickAccount", v.pickAccount(["tg1234567", "tg7654321"]));
  add("confirmReset", v.confirmReset("tg1234567"));
  add("newPassword", v.newPassword("tg1234567", "Ab3dE6gH9jK2mN"));
  add("receiptNew", v.receiptNew(receipt));
  add("receiptNew no end", v.receiptNew({ ...receipt, expires: 0 }));
  add("receiptTopup", v.receiptTopup(receipt));
  add("receiptTopup no end", v.receiptTopup({ ...receipt, expires: 0 }));
  for (const why of ["delivery", "team"]) add(`gaveBack(${why})`, v.gaveBack(7, 29, why));
  return out;
}

const bytes = (s) => new TextEncoder().encode(s).length;

test("every screen of every language is HTML, buttons and lengths that Telegram takes, with nothing left unfilled", () => {
  for (const p of PACKS) {
    const v = screens(p.code);
    for (const [name, s] of everyScreen(v)) {
      const where = `${p.code} ${name}`;
      assert.ok(typeof s.text === "string" && s.text.trim(), `${where}: has text`);
      assert.ok(s.text.length <= 4000, `${where}: ${s.text.length} characters`);
      assert.deepEqual(htmlProblems(s.text), [], where);
      assert.ok(!/\{[a-z_]+\}|undefined|NaN|\[object/.test(s.text), `${where}: something unfilled in ${JSON.stringify(s.text.slice(0, 80))}`);
      for (const b of (s.markup ? s.markup.inline_keyboard : []).flat()) {
        assert.ok(b.text && b.text.length <= 64, `${where}: button ${JSON.stringify(b.text)}`);
        assert.ok(!/\{[a-z_]+\}|undefined|NaN/.test(b.text), `${where}: button ${b.text}`);
        assert.ok(Boolean(b.callback_data) !== Boolean(b.url), `${where}: a button has data or a link`);
        if (b.callback_data) assert.ok(bytes(b.callback_data) <= 64, `${where}: ${b.callback_data}`);
        if (b.url) assert.match(b.url, /^https:\/\//, where);
      }
    }
    // The words around a payment and a ticket's reply are texts of their own.
    for (const s of [v.SLOW_DOWN, v.WENT_WRONG, v.TICKET_LIMIT, v.TEAM_BUSY, v.TEAM_UNREACHABLE, v.teamSays("hi"), v.declined("invalid"), v.declined("blocked"), v.declined("paused"), v.declined("expired"), v.declined("account"), v.declined("used"), v.deliveryFailed(), v.invoiceFailed("nope"), v.t("toast_great")]) {
      assert.ok(s && !/\{[a-z_]+\}/.test(s), `${p.code}: ${s}`);
    }
    for (const why of ["invalid", "blocked", "paused", "expired", "account", "used"]) assert.ok(v.declined(why).length <= 200, `${p.code}: a refusal is short`);
    const inv = v.invoice({ gb: 10, days: 30, kind: "topup", target: "sara_1" });
    assert.ok(inv.title.length >= 1 && inv.title.length <= 32 && inv.label.length >= 1 && inv.label.length <= 32 && inv.description.length >= 1 && inv.description.length <= 255, `${p.code}: an invoice Telegram takes`);
    assert.ok(inv.description.includes("sara_1") && inv.description.includes("10"), p.code);
  }
});

test("the same screens in every language have the same buttons doing the same things", () => {
  const shapeOf = (v) => everyScreen(v).map(([name, s]) => [name, (s.markup ? s.markup.inline_keyboard : []).map((row) => row.map((b) => b.callback_data || b.url))]);
  const english = shapeOf(screens("en"));
  for (const p of PACKS) assert.deepEqual(shapeOf(screens(p.code)), english, `${p.code}: a language cannot change what a button does`);
});

test("the picker lists every language once, the person's own first, and carries the topic of a link through the choice", () => {
  const plain = languagePicker();
  const buttons = plain.markup.inline_keyboard.flat();
  assert.deepEqual(buttons.map((b) => b.callback_data), PACKS.map((p) => `l:${p.code}`));
  assert.ok(buttons.every((b) => bytes(b.callback_data) <= 64) && buttons.every((b) => b.text.length <= 40));
  assert.ok(plain.text.startsWith("🌐 <b>Choose your language</b>"));
  for (const p of PACKS.filter((x) => x.code !== "en")) assert.ok(plain.text.split("\n").length > 1, p.code);
  assert.equal(languagePicker("buy", "ru").markup.inline_keyboard[0][0].callback_data, "l:ru:buy");
  assert.equal(languagePicker("buy", "pt-BR").markup.inline_keyboard[0][0].callback_data, `l:${PACKS[0].code}:buy`, "a language there is no pack for keeps the usual order");
  assert.equal(languagePicker("not-a-topic").markup.inline_keyboard[0][0].callback_data, `l:${PACKS[0].code}`, "only the topics a link can open are carried");
  assert.equal(languagePicker("", "zh-hans").markup.inline_keyboard[0][0].callback_data, "l:zh");
});

// ---------- The bot asks first and remembers ----------

/** A service where nobody has picked a language. */
const fresh = (opts = {}) => world({ lang: false, ...opts });

test("a person who writes for the first time is asked for a language, in all of them, and nothing else is done", async () => {
  const w = await fresh();
  await w.send(text(SARA, "my vpn does not connect"));
  const picker = w.lastText(SARA);
  assert.ok(picker.text.startsWith("🌐 <b>Choose your language</b>"));
  assert.deepEqual(datas(picker.reply_markup), PACKS.map((p) => `l:${p.code}`));
  assert.equal(w.env.asked.length, 0, "the model is not asked before a language is chosen");
  assert.equal(await w.langOf(SARA), "");
  // A button of an old menu asks too, in place.
  await w.send(press(SARA, "c"));
  assert.ok(w.api.of("editMessageText").pop().text.startsWith("🌐 <b>Choose your language</b>"));
  // /start does the same, and so do the other commands.
  for (const c of ["/start", "/menu", "/help", "/human", "/buy"]) {
    await w.send(text(SARA, c));
    assert.ok(w.lastText(SARA).text.startsWith("🌐 <b>Choose your language</b>"), c);
  }
  assert.equal(await w.row("SELECT COUNT(*) AS n FROM tg_tickets").then((r) => r.n), 0, "no ticket opened for a person who has not chosen");
});

test("choosing a language turns the picker into the menu in that language, and the choice is kept without the person's number", async () => {
  const w = await fresh();
  await w.send(text(SARA, "/start"));
  await w.send(press(SARA, "l:ru", 91));
  const menu = w.api.of("editMessageText").pop();
  assert.equal(menu.message_id, 91, "the picker itself became the menu");
  assert.equal(menu.text, screens("ru").menu("Sara", false).text);
  assert.deepEqual(menu.reply_markup, screens("ru").menu("Sara", false).markup);
  assert.equal(await w.langOf(SARA), "ru");
  const stored = await w.rows("SELECT * FROM tg_langs");
  assert.equal(stored.length, 1);
  assert.ok(!JSON.stringify(stored).includes(String(SARA)), "the table holds a keyed hash, not the person's number");
  assert.match(stored[0].tag, /^[A-Za-z0-9_-]{22}$/);
  // Nothing is asked again, and every screen is in Russian from now on.
  await w.send(text(SARA, "/menu"));
  assert.equal(w.lastText(SARA).text, screens("ru").menu("Sara", false).text);
  await w.send(press(SARA, "i"));
  assert.equal(w.api.of("editMessageText").pop().text, screens("ru").installPicker().text);
  await w.send(press(SARA, "c:win"));
  assert.equal(w.api.of("editMessageText").pop().text, screens("ru").connectGuide("win").text);
  await w.send(text(SARA, "/help"));
  assert.equal(w.lastText(SARA).text, screens("ru").help(false).text);
  await w.send(text(SARA, "/nonsense"));
  assert.equal(w.lastText(SARA).text, screens("ru").unknownCommand(false).text);
  await w.send(press(SARA, "fb:u"));
  assert.equal(w.api.of("answerCallbackQuery").pop().text, screens("ru").t("toast_thanks"));
  // A language the bot does not have is not a choice.
  await w.send(press(SARA, "l:klingon"));
  assert.equal(await w.langOf(SARA), "ru");
});

test("the choice outlives the chat: half an hour of quiet forgets the person, not their language", async () => {
  const w = await fresh();
  await w.send(text(SARA, "/start"));
  await w.send(press(SARA, "l:tr"));
  await w.send(text(SARA, "wifi calismiyor"));
  assert.ok(w.env.asked.length === 1);
  await w.env.DB.prepare("UPDATE tg_users SET updated = updated - 4000 WHERE chat_id = ?1").bind(SARA).run();
  await sweep(w.env, w.kit);
  assert.equal(await w.row("SELECT COUNT(*) AS n FROM tg_users WHERE chat_id = ?1", SARA).then((r) => r.n), 0, "the chat is forgotten");
  assert.equal(await w.langOf(SARA), "tr", "the language is not");
  await w.send(text(SARA, "/menu"));
  assert.equal(w.lastText(SARA).text, screens("tr").menu("Sara", false).text, "no picker the second time");
  // A language nobody used for a year is forgotten.
  await w.env.DB.prepare("UPDATE tg_langs SET updated = updated - ?1").bind(366 * 86400).run();
  await sweep(w.env, w.kit);
  assert.equal(await w.row("SELECT COUNT(*) AS n FROM tg_langs").then((r) => r.n), 0);
});

test("/language and the menu's button offer the choice again; a new choice replaces the old", async () => {
  const w = await world({ lang: "fr" });
  await w.send(text(SARA, "/menu"));
  const known = (await w.rows("SELECT * FROM tg_langs")).length;
  assert.ok(labels(w.lastText(SARA).reply_markup).includes(screens("fr").t("btn_language")));
  assert.ok(datas(w.lastText(SARA).reply_markup).includes("lg"));
  await w.send(press(SARA, "lg", 61));
  const picker = w.api.of("editMessageText").pop();
  assert.equal(picker.message_id, 61);
  assert.ok(picker.text.startsWith("🌐"));
  await w.send(text(SARA, "/language"));
  assert.ok(w.lastText(SARA).text.startsWith("🌐"));
  await w.send(press(SARA, "l:zh", 62));
  assert.equal(await w.langOf(SARA), "zh");
  assert.equal(w.api.of("editMessageText").pop().text, screens("zh").menu("Sara", false).text);
  assert.equal((await w.rows("SELECT * FROM tg_langs")).length, known, "one record for one person, however often they choose");
});

test("a link into the bot (/start buy) is remembered through the choice of a language", async () => {
  const w = await fresh();
  await w.env.DB.prepare("INSERT INTO settings (key, value) VALUES ('stars_enabled', '1')").run();
  await w.send(text(SARA, "/start buy"));
  assert.deepEqual(datas(w.lastText(SARA).reply_markup), PACKS.map((p) => `l:${p.code}:buy`));
  await w.send(press(SARA, "l:es:buy", 70));
  const store = w.api.of("editMessageText").pop();
  assert.equal(store.message_id, 70);
  assert.ok(store.text.startsWith("⭐") && store.text.includes("<b>10 GB</b> - ⭐29"), store.text);
  assert.deepEqual(labels(store.reply_markup).slice(0, 2), ["5 GB · ⭐15", "10 GB · ⭐29"]);
  assert.equal(store.text.split("\n")[0], screens("es").t("store").split("\n")[0]);
  // Another topic, another screen (with the packages on sale: the plans screen offers them).
  await w.send(text(SARA + 1, "/start plans"));
  await w.send(press(SARA + 1, "l:vi:plans", 71));
  const plans = w.api.of("editMessageText").pop();
  assert.equal(plans.message_id, 71);
  assert.ok(plans.text.startsWith(screens("vi").t("plans").split("\n")[0]) && plans.text.includes("• 25 GB - ⭐72 - Silver"));
  assert.ok(datas(plans.reply_markup).includes("s"), "and its buy button");
  // A person asking for the team gets a ticket after the choice.
  await w.send(text(SARA + 2, "/start human"));
  await w.send(press(SARA + 2, "l:id:human", 72));
  assert.ok(w.api.of("editMessageText").pop().text.includes(screens("id").t("menu_title")));
  assert.ok(w.lastText(SARA + 2).text.includes("1001"), "the ticket opened, in Indonesian");
  assert.equal(w.lastText(SARA + 2).text, screens("id").ticketOpened(1001, false).text);
});

test("a person whose ticket is open is not stopped to pick a language: the team has their words, and they are spoken to in English", async () => {
  const w = await fresh();
  await w.speak(SARA, "en");
  await w.send(text(SARA, "/human"));
  await w.env.DB.prepare("DELETE FROM tg_langs").run();
  await w.send(text(SARA, "the app shows an error"));
  assert.ok(w.api.calls.some((c) => c.chat_id === TEAM && /the app shows an error/.test(c.text || "")), "the team got it");
  assert.ok(!w.api.calls.some((c) => c.chat_id === SARA && /Choose your language/.test(c.text || "")));
  // The team closes it: the person hears it in English, and is asked at their next question.
  await w.send(text(TEAM, "/close 1001"));
  assert.ok(w.api.of("sendMessage").filter((c) => c.chat_id === SARA).pop().text.includes("Ticket #1001 is closed"));
  await w.send(text(SARA, "hello?"));
  assert.ok(w.lastText(SARA).text.startsWith("🌐"));
});

test("the assistant is told to answer in the person's language, and answers the website's visitors in English as before", async () => {
  const w = await world({ lang: "ru" });
  await w.send(text(SARA, "VPN не подключается"));
  const system = w.env.asked.pop().messages[0].content;
  assert.ok(system.includes("Answer in Russian only"), "the rules say which language");
  assert.ok(!system.includes("read English as a second language"));
  assert.ok(system.includes(screens("ru").t("ctx_language", { language: "Russian" })), "and so does the context, in that language");
  await w.speak(SARA, "en");
  await w.send(text(SARA, "it does not connect"));
  const english = w.env.asked.pop().messages[0].content;
  assert.ok(english.includes("Answer in English only") && english.includes("read English as a second language"));
});

test("the assistant's failures are said in the person's language, with the way to the team", async () => {
  const w = await world({ lang: "es" });
  w.env.aiDown = true;
  await w.send(text(SARA, "no conecta"));
  const said = w.lastText(SARA);
  assert.equal(said.text, screens("es").failed().text);
  assert.deepEqual(datas(said.reply_markup), ["h", "mn"]);
});

test("a ticket is opened, answered and closed in the person's language; the team's card names it", async () => {
  const w = await world({ lang: "ru" });
  await w.send(text(SARA, "/human"));
  const opened = w.lastText(SARA);
  assert.equal(opened.text, screens("ru").ticketOpened(1001, false).text);
  assert.equal(labels(opened.reply_markup)[0], screens("ru").t("btn_solved"));
  const card = w.api.calls.filter((c) => c.method === "sendMessage" && c.chat_id === TEAM).pop();
  assert.ok(card.text.includes(`🌐 ${pack("ru").name}`), card.text);
  // The team answers by replying to the card: the person reads it with their own label.
  const cardId = (await w.row("SELECT message_id FROM tg_relay WHERE team_chat = ?1 AND ticket_id = 1001", TEAM)).message_id;
  await w.send({ update_id: nextUpdate(), message: { message_id: 900, from: { id: TEAM, first_name: "Owner" }, chat: { id: TEAM, type: "private" }, text: "Try again now", reply_to_message: { message_id: cardId } } });
  const answer = w.api.of("sendMessage").filter((c) => c.chat_id === SARA).pop();
  assert.equal(answer.text, screens("ru").teamSays("Try again now"));
  assert.equal(labels(answer.reply_markup)[0], screens("ru").t("btn_solved"));
  await w.send(text(TEAM, "/close 1001"));
  assert.equal(w.api.of("sendMessage").filter((c) => c.chat_id === SARA).pop().text, screens("ru").ticketClosedByTeam(1001).text);
  // An English chat needs no language on its card. (A second service stands in for Telegram from here on.)
  const english = await world({ lang: "en" });
  await english.send(text(SARA, "/human"));
  const englishCard = english.api.calls.filter((c) => c.method === "sendMessage" && c.chat_id === TEAM).pop();
  assert.ok(!englishCard.text.includes("🌐"), "an English chat needs no language on its card");
});

test("the bot's menu, description and about line are set for every language, under its code", async () => {
  const w = await world({ lang: "en" });
  const calls = w.boot;
  const lcs = PACKS.filter((p) => p.code !== DEFAULT).map((p) => p.code);
  for (const lc of lcs) {
    const set = calls.filter((c) => c.language_code === lc);
    const menu = set.find((c) => c.method === "setMyCommands");
    const desc = set.find((c) => c.method === "setMyDescription");
    const short = set.find((c) => c.method === "setMyShortDescription");
    assert.ok(menu && desc && short, `${lc}: all three are set`);
    assert.deepEqual(menu.commands.map((c) => c.command), ["start", "menu", "human", "close", "password", "language", "help"], lc);
    assert.equal(menu.commands.find((c) => c.command === "start").description, pack(lc).s.cmd_start);
    assert.equal(desc.description, pack(lc).s.about);
    assert.equal(short.short_description, pack(lc).s.about_short);
    assert.ok(menu.commands.every((c) => c.description.length >= 3 && c.description.length <= 256), lc);
  }
  // The default, for everyone else, is English.
  const plain = calls.filter((c) => c.method === "setMyCommands" && !c.language_code && !c.scope)[0];
  assert.equal(plain.commands.find((c) => c.command === "start").description, en.s.cmd_start);
});

test("what is set for the languages is looked at again, and only what differs is written", async () => {
  const w = await world({ lang: "en" });
  await w.env.DB.prepare("DELETE FROM settings WHERE key = 'telegram_texts'").run();
  w.api.calls.length = 0;
  await sweep(w.env, w.kit);
  assert.equal(w.api.of("setMyCommands").length + w.api.of("setMyDescription").length + w.api.of("setMyShortDescription").length, 0, "all of it is as wanted already");
  assert.ok(w.api.of("getMyCommands").length >= PACKS.length && w.api.of("getMyDescription").length === PACKS.length);
  // One language lost its description: just that one is set again.
  w.api.shown.local.set("zh", { description: "something else", about: w.api.shown.local.get("zh").about });
  await w.env.DB.prepare("DELETE FROM settings WHERE key = 'telegram_texts'").run();
  w.api.calls.length = 0;
  await sweep(w.env, w.kit);
  const set = w.api.calls.filter((c) => c.method.startsWith("setMy"));
  assert.deepEqual(set.map((c) => [c.method, c.language_code]), [["setMyDescription", "zh"]]);
});

test("many languages refused for one reason are told as one problem", async () => {
  const env = { DB: d1(), TOKEN_SECRET: "s".repeat(40) };
  telegram({ setMyDescription: { ok: false, error_code: 400, description: "Bad Request: DESCRIPTION_TOO_LONG" } });
  const linked = await connect(env, ADMIN_REQUEST(), TOKEN);
  assert.match(linked.warning, /description \(default, \w\w, \w\w and \d+ more\): Bad Request: DESCRIPTION_TOO_LONG/);
  assert.ok(linked.warning.length < 250 && (linked.warning.match(/DESCRIPTION_TOO_LONG/g) || []).length === 1, "a short warning that says the reason once, not one for each language");
});

test("disconnecting forgets the languages with the chats", async () => {
  const w = await world({ lang: "ru" });
  await w.send(text(SARA, "/menu"));
  assert.equal(await w.row("SELECT COUNT(*) AS n FROM tg_langs").then((r) => r.n), 1);
  await disconnect(w.env);
  assert.equal(await w.row("SELECT COUNT(*) AS n FROM tg_langs").then((r) => r.n), 0);
});
