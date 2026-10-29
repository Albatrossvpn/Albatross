import assert from "node:assert/strict";
import test from "node:test";
import { keyedID, openKeyed, pool } from "../src/sub.js";
import {
  DAY, FEATURES, TIERS, accountOf, checkPassword, checkUsername, featuresOf, hashPassword, maxDevices, planFeatures, randomPassword, rollPeriod, safeEqual, sign,
  tierOf, verify, verifyPassword,
} from "../src/lib.js";

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

test("the plan says how many devices may hold a slot at once", () => {
  const t = 10_100;
  assert.deepEqual(Object.keys(TIERS), ["bronze", "silver", "gold", "vip"]);
  assert.equal(tierOf("gold"), "gold");
  assert.equal(tierOf("platinum"), "bronze", "an unknown plan is the first one");
  assert.equal(tierOf(undefined), "bronze", "rows from before plans existed");
  assert.equal(maxDevices({ tier: "vip" }), 0, "VIP: no limit");
  // A device asking for a slot: refused once the others fill the plan.
  const a = accountOf(user({ tier: "silver" }), t, { others: 1 });
  assert.equal(a.allowed, true);
  assert.deepEqual([a.tier, a.max_devices, a.devices], ["silver", 2, 2]);
  const b = accountOf(user({ tier: "silver" }), t, { others: 2 });
  assert.equal(b.reason, "devices");
  assert.equal(b.devices, 2);
  assert.equal(accountOf(user(), t, { others: 1 }).reason, "devices", "bronze: one device");
  const v = accountOf(user({ tier: "vip" }), t, { others: 70 });
  assert.equal(v.allowed, true);
  assert.deepEqual([v.max_devices, v.devices], [0, 71]);
  // The other limits come first.
  assert.equal(accountOf(user({ used_bytes: 1000 }), t, { others: 5 }).reason, "quota");
  // A page sees how many devices hold a slot and is never refused for it.
  const w = accountOf(user(), t, { active: 1 });
  assert.equal(w.allowed, true);
  assert.equal(w.devices, 1);
  assert.equal(accountOf(user(), t).devices, 0);
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
  const byAddr = edgeLinks("edge.example.org", uuid, ["104.16.1.2", "not an address", "188.114.96.3"]);
  assert.equal(byAddr.length, 5, "one more link per clean address");
  assert.equal(new URL(byAddr[3]).hostname, "104.16.1.2");
  assert.equal(new URL(byAddr[3]).searchParams.get("sni"), "edge.example.org");
  assert.equal(new URL(byAddr[4]).hostname, "188.114.96.3");
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
  for (const p of ["servers/hot.txt", "servers/all-8.txt", "servers/tor.json", "servers/crowd.json", "servers/iran.json", "servers/own-0.bin", "servers/own-4.bin", "servers/hot.txt.sealed", "servers/own-2.bin.sealed", "servers/reach.json", "main/update.json", "rules/ads.srs", "rules/ads.json", "rules/LICENSE-GPL-3.0.txt", "tools/collect-linux-amd64"]) assert.ok(LIST_PATH.test(p), p);
  assert.equal(listType("tools/collect-linux-amd64"), "application/octet-stream");
  for (const p of ["servers/all-9.txt", "servers/own-5.bin", "main/README.md", "../x", "servers/hot.txt/..", "rules/other.json", "servers/iran-tcp.json", "servers/tor.json.sealed"]) assert.ok(!LIST_PATH.test(p), p);
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

test("subscriptions: countries read from names, servers renamed, the edge first", async () => {
  const { build, countriesOf, headers, nameOf, parseList, rename } = await import("../src/sub.js");
  const vmess = "vmess://" + Buffer.from(JSON.stringify({ v: "2", ps: "🇯🇵 Tokyo | @chan", add: "1.2.3.4", port: "443", id: "x" })).toString("base64");
  const text = [
    "vless://u@h:443?security=tls#US%20%F0%9F%87%BA%F0%9F%87%B8%20%7C%20%40Raydikalx%20%7C%200F0A76",
    "trojan://p@h:443#Germany-1", "ss://YWVzOmJhc2U@h:8388#@vpnclashfa - 673", vmess, "", "# a comment", "not a link",
    "vless://u@h:443#%F0%9F%87%AE%F0%9F%87%B7%20Iran",
  ].join("\n");
  const entries = parseList(text);
  assert.deepEqual(entries.map((e) => e.country), ["US", "DE", "", "JP", "IR"]);
  assert.equal(nameOf(vmess), "🇯🇵 Tokyo | @chan");
  assert.deepEqual(countriesOf(entries).map((c) => [c.code, c.name, c.count]), [["DE", "Germany", 1], ["JP", "Japan", 1], ["US", "United States", 1]]);

  const edge = ["vless://e@edge:443?type=ws#Albatross%20edge%20443"];
  const all = build(entries, "", "Albatross", edge);
  const links = atob(all.body).trim().split("\n");
  assert.equal(links.length, 5, "every server but the home country's, after the edge");
  assert.equal(links[0], "vless://e@edge:443?type=ws#Albatross%20Edge%201");
  assert.equal(nameOf(links[1]), "Albatross 🇺🇸 United States 1 · vless");
  assert.equal(nameOf(links[3]), "Albatross Server 3 · shadowsocks", "a server without a country keeps no name of its own");
  assert.equal(nameOf(links[4]), "Albatross 🇯🇵 Japan 4 · vmess");
  assert.ok(links[4].startsWith("vmess://"));
  assert.equal(all.title, "Albatross All servers");

  const de = build(entries, "DE", "Albatross", edge);
  assert.deepEqual(atob(de.body).trim().split("\n").map(nameOf), ["Albatross 🇩🇪 Germany 1 · trojan"]);
  assert.equal(de.title, "Albatross Germany");
  assert.equal(build(entries, "DE", "Albatross", edge, "2026-10-26T11:05:09Z").title, "Albatross Germany · 26 Oct 11:05 UTC");
  assert.equal(build(entries, "DE", "Albatross", edge, "junk").title, "Albatross Germany");
  assert.equal(atob(build(entries, "edge", "Albatross", edge).body).trim().split("\n").length, 1);
  assert.equal(rename("ss://x@y:1#old", "new name"), "ss://x@y:1#new%20name");

  const h = headers("Albatross Germany", { used_bytes: 5, quota_bytes: 100, expires_at: 1700000000 });
  assert.equal(h.get("Subscription-Userinfo"), "upload=0; download=5; total=100; expire=1700000000");
  assert.equal(h.get("Profile-Title"), "base64:" + btoa("Albatross Germany"));
  assert.equal(headers("x", { used_bytes: 0, quota_bytes: 0, expires_at: 0 }).get("Subscription-Userinfo"), "upload=0; download=0");
});


test("a subscription is topped up from the fresh list when the hot list is short, with Apple-usable servers only", () => {
  const hot = ["vless://a@h1:443#Germany", "ss://YWVzOmJhc2U@h2:8388#US", "albatross://x@h3:1#own", "trojan://p@h4:443#Iran", "wireguard://k@h5:51820#NL"].join("\n");
  const fresh = Array.from({ length: 500 }, (_, i) => `vmess://${Buffer.from(JSON.stringify({ add: "f" + i, ps: "FR " + i })).toString("base64")}`).join("\n");
  const full = pool(hot, fresh, 2);
  assert.deepEqual(full.map((e) => e.link.split("://")[0]), ["vless", "ss", "wireguard"], "enough hot servers: no top-up, own protocols and Iran left out, WireGuard kept");
  let n = 0;
  const seq = () => ((n += 0.37) % 1);
  const topped = pool(hot, fresh, 10, seq);
  assert.equal(topped.length, 10);
  assert.equal(topped.filter((e) => e.link.startsWith("vmess://")).length, 7);
  const other = pool(hot, fresh, 10);
  assert.equal(other.length, 10);
  assert.equal(pool(hot, "", 10).length, 3, "nothing fresh: the hot list alone");
  const udp = pool("hysteria2://a@h:443#DE\nvless://b@h:443#US\ntuic://c@h:443#FR\nss://YWVzOmJhc2U@h2:8388#NL", "", 1);
  assert.deepEqual(udp.map((e) => e.link.split("://")[0]), ["vless", "ss", "hysteria2", "tuic"], "UDP servers go last");
});

// A sealed list (the collector's SealKeyed, sample made with the Go side)
// opens with the key of its id, and with nothing else.
test("a sealed list opens with the collector's key of its id", async () => {
  const key = "00070e151c232a31383f464d545b626970777e858c939aa1a8afb6bdc4cbd2d9";
  const bytes = Uint8Array.from(atob("QUxCSzEAAAAAaP4NQHJEHYIFi+TuDMmpLmqPOJKUD8RU3TSupdYsuHO/I9i+lWY3jVMXKSqf+K8sz/qHL07M/WVrDYU8jDSs+WnVQYLZaOelP8y6dPQGcllDHqRK6g=="), (c) => c.charCodeAt(0));
  assert.equal(keyedID(bytes), 1761480000);
  assert.equal(await openKeyed([{ id: 1, key: "ab".repeat(32) }, { id: 1761480000, key }], bytes), "vless://a@h1:443#Germany\nss://YWVzOmJhc2U@h2:8388#US\n");
  assert.equal(await openKeyed([{ id: 1761480000, key: "ab".repeat(32) }], bytes), "", "the wrong key opens nothing");
  assert.equal(await openKeyed([], bytes), "");
  assert.equal(keyedID(new TextEncoder().encode("vless://plain")), null);
});

test("a subscription of a full hot list of long links builds (no argument-limit error)", async () => {
  const { build } = await import("../src/sub.js");
  const long = "vless://11111111-1111-1111-1111-111111111111@192.0.2.1:443?security=reality&type=xhttp&extra=" + "x".repeat(1200);
  const entries = Array.from({ length: 450 }, (_, i) => ({ link: long + i + "#s" + i, country: "DE" }));
  const sub = build(entries, "", "Albatross", []);
  assert.equal(sub.count, 450);
  const text = Buffer.from(sub.body, "base64").toString("utf8");
  assert.equal(text.split("\n").filter(Boolean).length, 450);
  assert.ok(text.includes("Germany"), "the names survive the encoding (and the flag, which is not ASCII)");
});

test("each feature comes with its plan and every plan above it", () => {
  assert.deepEqual(featuresOf("bronze"), []);
  assert.deepEqual(featuresOf("silver"), ["locations", "adblock", "bypass_apps"]);
  assert.deepEqual(featuresOf("gold"), ["locations", "adblock", "bypass_apps", "protocols", "tun", "gps", "edge", "own"]);
  assert.deepEqual(featuresOf("vip"), Object.keys(FEATURES));
  assert.deepEqual(featuresOf("platinum"), [], "an unknown plan is the first one");
  for (const f of Object.values(FEATURES)) assert.ok(Object.hasOwn(TIERS, f.tier), f.name);
});

test("priority on the own servers and direct support are the highest plan's alone", () => {
  for (const f of ["priority", "support"]) {
    assert.equal(FEATURES[f].tier, "vip");
    assert.ok(!featuresOf("gold").includes(f), f + " is not in Gold");
    assert.ok(featuresOf("vip").includes(f), f + " is in VIP");
  }
});

test("the administrator's choice of plan for a feature wins where it names a known feature and plan", () => {
  const p = planFeatures('{"lan":"gold","own":"platinum","teleport":"vip"}');
  assert.equal(p.lan, "gold");
  assert.equal(p.own, "gold", "an unknown plan keeps the feature's own");
  assert.equal(Object.hasOwn(p, "teleport"), false);
  assert.deepEqual(planFeatures("not json"), planFeatures(), "an unreadable setting is no choice");
  assert.deepEqual(featuresOf("gold", p), ["locations", "adblock", "bypass_apps", "protocols", "tun", "gps", "edge", "own", "lan"]);
  const a = accountOf({ username: "x", tier: "silver", quota_bytes: 0, max_seconds: 0, expires_at: 0, reset_days: 0 }, 1e9, { plans: p });
  assert.deepEqual(a.features, ["locations", "adblock", "bypass_apps"]);
  assert.equal(a.plans.lan, "gold");
  assert.deepEqual(a.plan_devices, { bronze: 1, silver: 2, gold: 4, vip: 0 });
});

// The links of the Apple subscriptions, in the standard form the engine's
// reading of them makes: what public lists write every which way comes
// out as the share links every app reads.
test("subscription links come out in the standard form, and the broken ones not at all", async () => {
  const { cleanLink } = await import("../src/links.js");
  const id = "11111111-2222-4333-8444-555555555555";
  // A vless link behind "ss://", and one with "&amp;" for "&".
  assert.equal(cleanLink(`ss://${id}@1.2.3.4:443?security=tls&type=ws&host=a.example.com&path=%2Fws#x`, "x"),
    `vless://${id}@1.2.3.4:443?encryption=none&security=tls&type=ws&host=a.example.com&path=%2Fws#x`);
  assert.equal(cleanLink(`vless://${id}@1.2.3.4:443?security=tls&amp;type=ws&amp;host=a.example.com&amp;path=%2F%3Fed%3D2048`),
    `vless://${id}@1.2.3.4:443?encryption=none&security=tls&type=ws&host=a.example.com&path=%2F%3Fed%3D2048`);
  assert.equal(cleanLink("hy2://pw@h.example.com:443?obfs=salamander&obfs-password=op&mport=20000-30000", "n"),
    "hysteria2://pw@h.example.com:443?obfs=salamander&obfs-password=op&mport=20000-30000#n");
  // VMess JSON with numbers and its early data in the path; and behind "ss://".
  const j = { v: 2, ps: "a", add: "v.example.com", port: 443, id, aid: 0, net: "ws", path: "/p?ed=2048", tls: "tls" };
  const vm = "vmess://" + Buffer.from(JSON.stringify(j)).toString("base64");
  const out = JSON.parse(Buffer.from(cleanLink(vm, "a").slice(8), "base64").toString());
  assert.deepEqual([out.port, out.aid, out.net, out.path, out.tls, out.ps], ["443", "0", "ws", "/p?ed=2048", "tls", "a"]);
  assert.ok(cleanLink("ss://" + vm.slice(8), "a").startsWith("vmess://"));
  // Shadowsocks: SIP002 base64, and SIP022's key in the clear.
  assert.equal(cleanLink("ss://" + btoa("aes-256-gcm:pass") + "@s.example.com:8388", "s"), "ss://YWVzLTI1Ni1nY206cGFzcw@s.example.com:8388#s");
  const key = btoa("k".repeat(32));
  assert.equal(cleanLink("ss://" + btoa("2022-blake3-aes-256-gcm:" + key) + "@s.example.com:8388"),
    "ss://2022-blake3-aes-256-gcm:" + encodeURIComponent(key) + "@s.example.com:8388");
  // What the engine refuses stays out: a bad Reality key, no password, no port, a name without a dot, a short 2022 key.
  for (const bad of [
    `vless://${id}@1.2.3.4:443?security=reality&pbk=short&type=tcp`, "trojan://@t.example.com:443", `vless://${id}@1.2.3.4`,
    `vless://${id}@localhost:443`, "ss://" + btoa("2022-blake3-aes-256-gcm:" + btoa("short")) + "@s.example.com:8388", "albatross://x@h.example.com:1",
  ]) assert.equal(cleanLink(bad), null, bad);
  // Written once, a link reads back the same.
  for (const l of [`ss://${id}@1.2.3.4:443?security=tls&type=ws&host=a.example.com&path=%2Fws`, vm, "hy2://pw@h.example.com:443?mport=20000-30000"]) {
    assert.equal(cleanLink(cleanLink(l, "n"), "n"), cleanLink(l, "n"));
  }
});

test("the sing-box apps get a sing-box config, with what sing-box cannot run left out", async () => {
  const { singBoxConfig, wantsSingBox } = await import("../src/singbox.js");
  const id = "11111111-2222-4333-8444-555555555555";
  const wg = (k) => btoa(String.fromCharCode(...new Uint8Array(32).fill(k)));
  const c = await singBoxConfig([
    { link: `vless://${id}@1.2.3.4:443?security=reality&pbk=${wg(1).replace(/=+$/, "")}&sid=ab&sni=www.example.com&type=tcp&flow=xtls-rprx-vision`, name: "A 1 · vless" },
    { link: `vless://${id}@x.example.com:443?type=xhttp&security=tls`, name: "A 2 · vless" },
    { link: `vmess://${id}@v.example.com:443?type=grpc&serviceName=g&mode=multi&security=tls`, name: "A 3 · vmess" },
    { link: `wireguard://${encodeURIComponent(wg(2))}@w.example.com:51820?publickey=${encodeURIComponent(wg(3))}&address=172.16.0.2&reserved=1,2,3`, name: "A 4 · wireguard" },
    { link: "hy2://pw@h.example.com:443?obfs=salamander&obfs-password=op&mport=20000-30000", name: "A 5 · hysteria2" },
  ]);
  assert.deepEqual(c.outbounds.map((o) => o.tag), ["select", "auto", "A 1 · vless", "A 5 · hysteria2", "direct"], "XHTTP and gRPC's multi mode are Xray's");
  assert.deepEqual(c.outbounds[0].outbounds, ["auto", "A 1 · vless", "A 4 · wireguard", "A 5 · hysteria2"]);
  const v = c.outbounds[2];
  assert.deepEqual([v.flow, v.tls.reality.short_id, v.tls.utls.fingerprint, v.tls.server_name], ["xtls-rprx-vision", "ab", "chrome", "www.example.com"]);
  assert.deepEqual(c.endpoints.map((e) => [e.type, e.address[0], e.peers[0].reserved]), [["wireguard", "172.16.0.2/32", [1, 2, 3]]]);
  assert.deepEqual([c.outbounds[3].server_ports, c.outbounds[3].obfs], [["20000:30000"], { type: "salamander", password: "op" }]);
  assert.equal(await singBoxConfig([{ link: `vless://${id}@x.example.com:443?type=xhttp`, name: "x" }]), null);

  const req = (ua) => new Request("https://a.example/sub/t", { headers: ua ? { "User-Agent": ua } : {} });
  const url = (q = "") => new URL("https://a.example/sub/t" + q);
  assert.equal(wantsSingBox(req(""), url("?f=singbox")), true);
  assert.equal(wantsSingBox(req("SFI/1.12.0 (Build 1; sing-box 1.12.0; language en_US)"), url()), true);
  assert.equal(wantsSingBox(req("HiddifyNext/2.5.7 (ios) like ClashMeta v2ray sing-box"), url()), false, "Hiddify reads the links itself");
  assert.equal(wantsSingBox(req("V2Box/10.1.9"), url()), false);
});
