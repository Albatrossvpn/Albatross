# Accounts

A Cloudflare Worker with a D1 database. It serves the account pages (user account page, administration panel, privacy and terms pages) and the API the apps use. The public website is built elsewhere (GoDaddy); this service lives on its own subdomain, `accounts.albatrossvpn.online` (in `wrangler.toml`), and `/` redirects to the account page. Link to it from the website.

| What | Where |
|---|---|
| API | `src/index.js` (`/api/v1/...`), rules and crypto in `src/lib.js` |
| Database | `migrations/0001_init.sql` |
| Pages | `public/` (plain HTML, CSS and JavaScript; no build step; `icon.svg` is the logo) |
| Tests | `npm test` (rules, passwords, tokens), `npm run test:api` (a local Worker with a local database) |

## What it does

- **Users** have a username and a password (stored as a salted PBKDF2-SHA256 hash), a plan, a data allowance, a time allowance, a renewal period and an end date. Zero means no limit.
- **Plans** say how many devices may be connected with one account at the same time: Bronze 1, Silver 2, Gold 4, VIP 8 (`TIERS` in `src/lib.js`). New accounts are Bronze unless the administrator picks another plan or changes the default for sign-ups.
- **The apps** sign in and then report bytes and seconds used every minute. The answer says whether the user may go on; the app disconnects when not. Each copy names its device with a random number kept on the device (`X-Albatross-Device`) and a word for its kind (`X-Albatross-Device-Name`): a report holds one of the account's device slots for five minutes, and a report that finds the other slots taken is answered `devices`, so that copy disconnects while the others stay. Copies from before 1.0.1 send no device number and share one slot. The subscription for Apple devices (`/sub/<token>`) holds a slot for six hours after each fetch, since the service never hears from those devices while they are connected.
- **The administrator** (`/admin`) creates users one by one or in bulk, changes limits, resets usage and passwords, turns accounts on and off, signs users out, searches, and sets the defaults for self-service sign-ups, the download links and whether sign-ups are open (closed by default).
- **Users** (`/account`) see their plan, usage, renewal and end dates, the devices their account has been used on (and can remove one, which frees its slot at once), can change their password and find the download links. Changing the password, or the administrator signing the user out, forgets the devices.

Only usernames and counters are stored. There is no email address. Failed sign-ins are counted per username and per one-way tag of the network address (no addresses), and dropped after a day. Sign-in is limited per address, where an IPv6 /64 counts as one address: 40 failures in 15 minutes, 8 for one username, and only 3 once that username has 8 failures from all addresses. A username with 100 failures from all addresses is closed for 15 minutes. Administrator sign-in is limited to 10 per address.

## Limits are enforced by the app

The service decides; the official app obeys. The servers that carry the traffic are not under this service's control, so a modified client could ignore the answer. Treat limits as accounting and fair use for honest users, not as a security boundary. Each device may keep working for up to 72 hours when the service cannot be reached, then asks for a connection to it.

## Deploying

You need a Cloudflare account and a domain whose DNS is on Cloudflare (a Worker can only answer on a custom domain of a zone Cloudflare serves). With the domain registered at GoDaddy: add it to Cloudflare, copy GoDaddy's DNS records into the Cloudflare zone (the website's `A` and `www` records, and any mail records), then set the domain's nameservers at GoDaddy to the two Cloudflare gives you. Use a subdomain for this service (`accounts.albatrossvpn.online` in `wrangler.toml`); the website keeps the bare domain.

The Worker answers only on its custom domain (`routes` in `wrangler.toml`), never on `*.workers.dev`: that address names the Cloudflare account and links the service to the account's other Workers. Release builds take the domain from `wrangler.toml` (`build/account-url.sh`); `SITE_DOMAIN` can add another, tried after it.

**From GitHub (no tools on your computer).** In the repository's *Settings → Secrets and variables → Actions*:

| Secrets | |
|---|---|
| `CLOUDFLARE_API_TOKEN` | an API token with *Workers Scripts: Edit*, *D1: Edit* and *Zone: Workers Routes: Edit* (the *Edit Cloudflare Workers* template works) |
| `Cloudflare_API_edit` | optional: a token with *Zone: Zone Settings: Edit* and *Zone: Zone: Read* on the domain, which the deploy uses to keep Encrypted Client Hello and HTTP/3 off (browsers inside Iran cannot open a Cloudflare site with either on); without it the deploy tries the token above and warns when it may not |
| `CLOUDFLARE_ACCOUNT_ID` | shown on the Cloudflare dashboard's Workers page |
| `TOKEN_SECRET` | a long random string (32+ characters) that signs sessions; changing it signs everyone out |
| `EDGE_EPOCH` (variable, optional) | a number, default `1`; change it to retire the edge's links handed out so far (edge/README.md) |
| `EDGE_PROXY_IP` (variable, optional) | relays the edge reaches sites behind Cloudflare through, comma separated (edge/wrangler.toml) |
| `ADMIN_USER`, `ADMIN_PASSWORD` | the administrator's login (use a long password) |

| Variables | |
|---|---|
| `SITE_DOMAIN` | the domain to serve, e.g. `vpn.example.com` |
| `SITE_NAME`, `COMPANY_NAME`, `COMPANY_ADDRESS`, `CONTACT_EMAIL` | shown on the site and in its footer; leave out what you do not want shown |
| `ACCOUNT_URL` | the same address with `https://`; builds of the apps use it. Several addresses may be given, comma separated |

Then run *Actions → Deploy accounts and website → Run workflow*. It creates the database on the first run, applies the migrations, deploys, and sets the secrets.

**From a computer.**

```sh
cd backend && npm ci
npx wrangler d1 create albatross        # paste the id into wrangler.toml
npx wrangler d1 migrations apply DB --remote
npx wrangler secret put TOKEN_SECRET  # also ADMIN_USER and ADMIN_PASSWORD
npx wrangler deploy
```

Then open `/admin`, sign in, turn on sign-ups if you want them, set the default limits and the download links.

### Protecting the administration panel further

`/admin` is protected by the login and a rate limit. Put **Cloudflare Access** in front of `/admin*` (Zero Trust → Access → Applications) to require your own identity provider as well, and, where you can, restrict it to your own addresses.

### Optional: Turnstile on sign-up

Create a Turnstile widget in Cloudflare, put its site key in `wrangler.toml` (`TURNSTILE_SITEKEY`) and run `npx wrangler secret put TURNSTILE_SECRET`. The sign-up form then asks for the check.

## Running locally

```sh
cd backend && npm ci
npx wrangler d1 migrations apply DB --local
printf 'TOKEN_SECRET=%s\nADMIN_USER=root\nADMIN_PASSWORD=change-me-please\n' "$(head -c 32 /dev/urandom | base64)" > .dev.vars
npx wrangler dev          # http://localhost:8787
```
