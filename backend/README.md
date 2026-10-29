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
- **Plans** say how many devices may be connected with one account at the same time: Bronze 1, Silver 2, Gold 4, VIP as many as they like (`TIERS` in `src/lib.js`). New accounts are Bronze unless the administrator picks another plan or changes the default for sign-ups.
- **Plans' features** (`FEATURES` in `src/lib.js`): every plan connects to the fastest location with every protocol and the public servers, reaches the relays inside the country in a shutdown, refuses unencrypted traffic, opens local sites directly and shares servers with friends. Silver adds choosing a location, the ad and tracker blocker and local apps outside the VPN; Gold the choice of protocol, the whole-device mode, the GPS override, the edge and the project's own servers (the only ones most protocols have); VIP sharing with other devices. A feature comes with its plan and every plan above it; the administrator moves one to another plan in the settings (`plan_features`). The apps keep what a plan lacks switched off; the service itself keeps the edge's links, the key to the own servers' lists (the collector seals them with a premium key of their own) and a country's subscription for Apple devices from the plans without them.
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
| `SITE_NAME`, `COMPANY_NAME`, `COMPANY_ADDRESS`, `CONTACT_EMAIL` | shown on the site and in its footer; leave out what you do not want shown (`CONTACT_EMAIL` is support@albatrossvpn.online unless the variable says otherwise) |
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

### The support chat

The websites carry a chat button (`public/support.js`, copied to `site/public/`). An AI assistant answers visitors in English from the pages' own words (`src/support.js`), and when a chat ends a summary of it goes to the owner by email. It needs no account beyond Cloudflare's:

- **Workers AI** (the `AI` binding, `@cf/google/gemma-4-26b-a4b-it`) writes the answers. An answer costs about 30 of the 10,000 free neurons a day, so roughly 300 answers a day are free; past that, the Free plan refuses (visitors are told to try again later and the summary still goes out) and the Paid plan bills about a cent per thirty answers. The service itself stops at 900 answers and 150 summaries a day.
- **Email** goes out through the `EMAIL` binding (`[[send_email]]`), from the contact address, to an address Cloudflare **Email Routing** has verified (Email → Email Routing → Destination addresses). Set that address in the admin panel's Settings ("Mail the support chat's summaries to"), or in `SUPPORT_NOTIFY_TO`. Empty: no mail is sent. The same Settings dialog shows what the last try came to (Sent, or Failed with the mail service's words).
- Nothing of a chat is stored on the service; the browser keeps it for the tab only. The privacy page says so.

### The Telegram support bot

The same assistant, in Telegram (`src/telegram.js`, its words in `src/telegram-content.js`): a menu with guides for a connection that fails, installing, plans, the account and the downloads; the assistant for everything else, with thumbs under each answer; and a team behind it, as tickets. Set it up in three steps:

1. In Telegram, open **@BotFather**, send `/newbot`, pick a name and a username ending in `bot`, and copy the token.
2. In the administration panel, Settings → Telegram support bot, paste the token and Save. The service checks it, sets the webhook (`POST /api/v1/telegram`, with a secret derived from `TOKEN_SECRET`, so nothing else is stored), the menu and the bot's description. The token is kept in the database and never shown again.
3. The panel shows `/pair CODE`. Send it to the bot from the Telegram account (or the group) that should receive tickets. The code works once; "New pairing code" makes another.

A person who taps "Talk to our team" (or sends `/human`) opens a numbered ticket: the owner is mailed the chat, and the paired chat gets a card with the conversation. **Reply to a ticket's message to answer the person**; photos, files and voice notes work in both directions. `/tickets` lists the open ones, `/stats` the day's numbers (with the ratings people give the help after a ticket), `/close 1042` (or `/close` as a reply) closes one, `/block 123456` (or `/block` as a reply) and `/unblock` deal with abuse. Until a chat is paired nobody is promised a ticket: the person is told so and the owner still gets the chat by email. If the team's chat cannot be reached (the bot was removed from it) the owner gets each message by email instead. The account page's "Message support" link and the websites' chat lead to the bot once it is connected.

The panel's **Check the bot** says what Telegram reports about the webhook (waiting updates, the last error) and sets the bot's menu, description and "about" line if they are not what the service wants (a refusal by Telegram is shown, not hidden); the schedule does the same within a minute of a deploy that changes those texts, and an hourly one also sets the webhook again if it was lost. Pasting the token of another bot replaces the first: its webhook is removed and its chats and paired chat are forgotten; a new token of the same bot (made again in BotFather) changes nothing else.

