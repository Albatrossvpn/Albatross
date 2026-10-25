import assert from "node:assert/strict";
import test from "node:test";
import { DAY, accountOf, checkPassword, checkUsername, hashPassword, randomPassword, rollPeriod, safeEqual, sign, verify, verifyPassword } from "../src/lib.js";

test("passwords are salted and verified", async () => {
  const a = await hashPassword("correct horse");
  const b = await hashPassword("correct horse");
  assert.notEqual(a, b, "the same password must hash differently");
  assert.ok(a.startsWith("pbkdf2$100000$"));
  assert.equal(await verifyPassword("correct horse", a), true);
  assert.equal(await verifyPassword("Correct horse", a), false);
  assert.equal(await verifyPassword("anything", "plain$1$2$3"), false);
});

test("tokens carry a payload, expire and cannot be altered", async () => {
  const secret = "s".repeat(32);
  const t = await sign({ u: 7, exp: 2000 }, secret);
  assert.deepEqual(await verify(t, secret, 1000), { u: 7, exp: 2000 });
  assert.equal(await verify(t, secret, 2000), null, "expired");
  assert.equal(await verify(t, "x".repeat(32), 1000), null, "other secret");
  const [body, mac] = t.split(".");
  const forged = Buffer.from(JSON.stringify({ u: 1, exp: 9e9 })).toString("base64url");
  assert.equal(await verify(`${forged}.${mac}`, secret, 1000), null, "payload swapped");
  assert.equal(await verify(`${body}.`, secret, 1000), null);
  assert.equal(await verify(undefined, secret, 1000), null);
});

test("safeEqual", async () => {
  assert.equal(await safeEqual("abc", "abc"), true);
  assert.equal(await safeEqual("abc", "abd"), false);
  assert.equal(await safeEqual("abc", "abcd"), false);
});

const user = (over = {}) => ({
  username: "ada", disabled: 0, quota_bytes: 1000, max_seconds: 600, expires_at: 0, reset_days: 30,
  period_start: 10_000, used_bytes: 0, used_seconds: 0, ...over,
});

test("an account is allowed until a limit is reached, in a fixed order of reasons", () => {
  const t = 10_100;
  assert.equal(accountOf(user(), t).allowed, true);
  assert.equal(accountOf(user({ used_bytes: 1000 }), t).reason, "quota");
  assert.equal(accountOf(user({ used_seconds: 600 }), t).reason, "time");
  assert.equal(accountOf(user({ expires_at: t }), t).reason, "expired");
  assert.equal(accountOf(user({ disabled: 1, used_bytes: 5000, expires_at: 1 }), t).reason, "disabled");
  assert.equal(accountOf(user({ quota_bytes: 0, max_seconds: 0, used_bytes: 1e12, used_seconds: 1e9 }), t).allowed, true, "zero means unlimited");
});

test("the allowance renews every period and the renewal date is reported", () => {
  const len = 30 * DAY;
  const u = user({ used_bytes: 1000, used_seconds: 600 });
  assert.equal(accountOf(u, 10_000 + len - 1).reason, "quota");
  const next = accountOf(u, 10_000 + len);
  assert.equal(next.allowed, true);
  assert.equal(next.used_bytes, 0);
  assert.equal(next.resets_at, 10_000 + 2 * len);
  // Several missed periods land on the current one.
  const p = rollPeriod(u, 10_000 + 3.5 * len);
  assert.equal(p.period_start, 10_000 + 3 * len);
  assert.equal(p.rolled, true);
  assert.equal(rollPeriod(user({ reset_days: 0, used_bytes: 5 }), 1e10).used_bytes, 5, "no renewal when reset_days is 0");
  assert.equal(accountOf(user({ reset_days: 0 }), t0()).resets_at, 0);
});
const t0 = () => 10_100;

test("usernames and passwords", () => {
  for (const ok of ["ada", "Ada.Lovelace", "a_b-c.9"]) assert.equal(checkUsername(ok), true, ok);
  for (const bad of ["ab", "-ab", "a b", "ünï", "x".repeat(33), "", null, 5]) assert.equal(checkUsername(bad), false, String(bad));
  assert.equal(checkPassword("12345678"), true);
  assert.equal(checkPassword("1234567"), false);
  assert.equal(checkPassword("x".repeat(201)), false);
  const pw = randomPassword();
  assert.equal(pw.length, 14);
  assert.match(pw, /^[A-Za-z2-9]+$/);
});

