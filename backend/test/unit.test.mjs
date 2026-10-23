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
