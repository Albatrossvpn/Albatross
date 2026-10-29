// Checks a language pack of the Telegram bot (src/i18n/<code>.js) against the
// English master: every key, the same placeholders, the same HTML, the same
// commands and names, the lengths Telegram allows, and that it is written in
// the language's script rather than left in English.
//
//   node test/i18n-check.mjs fa        one pack; prints the problems, exits 1 if any
//   node --test test/i18n.test.mjs     every pack there is
import en from "../src/i18n/en.js";

// What a language is written in. A pack of a Latin-script language is not checked for a script.
export const SCRIPTS = {
  fa: /[؀-ۿ]/, ar: /[؀-ۿ]/, ur: /[؀-ۿ]/, ps: /[؀-ۿ]/,
  ru: /[Ѐ-ӿ]/, uk: /[Ѐ-ӿ]/, be: /[Ѐ-ӿ]/, kk: /[Ѐ-ӿ]/, tg: /[Ѐ-ӿ]/,
  zh: /[一-鿿]/, ko: /[가-힯]/, th: /[฀-๿]/, hi: /[ऀ-ॿ]/, bn: /[ঀ-৿]/, my: /[က-႟]/, am: /[ሀ-፿]/,
};

// Names that stay as they are in every language (the apps and the website use them too).
const NAMES = ["Albatross", "Bronze", "Silver", "Gold", "VIP", "GB", "SHA-256", "V2Box", "Streisand", "Shadowrocket", "Hiddify", "sing-box", "Safari", "Cloudflare"];
const ALLOWED_TAGS = new Set(["b", "i", "code", "a"]);

// Buttons, the menu of commands, the profile texts and the model's instruction are plain text, not HTML.
const isPlain = (key) => key.startsWith("btn_") || key.startsWith("cmd_") || key.startsWith("about") || key === "ctx_language" || key.startsWith("toast_");
const placeholders = (s) => (String(s).match(/\{[a-z_]+\}/g) || []).sort();
const commands = (s) => (String(s).match(/\/[a-z]+/g) || []).sort();
const tags = (s) => (String(s).match(/<\/?[a-z]+(?:\s[^>]*)?>/gi) || []).map((t) => t.toLowerCase().replace(/\s+/g, " ")).sort();

/** The HTML of one string: only the tags of Telegram's, balanced, and no stray & < >. */
export function htmlProblems(s) {
  const out = [];
  const stack = [];
  const body = String(s).replace(/<(\/?)([a-z]+)((?:\s[^>]*)?)>/gi, (all, close, name, rest) => {
    const n = name.toLowerCase();
    if (!ALLOWED_TAGS.has(n)) out.push(`the tag <${n}> is not one Telegram's HTML allows here`);
    else if (close) {
      if (stack.pop() !== n) out.push(`</${n}> closes what is not open`);
    } else stack.push(n);
    return "";
  });
  if (stack.length) out.push(`<${stack.join(">, <")}> is never closed`);
  if (/&(?!amp;|lt;|gt;)/.test(body)) out.push("a bare & (write &amp;)");
  if (/[<>]/.test(body)) out.push("a bare < or > (write &lt; or &gt;)");
  return out;
}

/** Everything wrong with a pack, as strings; none when it is right. */
export function check(code, pack, master = en) {
  const problems = [];
  const add = (key, why) => problems.push(`${key}: ${why}`);
  for (const f of ["code", "name", "flag", "ai"]) if (typeof pack[f] !== "string" || !pack[f].trim()) problems.push(`the pack has no ${f}`);
  if (pack.code !== code) problems.push(`code is "${pack.code}", the file is ${code}.js`);
  if (!pack.s || typeof pack.s !== "object") return [...problems, "the pack has no s (its strings)"];
  for (const key of Object.keys(master.s)) if (!(key in pack.s)) add(key, "missing");
  for (const key of Object.keys(pack.s)) if (!(key in master.s)) add(key, "not a key of the English pack");
  const script = SCRIPTS[code];
  let withScript = 0;
  let counted = 0;
  for (const [key, want] of Object.entries(master.s)) {
    const got = pack.s[key];
    if (got === undefined) continue;
    const pairs = Array.isArray(want) ? (Array.isArray(got) && got.length === want.length ? want.map((w, i) => [w, got[i]]) : null) : typeof got === "string" ? [[want, got]] : null;
    if (!pairs) {
      add(key, Array.isArray(want) ? `must be a list of ${want.length} strings` : "must be a string");
      continue;
    }
    for (const [w, g] of pairs) {
      if (typeof g !== "string" || !g.trim()) {
        add(key, "empty");
        continue;
      }
      if (placeholders(w).join() !== placeholders(g).join()) add(key, `placeholders ${placeholders(w).join(" ") || "none"} in English, ${placeholders(g).join(" ") || "none"} here`);
      if (tags(w).join() !== tags(g).join()) add(key, `the HTML differs from English (${tags(w).join(" ") || "none"} / ${tags(g).join(" ") || "none"})`);
      if (!isPlain(key)) for (const p of htmlProblems(g)) add(key, p);
      if (commands(w).join() !== commands(g).join()) add(key, `commands ${commands(w).join(" ") || "none"} in English, ${commands(g).join(" ") || "none"} here: keep them as they are`);
      for (const n of NAMES) if (w.includes(n) && !g.includes(n)) add(key, `the name ${n} is missing: keep it as it is`);
      if (key.startsWith("btn_") && g.length > 40) add(key, `a button of ${g.length} characters (40 at most)`);
      if (key.startsWith("cmd_") && (g.length < 3 || g.length > 256)) add(key, "a command's description has 3 to 256 characters");
      if (key === "about" && g.length > 512) add(key, `the description has ${g.length} characters (512 at most)`);
      if (key === "about_short" && g.length > 120) add(key, `the about line has ${g.length} characters (120 at most)`);
      if (g.length > 3500) add(key, `${g.length} characters is too long for a message`);
      // Left in English? Not when it has words of its own; names, emojis and placeholders alone may be the same.
      const words = w.replace(/<[^>]+>/g, " ").replace(/\{[a-z_]+\}/g, " ").replace(/[^\p{L}]+/gu, " ");
      const own = NAMES.reduce((s2, n) => s2.replaceAll(n, " "), words).trim();
      if (code !== "en" && own && g === w) add(key, "is the English text");
      if (script && own) {
        counted++;
        if (script.test(g)) withScript++;
      }
    }
  }
  if (script && counted && withScript / counted < 0.9) problems.push(`only ${withScript} of ${counted} strings are in the language's script: the rest look left in English`);
  return problems;
}

if (process.argv[1] && process.argv[1].endsWith("i18n-check.mjs")) {
  const code = process.argv[2];
  if (!code) {
    console.error("usage: node test/i18n-check.mjs <code>");
    process.exit(2);
  }
  const pack = (await import(`../src/i18n/${code}.js`)).default;
  const problems = check(code, pack);
  if (problems.length) {
    console.log(problems.join("\n"));
    console.log(`\n${problems.length} problem(s) in ${code}.js`);
    process.exit(1);
  }
  console.log(`${code}.js: all ${Object.keys(en.s).length} strings are right.`);
}
