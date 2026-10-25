# Albatross's own servers

Public free servers cover VLESS, VMess, Trojan, Shadowsocks, Hysteria, TUIC,
AnyTLS and WireGuard. A server of the project's own carries every protocol
the app runs at once, on its own ports, so that one being blocked leaves the
rest:

| Protocol | Port |
|---|---|
| VLESS + Reality + Vision | 443/tcp |
| VLESS + XHTTP + Reality (Xray) | 8443/tcp |
| Trojan + TLS | 2053/tcp |
| VLESS + WebSocket + TLS | 2083/tcp |
| VLESS + gRPC + TLS | 2087/tcp |
| VMess + HTTPUpgrade + TLS | 2096/tcp |
| AnyTLS | 8880/tcp |
| Shadowsocks 2022 | 8388/tcp+udp |
| Shadowsocks 2022 behind ShadowTLS v3 | 8445/tcp |
| Hysteria 2 + Salamander, port hopping 20000–40000 and 53 | 443/udp |
| TUIC v5 (BBR) | 8444/udp |
| VMess (AEAD) + HTTPUpgrade, no TLS | 80/tcp |
| VMess (AEAD) + HTTPUpgrade, no TLS | 53/tcp |
| VMess (AEAD) + mKCP inside ICMP echo (Xray xicmp) | ping |
| VMess (AEAD) + mKCP inside DNS queries (Xray xdns), with `DNS_TUNNEL` | 53/udp |

Every TLS protocol uses a real Let's Encrypt certificate (for the machine's
`<ip>.sslip.io` name unless `DOMAIN` is set), so the app verifies the server
like any HTTPS site; no link is "insecure". The port 80 inbound has no TLS
on purpose: plain HTTP on port 80 outlasted every other disguise in Iran's
protocol allowlist, and VMess's AEAD encryption still protects everything
inside it. UDP 53 also reaches Hysteria 2, since it was the only UDP port
left open in the June 2025 shutdown. The ICMP carrier is for the days when
neither TCP nor UDP to foreign addresses gets out but ping still does: the
session travels inside echo packets, slowly, but it connects.

## Setting one up

1. A Linux machine with a public IPv4 address, Ubuntu 22.04 or 24.04,
   x86-64 or ARM. Oracle Cloud's *Always Free* tier gives up to four ARM
   cores and 24 GB in total at no cost; creating the account needs the
   owner's identity and a card for verification.
2. In the cloud's firewall (Oracle: *Networking → Virtual cloud networks →
   the subnet's security list*), allow inbound TCP 80, 443, 2053, 2083, 2087,
   2096, 8388, 8443, 8445, 8880, TCP 53, UDP 53, 443, 8388, 8444, 20000–40000, and ICMP
   echo requests (type 8) from everywhere, which carry the ICMP protocol.
3. On the machine, as root:
   ```sh
   curl -fsSL https://raw.githubusercontent.com/cezomagoanlace29861-jpg/Albatross/main/deploy/server/install.sh | bash
   ```
   To add the DNS tunnel, which gets out through the network's own resolver
   when every foreign address is cut, you need a domain: add an `A` record
   `ns1.<domain>` pointing at the machine's IP (in Cloudflare: DNS only, grey
   cloud) and an `NS` record `t.<domain>` pointing at `ns1.<domain>`, then
   run the script with `DNS_TUNNEL=t.<domain>` (it is remembered). UDP 53
   then serves the tunnel instead of Hysteria 2.
   It installs sing-box and Xray, makes the keys (kept in
   `/etc/albatross/secrets.env`, so running it again keeps the links),
   gets the certificate, opens the machine's own firewall, turns on BBR and
   starts both cores as services. The links end up in
   `/root/albatross-links.txt`.
4. Add the links to the app: paste them into the repository secret
   `OWN_SERVERS`, one per line. The collector then publishes them for the
   apps, encrypted, so public collectors do not pick them up and overload
   them.
