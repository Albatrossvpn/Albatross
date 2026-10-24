# Albatross

A VPN client for Windows and Android. Pick a location, press the button, and it connects to the lowest-latency server there. It keeps a group of ready backups in the same location, so if the server in use drops, traffic moves to a backup within a second and the location does not change.

## Features

- **Locations:** the fastest location overall, or any location with servers available, each shown with its server count and best ping.
- **Instant failover:** a connection holds a group of servers in one location behind a selector. The server in use is watched continuously, backups are checked in the background, and a drop switches to the best backup without restarting anything.
- **Protocols:** automatic, or a fixed choice of VLESS, VMess, Trojan, Shadowsocks, Hysteria 2, TUIC, AnyTLS, WireGuard or OpenVPN.
- **Bypass local sites:** open the sites of one region (China or Iran) directly instead of through the VPN. Besides domain and address lists, names the VPN carries are looked up and added to a list of the region's sites as they are found.
- **Secure by default:** plain HTTP, FTP, Telnet and unprotected mail ports are refused, because whoever carries them can read them (a setting turns this off). On Android a shortcut opens the system's *Always-on VPN* and *Block connections without VPN* switches.
- **Accounts:** users sign in with a username and password; each account can have a data allowance, a time allowance, a renewal period and an end date, managed from a website (see [backend/README.md](backend/README.md)).
- **Ad and tracker blocker, auto-connect, start with the system** (Windows), and whole-device protection on Windows.
- **Light on the machine:** lists are parsed as a stream, scanning slows down once enough servers are ready, and the window only asks the engine for state while it is visible.

The app shows only what a user needs: how many servers are available, where, and how fast. There is no server list.

## Privacy and security

What the app guarantees, and what it cannot:

- Traffic is encrypted from the device to the server, and sites that use HTTPS stay encrypted to the site, so a server cannot read their content.
- Unencrypted traffic is refused (after the region bypass, which is not carried by a server), so nothing the app carries can be read in plain text by whoever carries it.
- Name lookups for carried sites go through the connection.
- The account service receives a username, a password at sign-in, and counts of bytes and seconds. It never receives the sites visited.
- **Not** guaranteed: a server operator can still see which sites a user connects to and how much data they move. The app says so in its Privacy sheet and does not claim anonymity or "no logs".
- Account limits are enforced by the app (the servers belong to others), so they stop honest overuse, not someone who rebuilds the client.

## How it works

| Part | Package |
|---|---|
| Share-link and Clash parsing, sing-box/Xray outbound generation | `internal/node` |
| Server lists, GitHub CDN mirrors, VPN Gate, ETag cache | `internal/sources` |
| Disk-backed candidate pool (hundreds of thousands of servers in tens of MB) | `internal/pool` |
| Runs sing-box with Xray and warp-plus sidecars; drops configs a core rejects | `internal/core` |
| TCP probe plus a real HTTPS request through each server (delay, exit IP, exit country) | `internal/tester` |
| DNS unpoisoning over DoH, edge-address scanner, IP pinning | `internal/antiblock` |
| Scheduler, connect, same-location failover, speed checks, local-site learning, update check | `internal/engine` |
| Local API and the embedded page; it only receives the public view of the state | `internal/ui` |
| Account session, usage reporting, limits | `internal/account`, `internal/engine/account.go` |
| System proxy, elevation, autostart, single instance | `internal/platform` |
| Account service and website (Cloudflare Worker + D1) | `backend/` |
| Android bridge (gomobile): VpnService hooks and tun2socks into the local proxy | `mobile`, `android/` |

Every server runs through sing-box. Servers only Xray supports are served by an Xray sidecar on a local SOCKS port that sing-box connects to, and warp-plus methods work the same way.

## Build

```sh
go test ./...
VERSION=0.1.0 bash build/package.sh      # dist/Albatross + portable zip (needs curl, unzip, zip)
iscc /DAppVersion=0.1.0 build/windows/installer.iss   # on Windows, needs Inno Setup 6
```

Android (needs Go, Java 17 and an Android SDK with the NDK):

```sh
ANDROID_HOME=/path/to/sdk VERSION=0.1.0 bash build/android/build.sh   # dist/Albatross-0.1.0-android.apk
```

On Android the engine runs in the app process (gomobile) and the cores run as executables shipped as native libraries. Where Windows sets the system proxy, Android starts a `VpnService`; tun2socks carries the tunnel's packets to the local proxy. Set the `ANDROID_KEYSTORE_BASE64`, `ANDROID_KEYSTORE_PASSWORD`, `ANDROID_KEY_ALIAS` and `ANDROID_KEY_PASSWORD` repository secrets so CI signs APKs with one key (otherwise each build is signed with a throwaway debug key and cannot update the previous install).

Builds ask users to sign in only when the account service address is set: `ACCOUNT_URL=https://vpn.example.com bash build/package.sh` (CI reads the `ACCOUNT_URL` repository variable). Without it the app connects without an account.

### Forcing an update

Every copy reads `update.json` from the `main` branch (directly, through GitHub's CDN mirrors, or through its own VPN) at start and every 6 hours, and keeps the last copy so it also holds offline. Raise `min_version` to stop older versions from connecting: they show an "update required" screen with `download_url`. A `latest_version` newer than the running one only shows an "update available" banner. Mirrors cache the file, so a change can take a few hours to reach everyone.

CI (`.github/workflows/build.yml`) runs the tests including end-to-end runs against real local servers, builds the app, bundles the cores, smoke-tests it on Windows, builds and installs the installer, and publishes a release for `v*` tags or for a manual run with a `release` version.

Opt-in validation against real data:

```sh
ALBATROSS_LINKS=links.txt SING_BOX=/path/sing-box XRAY=/path/xray go test -run RealData ./internal/node
```

## Licenses

Bundled third-party components keep their own licenses: sing-box (GPL-3.0-or-later), Xray-core (MPL-2.0), warp-plus (MIT), tun2socks (GPL-3.0), Twemoji flags (CC-BY 4.0), and the site rule sets from SagerNet (sing-geoip, sing-geosite; GPL-3.0-or-later) and Chocolate4U.