test("edge links name the edge on Cloudflare's TLS ports, or none without an edge", async () => {
  const { edgeLinks } = await import("../src/lib.js");
  const uuid = "0f1e2d3c-4b5a-4978-8a9b-0c1d2e3f4a5b";
  const links = edgeLinks("edge.example.org", uuid);
  assert.equal(links.length, 3);
  for (const l of links) {
    const u = new URL(l);
    assert.equal(u.protocol, "vless:");
    assert.equal(u.username, uuid);
    assert.equal(u.hostname, "edge.example.org");
    assert.equal(u.searchParams.get("security"), "tls");
    assert.equal(u.searchParams.get("type"), "ws");
    assert.equal(u.searchParams.get("host"), "edge.example.org");
    assert.equal(u.searchParams.get("path"), "/0f1e2d3c4b5a?ed=2560");
  }
  assert.deepEqual(new Set(links.map((l) => new URL(l).port)), new Set(["443", "2053", "8443"]));
  assert.deepEqual(edgeLinks("", uuid), []);
  assert.deepEqual(edgeLinks("edge.example.org", ""), []);
  assert.deepEqual(edgeLinks("edge.example.org", "not-a-uuid"), []);
});

test("release files: names, sources and ranges", async () => {
  const { FILE_NAME, FILE_SOURCE, parseRange } = await import("../src/files.js");
  assert.ok(FILE_NAME.test("Albatross-0.4.1-android.apk"));
  assert.ok(FILE_NAME.test("Albatross-Setup-0.4.1.exe"));
  assert.ok(!FILE_NAME.test("../x.apk"));
  assert.ok(!FILE_NAME.test("Albatross-0.4.1-android.apk.part"));
  const src = "https://github.com/Albatrossvpn/Albatross/releases/download/v0.4.1/Albatross-0.4.1-android.apk";
  assert.equal(FILE_SOURCE.exec(src)[1], "Albatross-0.4.1-android.apk");
  assert.equal(FILE_SOURCE.exec("https://example.org/Albatross-0.4.1-android.apk"), null);
  assert.deepEqual(parseRange("bytes=0-99", 1000), { offset: 0, length: 100 });
  assert.deepEqual(parseRange("bytes=900-", 1000), { offset: 900, length: 100 });
  assert.deepEqual(parseRange("bytes=-100", 1000), { offset: 900, length: 100 });
  assert.deepEqual(parseRange("bytes=0-5000", 1000), { offset: 0, length: 1000 });
  assert.deepEqual(parseRange("bytes=1000-", 1000), { invalid: true });
  assert.equal(parseRange("", 1000), null);
  assert.equal(parseRange("items=0-1", 1000), null);
});

test("the app's own files: names and types", async () => {
  const { LIST_PATH, listType } = await import("../src/files.js");
  for (const p of ["servers/hot.txt", "servers/all-8.txt", "servers/tor.json", "main/update.json", "rules/ads.srs", "rules/ads.json", "rules/LICENSE-GPL-3.0.txt"]) assert.ok(LIST_PATH.test(p), p);
  for (const p of ["servers/all-9.txt", "main/README.md", "../x", "servers/hot.txt/..", "rules/other.json"]) assert.ok(!LIST_PATH.test(p), p);
  assert.equal(listType("main/update.json"), "application/json; charset=utf-8");
  assert.equal(listType("rules/ads.srs"), "application/octet-stream");
  assert.equal(listType("servers/hot.txt"), "text/plain; charset=utf-8");
});

test("versions compare by number", async () => {
  const { versionLess } = await import("../src/lib.js");
  assert.equal(versionLess("0.4.0", "1.0.0"), true);
  assert.equal(versionLess("1.0.0", "1.0.0"), false);
  assert.equal(versionLess("1.0.10", "1.0.9"), false);
  assert.equal(versionLess("v0.9.9", "1.0.0"), true);
  assert.equal(versionLess("1.2.0-dev.4", "1.2.0"), false);
  assert.equal(versionLess("", "0.0.1"), true);
});
