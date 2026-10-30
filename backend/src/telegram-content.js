// What the Telegram support bot (telegram.js) says and shows: the main menu,
// the guides for a connection that fails and for installing, the plans, the
// account, the downloads and the store. Each is a screen: a text in Telegram's
// HTML and the buttons under it. The words are the bot's languages' own
// (src/i18n, one pack for each); `screens(code)` gives the screens in one
// language, built from the same layout, so a language cannot change what a
// button does. The words come from the websites; the assistant (support.js)
// knows the same things, and answers what no screen covers.
import { DEFAULT, has, pack, picker, translator } from "./i18n/index.js";

export const SITE = "https://albatrossvpn.online";
export const ACCOUNT = "https://accounts.albatrossvpn.online/account";
export const MAIL = "support@albatrossvpn.online";

/** Text made safe for Telegram's HTML. */
export const esc = (s) => String(s ?? "").replace(/&/g, "&amp;").replace(/</g, "&lt;").replace(/>/g, "&gt;");

const btn = (text, data) => ({ text, callback_data: data });
const link = (text, url) => ({ text, url });
// Buying stands out: its button and every one that carries a purchase on to
// its end are in the brand's colour (Telegram's "primary", blue), where the
// rest keep the app's own style.
const BUY_STYLE = "primary";
const buy = (b) => ({ ...b, style: BUY_STYLE });
/**
 * The money a language's buyers think in: a price in Stars is shown in it
 * too, as what those Stars cost a buyer (sales.js buyPer1000). "IRT" is the
 * Iranian toman, at the owner's own rate; the others at the day's rates
 * (fx.js). A language spoken where several are used shows the commonest.
 */
const CURRENCIES = { en: ["USD"], fa: ["IRT", "AFN", "TJS"], zh: ["CNY"], ru: ["RUB"], ar: ["SAR", "EGP", "IQD"], tr: ["TRY"], ur: ["PKR"], vi: ["VND"], id: ["IDR"], es: ["USD", "EUR", "MXN"], fr: ["EUR", "MAD"] };
const MONEY = {
  USD: ["$", ""], EUR: ["€", ""], IRT: ["", " تومان"], AFN: ["", " افغانی"], TJS: ["", " سامانی"], CNY: ["¥", ""], RUB: ["", " ₽"], SAR: ["", " SAR"],
  EGP: ["", " EGP"], IQD: ["", " IQD"], TRY: ["₺", ""], PKR: ["Rs ", ""], VND: ["", " ₫"], IDR: ["Rp ", ""], MXN: ["MX$", ""], MAD: ["", " MAD"],
};
function amountIn(v, code) {
  let digits = v < 10 ? 2 : v < 100 ? 1 : 0;
  if (code === "IRT") {
    v = v >= 10000 ? Math.round(v / 1000) * 1000 : Math.round(v / 100) * 100;
    digits = 0;
  }
  const [pre, post] = MONEY[code];
  // A whole amount drops its zeros: "70", not "70.0"; cents stay "$0.10".
  const whole = Number.isInteger(Math.round(v * 10 ** digits) / 10 ** digits);
  return pre + v.toLocaleString("en-US", { minimumFractionDigits: whole ? 0 : digits, maximumFractionDigits: digits }) + post;
}
/** What `stars` cost a buyer in the money of language `code`, "≈ $0.10"; "" without a rate. `m` is { starUsd, toman, rates }. */
export function moneyIn(code, stars, m) {
  if (!m || !(m.starUsd > 0) || !(stars > 0)) return "";
  const usd = stars * m.starUsd;
  const parts = (CURRENCIES[code] || CURRENCIES.en).map((c) => {
    const rate = c === "USD" ? 1 : c === "IRT" ? m.toman : (m.rates || {})[c];
    return rate > 0 ? amountIn(usd * rate, c) : "";
  }).filter(Boolean);
  return parts.length ? `≈ ${parts.join(" · ")}` : "";
}

/** The medal of a package's plan, on its button: every language reads it. */
const MEDALS = { bronze: "🥉", silver: "🥈", gold: "🥇", vip: "💎" };
const WEBSITE_LINE = `🌐 <a href="${SITE}">albatrossvpn.online</a>`;
const keys = (...rows) => ({ inline_keyboard: rows });

/** The devices of the guides, with the name the assistant is told (always English: it reads that). */
export const DEVICES = {
  win: { name: "Windows" },
  and: { name: "Android" },
  ios: { name: "an iPhone or iPad" },
  mac: { name: "a Mac" },
};

