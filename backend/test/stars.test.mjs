// Selling data with Telegram Stars (src/sales.js and the Telegram bot's store):
// the packages and their prices, the order, Telegram's questions and answers
// about a payment, the account and password that follow it, recharging, refunds
// and a new password for the buyer. Telegram's side is stood in for.
import assert from "node:assert/strict";
import test, { afterEach } from "node:test";
import { sweep } from "../src/telegram.js";
import { verifyPassword } from "../src/lib.js";
import { SARA, TEAM, datas, labels, press, real, telegram, text, world } from "./tg-helpers.mjs";

afterEach(() => (globalThis.fetch = real));

const GB = 1024 ** 3;
const DAY = 86400;
const now = () => Math.floor(Date.now() / 1000);

let uid = 50000;
let charges = 0;
const checkoutOf = (chat, payload, amount, extra = {}) => ({ update_id: ++uid, pre_checkout_query: { id: `pq${uid}`, from: { id: chat, first_name: "Sara" }, currency: "XTR", total_amount: amount, invoice_payload: payload, ...extra } });
const paidBy = (chat, payload, amount, charge = `charge${++charges}`) => ({
  update_id: ++uid,
  message: { message_id: uid, from: { id: chat, first_name: "Sara" }, chat: { id: chat, type: "private" }, successful_payment: { currency: "XTR", total_amount: amount, invoice_payload: payload, telegram_payment_charge_id: charge, provider_payment_charge_id: "" } },
});

/** A service with a bot, a team chat and data on sale (unless `on` is false). */
async function shop({ on = true, ...rest } = {}) {
  const w = await world(rest);
  if (on) await w.env.DB.prepare("INSERT INTO settings (key, value) VALUES ('stars_enabled', '1')").run();
  return w;
}

/** The invoice-bearing screen last shown: the order's number, from its pay button. */
const lastOrder = (w) => {
  for (const c of [...w.api.calls].reverse()) {
    const pay = c.reply_markup && datas(c.reply_markup).find((d) => typeof d === "string" && d.startsWith("sp:"));
    if (pay) return Number(pay.slice(3));
  }
  throw new Error("no order on screen");
};

/** Picks a package for a new account (or, with `target`, for that account), up to the order screen. Answers the order's number. */
async function order(w, chat, { gb = 10, target = "" } = {}) {
  await w.send(press(chat, `s:${gb}`));
  if (target) {
    await w.send(press(chat, `sr:${gb}`));
    await w.send(text(chat, target));
  } else await w.send(press(chat, `sn:${gb}`));
  return lastOrder(w);
}

/** Presses Pay, and has Telegram ask and then report the payment, as it does. */
async function pay(w, chat, orderId, amount) {
  await w.send(press(chat, `sp:${orderId}`));
  const invoice = w.api.of("sendInvoice").pop();
  await w.send(checkoutOf(chat, invoice.payload, amount ?? invoice.prices[0].amount));
  const answer = w.api.of("answerPreCheckoutQuery").pop();
  if (answer.ok) await w.send(paidBy(chat, invoice.payload, amount ?? invoice.prices[0].amount));
  return { invoice, answer };
}

const accountOf = (w, name) => w.row("SELECT * FROM users WHERE username = ?1", name);
const credentials = (w, chat) => {
  const receipt = w.api.of("sendMessage").filter((c) => c.chat_id === chat && /Username: <code>/.test(c.text)).pop();
  return receipt && { username: /Username: <code>([^<]+)</.exec(receipt.text)[1], password: /Password: <code>([^<]+)</.exec(receipt.text)[1], text: receipt.text };
};

// ---------- On sale, and off ----------

test("selling is off until it is turned on: no Buy button, the store says so, the menu has no store commands", async () => {
  const w = await shop({ on: false });
  await w.send(text(SARA, "/start"));
  assert.equal(labels(w.lastText(SARA).reply_markup).length, 7, "six topics and the language");
  assert.ok(!labels(w.lastText(SARA).reply_markup).includes("⭐ Buy data"));
  await w.send(text(SARA, "/buy"));
  assert.ok(w.lastText(SARA).text.startsWith("Buying data in this chat is not available right now"));
  await w.send(press(SARA, "s:10"));
  assert.ok(w.api.of("editMessageText").pop().text.startsWith("Buying data in this chat is not available"));
  await sweep(w.env, w.kit);
  assert.ok(!w.api.shown.menus.get('"all"').some((c) => c.command === "buy"));
  // The terms and the support for payments are there anyway.
  await w.send(text(SARA, "/terms"));
  assert.ok(w.lastText(SARA).text.includes("Terms of buying data") && w.lastText(SARA).text.includes("valid for 30 days"));
  await w.send(text(SARA, "/paysupport"));
  assert.ok(w.lastText(SARA).text.includes("payment number") && w.lastText(SARA).text.includes("Telegram support cannot help"));
});

