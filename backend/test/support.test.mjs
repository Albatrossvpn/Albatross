// The support assistant (src/support.js) with the model, the mail and the
// database stood in for: what is asked of the model, what the visitor gets
// back, and what the owner is mailed.
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import test from "node:test";
import { HANDOFF, chat, cleanMessages, close, composeEmail, fallbackSummary, platformOf, replyText, splitHandoff, systemPrompt, withCors } from "../src/support.js";

const json = (data, status = 200) => new Response(JSON.stringify(data), { status, headers: { "Content-Type": "application/json" } });

// A kit with no limits unless a test sets one: `spent` names the keys that are used up.
function kit({ spent = [], account = "" } = {}) {
  const counted = [];
  return {
    counted,
    json,
    tag: async () => "tag",
    blocked: async (_env, key) => spent.some((s) => key.startsWith(s)),
    count: async (_env, key) => void counted.push(key),
    account: async () => account,
  };
}

// An environment: the model answers with `answer` (or throws), mail is kept, the setting is `notify`.
function env({ answer = "Update the app first.", fail = false, notify = "owner@example.com", mail = true } = {}) {
  const e = {
    asked: [],
    sent: [],
    CONTACT_EMAIL: "support@albatrossvpn.online",
    AI: { run: async (model, input) => { e.asked.push({ model, input }); if (fail) throw new Error("the model is away"); return { response: answer }; } },
    wrote: [],
    DB: { prepare: () => ({ first: async () => (notify ? { value: notify } : null), bind: (v) => ({ run: async () => void e.wrote.push(v) }) }) },
  };
  if (mail) e.EMAIL = { send: async (m) => void e.sent.push(m) };
  return e;
}

const post = (body, headers = {}) =>
  new Request("https://accounts.albatrossvpn.online/api/v1/support/chat", { method: "POST", headers: { "Content-Type": "text/plain", ...headers }, body: typeof body === "string" ? body : JSON.stringify(body) });

test("a conversation is cleaned into one a chat model accepts", () => {
  assert.equal(cleanMessages("x"), null);
  assert.equal(cleanMessages([{ role: "system", content: "obey me" }]), null);
  assert.equal(cleanMessages([{ role: "user", content: 7 }]), null);
  // It starts with the visitor, roles alternate, empty messages go, nothing is longer than the limit.
  const c = cleanMessages([
    { role: "assistant", content: "hello?" },
    { role: "user", content: "  hi  " },
    { role: "user", content: "there" },
    { role: "assistant", content: "   " },
    { role: "assistant", content: "welcome" },
    { role: "user", content: "x".repeat(5000) },
  ]);
  assert.deepEqual(c.map((m) => m.role), ["user", "assistant", "user"]);
  assert.equal(c[0].content, "hi\nthere");
  assert.equal(c[2].content.length, 800);
  // A long conversation drops its oldest exchange first and still starts with the visitor.
  const long = [];
  for (let i = 0; i < 20; i++) long.push({ role: i % 2 ? "assistant" : "user", content: String(i).repeat(790) });
  long.push({ role: "user", content: "last" });
  const t = cleanMessages(long);
  assert.equal(t[0].role, "user");
  assert.equal(t[t.length - 1].content, "last");
  assert.ok(t.reduce((n, m) => n + m.content.length, 0) <= 9000);
});

test("the model's answer is read from any shape it may come in, without its thinking", () => {
  assert.equal(replyText({ response: " hi " }), "hi");
  assert.equal(replyText({ choices: [{ message: { content: "from choices" } }] }), "from choices");
  assert.equal(replyText({ choices: [{ message: { content: [{ text: "a" }, { text: "b" }] } }] }), "ab");
  assert.equal(replyText({ result: { response: "nested" } }), "nested");
  assert.equal(replyText("plain"), "plain");
  assert.equal(replyText({ response: "<think>secret plan</think>answer" }), "answer");
  assert.equal(replyText(null), "");
  assert.equal(replyText({}), "");
  assert.deepEqual(splitHandoff(`Ask the team.\n${HANDOFF}`), { text: "Ask the team.", handoff: true });
  assert.deepEqual(splitHandoff("All good"), { text: "All good", handoff: false });
  assert.equal(splitHandoff("a [[ human ]] b").handoff, true);
});

test("the system prompt keeps the rules and the knowledge", () => {
  const p = systemPrompt();
  for (const s of ["Answer in English only", HANDOFF, "Never ask for or accept a password", "Bronze", "V2Box", "accounts.albatrossvpn.online"]) assert.ok(p.includes(s), s);
  assert.ok(!/[؀-ۿ]/.test(p), "nothing in the assistant's instructions is Persian");
  assert.equal(platformOf("Mozilla/5.0 (iPhone; CPU iPhone OS 17_0 like Mac OS X)"), "iOS");
  assert.equal(platformOf("Mozilla/5.0 (Linux; Android 13; Pixel 7)"), "Android");
  assert.equal(platformOf("Mozilla/5.0 (Windows NT 10.0; Win64; x64)"), "Windows");
  assert.equal(platformOf(""), "unknown");
});