/**
 * A link from the app to recharge its account: "rn" and the username in
 * base32 (lowercase, no padding), since a link's start may hold only
 * letters, digits, _ and -, and Telegram lowers its case on the way.
 */
export const RENEW = /^rn[a-z2-7]{5,52}$/;

/** The username a RENEW link names, or "". */
export function renewTarget(topic) {
  if (!RENEW.test(topic)) return "";
  const abc = "abcdefghijklmnopqrstuvwxyz234567";
  let bits = 0, value = 0, out = "";
  for (const ch of topic.slice(2)) {
    value = (value << 5) | abc.indexOf(ch);
    bits += 5;
    if (bits >= 8) {
      out += String.fromCharCode((value >>> (bits - 8)) & 255);
      bits -= 8;
    }
  }
  return out;
}

/** The topics a link into the bot can open (t.me/<bot>?start=buy), and that a language choice carries on to. */
export const TOPICS = ["connect", "install", "plans", "account", "downloads", "buy", "human"];

const dateOf = (unix) => new Date(unix * 1000).toISOString().slice(0, 10);
const gbOf = (bytes) => `${Math.round((bytes / 1024 ** 3) * 10) / 10} GB`;

/** The language picker. It is read before any language is chosen, so it is in all of them; `topic` is carried through the choice, `hint` is the language of the person's Telegram. */
export function languagePicker(topic = "", hint = "") {
  const { packs, title } = picker(hint);
  const suffix = TOPICS.includes(topic) || RENEW.test(topic) ? `:${topic}` : "";
  const rows = [];
  for (let i = 0; i < packs.length; i += 2) rows.push(packs.slice(i, i + 2).map((p) => btn(`${p.flag} ${p.name}`, `l:${p.code}${suffix}`)));
  return { text: title, markup: keys(...rows) };
}

const cache = new Map();

/** The screens in one language (English for one the bot does not have). */
export function screens(code = DEFAULT) {
  const lang = has(code) ? code : DEFAULT;
  if (!cache.has(lang)) cache.set(lang, build(lang));
  return cache.get(lang);
}

