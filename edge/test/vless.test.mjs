import assert from "node:assert/strict";
import test from "node:test";
import { decodeEarly, forbidden, parseHeader, relayTarget, relays } from "../src/vless.js";

const uuid = "0f1e2d3c4b5a49788a9b0c1d2e3f4a5b";
const idBytes = uuid.match(/../g).map((h) => parseInt(h, 16));

function header({ id = idBytes, cmd = 1, port = 443, atyp = 2, addr = "example.org", rest = [1, 2, 3] } = {}) {
  const a = atyp === 2 ? [addr.length, ...new TextEncoder().encode(addr)] : addr;
  return new Uint8Array([0, ...id, 0, cmd, port >> 8, port & 255, atyp, ...a, ...rest]);
}

test("a TCP request to a name is read, and the payload after the header is kept", () => {
  const h = parseHeader(header(), uuid);
  assert.equal(h.err, undefined);
  assert.equal(h.version, 0);
  assert.equal(h.cmd, 1);
  assert.equal(h.port, 443);
  assert.equal(h.host, "example.org");
  assert.deepEqual(Array.from(h.rest), [1, 2, 3]);
});

test("IPv4 and IPv6 addresses are read", () => {
  assert.equal(parseHeader(header({ atyp: 1, addr: [1, 1, 1, 1], port: 53, cmd: 2 }), uuid).host, "1.1.1.1");
  const v6 = [0x26, 0x06, 0x47, 0x00, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0x11, 0x11];
  assert.equal(parseHeader(header({ atyp: 3, addr: v6 }), uuid).host, "2606:4700:0:0:0:0:0:1111");
});

test("another user, a short header, a bad address type and an odd command are refused", () => {
  assert.equal(parseHeader(header({ id: idBytes.map((b) => b ^ 1) }), uuid).err, "unknown user");
  assert.equal(parseHeader(new Uint8Array(10), uuid).err, "short header");
  assert.equal(parseHeader(header({ atyp: 4, addr: [] }), uuid).err, "bad address type");
  assert.equal(parseHeader(header({ cmd: 3 }), uuid).err, "unsupported command");
  assert.equal(parseHeader(header({ atyp: 2, addr: "" , rest: [] }).subarray(0, 23), uuid).err, "short header");
});

test("private and local destinations are never opened", () => {
  for (const h of ["localhost", "127.0.0.1", "10.1.2.3", "192.168.1.1", "172.16.0.1", "169.254.169.254", "100.64.0.1", "::1", "fd00::1", "fe80::1", "0.0.0.0", "224.0.0.1"]) {
    assert.equal(forbidden(h), true, h);
  }
  for (const h of ["example.org", "1.1.1.1", "8.8.8.8", "172.32.0.1", "2606:4700::1111", "100.128.0.1"]) {
    assert.equal(forbidden(h), false, h);
  }
});

test("early data is base64url in the protocol header", () => {
  assert.deepEqual(Array.from(decodeEarly("AAEC_w")), [0, 1, 2, 255]);
  assert.equal(decodeEarly(""), null);
  assert.equal(decodeEarly("%%%"), null);
});

test("relays come from PROXY_IP, comma separated", () => {
  assert.deepEqual(relays({ PROXY_IP: " a.example:8443, b.example ,, " }), ["a.example:8443", "b.example"]);
  assert.deepEqual(relays({}), []);
  assert.deepEqual(relayTarget("b.example", 80), { hostname: "b.example", port: 80 });
  assert.deepEqual(relayTarget("a.example:10443", 443), { hostname: "a.example", port: 10443 });
  assert.equal(relayTarget("a.example:10443", 80), null, "a TLS relay takes TLS destinations only");
  assert.deepEqual(relayTarget("[2001:db8::1]:10443", 443), { hostname: "2001:db8::1", port: 10443 });
});