What is kept: a chat's messages until about half an hour after the last one, then the whole chat is mailed to the owner (with a summary the model writes from its start and its end) and deleted; the schedule runs every minute and takes at most three chats a run. A ticket's messages are kept until it is closed (a ticket nobody writes to for three days is closed), and a closed ticket keeps its number and nothing of the person; the ids of the team's messages are kept two weeks, so that an answer to an old card says the ticket is closed. A person who has not written is not kept. A blocked person is kept as an id only. Rate limits remember a keyed hash of a person, never the id: each person may send 30 messages an hour (120 a day), press 120 buttons an hour and open 5 tickets a day. The bot has its own daily budgets - 600 answers, 100 mails, 60 tickets - apart from the website chat's.

**Languages.** A person is asked for a language first (a picker with every language, the one their Telegram speaks at the top; `/language` or the menu's 🌐 button changes it) and everything the bot says is then in it: menus, guides, the store, receipts and refusals, the assistant's answers, even the bot's profile and its menu of commands, which Telegram shows in the person's own language. The choice is kept for a year in `tg_langs` under the keyed hash of their number, apart from their chat. A link into the bot (`t.me/<bot>?start=buy`) is carried through the choice. The team's side stays English; a ticket's card names the person's language, so you can answer in it.

Every language is a pack, `src/i18n/<code>.js`, with the same keys as the English master `src/i18n/en.js` (a value is Telegram HTML with `{placeholders}`, a list is a list of steps). To add a language: copy `en.js`, translate the values (keep the placeholders, the HTML tags, the commands and the names Albatross, Bronze, Silver, Gold, VIP, GB, SHA-256), run `node test/i18n-check.mjs <code>` until it is quiet, add the file to `src/i18n/index.js` (its import and the `PACKS` list, which is the picker's order) and run `npm test`: the tests render every screen of every language and hold it to what Telegram takes (lengths, HTML, buttons). The packs were written by a model and should be read by someone who speaks the language before they are relied on; a string a pack lacks is shown in English.

### Selling data with Telegram Stars

The bot can sell data packages (`src/sales.js`, the store in `src/telegram.js`). Telegram requires **Stars** (currency `XTR`) for digital goods and services, and forbids payment providers for them, so no provider token or key is involved: the invoice is in Stars, Telegram takes the payment, and the service is told twice (a "pre-checkout" question it must answer within ten seconds, then the payment).

- **Prices** come from four settings in the panel (Settings → Selling data with Telegram Stars): the price of 1 GB in toman, the price of a dollar in toman, what 1,000 Stars are worth to you in dollars (Telegram pays out about $13, after 21 days, from 1,000 Stars up, through Fragment in TON), and how many days a package is valid. At 10,000 toman, 270,000 toman and $13 the packages are 5 GB = 15 ⭐, 10 GB = 29 ⭐, 25 GB = 72 ⭐, 50 GB = 143 ⭐, 100 GB = 285 ⭐; change a rate and the Stars follow. The panel shows the packages the saved settings make.
- **The plan comes with the size** (`tierForQuota`): under 10 GB Bronze, 10 GB and more Silver, 100 GB and more Gold; VIP (no data limit) is given by you.
- **A new account**: the person picks a package, agrees to the terms, pays; the account (`tg` and seven digits, a note `Stars P12`) and a random password are sent in the same chat at once, and the account remembers its buyer's Telegram number. **A recharge** asks for the username of an account that has a data limit and does not renew by itself; what is left of its data stays, the days start again, and its plan is never lowered. Nobody can list accounts: the lookups are limited.
- **A new password**: the buyer of a new account sends `/password` (or taps Forgot password) and gets one; only the Telegram account that bought it can, all its devices are signed out, a few times a day. Whoever only recharged an account cannot.
- **When it goes wrong** the Stars go back at once: a delivery that fails is refunded and the buyer and the team are told; a payment matching no order is refunded; one left undelivered for three minutes is refunded by the schedule. `/refund P12` in the team's chat gives any payment back (a new account is switched off, data added is taken off again); `/sales` shows today's numbers and the last payments.
- **Try it first**: selling is **off** until it is turned on. In the team's chat send `/teststars` (an invoice of 1 Star, no account, works while selling is off), pay it, and `/refund` it. Then tick "Sell data in the Telegram bot".
- Telegram requires a bot that sells to answer `/terms` and `/paysupport` and to make the person agree to the terms before paying; the bot does. **Read the terms in `src/telegram-content.js` (`terms`) and make them yours**: they promise a refund when delivery fails and none once data was used.
- Kept: a payment's record (Telegram's charge id, the buyer's number, the package, the account) two years, for refunds and your accounts; an account bought here keeps its buyer's number while the account exists. An order nobody paid is forgotten after a day.

## Running locally

```sh
cd backend && npm ci
npx wrangler d1 migrations apply DB --local
printf 'TOKEN_SECRET=%s\nADMIN_USER=root\nADMIN_PASSWORD=change-me-please\n' "$(head -c 32 /dev/urandom | base64)" > .dev.vars
npx wrangler dev          # http://localhost:8787
```