function build(code) {
  const t = translator(code);
  // A button on a screen of the menu changes that screen in place ("menu");
  // one under an answer or a closing note sends a new message ("mn", "cn"), so
  // that what is above it stays.
  const MENU_BUTTON = btn(t("btn_menu_back"), "menu");
  const MENU_NEW = btn(t("btn_menu"), "mn");
  const TEAM_BUTTON = btn(t("btn_team"), "h");
  const BUY_BUTTON = buy(btn(t("btn_buy"), "s"));
  const LANGUAGE_BUTTON = btn(t("btn_language"), "lg");
  const WEBSITE = link("🌐 albatrossvpn.online", SITE);
  const ACCOUNT_BUTTON = link(t("btn_account_page"), ACCOUNT);
  const BACK_TO_PACKAGES = btn(t("btn_packages"), "s");
  const DOWNLOAD_PAGE = `${SITE}/#download`;

  const validText = (days) => (days > 0 ? t("valid_days", { days }) : t("valid_forever"));
  // A price in Stars and, in brackets, what it comes to in this language's money.
  const priced = (stars, money) => {
    const m = moneyIn(code, stars, money);
    return m ? ` (${m})` : "";
  };
  const untilText = (unix) => (unix > 0 ? t("until", { date: dateOf(unix) }) : t("until_forever"));
  const planLine = (p) => `${p.plan}, ${p.devices ? t("devices_n", { n: p.devices }) : t("devices_unlimited")}`;

  const rating = keys([1, 2, 3, 4, 5].map((n) => btn(`${n}⭐`, `r:${n}`)), [MENU_NEW]);
  const THANKS = keys([MENU_NEW]);
  /** The button under a ticket's messages carries the ticket's number, so an old one never closes a newer ticket. */
  const ticketKeys = (id) => keys([btn(t("btn_solved"), `tc:${id}`)]);

  /** The main menu; with the Buy button first, on a row of its own, when data is on sale. */
  function menu(name = "", selling = false) {
    const hello = name ? t("menu_hello", { name: esc(name) }) : t("menu_hello_anon");
    return {
      text: `${t("menu_title")}\n\n${hello} ${t(selling ? "menu_intro_sales" : "menu_intro")}\n\n${t("menu_pick")}\n\n${WEBSITE_LINE}`,
      markup: keys(
        ...(selling ? [[BUY_BUTTON]] : []),
        [btn(t("btn_connect"), "c"), btn(t("btn_install"), "i")],
        [btn(t("btn_plans"), "p"), btn(t("btn_account"), "a")],
        [btn(t("btn_downloads"), "d"), TEAM_BUTTON],
        [LANGUAGE_BUTTON],
      ),
    };
  }

  const help = (selling = false) => ({ text: `${t("help", { buy: selling ? t("help_buy") : "" })}\n\n${WEBSITE_LINE}`, markup: keys([WEBSITE], [MENU_NEW]) });

  // ---------- A connection that fails ----------

  /** Which device? */
  const connectPicker = () => ({
    text: t("connect_picker"),
    markup: keys([btn(t("dev_win"), "c:win"), btn(t("dev_and"), "c:and")], [btn(t("dev_ios"), "c:ios"), btn(t("dev_mac"), "c:mac")], [MENU_BUTTON]),
  });

  const guides = () => ({
    win: { intro: t("guide_try"), steps: [...t("guide_common"), t("guide_win_reopen"), t("guide_allowance")] },
    and: { intro: t("guide_try"), steps: [...t("guide_common"), t("guide_and_reopen"), t("guide_allowance")] },
    ios: { intro: t("guide_ios_intro"), steps: t("guide_ios") },
    mac: { intro: t("guide_mac_intro"), steps: t("guide_mac") },
  });

  /** The checklist for one device, and whether it helped. */
  function connectGuide(device) {
    const g = Object.hasOwn(DEVICES, device) ? guides()[device] : null;
    if (!g) return connectPicker();
    const steps = g.steps.map((s, i) => `${i + 1}. ${s}`).join("\n");
    const account = device === "ios" || device === "mac";
    return {
      text: `${t("guide_title", { device: t(`dev_${device}`) })}\n\n${g.intro}\n\n${steps}\n\n${t("guide_did_it_work")}`,
      markup: keys(
        [btn(t("btn_works"), `ok:${device}`), btn(t("btn_still"), `no:${device}`)],
        ...(account ? [[ACCOUNT_BUTTON]] : []),
        [btn(t("btn_other_device"), "c"), MENU_BUTTON],
      ),
    };
  }

  const FIXED = { text: t("fixed"), markup: keys([MENU_NEW]) };

  /** After the checklist did not help: the next message is the problem, in words. */
  const stillBroken = () => ({ text: t("still_broken"), markup: keys([TEAM_BUTTON], [MENU_BUTTON]) });

  // ---------- Installing ----------

  const installPicker = () => ({
    text: t("install_picker"),
    markup: keys([btn(t("dev_win"), "i:win"), btn(t("dev_and"), "i:and")], [btn(t("dev_ios"), "i:ios"), btn(t("dev_mac"), "i:mac")], [MENU_BUTTON]),
  });

  function installGuide(device) {
    const back = [btn(t("btn_other_device"), "i"), MENU_BUTTON];
    if (device === "win") return { text: t("install_win"), markup: keys([link(t("btn_dl_win"), DOWNLOAD_PAGE)], back) };
    if (device === "and") return { text: t("install_and"), markup: keys([link(t("btn_dl_apk"), DOWNLOAD_PAGE)], back) };
    if (device === "ios") return { text: t("install_ios"), markup: keys([ACCOUNT_BUTTON], back) };
    if (device === "mac") return { text: t("install_mac"), markup: keys([ACCOUNT_BUTTON], back) };
    return installPicker();
  }

  // ---------- Plans, account, downloads ----------

  /**
   * The plans: what each has and how an account gets it. The plan comes with
   * the account's data (10 GB and more Silver, 100 GB and more Gold, no limit
   * VIP: lib.js tierForQuota). `shop` is `{ catalog, days }` while data is on
   * sale, and then the screen lists the packages and offers to buy one.
   */
  function plans(shop = null) {
    const buy = shop
      ? t("plans_buy_sales", { lines: shop.catalog.map((p) => `• ${p.gb} GB - ⭐${p.stars}${priced(p.stars, shop.money)} - ${p.plan}`).join("\n"), valid: validText(shop.days) })
      : t("plans_buy_manual");
    return {
      text: t("plans", { buy }),
      markup: shop ? keys([BUY_BUTTON], [TEAM_BUTTON, WEBSITE], [MENU_BUTTON]) : keys([TEAM_BUTTON, WEBSITE], [MENU_BUTTON]),
    };
  }

  const ACCOUNT_INFO = {
    text: t("account_info"),
    markup: keys([btn(t("btn_forgot_password"), "pw")], [ACCOUNT_BUTTON, WEBSITE], [TEAM_BUTTON], [MENU_BUTTON]),
  };

  const DOWNLOADS = {
    text: t("downloads"),
    markup: keys([link(t("btn_download_page"), DOWNLOAD_PAGE)], [btn(t("btn_ios_guide"), "i:ios"), MENU_BUTTON]),
  };

  // ---------- Answers, tickets ----------

  /** What sits under an answer of the assistant: whether it helped, and the way to a person. The last part of the data says whether the answer handed over to the team. */
  function feedback(handoff = false) {
    const h = handoff ? ":1" : "";
    const rate = [btn(t("btn_helpful"), `fb:u${h}`), btn(t("btn_not_helpful"), `fb:d${h}`)];
    return handoff ? keys([TEAM_BUTTON], rate) : keys(rate);
  }

  return {
    code,
    t,
    /** The language's name in English: the assistant is told to answer in it. */
    ai: pack(code).ai,
    menu,
    help,
    connectPicker,
    connectGuide,
    FIXED,
    stillBroken,
    installPicker,
    installGuide,
    plans,
    ACCOUNT_INFO,
    DOWNLOADS,
    feedback,
    ticketKeys,

    /** After a thumbs up: the way to a person stays where the answer pointed to it. */
    thanks: (handoff = false) => (handoff ? keys([TEAM_BUTTON], [MENU_NEW]) : keys([MENU_NEW])),
    /** After a thumbs down: what else can be done. */
    SORRY: keys([TEAM_BUTTON], [btn(t("btn_fix_connection"), "cn"), MENU_NEW]),
    THANKS,
    TRY_AGAIN: keys([TEAM_BUTTON], [MENU_NEW]),
    CAN_ONLY_READ: { text: t("can_only_read"), markup: keys([TEAM_BUTTON]) },

    unknownCommand: (selling = false) => ({ text: `${t("unknown_command")}\n\n${help(selling).text}`, markup: help(selling).markup }),
    /** The assistant could not answer: said in the person's words, with the way to the team. */
    busy: () => ({ text: t("ai_busy", { mail: MAIL }), markup: keys([TEAM_BUTTON], [MENU_NEW]) }),
    failed: () => ({ text: t("ai_failed", { mail: MAIL }), markup: keys([TEAM_BUTTON], [MENU_NEW]) }),

    ticketOpened: (id, hadChat) => ({ text: t(hadChat ? "ticket_opened_chat" : "ticket_opened_new", { id }), markup: ticketKeys(id) }),
    ticketOpen: (id) => ({ text: t("ticket_open", { id }), markup: ticketKeys(id) }),
    ticketClosedByUser: (id) => ({ text: t("ticket_closed_user", { id }), markup: rating }),
    ticketClosedByTeam: (id) => ({ text: t("ticket_closed_team", { id }), markup: rating }),
    ticketClosedIdle: (id) => ({ text: t("ticket_closed_idle", { id }), markup: THANKS }),
    rated: (n) => ({ text: t("rated", { n }), markup: THANKS }),
    TICKET_GONE: { text: t("ticket_gone"), markup: THANKS },
    /** The team's words, already made safe for HTML (the bot cuts them to fit first). */
    teamSays: (html) => `${t("team_says")}\n${html}`,

    /** The end of a chat: its summary went to the team only if it did. */
    ended: (mailed) => ({ text: t(mailed ? "ended_mailed" : "ended_plain"), markup: THANKS }),
    NOTHING_TO_END: { text: t("nothing_to_end"), markup: THANKS },

    SLOW_DOWN: t("slow_down", { mail: MAIL }),
    WENT_WRONG: t("went_wrong", { mail: MAIL }),
    TICKET_LIMIT: t("ticket_limit", { mail: MAIL }),
    TEAM_BUSY: t("team_busy", { mail: MAIL }),
    TEAM_UNREACHABLE: t("team_unreachable", { mail: MAIL }),

    /** No team chat is paired: there is nobody to answer in Telegram, and the person is told so. */
    noTeam: (mailed) => ({ text: t(mailed ? "no_team_mailed" : "no_team", { mail: MAIL }), markup: keys([MENU_NEW]) }),

    // ---------- Buying data with Telegram Stars ----------

    /** The packages, with the Stars each costs and the plan it earns. `cat` is sales.js catalog. */
    store(cat, days, money = null) {
      const rows = [];
      for (let i = 0; i < cat.length; i += 2) rows.push(cat.slice(i, i + 2).map((p) => buy(btn(`${MEDALS[p.tier] || "⭐"} ${p.gb} GB · ⭐${p.stars}`, `b:${p.gb}:1`))));
      return {
        text: t("store", { lines: cat.map((p) => `• <b>${p.gb} GB</b> - ⭐${p.stars}${priced(p.stars, money)} - ${planLine(p)}`).join("\n"), valid: validText(days) }),
        markup: keys(...rows, [buy(btn(t("btn_build"), "b:10:3"))], [btn(t("btn_what_plans"), "p"), btn(t("btn_terms"), "st")], [WEBSITE, MENU_BUTTON]),
      };
    },

    /**
     * A plan of the buyer's own (sales.plan): the data a month with - and +,
     * the months, and its price, worked out at once; then a new account or
     * one that exists. `limits` is { min, max, step } of the data a month.
     */
    builder(p, limits, money = null) {
      const down = p.gb - limits.stepDown, up = p.gb + limits.stepUp;
      const months = limits.months.map((m) => {
        const label = t("months_short", { n: m });
        return btn(m === p.months ? `✅ ${label}` : label, `b:${p.gb}:${m}`);
      });
      const save = p.save > 0 ? `\n${t("builder_save", { save: p.save })}` : "";
      return {
        text: t("builder", { gb: p.gb, months: t("months_short", { n: p.months }), total: p.total, valid: validText(p.days), plan: `${MEDALS[p.tier] || ""} ${planLine(p)}`.trim(), stars: p.stars }).replace(`⭐${p.stars}</b>`, `⭐${p.stars}</b>${priced(p.stars, money)}`) + save,
        markup: keys(
          [btn(down >= limits.min ? `➖ ${limits.stepDown} GB` : "·", down >= limits.min ? `b:${down}:${p.months}` : `b:${p.gb}:${p.months}`),
            btn(`📦 ${p.gb} GB`, `b:${p.gb}:${p.months}`),
            btn(up <= limits.max ? `➕ ${limits.stepUp} GB` : "·", up <= limits.max ? `b:${up}:${p.months}` : `b:${p.gb}:${p.months}`)],
          months,
          [buy(btn(t("btn_new_account"), `sn:${p.gb}:${p.months}`)), buy(btn(t("btn_add_account"), `sr:${p.gb}:${p.months}`))],
          [BACK_TO_PACKAGES, btn(t("btn_terms"), "st")],
        ),
      };
    },

    askAccount: (p) => ({ text: t("ask_account", { gb: p.total ?? p.gb }), markup: keys([BACK_TO_PACKAGES]) }),
    NOT_A_USERNAME: { text: t("not_a_username"), markup: keys([BACK_TO_PACKAGES]) },
    NO_SUCH_ACCOUNT: { text: t("no_such_account"), markup: keys([BACK_TO_PACKAGES]) },
    CANNOT_RECHARGE: { text: t("cannot_recharge"), markup: keys([TEAM_BUTTON], [BACK_TO_PACKAGES]) },
    TOO_MANY_LOOKUPS: { text: t("too_many_lookups"), markup: keys([BACK_TO_PACKAGES]) },

    /** The order, before the payment: what it is, for whom, and the terms to agree to. */
    checkout(o, p, days, money = null) {
      const what = o.kind === "topup" ? t("checkout_topup", { account: esc(o.target) }) : t("checkout_new");
      return {
        text: t("checkout", { gb: o.gb, stars: o.stars, plan: planLine(p), valid: validText(p.days ?? days), what }).replace(`⭐${o.stars}`, `⭐${o.stars}${priced(o.stars, money)}`),
        markup: keys([buy(btn(t("btn_pay", { stars: o.stars }), `sp:${o.id}`))], [btn(t("btn_terms"), "st"), BACK_TO_PACKAGES]),
      };
    },

    terms: (days) => ({
      text: `${t("terms", { valid_for: days > 0 ? t("terms_valid", { days }) : t("terms_forever") })}\n\n${WEBSITE_LINE}`,
      markup: keys([BACK_TO_PACKAGES, MENU_NEW]),
    }),
    PAYSUPPORT: { text: t("paysupport", { mail: MAIL, site: WEBSITE_LINE }), markup: keys([TEAM_BUTTON], [MENU_NEW]) },
    STORE_OFF: { text: t("store_off"), markup: keys([TEAM_BUTTON], [MENU_NEW]) },
    ORDER_GONE: { text: t("order_gone"), markup: keys([BUY_BUTTON], [MENU_NEW]) },

    /** What Telegram's pre-checkout answer says when it turns an order away (plain text, at most 200 characters). */
    declined: (why) => t(`pc_${why}`),
    /** The invoice's own texts (plain text): its title, what it is for, and the line of its price. */
    invoice: (order) => ({
      title: t("inv_title", { gb: order.gb }),
      description: t(order.kind === "topup" ? "inv_desc_topup" : "inv_desc_new", { gb: order.gb, valid: validText(order.days), account: order.target }),
      label: t("inv_label", { gb: order.gb }),
    }),
    invoiceFailed: (why) => t("inv_failed", { why: esc(why) }),

    // ---------- A new password for an account bought in this bot ----------

    NO_OWN_ACCOUNT: { text: t("no_own_account"), markup: keys([TEAM_BUTTON], [MENU_NEW]) },
    /** The accounts a person bought, to pick from. */
    pickAccount: (names) => ({ text: t("pick_account"), markup: keys(...names.map((n) => [btn(n, `pw:${n}`)]), [MENU_NEW]) }),
    /** Before it is done: it signs everything out. */
    confirmReset: (name) => ({ text: t("confirm_reset", { account: esc(name) }), markup: keys([btn(t("btn_reset_yes"), `py:${name}`)], [btn(t("btn_cancel"), "a")]) }),
    newPassword: (name, password) => ({ text: t("new_password", { account: esc(name), password: esc(password) }), markup: keys([link(t("btn_account_page_short"), ACCOUNT)], [MENU_NEW]) }),
    TOO_MANY_RESETS: { text: t("too_many_resets"), markup: keys([TEAM_BUTTON], [MENU_NEW]) },

    /** After a new account's payment: the account, once. */
    receiptNew: (r) => ({
      text: t("receipt_new", { id: r.id, gb: r.gb, stars: r.stars, username: esc(r.username), password: esc(r.password), plan: planLine(r), until: untilText(r.expires) }),
      markup: keys([buy(link(t("btn_dl_app"), DOWNLOAD_PAGE))], [link(t("btn_account_page_short"), ACCOUNT), WEBSITE], [MENU_NEW]),
    }),

    /** After data was added to an account. */
    receiptTopup: (r) => ({
      text: t("receipt_topup", { id: r.id, gb: r.gb, stars: r.stars, username: esc(r.username), total: gbOf(r.quota_bytes), until: untilText(r.expires), plan: planLine(r) }),
      markup: keys([link(t("btn_account_page_short"), ACCOUNT), WEBSITE], [MENU_NEW]),
    }),

    /** An account bought here runs low on data or days: a recharge is one tap away. */
    renew: (r) => ({
      text: t("renew", { account: esc(r.username), left: gbOf(r.left), until: untilText(r.expires) }),
      markup: keys([buy(btn(t("btn_recharge"), "s"))], [MENU_NEW]),
    }),

    /** A payment was given back: `why` is "delivery" (it could not be delivered) or "team". */
    gaveBack: (id, stars, why) => ({ text: t("gave_back", { id, stars, why: t(why === "team" ? "why_team" : "why_delivery") }), markup: keys([TEAM_BUTTON], [MENU_NEW]) }),
    deliveryFailed: () => t("why_delivery"),
  };
}

// ---------- The team's side (always English: the team reads it) ----------

export const receiptTest = (id) => `✅ <b>Test payment received</b> (P${id}). Stars work end to end. Give it back with /refund P${id}.`;

export const OWNER_HELP = `<b>Team commands</b>

/tickets - the open tickets
/stats - today's numbers, and the new people of the last week by the link they came from (t.me/<bot>?start=ad_fa1)
/close 1042 - close a ticket (or reply /close to one of its messages)
/block 123456 - block a user, and /unblock to let them back
/sales - today's sales and the last payments
/refund P12 - give a payment back (the account is switched off, or the data taken off again)
/teststars - a test payment of 1 Star, to see that selling works

Reply to a ticket's message to answer the person. Photos, files and voice notes work too.`;

export const PAIRED = "✅ <b>Paired.</b> Tickets will arrive in this chat. Reply to a ticket's message to answer the person. /help lists the commands.";
