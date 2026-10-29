// Prints the sing-box config a subscription of every kind of link the Apple
// subscriptions carry makes, for CI to check with sing-box itself: one
// outbound sing-box refuses stops a whole profile in the sing-box apps.
import { edgeLinks } from "../src/lib.js";
import { singBoxConfig } from "../src/singbox.js";

const id = "11111111-2222-4333-8444-555555555555";
const key = (b) => btoa(String.fromCharCode(...new Uint8Array(32).fill(b)));
const pbk = key(1).replace(/=+$/, "").replace(/\+/g, "-").replace(/\//g, "_");
const links = [
  `vless://${id}@1.2.3.4:443?security=reality&pbk=${pbk}&sid=ab12&sni=www.example.com&fp=safari&type=tcp&flow=xtls-rprx-vision`,
  `vless://${id}@a.example.com:443?security=tls&type=ws&host=a.example.com&path=%2Fws%3Fed%3D2048&alpn=h2,http/1.1`,
  `vless://${id}@a.example.com:443?security=tls&type=grpc&serviceName=g&fp=firefox`,
  `vless://${id}@a.example.com:443?security=tls&type=httpupgrade&host=a.example.com&path=%2Fu`,
  `vless://${id}@a.example.com:443?security=tls&type=h2&host=a.example.com&path=%2Fh`,
  `vless://${id}@a.example.com:443?security=tls&type=quic`,
  `vless://${id}@a.example.com:80?type=tcp&headerType=http&host=a.example.com&path=%2F`,
  `vless://${id}@a.example.com:443?security=tls&type=tcp&fragment=1&allowInsecure=1`,
  "vmess://" + btoa(JSON.stringify({ v: "2", add: "v.example.com", port: "443", id, aid: "0", scy: "auto", net: "ws", host: "v.example.com", path: "/v?ed=2048", tls: "tls" })),
  "vmess://" + btoa(JSON.stringify({ v: "2", add: "v.example.com", port: "80", id: "not-a-uuid", aid: "0", net: "tcp", tls: "" })),
  `trojan://pw@t.example.com:443?sni=t.example.com&type=tcp`,
  `trojan://pw@t.example.com:443?type=ws&path=%2Ft&host=t.example.com`,
  "ss://" + btoa("aes-256-gcm:pass") + "@s.example.com:8388",
  "ss://" + btoa("2022-blake3-aes-256-gcm:" + key(2)) + "@s.example.com:8388",
  "ss://" + btoa("chacha20-ietf-poly1305:pass") + "@s.example.com:8388?plugin=" + encodeURIComponent("obfs-local;obfs=http;obfs-host=s.example.com"),
  "ss://" + btoa("aes-128-gcm:pass") + "@s.example.com:8388?plugin=" + encodeURIComponent("v2ray-plugin;tls;host=s.example.com"),
  "hy2://pw@h.example.com:443?obfs=salamander&obfs-password=op&mport=20000-30000&sni=h.example.com",
  "hysteria2://pw@1.2.3.5:443?insecure=1&alpn=h3",
  `tuic://${id}:pw@u.example.com:443?congestion_control=bbr&udp_relay_mode=quic&alpn=h3`,
  `wireguard://${encodeURIComponent(key(3))}@w.example.com:51820?publickey=${encodeURIComponent(key(4))}&address=172.16.0.2,fd01::2&reserved=1,2,3&mtu=1280`,
  ...edgeLinks("edge.example.com", id, ["104.16.1.1"]),
];
const config = await singBoxConfig(links.map((link, i) => ({ link, name: `Albatross ${i + 1}` })));
const count = config.outbounds.length - 3 + (config.endpoints || []).length;
if (count !== links.length) throw new Error(`${links.length - count} of the links made no outbound`);
console.log(JSON.stringify(config, null, 2));
