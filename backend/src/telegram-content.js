// What the Telegram support bot (telegram.js) says and shows: the main menu,
// the guides for a connection that fails and for installing, the plans, the
// account and the downloads. Each is a screen: a text in Telegram's HTML and
// the buttons under it. The words come from the websites; the assistant
// (support.js) knows the same things, and answers what no screen covers.

export const SITE = "https://albatrossvpn.online";
export const ACCOUNT = "https://accounts.albatrossvpn.online/account";
export const MAIL = "support@albatrossvpn.online";

/** Text made safe for Telegram's HTML. */
export const esc = (s) => String(s ?? "").replace(/&/g, "&amp;").replace(/</g, "&lt;").replace(/>/g, "&gt;");

const btn = (text, data) => ({ text, callback_data: data });
const link = (text, url) => ({ text, url });
const keys = (...rows) => ({ inline_keyboard: rows });

export const DEVICES = {
  win: { button: "🪟 Windows", name: "Windows" },
  and: { button: "🤖 Android", name: "Android" },
  ios: { button: "🍎 iPhone / iPad", name: "an iPhone or iPad" },
  mac: { button: "💻 Mac", name: "a Mac" },
};

// A button on a screen of the menu changes that screen in place ("menu");
// one under an answer or a closing note sends a new message ("mn", "cn"), so
// that what is above it stays.
const MENU_BUTTON = btn("⬅ Menu", "menu");
const MENU_NEW = btn("📋 Menu", "mn");
const TEAM_BUTTON = btn("🧑‍💼 Talk to our team", "h");

/** The main menu. */
export function menu(name = "") {
  const hello = name ? `Hi ${esc(name)}!` : "Hi!";
  return {
    text: `🛡 <b>Albatross Support</b>\n\n${hello} I can get you connected, walk you through installing the app, and answer questions about plans and your account.\n\nPick a topic, or just type your question.`,
    markup: keys(
      [btn("🔌 I can't connect", "c"), btn("📲 Install the app", "i")],
      [btn("💎 Plans & upgrading", "p"), btn("👤 Account & data", "a")],
      [btn("⬇️ Downloads", "d"), TEAM_BUTTON],
    ),
  };
}

const HELP = `<b>How I work</b>

Type your question and I answer right away, or use the menu.

/menu - the main menu
/human - talk to our team (opens a ticket)
/close - close your ticket, or end this chat
/help - this message

I am an AI assistant and can be wrong. Never send a password here. A chat ends with /close or about half an hour after your last message: a short summary goes to our team, and the chat is deleted.`;

export const help = () => ({ text: HELP, markup: keys([MENU_NEW]) });

// ---------- A connection that fails ----------

/** Which device? */
export function connectPicker() {
  return {
    text: "🔌 <b>It won't connect</b>\n\nWhich device are you on?",
    markup: keys([btn(DEVICES.win.button, "c:win"), btn(DEVICES.and.button, "c:and")], [btn(DEVICES.ios.button, "c:ios"), btn(DEVICES.mac.button, "c:mac")], [MENU_BUTTON]),
  };
}

const COMMON = [
  "<b>Update Albatross.</b> Open the app and tap Update, or install the newest version from the download page.",
  "<b>Check the clock.</b> Date and time must be set to automatic.",
  "<b>Leave the protocol on Automatic.</b> The app picks what works on your network.",
  "<b>Pick another location</b>, or choose the fastest one.",
  "<b>Switch network</b> (Wi-Fi and mobile data). On a heavily filtered network it can take up to a minute while the app finds a way out.",
];
const CLOSED = "<b>Close and reopen the app.</b> Do not run another VPN at the same time.";
const ALLOWANCE = "If your account page says the allowance is <b>used up or expired</b>, it needs a recharge from where you got the account.";

