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

// `ip` stands in for the client's address: the local Worker keeps a
// CF-Connecting-IP header it is given.
async function call(method, path, { token, body, ip, headers } = {}) {
  const res = await fetch(BASE + "/api/v1/" + path, {
    method,
    headers: {
      "Content-Type": "application/json", ...(token ? { Authorization: "Bearer " + token } : {}), ...(ip ? { "CF-Connecting-IP": ip } : {}), ...headers,
    },
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

test("a usage report sent again with the same id is counted once", async () => {
  const c = await call("POST", "admin/users", { token: admin, body: { username: "twice", password: "twice-pass-1", quota_bytes: 10 * GB } });
  assert.equal(c.status, 201);
  const tok = (await call("POST", "login", { body: { username: "twice", password: "twice-pass-1" } })).body.token;
  const body = { bytes: 1000, seconds: 10, id: "abcdef0123456789" };
  assert.equal((await call("POST", "usage", { token: tok, body })).body.account.used_bytes, 1000);
  assert.equal((await call("POST", "usage", { token: tok, body })).body.account.used_bytes, 1000, "the same report again");
  assert.equal((await call("POST", "usage", { token: tok, body: { ...body, id: "fedcba9876543210" } })).body.account.used_bytes, 2000, "a new report");
  assert.equal((await call("POST", "usage", { token: tok, body: { bytes: 5, seconds: 0 } })).body.account.used_bytes, 2005, "without an id, as before");
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

test("guesses from many addresses do not lock the owner out", async () => {
  await call("POST", "admin/users", { token: admin, body: { username: "owner", password: "ownerpass1" } });
  for (let i = 1; i <= 8; i++) {
    const r = await call("POST", "login", { ip: `198.51.100.${i}`, body: { username: "owner", password: "wrong" + i } });
    assert.equal(r.status, 401, `guess ${i}`);
  }
  const l = await call("POST", "login", { ip: "198.51.100.99", body: { username: "owner", password: "ownerpass1" } });
  assert.equal(l.status, 200, "the right password from a fresh address signs in");
  const p = await call("POST", "password", { ip: "198.51.100.99", token: l.body.token, body: { old_password: "ownerpass1", new_password: "ownerpass2" } });
  assert.equal(p.status, 200, "and may change the password");
});

test("guessing from many addresses is still limited", async () => {
  // The username is still under attack: an address that already failed at it gets few more tries.
  const guess = (ip, password) => call("POST", "login", { ip, body: { username: "owner", password } });
  for (let i = 0; i < 2; i++) assert.equal((await guess("198.51.100.1", "again" + i)).status, 401);
  assert.equal((await guess("198.51.100.1", "ownerpass2")).status, 429, "even the right password waits there");
  for (let i = 0; i < 3; i++) assert.equal((await guess("198.51.100.50", "fresh" + i)).status, 401);
  assert.equal((await guess("198.51.100.50", "ownerpass2")).status, 429, "a new address gets only a few tries");
  assert.equal((await guess("198.51.100.99", "ownerpass2")).status, 200, "the owner's address is unaffected");

  // One address guessing across many usernames is stopped too.
  for (let i = 0; i < 40; i++) {
    assert.equal((await call("POST", "login", { ip: "203.0.113.7", body: { username: "nobody" + i, password: "x" } })).status, 401);
  }
  assert.equal((await guess("203.0.113.7", "ownerpass2")).status, 429);
});

test("addresses in one IPv6 /64 count as one", async () => {
  await call("POST", "admin/users", { token: admin, body: { username: "six", password: "sixpass12" } });
  const guess = (ip, password) => call("POST", "login", { ip, body: { username: "six", password } });
  // One prefix's hosts, written every way an address can be.
  const hosts = ["2001:db8:1:2::1", "2001:DB8:1:2::2", "2001:0db8:0001:0002:0000:0000:0000:0003", "2001:db8:1:2:4::",
    "2001:db8:1:2:5:6:7:8", "2001:db8:1:2::9", "2001:db8:1:2:ffff::a", "2001:db8:1:2::b"];
  for (const [i, ip] of hosts.entries()) assert.equal((await guess(ip, "wrong" + i)).status, 401, ip);
  assert.equal((await guess("2001:db8:1:2::c", "sixpass12")).status, 429, "a new address in the prefix waits");
  assert.equal((await guess("2001:db8:1:3::1", "sixpass12")).status, 200, "the next prefix signs in");
});

test("a username guessed at from very many addresses is closed for a while", async () => {
  await call("POST", "admin/users", { token: admin, body: { username: "target", password: "targetpass1" } });
  // Eight tries from the first address, then three from each new one.
  let failures = 0;
  for (let a = 1; failures < 100; a++) {
    for (let i = 0; i < (a === 1 ? 8 : 3) && failures < 100; i++, failures++) {
      const r = await call("POST", "login", { ip: `192.0.2.${a}`, body: { username: "target", password: "guess" + failures } });
      assert.equal(r.status, 401, `guess ${failures + 1} from 192.0.2.${a}`);
    }
  }
  const r = await call("POST", "login", { ip: "192.0.2.250", body: { username: "target", password: "targetpass1" } });
  assert.equal(r.status, 429, "even the right password from a fresh address waits");
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
  assert.ok(r.body.users.every((u) => /^grp\d{6}$/.test(u.username)));
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

test("users' reports about servers are summed for the collector", async () => {
  const body = { asn: 44244, proto: [[2, 3, 4]], servers: [["1a2b", 3, 3], ["ffff", 0, 6]] };
  assert.equal((await call("POST", "stats", { body, ip: "5.5.5.1" })).status, 200);
  assert.equal((await call("POST", "stats", { body: { asn: 1, servers: [["zz", 1, 1]] }, ip: "5.5.5.2" })).status, 400, "a key is hex");
  assert.equal((await call("POST", "stats", { body: { asn: 1, servers: [["1a2b", 2, 1]] }, ip: "5.5.5.3" })).status, 400, "passes within tests");
  assert.equal((await call("POST", "stats", { body: { asn: 44244, servers: [["1a2b", 1, 2]] }, ip: "5.5.5.4" })).status, 200);
  assert.equal((await call("POST", "admin/crowd/publish")).status, 401);
  const pub = await call("POST", "admin/crowd/publish", { token: admin });
  assert.equal(pub.status, 200);
  assert.equal(pub.body.servers, 2);
  const f = await fetch(BASE + "/list/servers/crowd.json");
  assert.equal(f.status, 200);
  const j = await f.json();
  assert.deepEqual(j.servers["1a2b"], [4, 5]);
  assert.deepEqual(j.servers.ffff, [0, 6]);
  assert.equal(j.days, 3);
});

test("pages are served; unknown API paths are 404", async () => {
  const home = await fetch(BASE + "/");
  assert.equal(home.status, 200);
  assert.match(await home.text(), /url=\/account/, "the root sends visitors to the account page");
  for (const p of ["/account", "/admin", "/privacy", "/terms"]) assert.equal((await fetch(BASE + p)).status, 200, p);
  assert.equal((await call("GET", "nope")).status, 404);
});

test("the app's own files: only the administrator publishes them; anyone reads them", async () => {
  const body = "vless://a@b:443#one\nvless://c@d:443#two\n";
  const put = (path, token, data = body) => fetch(BASE + "/api/v1/admin/list/" + path, {
    method: "PUT", headers: { ...(token ? { Authorization: "Bearer " + token } : {}), "Content-Type": "text/plain" }, body: data,
  });
  assert.equal((await put("servers/hot.txt")).status, 401, "no token");
  assert.equal((await put("servers/hot.txt", "x.y")).status, 401, "bad token");
  assert.equal((await put("servers/secret.txt", admin)).status, 404, "not one of the app's files");
  assert.equal((await put("servers/hot.txt", admin, "")).status, 400, "empty");
  const ok = await put("servers/hot.txt", admin);
  assert.equal(ok.status, 200);
  assert.deepEqual(await ok.json(), { path: "servers/hot.txt", size: body.length });
  const got = await fetch(BASE + "/list/servers/hot.txt");
  assert.equal(got.status, 200);
  assert.equal(await got.text(), body);
  assert.match(got.headers.get("content-type"), /^text\/plain/);
  const etag = got.headers.get("etag");
  assert.ok(etag);
  assert.equal((await fetch(BASE + "/list/servers/hot.txt", { headers: { "If-None-Match": etag } })).status, 304);
  const part = await fetch(BASE + "/list/servers/hot.txt", { headers: { Range: "bytes=0-4" } });
  assert.equal(part.status, 206);
  assert.equal(await part.text(), "vless");
  assert.equal((await fetch(BASE + "/list/servers/secret.txt")).status, 404);
  const manifest = JSON.stringify({ latest_version: "9.9.9" });
  assert.equal((await put("main/update.json", admin, manifest)).status, 200);
  const m = await fetch(BASE + "/list/main/update.json");
  assert.match(m.headers.get("content-type"), /^application\/json/);
  assert.equal((await m.json()).latest_version, "9.9.9");
});

test("subscriptions for other devices: one address per account, servers by country, void after a password change", async () => {
  const put = fetch(BASE + "/api/v1/admin/list/servers/hot.txt", {
    method: "PUT", headers: { Authorization: "Bearer " + admin, "Content-Type": "text/plain" },
    body: "vless://a@b:443#Germany-1\ntrojan://p@h:443#US%20%F0%9F%87%BA%F0%9F%87%B8%20fast\nss://YWVzOmJhc2U@h:8388#no-country\n",
  });
  assert.equal((await put).status, 200);
  const c = await call("POST", "admin/users", { token: admin, body: { username: "subscriber", password: "sub-pass-1234", quota_bytes: 100 * GB } });
  assert.equal(c.status, 201);
  let token = (await call("POST", "login", { body: { username: "subscriber", password: "sub-pass-1234" } })).body.token;
  assert.equal((await call("GET", "sub")).status, 401, "signed-in users only");
  const info = await call("GET", "sub", { token });
  assert.equal(info.status, 200);
  assert.match(info.body.url, /^https?:\/\/[^/]+\/sub\/[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+$/);
  assert.deepEqual(info.body.countries.map((x) => [x.code, x.count]), [["DE", 1], ["US", 1]]);
  assert.equal(info.body.servers, 3);
  // The local server answers under the deployed name; the address is reached here.
  info.body.url = BASE + new URL(info.body.url).pathname;

  const all = await fetch(info.body.url);
  assert.equal(all.status, 200);
  assert.match(all.headers.get("subscription-userinfo"), /download=0; total=107374182400/);
  assert.equal(all.headers.get("profile-title"), "base64:" + btoa("Albatross All servers"));
  const links = atob(await all.text()).trim().split("\n");
  assert.equal(links.length, 3);
  assert.equal(decodeURIComponent(links[0].split("#")[1]), "Albatross 🇩🇪 Germany 1 · vless");
  assert.equal(links.some((l) => /Raydikalx|fast|no-country/.test(l)), false, "the servers' own names never show");
  const de = await fetch(info.body.url + "?c=de");
  assert.equal(atob(await de.text()).trim().split("\n").length, 1);
  assert.equal((await fetch(info.body.url + "?c=zz")).status, 200, "an unknown country is an empty list");
  assert.equal((await fetch(info.body.url + "?c=nope")).status, 404);
  assert.equal((await fetch(BASE + "/sub/not.a.token")).status, 401);

  // The address stops working when the password changes, like every other session.
  const pw = await call("POST", "password", { token, body: { old_password: "sub-pass-1234", new_password: "sub-pass-5678" } });
  assert.equal(pw.status, 200);
  assert.equal((await fetch(info.body.url)).status, 401);
  // An account that may not connect gets no servers.
  token = pw.body.token;
  const fresh = BASE + new URL((await call("GET", "sub", { token })).body.url).pathname;
  assert.equal((await fetch(fresh)).status, 200);
  const users = await call("GET", "admin/users?q=subscriber", { token: admin });
  const id = users.body.users[0].id;
  assert.equal((await call("PATCH", `admin/users/${id}`, { token: admin, body: { disabled: true } })).status, 200);
  assert.equal((await fetch(fresh)).status, 403);
});

test("plans: as many devices as the plan allows may be connected at once", async () => {
  const dev = (n, name = "Windows") => ({ "X-Albatross-Device": n.repeat(16), "X-Albatross-Device-Name": name });
  assert.equal((await call("POST", "admin/users", { token: admin, body: { username: "planned", password: "plan-pass-123", tier: "platinum" } })).status, 400);
  const c = await call("POST", "admin/users", { token: admin, body: { username: "planned", password: "plan-pass-123", tier: "silver" } });
  assert.equal(c.status, 201);
  assert.equal(c.body.user.tier, "silver");
  assert.equal(c.body.user.max_devices, 2);
  const l = await call("POST", "login", { body: { username: "planned", password: "plan-pass-123" }, headers: dev("a") });
  const tok = l.body.token;
  assert.deepEqual([l.body.account.tier, l.body.account.max_devices, l.body.account.devices], ["silver", 2, 1], "signing in holds no slot, but this device would get one");
  // Two devices connect; the third is refused and the first two stay.
  let r = await call("POST", "usage", { token: tok, body: { bytes: 1, seconds: 1 }, headers: dev("a") });
  assert.equal(r.body.account.allowed, true);
  assert.equal(r.body.account.devices, 1);
  r = await call("POST", "usage", { token: tok, body: { bytes: 1, seconds: 1 }, headers: dev("b", "Android") });
  assert.equal(r.body.account.allowed, true);
  assert.equal(r.body.account.devices, 2);
  r = await call("POST", "usage", { token: tok, body: { bytes: 1, seconds: 1 }, headers: dev("c") });
  assert.equal(r.body.account.allowed, false);
  assert.equal(r.body.account.reason, "devices");
  assert.equal(r.body.account.used_bytes, 3, "its use still counts");
  r = await call("POST", "usage", { token: tok, body: {}, headers: dev("a") });
  assert.equal(r.body.account.allowed, true, "the devices that hold a slot keep it");
  // The website sees the devices and may not be refused for them.
  const me = await call("GET", "me", { token: tok, headers: { "X-Albatross-Client": "web" } });
  assert.equal(me.body.account.allowed, true);
  assert.equal(me.body.account.devices, 2);
  const list = await call("GET", "devices", { token: tok, headers: { "X-Albatross-Client": "web" } });
  assert.equal(list.body.max_devices, 2);
  assert.deepEqual(list.body.devices.map((d) => [d.name, d.active]).sort(), [["Android", true], ["Windows", false], ["Windows", true]]);
  // Removing a device frees its slot at once.
  assert.equal((await call("DELETE", "devices/" + "a".repeat(16), { token: tok, headers: { "X-Albatross-Client": "web" } })).status, 200);
  assert.equal((await call("DELETE", "devices/" + "a".repeat(16), { token: tok, headers: { "X-Albatross-Client": "web" } })).status, 404);
  r = await call("POST", "usage", { token: tok, body: {}, headers: dev("c") });
  assert.equal(r.body.account.allowed, true);
  // A smaller plan: a device that finds the slots full gives up its own.
  const id = (await call("GET", "admin/users?q=planned", { token: admin })).body.users[0].id;
  r = await call("PATCH", `admin/users/${id}`, { token: admin, body: { tier: "bronze" } });
  assert.equal(r.body.user.tier, "bronze");
  assert.equal(r.body.user.devices, 2);
  assert.equal((await call("POST", "usage", { token: tok, body: {}, headers: dev("b") })).body.account.reason, "devices");
  assert.equal((await call("POST", "usage", { token: tok, body: {}, headers: dev("c") })).body.account.allowed, true);
  assert.equal((await call("GET", "admin/users?q=planned", { token: admin })).body.users[0].devices, 1);
  // The subscription for Apple devices holds a slot too.
  const sub = BASE + new URL((await call("GET", "sub", { token: tok, headers: { "X-Albatross-Client": "web" } })).body.url).pathname;
  const refused = await fetch(sub);
  assert.equal(refused.status, 403);
  assert.match(await refused.text(), /1 devices? of this account/);
  assert.equal((await call("DELETE", "devices/" + "c".repeat(16), { token: tok, headers: { "X-Albatross-Client": "web" } })).status, 200);
  assert.equal((await fetch(sub)).status, 200);
  assert.equal((await call("POST", "usage", { token: tok, body: {}, headers: dev("c") })).body.account.reason, "devices");
  assert.equal((await call("GET", "devices", { token: tok, headers: { "X-Albatross-Client": "web" } })).body.devices[0].name, "iPhone, iPad and Mac (subscription)");
  // Copies that name no device share one; signing everyone out forgets the devices.
  assert.equal((await call("POST", "usage", { token: tok, body: {} })).body.account.reason, "devices");
  await call("PATCH", `admin/users/${id}`, { token: admin, body: { sign_out: true } });
  const again = (await call("POST", "login", { body: { username: "planned", password: "plan-pass-123" } })).body.token;
  assert.equal((await call("POST", "usage", { token: again, body: {} })).body.account.allowed, true);
  // The default plan for sign-ups.
  assert.equal((await call("PUT", "admin/settings", { token: admin, body: { default_tier: "diamond" } })).status, 400);
  assert.equal((await call("PUT", "admin/settings", { token: admin, body: { default_tier: "gold", signup_open: true } })).status, 200);
  assert.equal((await call("GET", "public")).body.tiers.gold.devices, 4);
  assert.equal((await call("POST", "signup", { body: { username: "golden", password: "12345678" } })).body.account.tier, "gold");
});

test("encrypted DNS profiles for Apple devices are served as profiles", async () => {
  for (const which of ["google", "cloudflare"]) {
    const r = await fetch(BASE + "/api/v1/dns/" + which + ".mobileconfig");
    assert.equal(r.status, 200);
    assert.match(r.headers.get("content-type"), /^application\/x-apple-aspen-config/);
    const body = await r.text();
    assert.match(body, /com\.apple\.dnsSettings\.managed/);
    assert.match(body, which === "google" ? /dns\.google/ : /cloudflare-dns\.com/);
  }
  assert.equal((await fetch(BASE + "/api/v1/dns/other.mobileconfig")).status, 404);
});

test("copies older than the manifest's min_version are refused; the website never is", async () => {
  const put = (data) => fetch(BASE + "/api/v1/admin/list/main/update.json", {
    method: "PUT", headers: { Authorization: "Bearer " + admin, "Content-Type": "application/json" }, body: JSON.stringify(data),
  });
  const login = (headers) => fetch(BASE + "/api/v1/login", {
    method: "POST", headers: { "Content-Type": "application/json", ...headers }, body: JSON.stringify({ username: "nobody-here", password: "whatever-1" }),
  });
  assert.equal((await put({ min_version: "1.0.0" })).status, 200);
  const old = await login({});
  assert.equal(old.status, 426, "a copy that sends no version is 0.4.0");
  assert.match((await old.json()).error, /no longer works/);
  assert.equal((await login({ "X-Albatross-Version": "0.9.9" })).status, 426);
  assert.equal((await login({ "X-Albatross-Version": "1.0.0" })).status, 401, "current copies get the usual answer");
  assert.equal((await login({ "X-Albatross-Client": "web" })).status, 401, "the website is never refused");
  assert.equal((await put({ min_version: "" })).status, 200);
  assert.equal((await login({})).status, 401, "no minimum, no refusal");
});

test("too many failed administrator logins lock the sign-in for a while", async () => {
  let last;
  for (let i = 0; i < 11; i++) last = await call("POST", "admin/login", { body: { username: "root", password: "wrong" + i } });
  assert.equal(last.status, 429);
  assert.equal((await call("POST", "admin/login", { body: ADMIN })).status, 429);
});