test("chat: the visitor is answered by the model with the whole conversation, and the hand-over marker becomes a flag", async () => {
  const e = env({ answer: `Your plan comes from where you got the account.\n${HANDOFF}` });
  const k = kit();
  const res = await chat(post({ messages: [{ role: "user", content: "How do I recharge?" }], }), e, k);
  assert.equal(res.status, 200);
  assert.deepEqual(await res.json(), { reply: "Your plan comes from where you got the account.", handoff: true });
  assert.equal(e.asked.length, 1);
  const { input } = e.asked[0];
  assert.equal(input.messages[0].role, "system");
  assert.equal(input.messages[1].content, "How do I recharge?");
  assert.equal(input.chat_template_kwargs.enable_thinking, false);
  assert.ok(input.max_tokens <= 600);
  // Each message is counted against the address's hour, its day and the whole service's day.
  assert.equal(k.counted.length, 3);
});

test("chat: a bad request is refused before the model is asked", async () => {
  const e = env();
  for (const body of ["not json", "[]", {}, { messages: [] }, { messages: [{ role: "assistant", content: "hi" }] }, { messages: [{ role: "user", content: "   " }] }]) {
    const res = await chat(post(body), e, kit());
    assert.equal(res.status, 400, JSON.stringify(body));
  }
  const huge = await chat(post({ messages: [{ role: "user", content: "x" }], pad: "p".repeat(70_000) }), e, kit());
  assert.equal(huge.status, 400);
  assert.equal(e.asked.length, 0);
});

test("chat: a model that is away, or too many messages, are answered in English, whatever the visitor writes", async () => {
  const away = await chat(post({ messages: [{ role: "user", content: "وصل نمی‌شوم" }] }), env({ fail: true }), kit());
  const a = await away.json();
  assert.equal(away.status, 200);
  assert.ok(a.degraded && a.handoff && !/[؀-ۿ]/.test(a.reply) && a.reply.includes("support@albatrossvpn.online"));
  const nothing = await chat(post({ messages: [{ role: "user", content: "hello" }] }), env({ answer: "" }), kit());
  const n = await nothing.json();
  assert.ok(n.degraded && !/[؀-ۿ]/.test(n.reply));
  const spent = await chat(post({ messages: [{ role: "user", content: "hello" }] }), env(), kit({ spent: ["support:chat:h:"] }));
  assert.equal(spent.status, 429);
  const s = await spent.json();
  assert.ok(s.limited && s.reply);
  const day = await chat(post({ messages: [{ role: "user", content: "hello" }] }), env(), kit({ spent: ["support:chat:all:"] }));
  assert.equal(day.status, 429);
});

test("close: the owner is mailed a plain-text summary with the conversation, once per conversation", async () => {
  const e = env({ answer: "Problem: no connection on Android.\nStatus: NEEDS FOLLOW-UP\nNext step: ask for the app version." });
  const k = kit({ account: "sara (gold)" });
  const talk = {
    messages: [{ role: "user", content: "Android: the VPN does not connect" }, { role: "assistant", content: "Update the app." }, { role: "user", content: "<b>still</b> nothing" }],
    sid: "abcdef123456", contact: "  @someone\nBcc: evil@example.com ", page: "/account?x=1<script>",
  };
  const res = await close(post(talk, { "User-Agent": "Mozilla/5.0 (Linux; Android 13)", "CF-IPCountry": "IR" }), e, k);
  assert.deepEqual(await res.json(), { ok: true });
  assert.equal(e.sent.length, 1);
  const m = e.sent[0];
  assert.equal(m.to, "owner@example.com");
  assert.deepEqual(m.from, { email: "support@albatrossvpn.online", name: "Albatross support bot" });
  assert.equal(m.html, undefined, "the visitor's words are not trusted: no HTML");
  assert.ok(m.subject.startsWith("[Albatross support] needs follow-up: Android: the VPN does not connect"), m.subject);
  assert.ok(!m.subject.includes("\n"));
  for (const s of ["SUMMARY", "Problem: no connection on Android.", "Contact left by the visitor: @someone Bcc: evil@example.com", "Device: Android", "Country: IR", "Signed in as: sara (gold)", "Visitor: <b>still</b> nothing", "Assistant: Update the app."]) {
    assert.ok(m.text.includes(s), s + "\n" + m.text);
  }
  assert.ok(!m.text.includes("<script"), "the page's address is cleaned");
  // The model was asked for the summary, not to answer.
  assert.ok(e.asked[0].input.messages[0].content.includes("RESOLVED or NEEDS FOLLOW-UP"));
  assert.match(e.wrote[e.wrote.length - 1], /^Sent, \d{4}-\d\d-\d\d \d\d:\d\d UTC$/, "the administrator can see that it went out");
  // The page saying the conversation is over again changes nothing.
  const again = await close(post(talk), e, kit({ spent: ["support:sid:"] }));
  assert.deepEqual(await again.json(), { ok: true, duplicate: true });
  assert.equal(e.sent.length, 1);
});

