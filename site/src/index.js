// The website: static pages, plus the release files at /download/<name>
// from the project's own storage, so getting the app never depends on
// GitHub (backend/src/files.js). /download/ lists what is stored, and
// /update.json is the apps' release manifest, from the project's own
// storage (or GitHub while it is not there yet), so the page's buttons
// never reach GitHub themselves. /list/<path> serves the app's own files
// from the same storage, one more address for the apps.
import { FILE_NAME, serveFile, serveList } from "../../backend/src/files.js";
import { armor, securityTxt } from "../../backend/src/headers.js";

const OPEN = { "Strict-Transport-Security": "max-age=0" };
const serve = (response) => armor(response, OPEN);

const MANIFEST = [
  "https://raw.githubusercontent.com/Albatrossvpn/Albatross/main/update.json",
  "https://cdn.jsdelivr.net/gh/Albatrossvpn/Albatross@main/update.json",
];

export default {
  async fetch(request, env) {
    const url = new URL(request.url);
    // What the Worker answers itself carries the security headers; the static
    // pages carry theirs from public/_headers. The website answers over plain
    // HTTP as well as HTTPS (where HTTPS is blocked, HTTP may still get
    // through), so it asks no browser to insist on HTTPS. The account service,
    // where passwords are typed, stays HTTPS only.
    if (url.pathname === "/.well-known/security.txt") return serve(securityTxt(request, "support@albatrossvpn.online"));
    if (url.pathname === "/update.json") return serve((await serveList(request, env.FILES, "main/update.json")) || (await manifest()));
    if (url.pathname.startsWith("/list/")) {
      const r = await serveList(request, env.FILES, url.pathname.slice("/list/".length));
      return serve(r || new Response("Not found", { status: 404, headers: { "Content-Type": "text/plain; charset=utf-8" } }));
    }
    const m = /^\/download\/([^/]*)$/.exec(url.pathname);
    if (!m) return env.ASSETS.fetch(request);
    if (request.method !== "GET" && request.method !== "HEAD") {
      return serve(new Response("Method not allowed", { status: 405, headers: { Allow: "GET, HEAD" } }));
    }
    if (!m[1]) return serve(await index(env.FILES));
    let name;
    try {
      name = decodeURIComponent(m[1]);
    } catch {
      return serve(new Response("Not found", { status: 404, headers: { "Content-Type": "text/plain; charset=utf-8" } }));
    }
    const r = await serveFile(request, env.FILES, name);
    return serve(r || new Response("Not found", { status: 404, headers: { "Content-Type": "text/plain; charset=utf-8" } }));
  },
};

async function manifest() {
  for (const u of MANIFEST) {
    try {
      const up = await fetch(u, { cf: { cacheTtl: 600, cacheEverything: true } });
      if (up.ok) {
        return new Response(up.body, {
          headers: { "Content-Type": "application/json; charset=utf-8", "Cache-Control": "public, max-age=600", "X-Content-Type-Options": "nosniff" },
        });
      }
    } catch {}
  }
  return new Response("{}", { status: 503, headers: { "Content-Type": "application/json; charset=utf-8", "Cache-Control": "no-store" } });
}

const esc = (s) => String(s).replace(/[&<>"]/g, (c) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;" })[c]);

/** The stored files, newest version first, as a plain page. */
async function index(bucket) {
  const list = await bucket.list({ limit: 200 });
  const ver = (n) => (n.match(/(\d+)\.(\d+)\.(\d+)/) || [0, 0, 0, 0]).slice(1).map(Number);
  const files = list.objects.filter((o) => FILE_NAME.test(o.key)).sort((a, b) => {
    const [x, y] = [ver(a.key), ver(b.key)];
    for (let i = 0; i < 3; i++) if (x[i] !== y[i]) return y[i] - x[i];
    return a.key.localeCompare(b.key);
  });
  const mb = (n) => (n / 1048576).toFixed(1) + " MB";
  const rows = files.map((o) => `<li><a href="/download/${encodeURIComponent(o.key)}">${esc(o.key)}</a> <small>${mb(o.size)}</small></li>`).join("\n");
  const html = `<!doctype html><html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>Albatross downloads</title>
<link rel="stylesheet" href="/site.css"><link rel="icon" href="/icon.svg"></head>
<body><main class="wrap downloads"><h1>Downloads</h1>
<p class="muted">Every release file, served from Albatross's own storage. The SHA-256 of the current version is on the <a href="/#download">download page</a>; the app checks it before installing an update.</p>
<ul class="plain">${rows || "<li>No files yet.</li>"}</ul>
<p><a href="/">&larr; Back</a></p></main></body></html>`;
  return new Response(html, { headers: { "Content-Type": "text/html; charset=utf-8", "Cache-Control": "public, max-age=300", "X-Content-Type-Options": "nosniff" } });
}
