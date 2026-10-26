// The edge Worker: VLESS over WebSocket. The app connects to this name (or
// to any Cloudflare edge address with this name as SNI and Host) with the
// user id the account service gave it; the Worker opens the destination
// with Cloudflare's socket API and relays bytes both ways. UDP is carried
// only for DNS (port 53), answered over DNS over HTTPS. Anything that is
// not a WebSocket upgrade gets an ordinary "not found" page.
import { connect } from "cloudflare:sockets";
import { decodeEarly, forbidden, parseHeader, relayTarget, relays } from "./vless.js";

export default {
  async fetch(request, env) {
    if ((request.headers.get("Upgrade") || "").toLowerCase() !== "websocket") {
      return new Response("Not found", { status: 404, headers: { "Content-Type": "text/plain; charset=utf-8" } });
    }
    const uuid = (env.EDGE_UUID || "").toLowerCase();
    if (!/^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/.test(uuid)) return new Response("Not configured", { status: 503 });
    const pair = new WebSocketPair();
    const [client, server] = Object.values(pair);
    server.accept();
    const early = decodeEarly(request.headers.get("sec-websocket-protocol") || "");
    relay(server, early, uuid.replace(/-/g, ""), relays(env)).catch(() => close(server));
    return new Response(null, { status: 101, webSocket: client });
  },
};

function close(ws) {
  try {
    if (ws.readyState === 1 || ws.readyState === 2) ws.close(1000);
  } catch {}
}

/** The WebSocket's messages as a byte stream, early data first. */
function incoming(ws, early) {
  return new ReadableStream({
    start(ctl) {
      if (early && early.length) ctl.enqueue(early);
      ws.addEventListener("message", (e) => {
        const d = e.data;
        ctl.enqueue(typeof d === "string" ? new TextEncoder().encode(d) : new Uint8Array(d));
      });
      ws.addEventListener("close", () => {
        try { ctl.close(); } catch {}
      });
      ws.addEventListener("error", (e) => ctl.error(e));
    },
    cancel() {
      close(ws);
    },
  });
}

async function relay(ws, early, uuidHex, relayList) {
  const reader = incoming(ws, early).getReader();
  const first = await reader.read();
  if (first.done) return close(ws);
  const h = parseHeader(first.value, uuidHex);
  if (h.err || forbidden(h.host)) return close(ws);
  const reply = new Uint8Array([h.version, 0]);
  if (h.cmd === 2) {
    if (h.port !== 53) return close(ws);
    return dns(ws, reader, h.rest, reply);
  }
  await tcp(ws, reader, h, reply, relayList);
}

/** TCP: open the destination, then relay; retried through the relays when the destination sends nothing back. */
async function tcp(ws, reader, h, reply, relayList) {
  const targets = [{ hostname: h.host, port: h.port }];
  for (const r of relayList) {
    const t = relayTarget(r, h.port);
    if (t) targets.push(t);
  }
  let sock = null;
  let writer = null;
  let pending = h.rest.length ? [h.rest] : [];
  // Bytes from the app go to whatever socket is current.
  const pump = (async () => {
    for (;;) {
      const { value, done } = await reader.read();
      if (done) break;
      if (writer) await writer.write(value);
      else pending.push(value);
    }
    if (writer) await writer.close().catch(() => {});
  })();
  for (let i = 0; i < targets.length && ws.readyState === 1; i++) {
    try {
      sock = connect(targets[i]);
      await sock.opened;
    } catch {
      sock = null;
      continue;
    }
    writer = sock.writable.getWriter();
    for (const p of pending) await writer.write(p);
    pending = [];
    const got = await toClient(sock, ws, reply);
    if (got) break;
    // Nothing came back (a site behind Cloudflare, or a dead address): try the next way.
    try { writer.releaseLock(); } catch {}
    writer = null;
    try { sock.close(); } catch {}
    pending = h.rest.length ? [h.rest] : [];
  }
  close(ws);
  await pump.catch(() => {});
}

/** Relays the destination's bytes to the app; true when any arrived. */
async function toClient(sock, ws, reply) {
  let got = false;
  let header = reply;
  await sock.readable.pipeTo(new WritableStream({
    write(chunk) {
      got = true;
      if (ws.readyState !== 1) throw new Error("closed");
      if (header) {
        const out = new Uint8Array(header.length + chunk.length);
        out.set(header, 0);
        out.set(chunk, header.length);
        ws.send(out);
        header = null;
      } else {
        ws.send(chunk);
      }
    },
  })).catch(() => {});
  return got;
}

/** UDP to port 53: each length-prefixed query is answered over DNS over HTTPS. */
async function dns(ws, reader, rest, reply) {
  let buf = rest;
  let header = reply;
  const answer = async (q) => {
    const resp = await fetch("https://cloudflare-dns.com/dns-query", {
      method: "POST",
      headers: { "Content-Type": "application/dns-message" },
      body: q,
    });
    const a = new Uint8Array(await resp.arrayBuffer());
    const out = new Uint8Array((header ? header.length : 0) + 2 + a.length);
    let o = 0;
    if (header) { out.set(header, 0); o = header.length; header = null; }
    out[o] = a.length >> 8;
    out[o + 1] = a.length & 255;
    out.set(a, o + 2);
    if (ws.readyState === 1) ws.send(out);
  };
  for (;;) {
    while (buf.length >= 2) {
      const n = (buf[0] << 8) | buf[1];
      if (buf.length < 2 + n) break;
      await answer(buf.slice(2, 2 + n)).catch(() => {});
      buf = buf.subarray(2 + n);
    }
    const { value, done } = await reader.read();
    if (done) break;
    const joined = new Uint8Array(buf.length + value.length);
    joined.set(buf, 0);
    joined.set(value, buf.length);
    buf = joined;
  }
  close(ws);
}