test("with selling on, the packages cost what 10,000 toman a GB at 270,000 toman a dollar and 13 dollars per 1,000 Stars make, and the menu, the plans and the commands show them", async () => {
  const w = await shop();
  await w.send(text(SARA, "/start"));
  assert.deepEqual(labels(w.lastText(SARA).reply_markup), ["🔌 I can't connect", "📲 Install the app", "💎 Plans & upgrading", "👤 Account & data", "⭐ Buy data", "⬇️ Downloads", "🧑‍💼 Talk to our team", "🌐 Language"]);
  await w.send(press(SARA, "s"));
  const store = w.api.of("editMessageText").pop();
  assert.deepEqual(labels(store.reply_markup).slice(0, 5), ["5 GB · ⭐15", "10 GB · ⭐29", "25 GB · ⭐72", "50 GB · ⭐143", "100 GB · ⭐285"]);
  assert.ok(store.text.includes("<b>10 GB</b> - ⭐29 - Silver, devices: 2") && store.text.includes("<b>100 GB</b> - ⭐285 - Gold, devices: 4") && store.text.includes("<b>5 GB</b> - ⭐15 - Bronze, devices: 1"));
  assert.ok(store.text.includes("valid 30 days"));
  // The plans screen says what each plan has and how it is earned, and sells.
  await w.send(press(SARA, "p"));
  const plans = w.api.of("editMessageText").pop();
  for (const line of ["Bronze</b> - under 10 GB - 1 device", "Silver</b> - 10 GB and more - 2 devices", "Gold</b> - 100 GB and more - 4 devices", "VIP</b> - no data limit - unlimited devices", "VIP is given by our team", "• 25 GB - ⭐72 - Silver"]) assert.ok(plans.text.includes(line), line);
  assert.ok(labels(plans.reply_markup).includes("⭐ Buy data"));
  // The menu of commands has what Telegram asks of a bot that sells.
  await sweep(w.env, w.kit);
  assert.deepEqual(w.api.shown.menus.get('"all"').map((c) => c.command), ["start", "menu", "human", "buy", "terms", "paysupport", "close", "password", "language", "help"]);
  // A new rate makes new prices.
  await w.env.DB.prepare("INSERT INTO settings (key, value) VALUES ('stars_usd_toman', '540000')").run();
  await w.send(press(SARA, "s"));
  assert.deepEqual(labels(w.api.of("editMessageText").pop().reply_markup).slice(0, 5), ["5 GB · ⭐8", "10 GB · ⭐15", "25 GB · ⭐36", "50 GB · ⭐72", "100 GB · ⭐143"]);
});

test("the plans are explained in the bot whether or not data is on sale", async () => {
  const w = await shop({ on: false });
  await w.send(text(SARA, "/start plans"));
  const plans = w.lastText(SARA);
  assert.ok(plans.text.includes("it comes with your data") && plans.text.includes("Silver</b> - 10 GB and more") && plans.text.includes("ask where you got your account"));
  assert.ok(!labels(plans.reply_markup).includes("⭐ Buy data"));
});

test("the assistant is told when data is on sale, and sends people to the store instead of guessing prices", async () => {
  const on = await shop();
  await on.send(text(SARA, "How much is 20 GB?"));
  assert.ok(on.env.asked[0].messages[0].content.includes("Telegram Stars") && on.env.asked[0].messages[0].content.includes("You do not know the prices"));
  const off = await shop({ on: false });
  await off.send(text(SARA, "How much is 20 GB?"));
  assert.ok(!off.env.asked[0].messages[0].content.includes("Telegram Stars"));
});

// ---------- A new account ----------

