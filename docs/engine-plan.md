# Our own engine: the plan

Private. This is the story behind how Albatross stays unblockable; it never
leaves this repository.

The goal: our traffic does not look like every other app's, our apps and
servers give nothing away when unpacked or watched, and the whole thing is
as strong and private as the best in the world — while the proven, safe
cryptography underneath stays exactly that, proven and safe. We never write
our own cryptography; the strength and the uniqueness come from the layers
around it.

Own servers are cancelled: every server is one the finder collected, so we
do not control the server side. That rules out a transport only our two ends
would speak. It does not rule out looking unique — most of what a censor
recognises is decided on the client, in how we speak the standard protocols.

## Phase 0 — finish what is in flight
- 1.2.2 (the UI tidy-up, the stricter server bar).
- 1.2.3 (the apps obfuscated: garble on Windows, R8 on Android, the domestic
  mirror hidden).

## Phase 1 — our own signature (the private fork)

A censor blocks a protocol by its fingerprint: the exact bytes of the TLS
hello, the sizes and timing of the first packets, the multiplex pattern.
Thousands of apps ship stock sing-box with stock defaults, so they share one
fingerprint, and one block catches them all. We make ours our own.

Two steps, cheap win first.

**1a — tune what the config already controls (no fork needed).**
Most of the fingerprint is set when we build each server's config
(`internal/node/configs.go`) and by the TLS client we present:
- uTLS: choose and rotate our own ClientHello profiles instead of the common
  default, so our hello matches a real browser and not the usual proxy one.
- REALITY, flow, ALPN, SNI strategy: our own defaults, chosen per server.
- Multiplex (smux), padding and timing: tune away from the stock pattern
  where sing-box exposes the knob.
This alone moves us off the shared fingerprint, and it ships as an ordinary
app update.

**1b — build our own sing-box from a private fork.**
For what the config cannot reach, we keep a private fork of sing-box, pinned
to a version we track, and build the core ourselves in CI instead of
downloading the public release (`build/package.sh`, `build/android/build.sh`
download it today). In the fork we:
- change internal constants and patterns that are not config options;
- strip the protocols and features we never use, so the binary is smaller
  and shows less;
- keep our changes in a thin patch set, so each new upstream version is
  easy to carry forward.
The cryptography is left untouched.

## Phase 2 — privacy, hardened to the bone
- Keep nothing that identifies a user: no addresses, no traffic, no logs that
  could, anywhere in the app or the backend. Audit every write.
- The account already asks only for a username and a password — no email, no
  phone. Keep it that way, and make sure the counts we do keep cannot single
  anyone out.
- Safe defaults on by default: unencrypted traffic refused, DNS private, the
  kill switch within reach.

## Cleanup this cancels
Own servers are gone, so the own-servers code can be retired when convenient
(not urgently, and carefully, so nothing breaks): `OWN_KEY`, `own.bin`, the
"our private servers first" plan, `deploy/server`. Left for its own change.

## What this is not
We do not rebuild the protocols or the cryptography from scratch: that would
take years and come out weaker and slower to adapt than sing-box, which a
large community keeps current against new censorship. Our edge is the fork's
own signature, the strict quality of the servers we hand out, how widely the
finder reaches, and giving nothing away — not reinventing the plumbing.