const GUIDES = {
  win: { title: "🪟 Windows", steps: [...COMMON, CLOSED, ALLOWANCE] },
  and: { title: "🤖 Android", steps: [...COMMON, "<b>Close and reopen the app.</b> Allow the VPN permission when Android asks, and do not run another VPN at the same time.", ALLOWANCE] },
  ios: {
    title: "🍎 iPhone / iPad",
    intro: "On iPhone and iPad the servers live in a connection app (V2Box, Streisand, Shadowrocket, Hiddify or sing-box).\n\n",
    steps: [
      "Open your <b>account page</b> in Safari and check the allowance is not used up or expired.",
      "<b>Add the servers again</b> with the button on that page (all servers, one country, or the edge).",
      "In the connection app, <b>refresh</b> the Albatross servers.",
      "Check that date and time are set to automatic.",
      "Switch between Wi-Fi and mobile data; on a filtered network it can take up to a minute.",
      "Close and reopen the connection app.",
    ],
  },
  mac: {
    title: "💻 Mac",
    intro: "On a Mac the servers live in a connection app (V2Box, Streisand, Hiddify or sing-box).\n\n",
    steps: [
      "Open your <b>account page</b> in Safari and check the allowance is not used up or expired.",
      "<b>Add the servers again</b> with the button on that page (all servers, one country, or the edge).",
      "In the connection app, <b>refresh</b> the Albatross servers.",
      "Check that date and time are set automatically (System Settings, General, Date &amp; Time).",
      "Switch to another network (another Wi-Fi, or your phone's hotspot); on a filtered network it can take up to a minute.",
      "Quit the connection app and open it again.",
    ],
  },
};

/** The checklist for one device, and whether it helped. */
export function connectGuide(device) {
  const g = Object.hasOwn(GUIDES, device) ? GUIDES[device] : null;
  if (!g) return connectPicker();
  const steps = g.steps.map((s, i) => `${i + 1}. ${s}`).join("\n");
  const account = device === "ios" || device === "mac";
  return {
    text: `${g.title}: <b>it won't connect</b>\n\n${g.intro || "Try these in order:\n\n"}${steps}\n\nDid it work?`,
    markup: keys(
      [btn("✅ It works now", `ok:${device}`), btn("❌ Still not connecting", `no:${device}`)],
      ...(account ? [[link("🔗 Open account page", ACCOUNT)]] : []),
      [btn("⬅ Other device", "c"), MENU_BUTTON],
    ),
  };
}

export const FIXED = { text: "🎉 <b>Great, you are all set!</b>\n\nIf anything else comes up, I am right here.", markup: keys([MENU_NEW]) };

/** After the checklist did not help: the next message is the problem, in words. */
export function stillBroken() {
  return {
    text: "Sorry about that. Let us find out more.\n\n<b>Type the exact message you see</b> (or what happens), and tell me whether you are on Wi-Fi or mobile data and which version of the app you have. I will help from there.\n\nIf you would rather have a person look at it, tap the button below.",
    markup: keys([TEAM_BUTTON], [MENU_BUTTON]),
  };
}

// ---------- Installing ----------

export function installPicker() {
  return {
    text: "📲 <b>Install the app</b>\n\nWhich device?",
    markup: keys([btn(DEVICES.win.button, "i:win"), btn(DEVICES.and.button, "i:and")], [btn(DEVICES.ios.button, "i:ios"), btn(DEVICES.mac.button, "i:mac")], [MENU_BUTTON]),
  };
}