test("a new account: the person picks, agrees, pays, and the account and its password arrive in the chat at once", async () => {
  const w = await shop();
  const id = await order(w, SARA, { gb: 10 });
  const screen = w.api.of("editMessageText").pop();
  assert.ok(screen.text.includes("<b>10 GB</b> - ⭐29") && screen.text.includes("A new account, made as soon as you pay") && screen.text.includes("By paying you agree to the terms"));
  assert.deepEqual(labels(screen.reply_markup), ["✅ I agree - pay ⭐29", "📄 Terms", "⬅ Packages"]);
  assert.equal(w.api.of("sendInvoice").length, 0, "nothing is billed before the person agrees");
  const { invoice, answer } = await pay(w, SARA, id);
  assert.deepEqual({ currency: invoice.currency, provider_token: invoice.provider_token, prices: invoice.prices, title: invoice.title, chat_id: invoice.chat_id }, { currency: "XTR", provider_token: "", prices: [{ label: "10 GB", amount: 29 }], title: "10 GB of data", chat_id: SARA });
  assert.match(invoice.payload, /^o\d+$/);
  assert.ok(invoice.description.includes("10 GB") && invoice.description.includes("valid 30 days") && invoice.description.includes("A new account"));
  assert.equal(answer.ok, true);
  const got = credentials(w, SARA);
  assert.ok(got, "the account and its password were sent");
  assert.match(got.username, /^tg\d{7}$/);
  assert.match(got.password, /^[A-Za-z0-9]{14}$/);
  assert.ok(got.text.includes("Payment <b>P1</b>: 10 GB for ⭐29") && got.text.includes("Silver, devices: 2") && got.text.includes("Save the password now"));
  // The account is what was bought, and the password opens it.
  const user = await accountOf(w, got.username);
  assert.equal(user.quota_bytes, 10 * GB);
  assert.equal(user.tier, "silver");
  assert.equal(user.reset_days, 0);
  assert.equal(user.disabled, 0);
  assert.equal(user.tg_owner, SARA, "the buyer is remembered, for a new password");
  assert.ok(Math.abs(user.expires_at - (now() + 30 * DAY)) < 10);
  assert.equal(user.note, "Stars P1");
  assert.equal(await verifyPassword(got.password, user.pass_hash), true);
  const payment = await w.row("SELECT * FROM stars_payments WHERE id = 1");
  assert.deepEqual({ kind: payment.kind, gb: payment.gb, stars: payment.stars, status: payment.status, username: payment.username, chat_id: payment.chat_id }, { kind: "new", gb: 10, stars: 29, status: "delivered", username: got.username, chat_id: SARA });
  assert.equal((await w.row("SELECT status FROM stars_orders WHERE id = ?1", id)).status, "paid");
  // The team hears of the sale; the numbers count it.
  assert.ok(w.lastText(TEAM).text.includes("Sale P1") && w.lastText(TEAM).text.includes("10 GB for ⭐29") && w.lastText(TEAM).text.includes(got.username));
  assert.equal((await w.row("SELECT n FROM tg_stats WHERE key = 'stars'")).n, 29);
  // Bigger sizes earn the plan.
  const gold = await order(w, 11, { gb: 100 });
  await pay(w, 11, gold);
  assert.equal((await accountOf(w, credentials(w, 11).username)).tier, "gold");
  const small = await order(w, 12, { gb: 5 });
  await pay(w, 12, small);
  assert.equal((await accountOf(w, credentials(w, 12).username)).tier, "bronze");
});

test("a payment Telegram reports twice gives one account", async () => {
  const w = await shop();
  const id = await order(w, SARA);
  await w.send(press(SARA, `sp:${id}`));
  const invoice = w.api.of("sendInvoice").pop();
  await w.send(checkoutOf(SARA, invoice.payload, 29));
  const update = paidBy(SARA, invoice.payload, 29, "same-charge");
  await w.send(update);
  await w.send({ ...update, update_id: ++uid });
  assert.equal((await w.row("SELECT COUNT(*) AS n FROM users WHERE tg_owner = ?1", SARA)).n, 1);
  assert.equal((await w.row("SELECT COUNT(*) AS n FROM stars_payments")).n, 1);
  assert.equal(w.api.of("sendMessage").filter((c) => c.chat_id === SARA && /Username: <code>/.test(c.text)).length, 1);
});

test("Telegram's question is answered no for an invoice that is not the person's, not for the amount, expired, already used, paused, or from a blocked person", async () => {
  const w = await shop();
  const id = await order(w, SARA);
  await w.send(press(SARA, `sp:${id}`));
  const invoice = w.api.of("sendInvoice").pop();
  const ask = async (update) => {
    await w.send(update);
    return w.api.of("answerPreCheckoutQuery").pop();
  };
  assert.equal((await ask(checkoutOf(999, invoice.payload, 29))).ok, false, "another person's order");
  assert.equal((await ask(checkoutOf(SARA, invoice.payload, 1))).ok, false, "another amount");
  assert.equal((await ask(checkoutOf(SARA, invoice.payload, 29, { currency: "USD" }))).ok, false, "another currency");
  assert.equal((await ask(checkoutOf(SARA, "o99999", 29))).ok, false, "no such order");
  assert.equal((await ask(checkoutOf(SARA, "nonsense", 29))).ok, false);
  assert.match((await ask(checkoutOf(SARA, "o99999", 29))).error_message, /not valid/);
  // Paused: the question is no, and the person is told why.
  await w.env.DB.prepare("UPDATE settings SET value = '0' WHERE key = 'stars_enabled'").run();
  assert.match((await ask(checkoutOf(SARA, invoice.payload, 29))).error_message, /paused/);
  await w.env.DB.prepare("UPDATE settings SET value = '1' WHERE key = 'stars_enabled'").run();
  // Old: an invoice an hour later is stale.
  await w.env.DB.prepare("UPDATE stars_orders SET created = ?1 WHERE id = ?2").bind(now() - 7200, id).run();
  assert.match((await ask(checkoutOf(SARA, invoice.payload, 29))).error_message, /expired/);
  await w.env.DB.prepare("UPDATE stars_orders SET created = ?1 WHERE id = ?2").bind(now(), id).run();
  // Right: yes once; a second invoice of the same order is turned away.
  assert.equal((await ask(checkoutOf(SARA, invoice.payload, 29))).ok, true);
  assert.match((await ask(checkoutOf(SARA, invoice.payload, 29))).error_message, /already paid, or is being paid/);
  // Left unpaid for five minutes, it may be tried again.
  await w.env.DB.prepare("UPDATE stars_orders SET updated = ?1 WHERE id = ?2").bind(now() - 400, id).run();
  assert.equal((await ask(checkoutOf(SARA, invoice.payload, 29))).ok, true);
  // A blocked person cannot buy.
  const other = await order(w, 13);
  await w.send(press(13, `sp:${other}`));
  const second = w.api.of("sendInvoice").pop();
  await w.env.DB.prepare("UPDATE tg_users SET blocked = 1 WHERE chat_id = 13").run();
  assert.match((await ask(checkoutOf(13, second.payload, 29))).error_message, /cannot buy here/);
});

