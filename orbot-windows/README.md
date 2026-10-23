# Orbot for Windows (x64)

A Windows desktop counterpart of [Orbot](https://github.com/guardianproject/orbot):
one click routes Windows traffic through Tor, with bridges (Snowflake, obfs4,
meek, webtunnel) for censored networks such as Iran. The interface is in
Persian and English.

Upstream Orbot is Android/iOS only, so this is a separate app. It reuses
Orbot's built-in bridge list and runs the official **Tor Expert Bundle**
(`tor.exe` and `lyrebird.exe`, which also provides Snowflake) under the hood.
Transport lines and fresh built-in bridges are read from the bundle's
`pt_config.json`, falling back to Orbot's list.

## Download

Every push builds a ready-to-run package in GitHub Actions
(**Actions → Orbot for Windows x64 → Artifacts**). Pushing a tag such as `orbot-v0.1.0`
publishes a release with:

- `Orbot-Windows-x64-<ver>-setup.exe`: installer (Start menu and desktop shortcuts)
- `Orbot-Windows-x64-<ver>.zip`: portable folder, run `Orbot.exe`

The Tor Expert Bundle is downloaded from dist.torproject.org and its GPG
signature is checked; `wintun.dll` is checked against its SHA-256.

## Modes

| Mode | What it does | Admin |
| --- | --- | --- |
| System proxy | Sets the Windows (WinINet) proxy to a local HTTP proxy that forwards to Tor. Edge, Chrome and most apps follow it. The previous proxy settings are restored on disconnect, and after a crash on next start. | No |
| Full VPN | Creates an `OrbotTun` wintun adapter and routes all IPv4/IPv6 traffic into a userspace TCP/IP stack (tun2socks/gVisor) that sends TCP to Tor's SOCKS port and DNS to Tor's DNSPort. `.onion` names resolve too. Traffic from the bundled tor and pluggable-transport processes is detected by process and sent straight out of the physical adapter, so it does not loop. Other UDP is dropped because Tor carries TCP only. | Yes (the app offers to restart elevated) |
| Local ports only | Opens SOCKS5 `127.0.0.1:9050` and HTTP `127.0.0.1:8118` for apps you configure yourself. | No |

The VPN is brought up only after Tor reaches 100% bootstrap, so pluggable
transports can reach their servers first.

Other features: exit-country selection, "new identity" (NEWNYM), custom bridge
lines, live Tor log, configurable ports.

## Build from source

Requires Go (see `go.mod`). From this folder, on Linux, macOS or Windows:

```sh
go run github.com/tc-hib/go-winres@v0.3.3 make --in winres/winres.json --arch amd64
GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build -trimpath -ldflags "-H windowsgui -s -w" -o Orbot.exe .
```

Then place next to `Orbot.exe`:

```
Orbot.exe
wintun.dll                 (from https://www.wintun.net, bin/amd64)
tor/tor.exe                (from tor-expert-bundle-windows-x86_64-*.tar.gz)
tor/pluggable_transports/…
data/geoip, data/geoip6
```

The UI is a local web page (`ui/index.html`) shown in a WebView2 window
(built into Windows 10/11). The API listens on a random `127.0.0.1` port and
requires a per-launch token.

Settings and Tor's data directory live in `%APPDATA%\OrbotWindows`.

## Layout

| File | Purpose |
| --- | --- |
| `main.go` | Startup, local API, WebView2 window |
| `controller.go` | Connect/disconnect, applies the chosen mode when Tor is ready |
| `tor.go` | Finds the bundle, writes `torrc`, runs tor, parses bootstrap, control port |
| `bridges.go`, `assets/builtin-bridges.json` | Built-in bridges (from Orbot) and custom bridge parsing |
| `httpproxy.go` | HTTP/CONNECT proxy to Tor's SOCKS port |
| `sysproxy_windows.go` | Windows system proxy set/restore |
| `vpn_windows.go`, `procinfo_windows.go` | wintun VPN, routing, DNS, Tor-process bypass |
| `platform_*.go` | Windows specifics (elevation, single instance, WebView2) and stubs elsewhere |
| `packaging/` | Installer script and bundled README |

## Limitations

- Tor does not carry UDP, so games, VoIP and QUIC do not work through it (browsers fall back to TCP).
- In VPN mode Windows may still send some DNS queries to other adapters' DNS servers; the
  tunnel gets the lowest interface metric to minimise this.
- A normal browser behind Orbot hides your IP but not your browser fingerprint. Use Tor Browser for anonymous browsing.

## License

BSD-3-Clause. Bundled third-party components keep their own licenses (see `LICENSE`).