export function installGuide(device) {
  const back = [btn("⬅ Other device", "i"), MENU_BUTTON];
  if (device === "win") {
    return {
      text: "🪟 <b>Install on Windows</b>\n\nWindows 10 or 11, 64-bit.\n\n1. Download the installer.\n2. Run it. If Windows says <i>\"Windows protected your PC\"</i>, tap <b>More info</b>, then <b>Run anyway</b> (the installer is not code-signed yet).\n3. Open Albatross, sign in with your username and password, and tap the big button.\n\nEvery file on the download page has a SHA-256 fingerprint if you want to check it.",
      markup: keys([link("⬇️ Download for Windows", `${SITE}/#download`)], back),
    };
  }
  if (device === "and") {
    return {
      text: "🤖 <b>Install on Android</b>\n\nAndroid 6 or newer, one APK for every phone. It is not on Google Play, so the phone asks once to allow installs from your browser.\n\n1. Download the APK.\n2. Open it and allow the install when Android asks.\n3. Open Albatross, sign in, and tap the big button. Allow the VPN permission when Android asks.\n\nLater updates install over it.",
      markup: keys([link("⬇️ Download the APK", `${SITE}/#download`)], back),
    };
  }
  if (device === "ios") {
    return {
      text: "🍎 <b>Install on iPhone or iPad</b>\n\nThere is no Albatross app in the App Store. Instead:\n\n1. In <b>Safari</b>, open your account page and sign in.\n2. Tap <b>Share, then Add to Home Screen</b>, so it opens like an app.\n3. Install a connection app from the App Store: <b>V2Box</b> or <b>Streisand</b> are free (Shadowrocket, Hiddify and sing-box work too).\n4. On the account page, tap the button that adds the servers: all of them, one country, or the edge. They appear under the Albatross name and refresh by themselves.\n\niOS 15 or newer.",
      markup: keys([link("🔗 Open account page", ACCOUNT)], back),
    };
  }
  if (device === "mac") {
    return {
      text: "💻 <b>Install on Mac</b>\n\nThere is no Albatross app for Mac yet. Instead:\n\n1. In <b>Safari</b>, open your account page and sign in.\n2. Install a connection app from the App Store: <b>V2Box</b> or <b>Streisand</b> are free (Hiddify and sing-box work too).\n3. On the account page, click the button that adds the servers: all of them, one country, or the edge. They appear under the Albatross name and refresh by themselves.\n\nThe connection app's page in the App Store says which macOS it needs.",
      markup: keys([link("🔗 Open account page", ACCOUNT)], back),
    };
  }
  return installPicker();
}

// ---------- Plans, account, downloads ----------

export const PLANS = {
  text: `💎 <b>Plans</b>

Every account connects to the fastest location with every protocol.

🥉 <b>Bronze</b> - 1 device
The fastest location found for you, relays inside the country in a shutdown, unencrypted traffic blocked.

🥈 <b>Silver</b> - 2 devices
Everything in Bronze, plus choosing any location, the ad and tracker blocker, and local apps outside the VPN (Android).

🥇 <b>Gold</b> - 4 devices
Everything in Silver, plus choosing the protocol, Albatross's own servers (the fastest), Albatross Edge through Cloudflare, protecting the whole device (Windows) and GPS that matches the VPN (Android).

👑 <b>VIP</b> - unlimited devices
Everything in Gold, plus our private servers first, direct support from the team, and sharing the connection with every device on your Wi-Fi.

To move up or to recharge, ask where you got your account. The account, its data and its devices carry over.`,
  markup: keys([TEAM_BUTTON], [MENU_BUTTON]),
};

export const ACCOUNT_INFO = {
  text: `👤 <b>Account and data</b>

- You sign in with a username and a password. No email, name or phone number.
- Your data or time allowance and its end date are shown in the app and on your account page.
- One account works on all your devices, up to your plan's limit.
- Passwords cannot be seen or reset in this chat. Never send yours here.
- Allowance used up or expired? It needs a recharge from where you got the account.`,
  markup: keys([link("🔗 Open account page", ACCOUNT)], [TEAM_BUTTON], [MENU_BUTTON]),
};

export const DOWNLOADS = {
  text: `⬇️ <b>Downloads</b>

🪟 Windows 10 and 11, 64-bit
🤖 Android 6 and newer
🍎 iPhone, iPad, Mac: nothing to download, you add it from your account page

Every file on the download page has a SHA-256 fingerprint.`,
  markup: keys([link("⬇️ Open the download page", `${SITE}/#download`)], [btn("🍎 iPhone and Mac guide", "i:ios"), MENU_BUTTON]),
};

// ---------- Answers, tickets ----------

/** What sits under an answer of the assistant: whether it helped, and the way to a person. The last part of the data says whether the answer handed over to the team. */
export function feedback(handoff = false) {
  const h = handoff ? ":1" : "";
  const rate = [btn("👍 Helpful", `fb:u${h}`), btn("👎 Not helpful", `fb:d${h}`)];
  return handoff ? keys([TEAM_BUTTON], rate) : keys(rate);
}

/** After a thumbs up: the way to a person stays where the answer pointed to it. */
export const thanks = (handoff = false) => (handoff ? keys([TEAM_BUTTON], [MENU_NEW]) : keys([MENU_NEW]));
/** After a thumbs down: what else can be done. */
export const SORRY = keys([TEAM_BUTTON], [btn("🔌 Fix a connection", "cn"), MENU_NEW]);
export const THANKS = keys([MENU_NEW]);
export const TRY_AGAIN = keys([TEAM_BUTTON], [MENU_NEW]);