test("an invoice is sent only for an order that is the person's and open, and the packages are limited per person", async () => {
  const w = await shop();
  const id = await order(w, SARA);
  await w.send(press(11, `sp:${id}`));
  assert.equal(w.api.of("sendInvoice").length, 0, "somebody else's order");
  assert.ok(w.lastText(11).text.startsWith("That order is no longer open"));
  await w.send(press(SARA, "sp:12345"));
  assert.equal(w.api.of("sendInvoice").length, 0);
  for (let i = 0; i < 12; i++) await w.send(press(SARA, "sn:5"));
  assert.equal((await w.row("SELECT COUNT(*) AS n FROM stars_orders WHERE chat_id = ?1", SARA)).n, 10, "ten an hour");
});

// ---------- Recharging ----------

test("data is added to an account: its username is asked, what is left stays, the days start again, and a plan is never taken away", async () => {
  const w = await shop();
  const t = now();
  await w.env.DB.prepare("INSERT INTO users (username, pass_hash, created_at, quota_bytes, expires_at, period_start, used_bytes, tier) VALUES ('ali', 'x', ?1, ?2, ?3, ?1, ?4, 'bronze')").bind(t, 5 * GB, t + 10 * DAY, 2 * GB).run();
  await w.send(press(SARA, "s:10"));
  await w.send(press(SARA, "sr:10"));
  assert.ok(w.api.of("editMessageText").pop().text.includes("Send me the account's <b>username</b>"));
  assert.equal((await w.row("SELECT mode, device FROM tg_users WHERE chat_id = ?1", SARA)).mode, "topup");
  const id = await order(w, SARA, { gb: 10, target: "ALI" });
  assert.ok(w.lastText(SARA).text.includes("Added to the account <code>ali</code>"));
  assert.equal((await w.row("SELECT mode FROM tg_users WHERE chat_id = ?1", SARA)).mode, "");
  await pay(w, SARA, id);
  const ali = await accountOf(w, "ali");
  assert.equal(ali.quota_bytes, 13 * GB, "3 GB left and 10 GB bought");
  assert.equal(ali.used_bytes, 0);
  assert.equal(ali.tier, "silver", "13 GB earns Silver");
  assert.ok(Math.abs(ali.expires_at - (t + 10 * DAY + 30 * DAY)) < 10, "thirty days on top of what was left");
  assert.equal(ali.tg_owner, 0, "the one who recharged does not own the account");
  const receipt = w.api.of("sendMessage").filter((c) => c.chat_id === SARA).pop();
  assert.ok(receipt.text.includes("<code>ali</code> now has <b>13 GB</b>") && !/Password/.test(receipt.text));
  // An account that ended starts again from what was bought.
  await w.env.DB.prepare("INSERT INTO users (username, pass_hash, created_at, quota_bytes, expires_at, period_start, used_bytes, tier) VALUES ('old', 'x', ?1, ?2, ?3, ?1, 0, 'gold')").bind(t, 50 * GB, t - DAY).run();
  const again = await order(w, SARA, { gb: 5, target: "old" });
  await pay(w, SARA, again);
  const old = await accountOf(w, "old");
  assert.equal(old.quota_bytes, 5 * GB, "the data of an account that has ended is gone");
  assert.ok(Math.abs(old.expires_at - (now() + 30 * DAY)) < 10);
  assert.equal(old.tier, "gold", "a plan the account had stays");
});

