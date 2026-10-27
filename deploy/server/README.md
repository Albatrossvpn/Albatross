# Albatross's own servers

Public free servers cover VLESS, VMess, Trojan, Shadowsocks, Hysteria, TUIC,
AnyTLS and WireGuard. A server of the project's own carries every protocol
the app runs at once, on its own ports, so that one being blocked leaves the
rest:

| Protocol | Port |
|---|---|
| VLESS + Reality + Vision | 443/tcp |
| VLESS + XHTTP + Reality (Xray) | 8443/tcp |
| Trojan + TLS | 2098/tcp |
| VLESS + WebSocket + TLS (also through Cloudflare with `CDN_HOST`) | 2083/tcp |
| VLESS + gRPC + TLS (also through Cloudflare with `CDN_HOST`) | 2087/tcp |
| VMess + HTTPUpgrade + TLS (also through Cloudflare with `CDN_HOST`) | 2096/tcp |
| AnyTLS | 8880/tcp |
| Shadowsocks 2022 | 8388/tcp+udp |
| Shadowsocks 2022 behind ShadowTLS v3 | 8445/tcp |
| Hysteria 2 + Salamander, port hopping 20000–40000 and 53 | 443/udp |
| TUIC v5 (BBR) | 8444/udp |
| VMess (AEAD) + HTTPUpgrade, no TLS | 80/tcp |
| VMess (AEAD) + HTTPUpgrade, no TLS | 53/tcp |
| VMess (AEAD) + mKCP inside ICMP echo (Xray xicmp) | ping |
| VMess (AEAD) + mKCP inside DNS queries (Xray xdns), with `DNS_TUNNEL` | 53/udp |
| OpenVPN over TCP (own CA, client certificate, tls-crypt) | 2082/tcp |
| SSH port forwarding (a tunnel-only user, host key in the link) | 22/tcp |
| Trojan + WebSocket + TLS (Trojan-Go; also through Cloudflare with `CDN_HOST`) | 2053/tcp |
| NaiveProxy (Caddy with the forward proxy; HTTP/2 CONNECT) | 2052/tcp |
| Juicity (QUIC, BBR) | 8446/udp |
| Mieru (TCP) | 2086/tcp |
| Brook over WebSocket + TLS | 2095/tcp |
| WStunnel (SOCKS5 inside WebSocket + TLS) | 2097/tcp |
| AmneziaWG (WireGuard with junk packets and masked headers) | 8447/udp |
| OpenConnect / AnyConnect (ocserv; official clients only, see `links-external.txt`) | 2099/tcp+udp |

Every TLS protocol uses a real Let's Encrypt certificate (for the machine's
`<ip>.sslip.io` name unless `DOMAIN` is set), so the app verifies the server
like any HTTPS site; no link is "insecure". The port 80 inbound has no TLS
on purpose: plain HTTP on port 80 outlasted every other disguise in Iran's
protocol allowlist, and VMess's AEAD encryption still protects everything
inside it. UDP 53 also reaches Hysteria 2, since it was the only UDP port
left open in the June 2025 shutdown. The ICMP carrier is for the days when
neither TCP nor UDP to foreign addresses gets out but ping still does: the
session travels inside echo packets, slowly, but it connects.

One address can be blocked at any time, so the server also offers its
WebSocket, gRPC and HTTPUpgrade protocols through Cloudflare's edge when
`CDN_HOST` names a proxied record pointing at it: those connections go to
Cloudflare's addresses (the app picks the edge addresses that answer from
inside the country), the server's own address never appears, and blocking
it leaves them working. A machine with a public IPv6 address gets links
at that address too (`…-v6`): a second address, which the filtering lists
separately and which the mobile networks there reach. A probe of the
Trojan port, or a browser on NaiveProxy's port, gets a plain web page
instead of a closed connection.

## Setting one up

1. A Linux machine with a public IPv4 address, Ubuntu 22.04 or 24.04,
   x86-64 or ARM. Oracle Cloud's *Always Free* tier gives up to four ARM
   cores and 24 GB in total at no cost; creating the account needs the
   owner's identity and a card for verification.
2. In the cloud's firewall (Oracle: *Networking → Virtual cloud networks →
   the subnet's security list*), allow inbound TCP 22, 80, 443, 2052, 2053, 2082, 2083,
   2086, 2087, 2095, 2096, 2097, 2098, 2099, 8388, 8443, 8445, 8880, TCP 53, UDP 53, 443, 2099, 8388, 8444, 8446, 8447, 20000–40000, and ICMP
   echo requests (type 8) from everywhere, which carry the ICMP protocol.
