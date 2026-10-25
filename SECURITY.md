# Security Policy

## Supported versions

Only the latest release is supported. Installed copies read `update.json`
from the `main` branch; when a release fixes a security problem it is
published as a forced update, so older versions stop connecting and tell
their users to update. Please check that a problem is still present in the
[latest release](https://github.com/Albatrossvpn/Albatross/releases/latest)
before reporting it.

## Reporting a vulnerability

Please report security problems privately, not in a public issue, so that a
fix can reach users before the problem is known:

- Open a private report on GitHub: **Security → Report a vulnerability**
  ([direct link](https://github.com/Albatrossvpn/Albatross/security/advisories/new)).
- Say which version and platform (Windows or Android) you used, how to
  reproduce the problem, and what an attacker could do with it.
- Reports about the account service and website (`backend/`) are welcome
  through the same form.

You can expect an acknowledgement within 7 days. A confirmed problem is
fixed in a new release; when users are at risk, the release is forced so
vulnerable copies stop connecting. The report is made public, with credit
if you want it, once the fix has reached users.

## What is in scope

- The Windows and Android apps and the Go engine (`cmd/`, `internal/`,
  `mobile/`, `android/`): anything that lets traffic leave the device
  unencrypted or outside the VPN, reveals a user's sites, addresses or
  account to the server or to a third party, lets a server operator run
  code on the device, or lets a user bypass account limits.
- The account service and website (`backend/`): account takeover, access
  to other users' data, or control of the admin panel.
- The build and release pipeline (`build/`, `.github/`): anything that
  could put code in a release that is not in this repository.

Out of scope: what a server operator can see by design (see "Privacy and
security" in the [README](README.md)), the third-party cores (report those
to sing-box, Xray-core, warp-plus or tun2socks directly), and denial of
service against the public server lists.