test("an account that cannot be recharged here, one that does not exist and a username that is none are each told", async () => {
  const w = await shop();
  const t = now();
  const add = (name, quota, extra = "") => w.env.DB.prepare(`INSERT INTO users (username, pass_hash, created_at, quota_bytes, period_start ${extra ? ", " + extra.split("=")[0] : ""}) VALUES (?1, 'x', ?2, ?3, ?2 ${extra ? ", " + extra.split("=")[1] : ""})`).bind(name, t, quota).run();
  await add("unlimited", 0);
  await add("renews", 5 * GB, "reset_days=30");
  await add("off", 5 * GB, "disabled=1");
  await w.send(press(SARA, "s:10"));
  await w.send(press(SARA, "sr:10"));
  for (const [name, said] of [["unlimited", "cannot be recharged here"], ["renews", "cannot be recharged here"], ["off", "cannot be recharged here"], ["nobody", "cannot find an account"], ["no good!", "does not look like a username"]]) {
    await w.send(text(SARA, name));
    assert.ok(w.lastText(SARA).text.includes(said), `${name}: ${w.lastText(SARA).text}`);
  }
  assert.equal((await w.row("SELECT COUNT(*) AS n FROM stars_orders")).n, 0, "no order for any of them");
  assert.equal((await w.row("SELECT mode FROM tg_users WHERE chat_id = ?1", SARA)).mode, "topup", "still waiting for a username that fits");
  // Asking after usernames is limited, so nobody can list the accounts.
  for (let i = 0; i < 12; i++) await w.send(text(SARA, `guess${i}`));
  assert.ok(w.lastText(SARA).text.includes("That is a lot of usernames"));
  // A command ends the wait; a button leaves the store.
  await w.send(text(SARA, "/menu"));
  assert.equal((await w.row("SELECT mode FROM tg_users WHERE chat_id = ?1", SARA)).mode, "");
});

// ---------- When it goes wrong ----------

test("a payment that cannot be delivered is given back at once, and the buyer and the team are told", async () => {
  const w = await shop();
  const t = now();
  await w.env.DB.prepare("INSERT INTO users (username, pass_hash, created_at, quota_bytes, expires_at, period_start) VALUES ('ali', 'x', ?1, ?2, 0, ?1)").bind(t, 5 * GB).run();
  const id = await order(w, SARA, { gb: 10, target: "ali" });
  await w.send(press(SARA, `sp:${id}`));
  const invoice = w.api.of("sendInvoice").pop();
  await w.send(checkoutOf(SARA, invoice.payload, 29));
  // The account is switched off between the question and the payment.
  await w.env.DB.prepare("UPDATE users SET disabled = 1 WHERE username = 'ali'").run();
  await w.send(paidBy(SARA, invoice.payload, 29, "failing-charge"));
  const refund = w.api.of("refundStarPayment").pop();
  assert.deepEqual({ user_id: refund.user_id, telegram_payment_charge_id: refund.telegram_payment_charge_id }, { user_id: SARA, telegram_payment_charge_id: "failing-charge" });
  assert.equal((await w.row("SELECT status FROM stars_payments WHERE charge_id = 'failing-charge'")).status, "refunded");
  assert.ok(w.lastText(SARA).text.includes("Your payment P1 (⭐29) was refunded") && w.lastText(SARA).text.includes("the Stars were given back"));
  assert.ok(w.lastText(TEAM).text.includes("Payment P1") && w.lastText(TEAM).text.includes("was refunded"));
  assert.equal((await accountOf(w, "ali")).quota_bytes, 5 * GB, "nothing was added");
});

test("when even the refund is refused, the payment is marked and the team is told to do it by hand", async () => {
  const w = await shop();
  const t = now();
  await w.env.DB.prepare("INSERT INTO users (username, pass_hash, created_at, quota_bytes, expires_at, period_start) VALUES ('ali', 'x', ?1, ?2, 0, ?1)").bind(t, 5 * GB).run();
  const id = await order(w, SARA, { gb: 10, target: "ali" });
  await w.send(press(SARA, `sp:${id}`));
  const invoice = w.api.of("sendInvoice").pop();
  await w.send(checkoutOf(SARA, invoice.payload, 29));
  await w.env.DB.prepare("UPDATE users SET disabled = 1 WHERE username = 'ali'").run();
  const api = telegram({ refundStarPayment: { ok: false, error_code: 400, description: "Bad Request: CHARGE_ALREADY_REFUNDED" } });
  await w.send(paidBy(SARA, invoice.payload, 29, "stuck"));
  assert.equal((await w.row("SELECT status FROM stars_payments WHERE charge_id = 'stuck'")).status, "failed");
  const warning = api.of("sendMessage").filter((c) => c.chat_id === TEAM).pop();
  assert.ok(warning.text.includes("could NOT be refunded") && warning.text.includes("/refund P1"), warning.text);
});

