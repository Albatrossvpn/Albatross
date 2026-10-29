// The Telegram bot's languages. A pack (./<code>.js) holds every string the
// bot says in one language; en.js is the master and test/i18n-check.mjs holds
// every other pack to it. A person picks their language when they first write,
// and the bot keeps it (telegram.js, "The language of a person").
import ar from "./ar.js";
import en from "./en.js";
import es from "./es.js";
import fa from "./fa.js";
import fr from "./fr.js";
import id from "./id.js";
import ru from "./ru.js";
import tr from "./tr.js";
import ur from "./ur.js";
import vi from "./vi.js";
import zh from "./zh.js";

/** The language everything falls back to: a string a pack lacks is read from it, and so is a person who has picked none. */
export const DEFAULT = "en";

/** Every pack, in the order the picker lists them. Add a language here once its pack passes `node test/i18n-check.mjs <code>`. */
export const PACKS = [fa, en, zh, ru, ar, tr, ur, vi, id, es, fr];

const byCode = new Map(PACKS.map((p) => [p.code, p]));
export const has = (code) => byCode.has(code);
export const pack = (code) => byCode.get(code) || en;

/** "Choose your language", in each language the picker offers: it is read before a language is chosen. */
const PICK = {
  fa: "زبان خود را انتخاب کنید",
  zh: "请选择语言",
  ru: "Выберите язык",
  ar: "اختر لغتك",
  tr: "Dilinizi seçin",
  ur: "اپنی زبان منتخب کریں",
  vi: "Chọn ngôn ngữ của bạn",
  id: "Pilih bahasa Anda",
  es: "Elige tu idioma",
  fr: "Choisissez votre langue",
  uk: "Оберіть мову",
  uz: "Tilni tanlang",
  kk: "Тілді таңдаңыз",
  az: "Dili seçin",
  my: "သင့်ဘာသာစကားကို ရွေးပါ",
  hi: "अपनी भाषा चुनें",
  bn: "আপনার ভাষা বেছে নিন",
  th: "เลือกภาษาของคุณ",
  ps: "خپله ژبه وټاکئ",
  tg: "Забонро интихоб кунед",
  tk: "Dilini saýlaň",
  be: "Абярыце мову",
  am: "ቋንቋዎን ይምረጡ",
};

/** The picker: the language of the person's Telegram first when there is a pack for it, then the others in order. Telegram's `language_code` looks like "fa", "pt-br" or "zh-hans". */
export function picker(hint = "") {
  const lead = String(hint || "").toLowerCase().split("-")[0];
  const list = [...PACKS.filter((p) => p.code === lead), ...PACKS.filter((p) => p.code !== lead)];
  return {
    packs: list,
    title: ["🌐 <b>Choose your language</b>", ...PACKS.filter((p) => p.code !== "en" && PICK[p.code]).map((p) => PICK[p.code])].join("\n"),
  };
}

/** Fills a string's {placeholders}: one with no value is left empty, and a value is put in as it is (the caller makes it safe for HTML). */
export const fill = (text, vars = {}) => String(text).replace(/\{([a-z_]+)\}/g, (_, key) => (Object.hasOwn(vars, key) ? String(vars[key]) : ""));

/** `t(key, vars)` for one language. A key the pack lacks is read from English; a key nobody has is a mistake and throws. A list of steps comes back as a list. */
export function translator(code) {
  const mine = pack(code).s;
  return (key, vars) => {
    const value = Object.hasOwn(mine, key) ? mine[key] : en.s[key];
    if (value === undefined) throw new Error(`No string "${key}" in the Telegram bot's languages.`);
    return Array.isArray(value) ? value.map((v) => fill(v, vars)) : fill(value, vars);
  };
}
