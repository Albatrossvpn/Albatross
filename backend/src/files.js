// The release files (the Android APK and the Windows installer), kept in
// the project's own storage (an R2 bucket) and served from the website
// and the account service, so no download depends on GitHub. Shared by
// the website Worker (site/src/index.js) and the account service.

/** The files that may be stored and served, by name. */
export const FILE_NAME = /^(Albatross-\d+\.\d+\.\d+-android\.apk|Albatross-Setup-\d+\.\d+\.\d+\.exe)$/;

/** Where the files come from: this project's GitHub releases, and only those. */
export const FILE_SOURCE = /^https:\/\/github\.com\/Albatrossvpn\/Albatross\/releases\/download\/v\d+\.\d+\.\d+\/(Albatross-[^/?#]+)$/;

export function contentType(name) {
  return name.endsWith(".apk") ? "application/vnd.android.package-archive" : "application/octet-stream";
}

/** "bytes=a-b", "bytes=a-" or "bytes=-n" as an R2 range, or null for the whole file. */
export function parseRange(h, size) {
  const m = /^bytes=(\d*)-(\d*)$/.exec(h || "");
  if (!m || (!m[1] && !m[2])) return null;
  if (!m[1]) {
    const n = Math.min(Number(m[2]), size);
    return n > 0 ? { offset: size - n, length: n } : null;
  }
  const start = Number(m[1]);
  if (start >= size) return { invalid: true };
  const end = m[2] ? Math.min(Number(m[2]), size - 1) : size - 1;
  if (end < start) return { invalid: true };
  return { offset: start, length: end - start + 1 };
}

/**
 * Serves one stored file for a GET or HEAD request, with ranges (so a cut
 * download resumes) and conditional requests. Null when the file is not
 * stored, so the caller may fall back or answer 404.
 */
export async function serveFile(request, bucket, name) {
  if (!FILE_NAME.test(name)) return null;
  const head = await bucket.head(name);
  if (!head) return null;
  const range = parseRange(request.headers.get("Range"), head.size);
  if (range && range.invalid) {
    return new Response("Range not satisfiable", { status: 416, headers: { "Content-Range": `bytes */${head.size}` } });
  }
  const headers = new Headers({
    "Content-Type": contentType(name),
    "Content-Disposition": `attachment; filename="${name}"`,
    "Accept-Ranges": "bytes",
    "Cache-Control": "public, max-age=86400",
    "X-Content-Type-Options": "nosniff",
    "ETag": head.httpEtag,
  });
  const inm = request.headers.get("If-None-Match");
  if (inm && !range && inm.split(",").map((s) => s.trim()).includes(head.httpEtag)) {
    return new Response(null, { status: 304, headers });
  }
  if (range) {
    headers.set("Content-Range", `bytes ${range.offset}-${range.offset + range.length - 1}/${head.size}`);
    headers.set("Content-Length", String(range.length));
  } else {
    headers.set("Content-Length", String(head.size));
  }
  if (request.method === "HEAD") return new Response(null, { status: range ? 206 : 200, headers });
  const obj = await bucket.get(name, range ? { range } : undefined);
  if (!obj) return null;
  return new Response(obj.body, { status: range ? 206 : 200, headers });
}

/**
 * Stores a release file from its source, streamed straight into the
 * bucket. Returns what was stored; throws with a message for the caller
 * on a bad name or source, or a failed fetch.
 */
export async function pullFile(bucket, name, source, { force = false } = {}) {
  if (!FILE_NAME.test(name)) throw new Error("Not a release file name.");
  const m = FILE_SOURCE.exec(source || "");
  if (!m || m[1] !== name) throw new Error("Not a release address for that file.");
  const have = await bucket.head(name);
  const up = await fetch(source, { redirect: "follow", headers: { "Accept-Encoding": "identity" } });
  if (!up.ok) throw new Error(`The source answered ${up.status}.`);
  const size = Number(up.headers.get("Content-Length"));
  if (!size) throw new Error("The source gives no length.");
  if (have && have.size === size && !force) {
    await up.body.cancel();
    return { name, size, stored: false };
  }
  const { readable, writable } = new FixedLengthStream(size);
  const pump = up.body.pipeTo(writable);
  await bucket.put(name, readable, { httpMetadata: { contentType: contentType(name) }, customMetadata: { source } });
  await pump;
  return { name, size, stored: true };
}