test("a payment that matches no order is refunded and the team is told", async () => {
  const w = await shop();
  await w.send(paidBy(SARA, "o424242", 29, "mystery"));
  assert.equal(w.api.of("refundStarPayment").pop().telegram_payment_charge_id, "mystery");
  assert.ok(w.lastText(TEAM).text.includes("matched no order") && w.lastText(TEAM).text.includes("was refunded"));
  assert.equal((await w.row("SELECT COUNT(*) AS n FROM stars_payments")).n, 0);
});

test("a payment whose handling breaks before it is noted is given back at once, not lost", async () => {
  const w = await shop();
  const id = await order(w, SARA);
  await w.send(press(SARA, `sp:${id}`));
  const invoice = w.api.of("sendInvoice").pop();
  await w.send(checkoutOf(SARA, invoice.payload, 29));
  const prepare = w.env.DB.prepare;
  w.env.DB.prepare = (sql) => {
    if (/INSERT INTO stars_payments/.test(sql)) throw new Error("the database is away");
    return prepare(sql);
  };
  const error = console.error;
  console.error = () => {};
  try {
    await w.send(paidBy(SARA, invoice.payload, 29, "lost-charge"));
  } finally {
    console.error = error;
    w.env.DB.prepare = prepare;
  }
  assert.deepEqual({ user_id: w.api.of("refundStarPayment")[0].user_id, charge: w.api.of("refundStarPayment")[0].telegram_payment_charge_id }, { user_id: SARA, charge: "lost-charge" });
  assert.ok(w.lastText(SARA).text.includes("the Stars were given back"));
  assert.ok(w.lastText(TEAM).text.includes("could not be processed") && w.lastText(TEAM).text.includes("was refunded") && w.lastText(TEAM).text.includes("lost-charge"));
  assert.equal((await w.row("SELECT COUNT(*) AS n FROM users WHERE tg_owner = ?1", SARA)).n, 0, "no account for a payment that was given back");
});

test("a payment paid but never delivered, left for minutes, is given back by the schedule; recent ones are left", async () => {
  const w = await shop();
  const t = now();
  const put = (charge, updated) => w.env.DB.prepare("INSERT INTO stars_payments (charge_id, chat_id, order_id, kind, gb, stars, status, created, updated) VALUES (?1, ?2, 1, 'new', 10, 29, 'paid', ?3, ?3)").bind(charge, SARA, updated).run();
  await put("recent", t - 30);
  await put("left", t - 600);
  await sweep(w.env, w.kit);
  assert.deepEqual(w.api.of("refundStarPayment").map((c) => c.telegram_payment_charge_id), ["left"]);
  assert.equal((await w.row("SELECT status FROM stars_payments WHERE charge_id = 'recent'")).status, "paid");
});

test("orders nobody paid are forgotten after a day, and records of payments after two years", async () => {
  const w = await shop();
  const t = now();
  await w.env.DB.prepare("INSERT INTO stars_orders (chat_id, kind, gb, stars, days, created, updated) VALUES (1, 'new', 5, 15, 30, ?1, ?1), (2, 'new', 5, 15, 30, ?2, ?2)").bind(t - 2 * DAY, t - 3600).run();
  await w.env.DB.prepare("INSERT INTO stars_payments (charge_id, chat_id, order_id, kind, gb, stars, status, created, updated) VALUES ('ancient', 1, 9, 'new', 5, 15, 'delivered', ?1, ?1), ('fresh', 1, 9, 'new', 5, 15, 'delivered', ?2, ?2)").bind(t - 800 * DAY, t - DAY).run();
  await sweep(w.env, w.kit);
  assert.deepEqual((await w.rows("SELECT chat_id FROM stars_orders")).map((r) => r.chat_id), [2]);
  assert.deepEqual((await w.rows("SELECT charge_id FROM stars_payments")).map((r) => r.charge_id), ["fresh"]);
});

// ---------- Refunds, sales, a test ----------