export const CAN_ONLY_READ = {
  text: "I can only read text here. Please describe the problem in words, or open a ticket if you want to send screenshots.",
  markup: keys([TEAM_BUTTON]),
};

/** The button under a ticket's messages carries the ticket's number, so an old one never closes a newer ticket. */
export const ticketKeys = (id) => keys([btn("✅ My problem is solved", `tc:${id}`)]);

export function ticketOpened(id, hadChat) {
  return {
    text: `🎫 <b>Ticket #${id} opened</b>\n\nOur team has been notified${hadChat ? " and has your chat so far" : ""}. ${hadChat ? "Add anything that helps" : "Write your problem here"}: the exact message, your device and network. Screenshots are welcome.\n\nTheir reply will arrive in this chat.`,
    markup: ticketKeys(id),
  };
}

const rating = keys([1, 2, 3, 4, 5].map((n) => btn(`${n}⭐`, `r:${n}`)), [MENU_NEW]);
export const ticketOpen = (id) => ({ text: `You already have <b>ticket #${id}</b> open. Just write here and our team will see it.`, markup: ticketKeys(id) });
export const ticketClosedByUser = (id) => ({ text: `✅ Glad it is sorted! <b>Ticket #${id}</b> is closed.\n\nHow was our help?`, markup: rating });
export const ticketClosedByTeam = (id) => ({ text: `✅ <b>Ticket #${id} is closed.</b> If something else comes up, just write to me or open the menu.\n\nHow was our help?`, markup: rating });
export const ticketClosedIdle = (id) => ({ text: `<b>Ticket #${id}</b> was closed after three quiet days. Write to me any time to start again.`, markup: THANKS });
export const rated = (n) => ({ text: `Thank you! You rated our help ${n} out of 5.`, markup: THANKS });
export const TICKET_GONE = { text: "That ticket is already closed. Write to me any time to start again.", markup: THANKS };
/** The team's words, already made safe for HTML (the bot cuts them to fit first). */
export const teamSays = (html) => `💬 <b>Albatross team</b>\n${html}`;

/** The end of a chat: its summary went to the team only if it did. */
export const ended = (mailed) => ({
  text: mailed ? "Thank you! A short summary of this chat has gone to our team, and the chat is deleted here. Write any time to start again." : "This chat is closed and deleted. Write any time to start again.",
  markup: THANKS,
});
export const NOTHING_TO_END = { text: "There is nothing to close right now. Write your question, or open the menu.", markup: THANKS };

export const SLOW_DOWN = `You are sending messages very fast. Please wait a little, or email ${MAIL}.`;
export const WENT_WRONG = `Something went wrong on our side. Please try again in a moment, or email ${MAIL}.`;
export const TICKET_LIMIT = `You have opened several tickets today, so I cannot open another one right now. Please try again tomorrow, or email ${MAIL}.`;
export const TEAM_BUSY = `Our team has a lot of tickets right now, so I cannot open another one. Please email ${MAIL}, or try again in a while.`;
export const TEAM_UNREACHABLE = `I could not reach our team's chat just now, so they may be slow to see your message. If it is urgent, email ${MAIL}.`;

/** No team chat is paired: there is nobody to answer in Telegram, and the person is told so. */
export const noTeam = (mailed) => ({
  text: `Our team is not connected to this chat yet, so I cannot open a ticket here.${mailed ? "\n\nI sent them this chat by email, but they cannot answer in Telegram." : ""}\n\nFor an answer from a person, email <b>${MAIL}</b> and include your Albatross username.`,
  markup: keys([MENU_NEW]),
});

// ---------- The team's side ----------

export const OWNER_HELP = `<b>Team commands</b>

/tickets - the open tickets
/stats - today's numbers
/close 1042 - close a ticket (or reply /close to one of its messages)
/block 123456 - block a user, and /unblock to let them back

Reply to a ticket's message to answer the person. Photos, files and voice notes work too.`;

export const PAIRED = "✅ <b>Paired.</b> Tickets will arrive in this chat. Reply to a ticket's message to answer the person. /help lists the commands.";
