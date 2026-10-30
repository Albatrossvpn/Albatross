// Exchange rates, for the Telegram bot to show what a price in Stars comes to
// in the money of the buyer's language (telegram-content.js CURRENCIES).
// They are fetched once an hour from a public source of daily rates and kept
// in the settings; the Iranian toman is never taken from it: the owner's own
// rate (stars_usd_toman) stands for it.

/** The currencies the bot's languages show, besides the toman. */
export const FX_CODES = ["EUR", "AFN", "TJS", "CNY", "RUB", "SAR", "EGP", "IQD", "TRY", "PKR", "VND", "IDR", "MXN", "MAD"];

const SOURCE = "https://open.er-api.com/v6/latest/USD";

/** Fetches the rates (units of each currency for a dollar) and keeps them; on any failure the last ones stay. */
export async function refreshRates(env, fetchFn = fetch) {
  try {
    const res = await fetchFn(SOURCE, { signal: AbortSignal.timeout(15000) });
    if (!res.ok) return false;
    const j = await res.json();
    if (!j || j.result !== "success" || !j.rates) return false;
    const rates = {};
    for (const c of FX_CODES) {
      const v = Number(j.rates[c]);
      if (Number.isFinite(v) && v > 0) rates[c] = v;
    }
    if (!Object.keys(rates).length) return false;
    const t = Math.floor(Date.now() / 1000);
    await env.DB.batch([
      env.DB.prepare("INSERT INTO settings (key, value) VALUES ('fx_rates', ?1) ON CONFLICT (key) DO UPDATE SET value = ?1").bind(JSON.stringify(rates)),
      env.DB.prepare("INSERT INTO settings (key, value) VALUES ('fx_at', ?1) ON CONFLICT (key) DO UPDATE SET value = ?1").bind(String(t)),
    ]);
    return true;
  } catch {
    return false;
  }
}

/** The kept rates; none (an empty object) before the first fetch or when they are more than a week old. */
export async function ratesOf(env) {
  const { results } = await env.DB.prepare("SELECT key, value FROM settings WHERE key IN ('fx_rates', 'fx_at')").all();
  const s = Object.fromEntries(results.map((r) => [r.key, r.value]));
  if (!s.fx_rates || Math.floor(Date.now() / 1000) - Number(s.fx_at || 0) > 7 * 86400) return {};
  try {
    return JSON.parse(s.fx_rates) || {};
  } catch {
    return {};
  }
}