test("the team gives a payment back: the buyer is told, a new account is switched off, data added is taken off again, and it is done once", async () => {
  const w = await shop();
  const t = now();
  const id = await order(w, SARA, { gb: 10 });
  await pay(w, SARA, id);
  const bought = credentials(w, SARA).username;
  await w.env.DB.prepare("INSERT INTO users (username, pass_hash, created_at, quota_bytes, expires_at, period_start, used_bytes) VALUES ('ali', 'x', ?1, ?2, 0, ?1, ?3)").bind(t, 5 * GB, 1 * GB).run();
  const top = await order(w, 11, { gb: 25, target: "ali" });
  await pay(w, 11, top);
  assert.equal((await accountOf(w, "ali")).quota_bytes, 29 * GB, "4 GB left and 25 GB bought");
  w.api.calls.length = 0;
  await w.send(text(TEAM, "/refund P1"));
  assert.equal(w.api.of("refundStarPayment")[0].user_id, SARA);
  assert.equal((await accountOf(w, bought)).disabled, 1);
  assert.ok(w.api.of("sendMessage").filter((c) => c.chat_id === SARA).pop().text.includes("Our team refunded it"));
  assert.ok(w.lastText(TEAM).text.includes("P1 refunded (⭐29)") && w.lastText(TEAM).text.includes("is switched off"));
  await w.send(text(TEAM, "/refund P2"));
  assert.equal((await accountOf(w, "ali")).quota_bytes, 4 * GB, "the 25 GB are gone, what was left is not");
  await w.send(text(TEAM, "/refund P1"));
  assert.ok(w.lastText(TEAM).text.includes("already refunded"));
  assert.equal(w.api.of("refundStarPayment").length, 2, "once each");
  await w.send(text(TEAM, "/refund P99"));
  assert.ok(w.lastText(TEAM).text.includes("There is no payment P99"));
  await w.send(text(TEAM, "/refund nonsense"));
  assert.ok(w.lastText(TEAM).text.includes("Say which payment"));
  // The person cannot ask for it.
  await w.send(text(SARA, "/refund P1"));
  assert.ok(w.lastText(SARA).text.startsWith("I do not know that command"));
});

test("a refund Telegram reports on its own takes back what the payment gave", async () => {
  const w = await shop();
  const id = await order(w, SARA, { gb: 10 });
  await pay(w, SARA, id);
  const bought = credentials(w, SARA).username;
  const charge = (await w.row("SELECT charge_id FROM stars_payments WHERE id = 1")).charge_id;
  await w.send({ update_id: ++uid, message: { message_id: uid, from: { id: SARA, first_name: "Sara" }, chat: { id: SARA, type: "private" }, refunded_payment: { currency: "XTR", total_amount: 29, invoice_payload: "o1", telegram_payment_charge_id: charge } } });
  assert.equal((await accountOf(w, bought)).disabled, 1);
  assert.equal((await w.row("SELECT status FROM stars_payments WHERE id = 1")).status, "refunded");
  assert.ok(w.lastText(TEAM).text.includes("Telegram says <b>payment P1</b>"));
});

test("/sales shows today's numbers and the last payments to the team only", async () => {
  const w = await shop();
  const id = await order(w, SARA, { gb: 10 });
  await pay(w, SARA, id);
  await w.send(text(TEAM, "/sales"));
  const report = w.lastText(TEAM).text;
  assert.ok(report.includes("on sale") && report.includes("1 sold for ⭐29") && report.includes("<b>P1</b> - 10 GB ⭐29") && report.includes("delivered"), report);
  await w.send(text(SARA, "/sales"));
  assert.ok(w.lastText(SARA).text.startsWith("I do not know that command"));
});

test("/sales shows the bot's Stars balance as Telegram reports it", async () => {
  const w = await shop();
  w.api.overrides.getMyStarBalance = { ok: true, result: { amount: 1234, nanostar_amount: 0 } };
  await w.send(text(TEAM, "/sales"));
  assert.ok(w.lastText(TEAM).text.includes("The bot's balance: ⭐1234"), w.lastText(TEAM).text);
});

test("/teststars sends an invoice of one Star that makes no account, works while selling is off, and can be given back", async () => {
  const w = await shop({ on: false });
  await w.send(text(TEAM, "/teststars"));
  const invoice = w.api.of("sendInvoice").pop();
  assert.deepEqual({ chat_id: invoice.chat_id, title: invoice.title, prices: invoice.prices, currency: invoice.currency }, { chat_id: TEAM, title: "Test payment", prices: [{ label: "Test", amount: 1 }], currency: "XTR" });
  await w.send(checkoutOf(TEAM, invoice.payload, 1));
  assert.equal(w.api.of("answerPreCheckoutQuery").pop().ok, true);
  await w.send(paidBy(TEAM, invoice.payload, 1));
  assert.ok(w.lastText(TEAM).text.includes("Test payment received") && w.lastText(TEAM).text.includes("/refund P1"));
  assert.equal((await w.row("SELECT COUNT(*) AS n FROM users")).n, 0, "no account for a test");
  await w.send(text(TEAM, "/refund P1"));
  assert.equal(w.api.of("refundStarPayment").length, 1);
  // Nobody else can start one.
  await w.send(text(SARA, "/teststars"));
  assert.equal(w.api.of("sendInvoice").length, 1);
});

// ---------- A new password for the buyer ----------