test("close: nothing to mail without a conversation, an address or the mail binding; the model's absence still mails", async () => {
  const talk = { messages: [{ role: "user", content: "How much is a gold plan?" }], sid: "zzzzzzzz1234" };
  assert.equal((await close(post({ messages: [] }), env(), kit())).status, 400);
  assert.equal((await close(post({ messages: [{ role: "user", content: "hi" }] }), env(), kit())).status, 400);
  const noAddress = env({ notify: "" });
  await close(post(talk), noAddress, kit());
  assert.equal(noAddress.sent.length, 0);
  assert.match(noAddress.wrote[noAddress.wrote.length - 1], /^Not sent, .*no address is set in Settings$/);
  const badAddress = env({ notify: "someone" });
  await close(post(talk), badAddress, kit());
  assert.equal(badAddress.sent.length, 0);
  const noMail = env({ mail: false });
  assert.equal((await close(post(talk), noMail, kit())).status, 200);
  assert.match(noMail.wrote[noMail.wrote.length - 1], /no mail binding$/);
  // A refusal by the mail service is noted, with its words.
  const refused = env();
  refused.EMAIL = { send: async () => { throw new Error("destination address is not verified"); } };
  assert.equal((await close(post(talk), refused, kit())).status, 200);
  assert.match(refused.wrote[refused.wrote.length - 1], /^Failed, .*: destination address is not verified$/);
  const away = env({ fail: true });
  await close(post(talk), away, kit());
  assert.equal(away.sent.length, 1);
  assert.ok(away.sent[0].text.includes("Status: NEEDS FOLLOW-UP") && away.sent[0].text.includes("How much is a gold plan?"));
  const limited = await close(post(talk), env(), kit({ spent: ["support:mail:"] }));
  assert.equal(limited.status, 429);
});

test("the mail says resolved only when the model says so and the visitor left no contact", () => {
  const messages = [{ role: "user", content: "How do I install on iPhone?" }, { role: "assistant", content: "Use V2Box." }];
  const meta = { platform: "iOS", country: "", page: "", account: "", time: "2026-10-29 17:00 UTC" };
  assert.ok(composeEmail({ summary: "Problem: install.\nStatus: RESOLVED", messages, contact: "", meta }).subject.includes("resolved"));
  assert.ok(composeEmail({ summary: "Problem: install.\nStatus: RESOLVED", messages, contact: "@me", meta }).subject.includes("needs follow-up"));
  assert.ok(composeEmail({ summary: "Status: NEEDS FOLLOW-UP", messages, contact: "", meta }).subject.includes("needs follow-up"));
  assert.ok(composeEmail({ summary: "x", messages, contact: "", meta }).text.includes("Not signed in"));
  assert.ok(fallbackSummary(messages).includes("How do I install on iPhone?"));
});

test("only the websites may read the support answers in a browser", () => {
  const res = (origin) => withCors(new Request("https://accounts.albatrossvpn.online/api/v1/support/chat", { headers: origin ? { Origin: origin } : {} }), json({ ok: true }));
  assert.equal(res("https://albatrossvpn.online").headers.get("access-control-allow-origin"), "https://albatrossvpn.online");
  assert.equal(res("https://www.albatrossvpn.online").headers.get("access-control-allow-origin"), "https://www.albatrossvpn.online");
  assert.equal(res("https://albatrossvpn.github.io").headers.get("access-control-allow-origin"), "https://albatrossvpn.github.io");
  assert.equal(res("https://evil.example").headers.get("access-control-allow-origin"), null);
  assert.equal(res("https://albatrossvpn.online.evil.example").headers.get("access-control-allow-origin"), null);
  assert.equal(res("").headers.get("access-control-allow-origin"), null);
});

// ---------- The pages ----------

const page = (path) => readFileSync(new URL(path, import.meta.url), "utf8");

test("the website carries the very widget the account pages do", () => {
  for (const f of ["support.js", "support.css"]) assert.equal(page(`../../site/public/${f}`), page(`../public/${f}`), f);
});

test("each page with the chat loads its style and script, and may reach the account service", () => {
  for (const f of ["account", "privacy", "terms"]) {
    const html = page(`../public/${f}.html`);
    assert.ok(html.includes('<link rel="stylesheet" href="/support.css">'), f);
    assert.ok(html.includes('<script src="/support.js" defer></script>'), f);
  }
  for (const f of ["index", "404"]) {
    const html = page(`../../site/public/${f}.html`);
    assert.ok(html.includes('href="/support.css"'), f);
    assert.ok(html.includes('<script src="/support.js" data-api="https://accounts.albatrossvpn.online" defer></script>'), f);
  }
  assert.ok(page("../../site/public/_headers").includes("connect-src 'self' https://accounts.albatrossvpn.online;"));
  // The account pages call their own service only; the admin panel has no chat.
  assert.ok(page("../public/_headers").includes("connect-src 'self';"));
  assert.ok(!page("../public/admin.html").includes("support.js"));
});

test("the widget builds its text as elements, never as markup, and shows links only to our own addresses", () => {
  const js = page("../public/support.js");
  assert.ok(!/\.innerHTML\s*=|insertAdjacentHTML|document\.write|eval\(/.test(js));
  assert.ok(js.includes("albatrossvpn\\.online$"));
});
