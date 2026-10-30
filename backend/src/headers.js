// The security headers of what a Worker answers itself. The static pages get
// theirs from each site's public/_headers; the answers the Workers build (the
// API, the lists, the release files, the downloads page) would otherwise
// carry only what each handler remembered to set.

const BASE = {
  "Strict-Transport-Security": "max-age=31536000; includeSubDomains",
  "X-Content-Type-Options": "nosniff",
  "X-Frame-Options": "DENY",
  "Referrer-Policy": "no-referrer",
  "Permissions-Policy": "camera=(), microphone=(), geolocation=(), payment=(), usb=()",
  "X-Permitted-Cross-Domain-Policies": "none",
  "Cross-Origin-Resource-Policy": "same-site",
};

// A page the Worker builds loads only its own style and images; everything
// else (JSON, text, files) may load nothing at all.
const PAGE = "default-src 'self'; style-src 'self'; img-src 'self' data:; base-uri 'none'; form-action 'none'; frame-ancestors 'none'";
const DATA = "default-src 'none'; frame-ancestors 'none'";

/** The response with the security headers a handler did not set itself;
 * `own` overrides some of them for one site. */
export function armor(response, own = {}) {
  const out = new Response(response.body, response);
  for (const [name, value] of Object.entries({ ...BASE, ...own })) if (!out.headers.has(name)) out.headers.set(name, value);
  if (!out.headers.has("Content-Security-Policy")) {
    out.headers.set("Content-Security-Policy", /^text\/html/i.test(out.headers.get("Content-Type") || "") ? PAGE : DATA);
  }
  return out;
}

/**
 * /.well-known/security.txt (RFC 9116): where to report a weakness. The
 * contact is the service's address, the one on its pages. The expiry moves
 * with the day, so the file never goes stale.
 */
export function securityTxt(request, contact) {
  const host = new URL(request.url).host;
  const expires = new Date(Date.now() + 90 * 86400 * 1000).toISOString().replace(/\.\d+Z$/, "Z");
  const text = [
    `Contact: mailto:${contact}`,
    `Expires: ${expires}`,
    "Preferred-Languages: en, fa",
    `Canonical: https://${host}/.well-known/security.txt`,
    "",
  ].join("\n");
  return new Response(text, { headers: { "Content-Type": "text/plain; charset=utf-8", "Cache-Control": "public, max-age=3600" } });
}

/**
 * A redirect to the same address over HTTPS for a request that came over
 * plain HTTP through Cloudflare's edge (it adds CF-Ray; `wrangler dev` and the
 * tests, which present the production host over HTTP, do not), else null. A
 * page, an API call or an installer fetched over HTTP can be read and changed
 * on the way (HSTS protects only a browser that has already been here over
 * HTTPS). The account service answers first for its pages and its API,
 * so no password or token travels in the clear. The public website, on
 * purpose, answers over HTTP too (site/src/index.js).
 */
export function httpsRedirect(request) {
  const url = new URL(request.url);
  if (url.protocol !== "http:" || !request.headers.has("CF-Ray")) return null;
  url.protocol = "https:";
  const safe = request.method === "GET" || request.method === "HEAD";
  return new Response(null, { status: safe ? 301 : 308, headers: { Location: url.toString(), "Cache-Control": "no-store" } });
}
