import { test } from "node:test";
import assert from "node:assert/strict";
import { readFileSync, readdirSync } from "node:fs";
import { armor, securityTxt } from "../src/headers.js";
import accounts from "../src/index.js";
import website from "../../site/src/index.js";
import { d1 } from "./d1.mjs";

// What the Workers answer carries the security headers, whatever handler
// built it; the static pages carry theirs from _headers; and no page needs
// what the policies forbid.

const REQUIRED = [
  "Strict-Transport-Security", "X-Content-Type-Options", "X-Frame-Options", "Referrer-Policy", "Permissions-Policy",
  "X-Permitted-Cross-Domain-Policies", "Cross-Origin-Resource-Policy", "Content-Security-Policy",
];
const missing = (res) => REQUIRED.filter((h) => !res.headers.get(h));

const SITE = new URL("../../site/public/", import.meta.url);
const ACCOUNTS = new URL("../public/", import.meta.url);
const read = (base, name) => readFileSync(new URL(name, base), "utf8");

test("armor adds what a handler left out and keeps what it set", async () => {
  const data = armor(new Response("{}", { headers: { "Content-Type": "application/json" } }));
  assert.deepEqual(missing(data), []);
  assert.equal(data.headers.get("Content-Security-Policy"), "default-src 'none'; frame-ancestors 'none'");
  const page = armor(new Response("<p>x</p>", { headers: { "Content-Type": "text/html; charset=utf-8" } }));
  assert.match(page.headers.get("Content-Security-Policy"), /^default-src 'self'; style-src 'self'/);
  assert.doesNotMatch(page.headers.get("Content-Security-Policy"), /unsafe/);
  const own = armor(new Response("x", { status: 206, headers: { "X-Frame-Options": "SAMEORIGIN", "Content-Range": "bytes 0-0/1" } }));
  assert.equal(own.status, 206);
  assert.equal(own.headers.get("X-Frame-Options"), "SAMEORIGIN");
  assert.equal(own.headers.get("Content-Range"), "bytes 0-0/1");
  assert.equal(await own.text(), "x");
  assert.equal(armor(new Response(null, { status: 304 })).status, 304);
});

test("security.txt says where to report a weakness and never goes stale", async () => {
  const res = securityTxt(new Request("https://accounts.albatrossvpn.online/.well-known/security.txt"), "support@albatrossvpn.online");
  const text = await res.text();
  assert.match(text, /^Contact: mailto:support@albatrossvpn\.online$/m);
  assert.match(text, /^Canonical: https:\/\/accounts\.albatrossvpn\.online\/\.well-known\/security\.txt$/m);
  const left = Date.parse(text.match(/^Expires: (.+)$/m)[1]) - Date.now();
  assert.ok(left > 30 * 86400 * 1000 && left < 365 * 86400 * 1000, "expires within a year, not yet");
});

const accountsEnv = (extra = {}) => ({ DB: d1(), TOKEN_SECRET: "t".repeat(40), ASSETS: { fetch: async () => new Response("asset") }, ...extra });
const ask = (worker, host, path, env) => worker.fetch(new Request(`https://${host}${path}`), env, { waitUntil() {} });

test("everything the account service builds itself carries the security headers", async () => {
  for (const path of ["/.well-known/security.txt", "/api/v1/public", "/api/v1/nothing-here", "/list/nothing", "/dl/nothing"]) {
    const res = await ask(accounts, "accounts.albatrossvpn.online", path, accountsEnv());
    assert.deepEqual(missing(res), [], path);
  }
  const bare = await ask(accounts, "accounts.albatrossvpn.online", "/api/v1/public", accountsEnv({ TOKEN_SECRET: "" }));
  assert.equal(bare.status, 503);
  assert.deepEqual(missing(bare), [], "an unconfigured service too");
  const api = await ask(accounts, "accounts.albatrossvpn.online", "/api/v1/public", accountsEnv());
  assert.equal(api.headers.get("Cache-Control"), "no-store");
});