3. On the machine, as root:
   ```sh
   # Copy deploy/server/install.sh to the machine (the repository may be
   # private), then:
   sudo bash install.sh
   ```
   To add the DNS tunnel, which gets out through the network's own resolver
   when every foreign address is cut, you need a domain: add an `A` record
   `ns1.<domain>` pointing at the machine's IP (in Cloudflare: DNS only, grey
   cloud) and an `NS` record `t.<domain>` pointing at `ns1.<domain>`, then
   run the script with `DNS_TUNNEL=t.<domain>` (it is remembered). UDP 53
   then serves the tunnel instead of Hysteria 2. Several names go in one
   comma-separated list: a name under `.ir` next to the foreign one keeps
   the tunnel reachable when the domestic resolvers only answer for
   domestic delegations; the script prints one link per name.
   To offer the server through Cloudflare's edge as well, add a proxied
   `A` record (orange cloud) such as `in.<domain>` pointing at the machine,
   with the zone's SSL/TLS mode *Full* or *Full (strict)*, and run the
   script with `CDN_HOST=in.<domain>` (remembered); the certificate then
   covers that name too. The workflow *Cloudflare DNS for the own server*
   (`.github/workflows/cloudflare-dns.yml`) makes both kinds of record
   from the repository's Cloudflare token.
   It installs sing-box and Xray, makes the keys (kept in
   `/etc/albatross/secrets.env`, so running it again keeps the links),
   gets the certificate, opens the machine's own firewall, turns on BBR and
   starts both cores as services. The links end up in
   `/root/albatross-links.txt`.
4. Add the links to the app: run [`publish-links.sh`](publish-links.sh)
   on the server through `server.yml` (below), which stores them in the
   project's private storage (`private/own-servers.txt`, never served),
   or paste them into the repository secret `OWN_SERVERS`, one per line.
   Each collection reads both and publishes the links for the apps,
   encrypted, so public collectors do not pick them up and overload them.
   A reinstall stores its links again the same way.

## A relay inside the country

When only domestic addresses can be reached (the national-network state,
where foreign sites are cut and only local services work), a server abroad
is out of reach however well it is disguised. What still works is a
machine *inside* the country that has its own way out: a server at a
domestic hosting company, whose international link stays up for business
customers. The app connects to that machine; the machine forwards every
Albatross port (TCP, UDP and ICMP) on to the server abroad.

On an Ubuntu machine inside the country, as root:

```sh
# install.sh copied from deploy/server in the repository
RELAY_TO=<server's address> LINKS=/root/albatross-links.txt bash install.sh
```

`LINKS` is the server's links file (copy it over, or give an https
address). The relay installs no core, only forwarding rules that come back
at every boot, and prints the server's links rewritten to its own address
(the TLS names stay the server's). Add those links to the app's own list
next to the server's: the app tests both and uses whichever answers, so
the relay is only used when the server itself cannot be reached. The
relay sees only encrypted traffic to the server.

The relay's links go into the repository secret `OWN_RELAYS` (one per
line): the collector publishes them unsharded in `own-0.bin`, which every
install reads and the build bundles, so a phone installed during a
shutdown already knows the relays. `RELAY_TO2=<second server>` adds a
watchdog: every minute it checks that the current server answers on port
443 and, after three misses, moves the forwarding to the other one (and
back to the first when it answers again). Running the script again keeps
the settled choice.

## A mirror inside the country

The lists, the update manifest and the installers normally come from the
account service and GitHub's CDN mirrors, all foreign. A mirror on a
domestic machine (the relay's, or another) keeps the apps supplied during
a shutdown:

```sh
MIRROR=1 MIRROR_SOURCE=https://<account service> bash install.sh
```

It installs nginx on port 8081 serving the copies under a secret path,
refreshed every ten minutes while the machine's own way out lasts (a file
that cannot be fetched stays as it was), and prints the mirror's address
(`http://<ip>:8081/<token>`, also in `/etc/albatross/mirror.txt`). Put
that address in the repository variable `DOMESTIC_MIRROR` (several,
comma-separated) and build: the apps then try it first whenever foreign
addresses are cut, and pass it on in share codes. Plain http is enough:
the lists are sealed and the installers are checked against the signed
manifest's SHA-256. `MIRROR=1` combines with `RELAY_TO` on one machine;
8081 is not among the forwarded ports.

## Looking after the server from the repository

A self-hosted GitHub Actions runner on the server (labelled `relay`,
installed in `/opt/runner` as the service
`actions.runner.Albatrossvpn-Albatross.relay-in`) runs the shell script
given to `.github/workflows/server.yml` when it is run by hand, as root;
its output is the job's log, which anyone who can read the repository's
Actions can read, so a script never carries a secret. The Cloudflare
credentials are in its environment (masked in the log) for
`publish-links.sh`; for example, to store the server's links:

    curl -fsSL https://raw.githubusercontent.com/Albatrossvpn/Albatross/main/deploy/server/publish-links.sh | bash

A runner attached to a public repository takes the jobs of any workflow
whose `runs-on` matches it, a pull request's from a fork included. It is
locked to the maintenance workflow by a job started hook
(`/usr/local/sbin/albatross-runner-hook.sh`, written by
[`runner-guard.sh`](runner-guard.sh)): every job but `server.yml` run by
hand from `main` fails before any of its steps runs. To put the hook on a
new runner, run `runner-guard.sh` as the script of `server.yml`, or on the
server with `RUNNER_ROOT` set to the runner's directory; it checks that
the job running it would pass before it turns the hook on, and restarts
the runner once the job is over.