test("the buyer of an account can get a new password in the bot, from the same Telegram account only", async () => {
  const w = await shop();
  const id = await order(w, SARA, { gb: 10 });
  await pay(w, SARA, id);
  const got = credentials(w, SARA);
  const before = await accountOf(w, got.username);
  await w.env.DB.prepare("INSERT INTO devices (user_id, device_id, name, first_seen, last_seen) VALUES (?1, 'phone', 'Phone', 1, 1)").bind(before.id).run();
  // Not the buyer: nothing to reset, and the account's button does nothing for them.
  await w.send(text(11, "/password"));
  assert.ok(w.lastText(11).text.includes("this Telegram account bought in this chat"));
  await w.send(press(11, `py:${got.username}`));
  assert.ok(w.api.of("editMessageText").pop().text.includes("this Telegram account bought in this chat"));
  assert.equal(await verifyPassword(got.password, (await accountOf(w, got.username)).pass_hash), true, "untouched");
  // The buyer: asked to confirm first, then the new password, once.
  await w.send(text(SARA, "/password"));
  const confirm = w.lastText(SARA);
  assert.ok(confirm.text.includes(got.username) && confirm.text.includes("signed out"));
  assert.deepEqual(datas(confirm.reply_markup).slice(0, 1), [`py:${got.username}`]);
  assert.equal(await verifyPassword(got.password, (await accountOf(w, got.username)).pass_hash), true, "asking changes nothing");
  await w.send(press(SARA, `py:${got.username}`));
  const shown = w.api.of("editMessageText").pop().text;
  const fresh = /Password: <code>([^<]+)</.exec(shown)[1];
  assert.notEqual(fresh, got.password);
  assert.match(fresh, /^[A-Za-z0-9]{14}$/);
  const after = await accountOf(w, got.username);
  assert.equal(await verifyPassword(fresh, after.pass_hash), true);
  assert.equal(await verifyPassword(got.password, after.pass_hash), false, "the old password is gone");
  assert.equal(after.token_version, before.token_version + 1, "every session ends");
  assert.equal((await w.row("SELECT COUNT(*) AS n FROM devices WHERE user_id = ?1", before.id)).n, 0, "every device is signed out");
  // A few times a day.
  await w.send(press(SARA, `py:${got.username}`));
  await w.send(press(SARA, `py:${got.username}`));
  await w.send(press(SARA, `py:${got.username}`));
  assert.ok(w.lastText(SARA).text.startsWith("A new password was made for you several times today"));
});

test("whoever recharged an account does not own it, and an account that was refunded cannot be asked for", async () => {
  const w = await shop();
  const t = now();
  await w.env.DB.prepare("INSERT INTO users (username, pass_hash, created_at, quota_bytes, expires_at, period_start) VALUES ('ali', 'x', ?1, ?2, 0, ?1)").bind(t, 5 * GB).run();
  const top = await order(w, SARA, { gb: 5, target: "ali" });
  await pay(w, SARA, top);
  await w.send(text(SARA, "/password"));
  assert.ok(w.lastText(SARA).text.includes("this Telegram account bought in this chat"), "a gift is not a key");
  const id = await order(w, 11, { gb: 5 });
  await pay(w, 11, id);
  const mine = credentials(w, 11).username;
  await w.send(text(TEAM, "/refund P2"));
  await w.send(text(11, "/password"));
  assert.ok(w.lastText(11).text.includes("this Telegram account bought in this chat"), `${mine} is switched off`);
  // Several accounts: pick one. The Account screen leads here too.
  await w.env.DB.prepare("UPDATE users SET disabled = 0 WHERE username = ?1").bind(mine).run();
  const second = await order(w, 11, { gb: 5 });
  await pay(w, 11, second);
  await w.send(text(11, "/password"));
  assert.ok(w.lastText(11).text.includes("Which account?") && labels(w.lastText(11).reply_markup).length === 3);
  await w.send(press(11, "a"));
  assert.ok(labels(w.api.of("editMessageText").pop().reply_markup).includes("🔑 Forgot password"));
});

// ---------- The webhook asks for payments ----------

test("the webhook is set again, with the updates payments need, when a deploy wants more than the last one set", async () => {
  const w = await shop();
  await sweep(w.env, w.kit);
  assert.equal(w.api.of("setWebhook").length, 0, "right already: left alone");
  await w.env.DB.prepare("UPDATE settings SET value = '{}' WHERE key = 'telegram_hook'").run();
  const api = telegram();
  await sweep(w.env, w.kit);
  const hook = api.of("setWebhook")[0];
  assert.deepEqual(hook.allowed_updates, ["message", "callback_query", "pre_checkout_query"]);
  assert.equal(hook.url, "https://accounts.albatrossvpn.online/api/v1/telegram");
  assert.match(hook.secret_token, /^[A-Za-z0-9_-]{43}$/);
  assert.equal(hook.drop_pending_updates, undefined, "waiting updates are kept");
  await sweep(w.env, w.kit);
  assert.equal(api.of("setWebhook").length, 1);
});
