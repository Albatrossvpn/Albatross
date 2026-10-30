import { test } from "node:test";
import assert from "node:assert/strict";
import { createHash } from "node:crypto";
import { readFileSync, readdirSync } from "node:fs";

// The public website (site/public) carries Google's ad-measurement tag in the
// head of every page, once and first, and its Content-Security-Policy lets
// that tag run (the inline part by its hash, so a changed snippet that is not
// hashed again is caught here, not by a silently blocked tag). The account
// service's pages carry no tag, and every privacy statement says what the
// pages really do.

const site = new URL("../../site/public/", import.meta.url);
const accounts = new URL("../public/", import.meta.url);
const read = (base, name) => readFileSync(new URL(name, base), "utf8");
const pages = readdirSync(site).filter((f) => f.endsWith(".html"));

const ID = "AW-18414057252";
const LOADER = `<script async src="https://www.googletagmanager.com/gtag/js?id=${ID}"></script>`;
const INLINE = /<script>(\s*window\.dataLayer[\s\S]*?)<\/script>/g;
const count = (text, part) => text.split(part).length - 1;

test("every page of the website has the Google tag once, high in the head", () => {
  assert.ok(pages.includes("index.html") && pages.includes("404.html"), pages.join());
  for (const p of pages) {
    const html = read(site, p);
    assert.equal(count(html, LOADER), 1, `${p}: the loader`);
    assert.equal([...html.matchAll(INLINE)].length, 1, `${p}: the inline script`);
    assert.equal(count(html, `gtag('config', '${ID}')`), 1, `${p}: the config call`);
    // Before the title, the styles and every other script: right after the charset.
    const before = html.slice(0, html.indexOf(LOADER));
    assert.ok(before.includes("<head>") && before.includes("<meta charset"), `${p}: inside the head`);
    assert.ok(!/<title|<link |<script/.test(before), `${p}: nothing else comes first`);
  }
});

test("the website's CSP lets the tag run, and no more than that", () => {
  const csp = read(site, "_headers").match(/Content-Security-Policy: (.+)/)[1];
  const directive = (name) =>
    (csp.split(";").map((s) => s.trim()).find((s) => s.startsWith(name + " ")) || "").split(/\s+/).slice(1);
  const hashes = new Set(pages.map((p) => {
    const inline = [...read(site, p).matchAll(INLINE)][0][1];
    return `'sha256-${createHash("sha256").update(inline).digest("base64")}'`;
  }));
  assert.equal(hashes.size, 1, "every page carries the same inline script");
  const [hash] = hashes;
  assert.ok(directive("script-src").includes(hash), `script-src lacks ${hash}`);
  for (const d of ["script-src", "img-src", "connect-src", "frame-src"]) {
    assert.ok(directive(d).includes("https://www.googletagmanager.com"), d);
    assert.ok(!directive(d).some((s) => s === "*" || s === "'unsafe-inline'" || s === "'unsafe-eval'" || s === "https:"), `${d} stays narrow`);
  }
  assert.deepEqual(directive("default-src"), ["'self'"]);
  assert.deepEqual(directive("frame-ancestors"), ["'none'"]);
});

test("the account service's pages carry no Google tag", () => {
  for (const p of readdirSync(accounts).filter((f) => f.endsWith(".html"))) {
    assert.ok(!/googletagmanager|gtag\(/.test(read(accounts, p)), p);
  }
  assert.ok(!/googletagmanager|google\.com/.test(read(accounts, "_headers")), "the account CSP stays closed");
});

test("the privacy statements say what the website does", () => {
  const index = read(site, "index.html");
  assert.ok(!/no analytics|no third-party requests/i.test(index), "the page no longer claims it loads nothing from others");
  assert.match(index, /Google's ad-measurement tag, which may set cookies/);
  assert.match(index, /privacy#cookies/);
  const privacy = read(accounts, "privacy.html");
  assert.match(privacy, /<h2 id="cookies">/);
  assert.match(privacy, /Google's advertising tag/);
  assert.match(privacy, /neither do the apps/);
});

test("the website is ready for Google Search Console: a sitemap, a robots file that names it, one canonical page", () => {
  const robots = read(site, "robots.txt");
  assert.match(robots, /^User-agent: \*$/m);
  assert.match(robots, /^Sitemap: https:\/\/albatrossvpn\.online\/sitemap\.xml$/m);
  assert.doesNotMatch(robots, /^Disallow: \/$/m, "the site is not shut out");
  const sitemap = read(site, "sitemap.xml");
  assert.match(sitemap, /<loc>https:\/\/albatrossvpn\.online\/<\/loc>/);
  assert.match(sitemap, /<urlset xmlns="http:\/\/www\.sitemaps\.org\/schemas\/sitemap\/0\.9">/);
  assert.match(read(site, "index.html"), /<link rel="canonical" href="https:\/\/albatrossvpn\.online\/">/);
  assert.match(read(site, "404.html"), /<meta name="robots" content="noindex">/, "the not-found page stays out of the index");
});
