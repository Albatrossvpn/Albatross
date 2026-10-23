// End-to-end test of the Worker: starts it locally with `wrangler dev` (a
// real workerd with a local D1 database) and drives the HTTP API.
//   npm run test:api
import assert from "node:assert/strict";
import { spawn, spawnSync } from "node:child_process";
import { mkdtempSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import test, { after, before } from "node:test";

const PORT = Number(process.env.PORT || 8799);
const BASE = `http://127.0.0.1:${PORT}`;
const ADMIN = { username: "root", password: "admin-pass-for-tests" };
const dir = mkdtempSync(join(tmpdir(), "albatross-api-"));
let proc;

async function call(method, path, { token, body } = {}) {
  const res = await fetch(BASE + "/api/v1/" + path, {
    method,
    headers: { "Content-Type": "application/json", ...(token ? { Authorization: "Bearer " + token } : {}) },
    body: body === undefined ? undefined : JSON.stringify(body),
  });
  return { status: res.status, body: await res.json().catch(() => null), headers: res.headers };
}

before(async () => {
  const m = spawnSync("npx", ["wrangler", "d1", "migrations", "apply", "DB", "--local", "--persist-to", dir], { encoding: "utf8" });
  assert.equal(m.status, 0, m.stdout + m.stderr);
  // A server left over from an earlier run would answer with its old data.
  assert.equal(await fetch(BASE + "/api/v1/public").then(() => true, () => false), false, `something already listens on port ${PORT}`);
  proc = spawn("npx", ["wrangler", "dev", "--local", "--port", String(PORT), "--persist-to", dir,
    "--var", "TOKEN_SECRET:" + "t".repeat(40), "--var", "ADMIN_USER:" + ADMIN.username, "--var", "ADMIN_PASSWORD:" + ADMIN.password,
    "--var", "COMPANY_NAME:Example Co"], { stdio: ["ignore", "pipe", "pipe"], detached: true });
  let log = "";
  proc.stdout.on("data", (d) => (log += d));
  proc.stderr.on("data", (d) => (log += d));
  for (let i = 0; i < 120; i++) {
    try {
      if ((await fetch(BASE + "/api/v1/public")).ok) return;
    } catch {}
    await new Promise((r) => setTimeout(r, 500));
  }
  throw new Error("wrangler dev did not start:\n" + log);
});

after(() => {
  // wrangler starts workerd as a child: stop the whole group.
  if (proc) try { process.kill(-proc.pid, "SIGTERM"); } catch {}
  setTimeout(() => { rmSync(dir, { recursive: true, force: true }); process.exit(); }, 500).unref();
});

let admin;
const GB = 1024 ** 3;

test("public information; sign-ups are closed until the administrator opens them", async () => {
  const r = await call("GET", "public");
  assert.equal(r.status, 200);
  assert.equal(r.body.name, "Albatross");
  assert.equal(r.body.company, "Example Co");
  assert.equal(r.body.signup_open, false);
  assert.equal(r.headers.get("cache-control"), "no-store");
  const s = await call("POST", "signup", { body: { username: "early", password: "12345678" } });
  assert.equal(s.status, 403);
});

test("the administrator signs in; panels reject everyone else", async () => {
  const bad = await call("POST", "admin/login", { body: { username: "root", password: "nope" } });
  assert.equal(bad.status, 401);
  const r = await call("POST", "admin/login", { body: ADMIN });
  assert.equal(r.status, 200);
  admin = r.body.token;
  assert.equal((await call("GET", "admin/users")).status, 401);
  assert.equal((await call("GET", "admin/users", { token: "x.y" })).status, 401);
  assert.equal((await call("GET", "admin/stats", { token: admin })).status, 200);
});

let ada;
test("the administrator creates a user with limits; the user signs in", async () => {
  const c = await call("POST", "admin/users", { token: admin, body: { username: "Ada", password: "correct horse", quota_bytes: 1000, max_seconds: 600, reset_days: 30, note: "first" } });
  assert.equal(c.status, 201);
  assert.equal(c.body.user.username, "ada");
  assert.equal((await call("POST", "admin/users", { token: admin, body: { username: "ADA" } })).status, 409, "usernames are case-insensitive");
  assert.equal((await call("POST", "admin/users", { token: admin, body: { username: "x" } })).status, 400);

  assert.equal((await call("POST", "login", { body: { username: "ada", password: "wrong" } })).status, 401);
  const l = await call("POST", "login", { body: { username: "ADA", password: "correct horse" } });
  assert.equal(l.status, 200);
  assert.equal(l.body.account.allowed, true);
  assert.equal(l.body.account.quota_bytes, 1000);
  assert.ok(l.body.account.resets_at > Date.now() / 1000);
  ada = l.body.token;
  assert.equal((await call("GET", "me", { token: ada })).body.account.username, "ada");
});

test("usage is counted; the quota turns the account off; a bad report is refused", async () => {
  let r = await call("POST", "usage", { token: ada, body: { bytes: 400, seconds: 60 } });
  assert.equal(r.status, 200);
  assert.equal(r.body.account.used_bytes, 400);
  assert.equal(r.body.account.allowed, true);
  assert.ok(r.body.token, "a session token comes back");
  r = await call("POST", "usage", { token: ada, body: { bytes: 600, seconds: 0 } });
  assert.equal(r.body.account.allowed, false);
  assert.equal(r.body.account.reason, "quota");
  for (const bad of [{ bytes: -1 }, { bytes: 1.5 }, { seconds: "9" }, { bytes: 1e15 }]) {
    assert.equal((await call("POST", "usage", { token: ada, body: bad })).status, 400, JSON.stringify(bad));
  }
  assert.equal((await call("POST", "usage", { body: { bytes: 1 } })).status, 401);
});

test("the administrator renews, limits time, disables and re-enables", async () => {
  const id = (await call("GET", "admin/users?q=ad", { token: admin })).body.users[0].id;
  let r = await call("PATCH", `admin/users/${id}`, { token: admin, body: { reset_usage: true } });
  assert.equal(r.body.user.used_bytes, 0);
  assert.equal((await call("POST", "usage", { token: ada, body: { bytes: 0, seconds: 0 } })).body.account.allowed, true);

  await call("PATCH", `admin/users/${id}`, { token: admin, body: { max_seconds: 100 } });
  r = await call("POST", "usage", { token: ada, body: { bytes: 1, seconds: 100 } });
  assert.equal(r.body.account.reason, "time");

  await call("PATCH", `admin/users/${id}`, { token: admin, body: { max_seconds: 0, disabled: true } });
  assert.equal((await call("POST", "usage", { token: ada, body: {} })).body.account.reason, "disabled");
  await call("PATCH", `admin/users/${id}`, { token: admin, body: { disabled: false, expires_at: 1 } });
  assert.equal((await call("GET", "me", { token: ada })).body.account.reason, "expired");
  r = await call("PATCH", `admin/users/${id}`, { token: admin, body: { expires_at: 0 } });
  assert.equal(r.body.user.status, "active");
  assert.equal((await call("PATCH", `admin/users/${id}`, { token: admin, body: {} })).status, 400);
  assert.equal((await call("PATCH", `admin/users/${id}`, { token: admin, body: { quota_bytes: -5 } })).status, 400);
});

test("concurrent reports from several devices add up exactly", async () => {
  const id = (await call("GET", "admin/users?q=ada", { token: admin })).body.users[0].id;
  await call("PATCH", `admin/users/${id}`, { token: admin, body: { reset_usage: true, quota_bytes: 0 } });
  await Promise.all(Array.from({ length: 20 }, () => call("POST", "usage", { token: ada, body: { bytes: 10, seconds: 1 } })));
  const a = (await call("GET", "me", { token: ada })).body.account;
  assert.equal(a.used_bytes, 200);
  assert.equal(a.used_seconds, 20);
});

test("changing the password signs out the other devices", async () => {
  assert.equal((await call("POST", "password", { token: ada, body: { old_password: "bad", new_password: "new password 1" } })).status, 403);
  assert.equal((await call("POST", "password", { token: ada, body: { old_password: "correct horse", new_password: "short" } })).status, 400);
  const r = await call("POST", "password", { token: ada, body: { old_password: "correct horse", new_password: "new password 1" } });
  assert.equal(r.status, 200);
  assert.equal((await call("GET", "me", { token: ada })).status, 401, "the old session is gone");
  ada = r.body.token;
  assert.equal((await call("GET", "me", { token: ada })).status, 200);
  assert.equal((await call("POST", "login", { body: { username: "ada", password: "new password 1" } })).status, 200);
});

test("the administrator resets a password and signs a user out", async () => {
  const id = (await call("GET", "admin/users?q=ada", { token: admin })).body.users[0].id;
  assert.equal((await call("PATCH", `admin/users/${id}`, { token: admin, body: { password: "abc" } })).status, 400);
  await call("PATCH", `admin/users/${id}`, { token: admin, body: { password: "reset by admin" } });
  assert.equal((await call("GET", "me", { token: ada })).status, 401);
  assert.equal((await call("POST", "login", { body: { username: "ada", password: "reset by admin" } })).status, 200);
});

test("repeated wrong passwords are slowed down", async () => {
  await call("POST", "admin/users", { token: admin, body: { username: "grace", password: "gracepass1" } });
  let last;
  for (let i = 0; i < 9; i++) last = await call("POST", "login", { body: { username: "grace", password: "wrong" + i } });
  assert.equal(last.status, 429);
  const right = await call("POST", "login", { body: { username: "grace", password: "gracepass1" } });
  assert.equal(right.status, 429, "even the right password waits");
});

test("settings, sign-up with the defaults, duplicate names", async () => {
  assert.equal((await call("PUT", "admin/settings", { token: admin, body: { download_windows: "http://x" } })).status, 400);
  assert.equal((await call("PUT", "admin/settings", { token: admin, body: { default_quota_bytes: "lots" } })).status, 400);
  const s = await call("PUT", "admin/settings", { token: admin, body: { signup_open: true, default_quota_bytes: String(2 * GB), default_max_seconds: "7200", default_valid_days: "30", default_reset_days: "7", download_android: "https://example.com/app.apk" } });
  assert.equal(s.status, 200);
  assert.equal(s.body.signup_open, "1");
  const pub = (await call("GET", "public")).body;
  assert.equal(pub.signup_open, true);
  assert.equal(pub.downloads.android, "https://example.com/app.apk");

  assert.equal((await call("POST", "signup", { body: { username: "new user", password: "12345678" } })).status, 400);
  assert.equal((await call("POST", "signup", { body: { username: "newbie", password: "short" } })).status, 400);
  const r = await call("POST", "signup", { body: { username: "Newbie", password: "12345678" } });
  assert.equal(r.status, 201);
  assert.equal(r.body.account.quota_bytes, 2 * GB);
  assert.equal(r.body.account.max_seconds, 7200);
  assert.ok(r.body.account.expires_at > Date.now() / 1000 + 29 * 86400);
  assert.equal((await call("POST", "signup", { body: { username: "newbie", password: "12345678" } })).status, 409);
});

test("bulk creation gives working accounts; search and delete", async () => {
  const r = await call("POST", "admin/users/bulk", { token: admin, body: { prefix: "grp", count: 5, quota_bytes: GB, reset_days: 30 } });
  assert.equal(r.status, 201);
  assert.equal(r.body.users.length, 5);
  assert.equal(new Set(r.body.users.map((u) => u.username)).size, 5);
  const u = r.body.users[0];
  assert.equal((await call("POST", "login", { body: u })).status, 200);
  assert.equal((await call("POST", "admin/users/bulk", { token: admin, body: { prefix: "Bad Prefix", count: 1 } })).status, 400);
  assert.equal((await call("POST", "admin/users/bulk", { token: admin, body: { prefix: "grp", count: 500 } })).status, 400);

  const list = await call("GET", "admin/users?q=grp&limit=3", { token: admin });
  assert.equal(list.body.total, 5);
  assert.equal(list.body.users.length, 3);
  assert.equal((await call("GET", "admin/users?q=%25", { token: admin })).body.total, 0, "wildcards are not special");

  const victim = list.body.users[0];
  assert.equal((await call("DELETE", `admin/users/${victim.id}`, { token: admin })).status, 200);
  assert.equal((await call("DELETE", `admin/users/${victim.id}`, { token: admin })).status, 404);
  const stats = (await call("GET", "admin/stats", { token: admin })).body;
  assert.ok(stats.users >= 7 && stats.total_bytes > 0);
});

test("crowd stats: operators appear after three reports; bad reports are refused", async () => {
  const report = { asn: 58224, proto: [[1, 40, 50]], style: [[10, 5, 30]], hello: [[2, 9, 10]], port: [[1, 0, 20]] };
  assert.equal((await call("POST", "stats", { body: { ...report, proto: [[1, 60, 50]] } })).status, 400, "ok above n");
  assert.equal((await call("POST", "stats", { body: { ...report, asn: 0 } })).status, 400);
  assert.equal((await call("POST", "stats", { body: { ...report, hello: [[99, 1, 1]] } })).status, 400, "code out of range");
  for (let i = 0; i < 2; i++) assert.equal((await call("POST", "stats", { body: report })).status, 200);
  assert.equal((await call("GET", "stats")).body.asns["58224"], undefined, "two reports are not enough to publish");
  assert.equal((await call("POST", "stats", { body: report })).status, 200);
  const a = (await call("GET", "stats")).body.asns["58224"];
  assert.deepEqual(a.proto, [[1, 120, 150]]);
  assert.deepEqual(a.port, [[1, 0, 60]]);
  const mine = (await call("GET", "stats?asn=self")).body;
  assert.equal(typeof mine.asn, "number", "the asker learns its own network");
  if (mine.asn !== 58224) assert.equal(mine.asns["58224"], undefined, "asn=self gives only the asker's network");
});

test("pages are served; unknown API paths are 404", async () => {
  const home = await fetch(BASE + "/");
  assert.equal(home.status, 200);
  assert.match(await home.text(), /url=\/account/, "the root sends visitors to the account page");
  for (const p of ["/account", "/admin", "/privacy", "/terms"]) assert.equal((await fetch(BASE + p)).status, 200, p);
  assert.equal((await call("GET", "nope")).status, 404);
});

test("too many failed administrator logins lock the sign-in for a while", async () => {
  let last;
  for (let i = 0; i < 11; i++) last = await call("POST", "admin/login", { body: { username: "root", password: "wrong" + i } });
  assert.equal(last.status, 429);
  assert.equal((await call("POST", "admin/login", { body: ADMIN })).status, 429);
});