test("the website's Worker answers carry the security headers too", async () => {
  const bucket = { list: async () => ({ objects: [{ key: "Albatross-1.1.0-android.apk", size: 1048576 }] }), head: async () => null, get: async () => null };
  const env = { FILES: bucket, ASSETS: { fetch: async () => new Response("asset") } };
  for (const path of ["/.well-known/security.txt", "/download/", "/download/nothing", "/list/nothing"]) {
    const res = await ask(website, "albatrossvpn.online", path, env);
    assert.deepEqual(missing(res), [], path);
  }
  // A percent sequence that is not text used to throw.
  const bad = await ask(website, "albatrossvpn.online", "/download/%E0%A4%A", env);
  assert.equal(bad.status, 404);
  assert.deepEqual(missing(bad), []);
  const wrong = await website.fetch(new Request("https://albatrossvpn.online/download/x", { method: "POST" }), env);
  assert.equal(wrong.status, 405);
  assert.equal(wrong.headers.get("Allow"), "GET, HEAD");
  // The page the Worker builds needs no inline style, which its policy forbids.
  const index = await ask(website, "albatrossvpn.online", "/download/", env);
  const html = await index.text();
  assert.doesNotMatch(html, / style="/);
  assert.match(html, /Albatross-1\.1\.0-android\.apk/);
  assert.match(index.headers.get("Content-Security-Policy"), /^default-src 'self'; style-src 'self'/);
});

test("the static headers of both sites are strict", () => {
  for (const [name, base] of [["website", SITE], ["accounts", ACCOUNTS]]) {
    const text = read(base, "_headers");
    for (const h of [
      "Strict-Transport-Security: max-age=31536000; includeSubDomains", "X-Content-Type-Options: nosniff", "X-Frame-Options: DENY",
      "Referrer-Policy: no-referrer", "X-Permitted-Cross-Domain-Policies: none",
    ]) assert.ok(text.includes(h), `${name}: ${h}`);
    const csp = text.match(/Content-Security-Policy: (.+)/)[1];
    for (const d of ["object-src 'none'", "base-uri 'none'", "frame-ancestors 'none'", "upgrade-insecure-requests"]) assert.ok(csp.includes(d), `${name}: ${d}`);
    assert.doesNotMatch(csp, /unsafe-inline|unsafe-eval/, name);
  }
  const accountsHeaders = read(ACCOUNTS, "_headers");
  assert.match(accountsHeaders, /Cross-Origin-Opener-Policy: same-origin/);
  for (const p of ["/account", "/account.html", "/admin", "/admin.html"]) {
    assert.match(accountsHeaders, new RegExp(`^${p}\\n  Cache-Control: no-store`, "m"), p);
  }
  assert.match(accountsHeaders, /^\/admin\n(?:  .+\n)*  X-Robots-Tag: noindex/m);
  // Google's tag tools reach a page through window.opener, which this would cut.
  assert.doesNotMatch(read(SITE, "_headers"), /Cross-Origin-Opener-Policy/);
});

test("no page needs what the policies forbid", () => {
  for (const base of [SITE, ACCOUNTS]) {
    for (const f of readdirSync(base).filter((n) => n.endsWith(".html"))) {
      const html = read(base, f);
      assert.doesNotMatch(html, / style="/, `${f}: inline style`);
      assert.doesNotMatch(html, /\son[a-z]+\s*=\s*["']/i, `${f}: inline event handler`);
      assert.doesNotMatch(html, /href=["']\s*javascript:/i, `${f}: javascript: link`);
    }
  }
});

test("the administrator sign-in is held to a total across all addresses", async () => {
  const env = accountsEnv({ ADMIN_USER: "root", ADMIN_PASSWORD: "a-long-passphrase-for-tests" });
  const login = (ip, password) =>
    accounts.fetch(new Request("https://accounts.albatrossvpn.online/api/v1/admin/login", {
      method: "POST", headers: { "Content-Type": "application/json", "CF-Connecting-IP": ip },
      body: JSON.stringify({ username: "root", password }),
    }), env, { waitUntil() {} });
  // One address is stopped after ten wrong tries.
  for (let i = 0; i < 10; i++) assert.equal((await login("203.0.113.1", "wrong")).status, 401);
  assert.equal((await login("203.0.113.1", "wrong")).status, 429);
  // Many addresses add up: 190 more wrong tries make 200 in all.
  for (let i = 0; i < 190; i++) assert.equal((await login(`10.0.${Math.floor(i / 250)}.${(i % 250) + 1}`, "wrong")).status, 401);
  // Now even the right password from an address nobody used waits for the window.
  assert.equal((await login("192.0.2.77", "a-long-passphrase-for-tests")).status, 429);
  // Before that total, the right password signs in.
  const fresh = accountsEnv({ ADMIN_USER: "root", ADMIN_PASSWORD: "a-long-passphrase-for-tests" });
  const ok = await accounts.fetch(new Request("https://accounts.albatrossvpn.online/api/v1/admin/login", {
    method: "POST", headers: { "Content-Type": "application/json", "CF-Connecting-IP": "192.0.2.78" },
    body: JSON.stringify({ username: "root", password: "a-long-passphrase-for-tests" }),
  }), fresh, { waitUntil() {} });
  assert.equal(ok.status, 200);
  assert.ok((await ok.json()).token);
});

test("nothing is served over plain HTTP", async () => {
  const bucket = { list: async () => ({ objects: [] }), head: async () => null, get: async () => null };
  const viaEdge = { headers: { "CF-Ray": "8d2f0c3a1b2c3d4e-DXB" } };
  for (const [worker, host, path, env] of [
    [accounts, "accounts.albatrossvpn.online", "/account", accountsEnv()],
    [accounts, "accounts.albatrossvpn.online", "/api/v1/public", accountsEnv()],
    [website, "albatrossvpn.online", "/download/Albatross-1.1.0-android.apk", { FILES: bucket, ASSETS: { fetch: async () => new Response("asset") } }],
  ]) {
    const res = await worker.fetch(new Request(`http://${host}${path}`, viaEdge), env, { waitUntil() {} });
    assert.equal(res.status, 301, path);
    assert.equal(res.headers.get("Location"), `https://${host}${path}`);
    assert.deepEqual(missing(res), [], path);
  }
  // A POST is sent on with its method kept (308), not turned into a GET.
  const post = await accounts.fetch(new Request("http://accounts.albatrossvpn.online/api/v1/login", { method: "POST", body: "{}", ...viaEdge }), accountsEnv(), { waitUntil() {} });
  assert.equal(post.status, 308);
  // HTTPS through the edge is served; so is a request that did not come through
  // the edge (`wrangler dev` and the tests show the production host over HTTP).
  const secure = await accounts.fetch(new Request("https://accounts.albatrossvpn.online/api/v1/public", viaEdge), accountsEnv(), { waitUntil() {} });
  assert.equal(secure.status, 200);
  const local = await accounts.fetch(new Request("http://accounts.albatrossvpn.online/api/v1/public"), accountsEnv(), { waitUntil() {} });
  assert.equal(local.status, 200);
});
