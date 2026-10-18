"use strict";

// The token arrives in the URL fragment so it never reaches a server log.
const token = (() => {
  const h = location.hash.slice(1);
  if (h) {
    try { sessionStorage.setItem("t", h); } catch (e) {}
    history.replaceState(null, "", location.pathname);
    return h;
  }
  try { return sessionStorage.getItem("t") || ""; } catch (e) { return ""; }
})();

const $ = (id) => document.getElementById(id);
const fa = (n) => Number(n || 0).toLocaleString("fa-IR");
const esc = (s) => String(s ?? "").replace(/[&<>"']/g, (c) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" }[c]));

async function api(method, path, body) {
  const res = await fetch(path, {
    method,
    headers: { "X-Token": token, "Content-Type": "application/json" },
    body: body === undefined ? undefined : JSON.stringify(body),
  });
  const data = await res.json().catch(() => ({}));
  if (!res.ok) {
    const err = new Error(data.error || res.statusText);
    err.code = data.code;
    throw err;
  }
  return data;
}

let toastTimer;
function toast(text) {
  const t = $("toast");
  t.textContent = text;
  t.classList.remove("hidden");
  clearTimeout(toastTimer);
  toastTimer = setTimeout(() => t.classList.add("hidden"), 3200);
}

function bytesRate(n) {
  if (!n) return "۰";
  const units = ["بایت", "کیلوبایت", "مگابایت", "گیگابایت"];
  let i = 0;
  while (n >= 1024 && i < units.length - 1) { n /= 1024; i++; }
  return `${(i ? n.toFixed(n < 10 ? 1 : 0) : n).toLocaleString("fa-IR")} ${units[i]}`;
}

function duration(sec) {
  sec = Math.max(0, Math.floor(sec));
  const h = Math.floor(sec / 3600), m = Math.floor((sec % 3600) / 60), s = sec % 60;
  const two = (x) => String(x).padStart(2, "0");
  return (h ? `${h}:${two(m)}:${two(s)}` : `${two(m)}:${two(s)}`).replace(/\d/g, (d) => "۰۱۲۳۴۵۶۷۸۹"[d]);
}

function ago(sec) {
  if (sec < 60) return "همین الان";
  if (sec < 3600) return `${fa(Math.floor(sec / 60))} دقیقه پیش`;
  return `${fa(Math.floor(sec / 3600))} ساعت پیش`;
}

function pingClass(ms) {
  if (!ms) return "plain";
  if (ms < 350) return "good";
  if (ms < 900) return "mid";
  return "slow";
}

const protoNames = {
  vless: "VLESS", vmess: "VMess", trojan: "Trojan", shadowsocks: "Shadowsocks", hysteria2: "Hysteria2",
  hysteria: "Hysteria", tuic: "TUIC", anytls: "AnyTLS", wireguard: "WireGuard", socks: "SOCKS",
  openvpn: "OpenVPN", warpplus: "WARP+",
};

let state = null;
let view = "home";

// ---------- Home ----------

function render(s) {
  state = s;
  const p = $("power");
  const busy = s.status === "searching" || s.status === "connecting" || s.status === "reconnecting";
  p.classList.toggle("busy", busy);
  p.classList.toggle("on", s.status === "connected");
  p.setAttribute("aria-label", s.status === "idle" ? "اتصال" : "قطع اتصال");

  const titles = {
    idle: "متصل نیستید",
    searching: "در حال جستجوی سرور",
    connecting: "در حال اتصال",
    reconnecting: "در حال جابه‌جایی",
    connected: "وصل هستید",
  };
  $("status-title").textContent = titles[s.status] || "";
  let sub = s.message || "";
  if (s.status === "idle" && !sub) sub = s.working_now > 0 ? "دکمه را بزنید تا به سریع‌ترین سرور وصل شوید" : "در حال پیدا کردن سرورهای سالم…";
  if (s.status === "connected" && s.conn) sub = `${s.conn.flag} ${s.conn.country_name}${s.conn.server ? " — " + s.conn.server : ""}`;
  $("status-sub").textContent = sub;

  const c = s.conn;
  $("conn-stats").classList.toggle("hidden", !c);
  $("exit-line").classList.toggle("hidden", !c);
  if (c) {
    $("st-ping").textContent = c.delay ? `${fa(c.delay)} ms` : "—";
    $("st-down").textContent = bytesRate(c.down_rate) + "/ث";
    $("st-up").textContent = bytesRate(c.up_rate) + "/ث";
    $("st-time").textContent = duration(Date.now() / 1000 - c.since);
    const proto = protoNames[c.proto] || c.proto || "";
    $("exit-line").innerHTML = `آی‌پی شما در اینترنت: <code>${esc(c.exit_ip || "—")}</code> · ${esc(proto)} · ${fa(c.group)} سرور آماده · ${c.mode === "tun" ? "تونل کامل" : "پروکسی " + `<code>${esc(c.proxy_addr)}</code>`}`;
  }

  const target = s.countries.find((x) => x.code === s.target);
  $("cc-flag").textContent = s.target ? s.target_flag : "⚡";
  $("cc-name").textContent = s.target ? s.target_name : "سریع‌ترین کشور";
  if (!s.target) {
    $("cc-meta").textContent = s.working_now ? `${fa(s.working_now)} سرور سالم در ${fa(s.countries.filter((x) => x.servers).length)} کشور` : "در حال جستجو…";
  } else if (target && target.servers) {
    $("cc-meta").textContent = `${fa(target.servers)} سرور سالم · بهترین پینگ ${fa(target.best_delay)} ms`;
  } else {
    $("cc-meta").textContent = "هنوز سرور سالمی پیدا نشده؛ با اولویت جستجو می‌شود";
  }

  const sc = s.scan;
  const live = sc.testing > 0 || sc.phase;
  $("scan-dot").classList.toggle("live", !!live);
  let text = "سرورها به‌طور خودکار و مداوم به‌روز و آزمایش می‌شوند";
  if (sc.phase === "loading") text = "در حال بارگذاری سرورهای ذخیره‌شده…";
  else if (sc.phase === "fetching") text = `در حال دریافت منابع (${fa(sc.sources_done)} از ${fa(sc.sources_total)})`;
  else if (sc.testing) text = `در حال آزمایش ${fa(sc.testing)} سرور…`;
  $("scan-text").textContent = text;
  $("n-working").textContent = fa(s.working_now);
  $("n-countries").textContent = fa(s.countries.filter((x) => x.servers).length);
  $("n-candidates").textContent = fa(sc.untested);
  $("n-rate").textContent = fa(sc.per_minute);

  $("admin-chip").classList.toggle("hidden", !s.admin);
  if (!settingsDirty) fillSettings(s.settings);
  $("version").textContent = s.version ? "نسخه " + s.version : "";
  const cores = [];
  cores.push(s.cores.sing_box ? "sing-box ✓" : "sing-box ✗");
  cores.push(s.cores.xray ? "Xray ✓" : "Xray ✗");
  cores.push(s.cores.warp_plus ? "warp-plus ✓" : "warp-plus ✗");
  $("cores-line").textContent = "هسته‌ها: " + cores.join(" · ");

  if (!$("sheet").classList.contains("hidden")) renderCountries();
  if (view === "servers") $("servers-country").textContent = s.target ? s.target_name : "همه کشورها";
}

async function poll() {
  try {
    render(await api("GET", "/api/state"));
  } catch (e) {
    $("status-sub").textContent = "ارتباط با برنامه قطع است…";
  }
}

$("power").addEventListener("click", async () => {
  if (!state) return;
  try {
    if (state.status === "idle") {
      await api("POST", "/api/connect", { country: state.target || "" });
    } else {
      await api("POST", "/api/disconnect");
    }
    poll();
  } catch (e) {
    if (e.code === "need_admin") $("admin-modal").classList.remove("hidden");
    else toast(e.message);
  }
});

// ---------- Country picker ----------

function openSheet() {
  $("sheet").classList.remove("hidden");
  $("sheet-backdrop").classList.remove("hidden");
  $("country-search").value = "";
  renderCountries();
  setTimeout(() => $("country-search").focus(), 50);
}
function closeSheet() {
  $("sheet").classList.add("hidden");
  $("sheet-backdrop").classList.add("hidden");
}
$("country-btn").addEventListener("click", openSheet);
$("sheet-backdrop").addEventListener("click", closeSheet);
document.addEventListener("keydown", (e) => { if (e.key === "Escape") { closeSheet(); $("admin-modal").classList.add("hidden"); } });
$("country-search").addEventListener("input", renderCountries);

function renderCountries() {
  if (!state) return;
  const q = $("country-search").value.trim().toLowerCase();
  const match = (c) => !q || c.name.includes(q) || c.name_en.toLowerCase().includes(q) || c.code.toLowerCase() === q;
  const ready = state.countries.filter((c) => c.servers && match(c));
  const searching = state.countries.filter((c) => !c.servers && match(c));
  let html = "";
  if (!q || "سریع‌ترین".includes(q) || "fastest".includes(q)) {
    html += countryItem({ code: "", flag: "⚡", name: "سریع‌ترین کشور", meta: `کمترین پینگ از بین ${fa(state.working_now)} سرور سالم`, badge: "" });
  }
  if (ready.length) html += `<div class="section-label">آماده اتصال</div>`;
  for (const c of ready) {
    html += countryItem({ code: c.code, flag: c.flag, name: c.name, meta: `${fa(c.servers)} سرور سالم · ${c.name_en}`, badge: `<span class="badge ${pingClass(c.best_delay)}">${fa(c.best_delay)} ms</span>` });
  }
  if (searching.length) html += `<div class="section-label">در صف جستجو (با انتخاب، اول این کشور بررسی می‌شود)</div>`;
  for (const c of searching) {
    html += countryItem({ code: c.code, flag: c.flag, name: c.name, meta: `${fa(c.candidates)} سرور احتمالی · ${c.name_en}`, badge: `<span class="badge plain">جستجو</span>`, dim: true });
  }
  if (!html) html = `<div class="empty">کشوری پیدا نشد</div>`;
  $("country-list").innerHTML = html;
}

function countryItem(c) {
  const sel = (state.target || "") === c.code ? " selected" : "";
  return `<button class="item${sel}${c.dim ? " dim" : ""}" data-code="${esc(c.code)}">
    <span class="flag big">${esc(c.flag)}</span>
    <span class="main"><span class="title">${esc(c.name)}</span><span class="sub">${esc(c.meta)}</span></span>
    ${c.badge}</button>`;
}

$("country-list").addEventListener("click", async (e) => {
  const item = e.target.closest(".item");
  if (!item) return;
  const code = item.dataset.code;
  closeSheet();
  try {
    // Picking a country connects to it right away.
    await api("POST", "/api/connect", { country: code });
    poll();
  } catch (err) {
    if (err.code === "need_admin") {
      await api("POST", "/api/target", { country: code }).catch(() => {});
      $("admin-modal").classList.remove("hidden");
    } else toast(err.message);
  }
});

// ---------- Tabs ----------

function show(v) {
  view = v;
  document.querySelectorAll(".view").forEach((el) => el.classList.toggle("active", el.id === "view-" + v));
  document.querySelectorAll(".tabs button").forEach((b) => b.classList.toggle("active", b.dataset.view === v));
  if (v === "servers") loadServers();
  if (v === "sources") loadSources();
  if (v === "logs") loadLogs();
}
document.querySelectorAll(".tabs button").forEach((b) => b.addEventListener("click", () => show(b.dataset.view)));
$("btn-logs").addEventListener("click", () => show(view === "logs" ? "home" : "logs"));

// ---------- Servers ----------

async function loadServers() {
  const country = state ? state.target : "";
  $("servers-country").textContent = country ? state.target_name : "همه کشورها";
  let list = [];
  try { list = await api("GET", `/api/servers?country=${encodeURIComponent(country || "")}&limit=150`); } catch (e) {}
  if (!list.length) {
    $("servers-list").innerHTML = `<div class="empty">هنوز سرور سالمی برای این کشور پیدا نشده. جستجو ادامه دارد…</div>`;
    return;
  }
  $("servers-list").innerHTML = list.map((s) => {
    const tags = [protoNames[s.proto] || s.proto];
    if (s.transport && s.transport !== "tcp") tags.push(s.transport);
    if (s.security) tags.push(s.security);
    const extra = [];
    if (s.fragment) extra.push(`<span class="tag on">fragment</span>`);
    if (s.plain) extra.push(`<span class="tag">بدون رمزنگاری</span>`);
    return `<div class="item${s.active ? " selected" : ""}" data-key="${esc(s.key)}">
      <span class="flag">${flagOf(s.country)}</span>
      <span class="main"><span class="title">${esc(s.name || "بدون نام")}</span>
        <span class="tags">${tags.map((t) => `<span class="tag">${esc(t)}</span>`).join("")}${extra.join("")}</span>
        <span class="sub">آزمایش ${ago(s.age_sec)}</span></span>
      <span class="badge ${pingClass(s.delay)}">${fa(s.delay)} ms</span>
      <button class="mini-btn" data-copy="${esc(s.link)}" title="کپی کانفیگ">کپی</button>
      <button class="mini-btn" data-connect="${esc(s.key)}">اتصال</button>
    </div>`;
  }).join("");
}

function flagOf(cc) {
  if (!cc || cc.length !== 2) return "🌐";
  return String.fromCodePoint(...[...cc.toUpperCase()].map((c) => 0x1f1e6 + c.charCodeAt(0) - 65));
}

$("servers-list").addEventListener("click", async (e) => {
  const copy = e.target.closest("[data-copy]");
  if (copy) {
    try { await navigator.clipboard.writeText(copy.dataset.copy); toast("کانفیگ کپی شد"); } catch (err) { toast("کپی ناموفق بود"); }
    return;
  }
  const btn = e.target.closest("[data-connect]");
  if (!btn) return;
  try {
    await api("POST", "/api/connect-server", { key: btn.dataset.connect });
    toast("در حال اتصال به این سرور…");
    show("home");
  } catch (err) { toast(err.message); }
});

// ---------- Sources ----------

let sourcesCache = [];
async function loadSources() {
  try { sourcesCache = await api("GET", "/api/sources"); } catch (e) { return; }
  renderSources();
}

function renderSources() {
  const q = $("sources-search").value.trim().toLowerCase();
  const enabled = sourcesCache.filter((s) => s.enabled);
  const okSum = sourcesCache.reduce((a, s) => a + (s.ok || 0), 0);
  const tg = sourcesCache.filter((s) => s.kind === "telegram").length;
  const dl = state ? ` · دریافت‌شده در این اجرا: ${bytesRate(state.scan.downloaded)}` : "";
  $("sources-summary").textContent = `${fa(sourcesCache.length)} منبع (${fa(tg)} کانال تلگرام) · ${fa(enabled.length)} فعال · ${fa(okSum)} سرور سالم از این منابع پیدا شده${dl}. تلگرام بعد از اتصال از طریق خود VPN خوانده می‌شود.`;
  const list = sourcesCache.filter((s) => !q || s.label.toLowerCase().includes(q)).slice(0, 300);
  $("sources-list").innerHTML = list.map((s) => {
    const kind = s.kind === "telegram" ? "تلگرام" : s.kind === "vpngate" ? "OpenVPN" : s.tier === 0 ? "اولویت‌دار" : s.tier === 2 ? "شما" : "عمیق";
    const rate = s.tested ? `${fa(s.ok)} سالم از ${fa(s.tested)} آزمایش` : "هنوز آزمایش نشده";
    const err = s.last_error ? ` · خطا: ${esc(s.last_error).slice(0, 60)}` : "";
    const action = s.builtin
      ? (s.enabled ? `<button class="mini-btn" data-off="${esc(s.id)}">غیرفعال</button>` : `<button class="mini-btn" data-on="${esc(s.id)}">فعال</button>`)
      : `<button class="mini-btn" data-off="${esc(s.id)}">حذف</button>`;
    return `<div class="item${s.enabled ? "" : " dim"}">
      <span class="main"><span class="title" dir="ltr" style="text-align:right">${esc(s.label)}</span>
      <span class="sub"><span class="tag">${kind}</span> ${fa(s.nodes)} کانفیگ · ${rate}${err}</span></span>${action}</div>`;
  }).join("") || `<div class="empty">منبعی پیدا نشد</div>`;
}
$("sources-search").addEventListener("input", renderSources);
$("sources-list").addEventListener("click", async (e) => {
  const off = e.target.closest("[data-off]");
  const on = e.target.closest("[data-on]");
  try {
    if (off) await api("POST", "/api/sources/remove", { id: off.dataset.off });
    if (on) await api("POST", "/api/sources/enable", { id: on.dataset.on });
    if (off || on) loadSources();
  } catch (err) { toast(err.message); }
});
$("import-btn").addEventListener("click", async () => {
  const text = $("import-text").value.trim();
  if (!text) return;
  try {
    const r = await api("POST", "/api/import", { text });
    $("import-text").value = "";
    toast(r.source ? `منبع «${r.source}» اضافه شد` : `${fa(r.added)} کانفیگ جدید اضافه شد و آزمایش می‌شود`);
    loadSources();
  } catch (err) { toast("قابل خواندن نبود: " + err.message); }
});
$("refresh-btn").addEventListener("click", async () => {
  await api("POST", "/api/refresh").catch(() => {});
  toast("همه منابع دوباره دریافت می‌شوند");
});

// ---------- Settings ----------

let settingsDirty = false;
const toggles = ["bypass_iran", "block_ads", "auto_switch", "clean_ip", "warp", "allow_plain", "deep_sources", "auto_connect", "start_with_windows", "allow_lan"];
const segments = ["mode", "fragment", "intensity"];

function fillSettings(st) {
  for (const k of toggles) $("set-" + k).checked = !!st[k];
  for (const k of segments) {
    document.querySelectorAll(`#set-${k} button`).forEach((b) => b.classList.toggle("active", b.dataset.v === st[k]));
  }
  if (document.activeElement !== $("set-proxy_port")) $("set-proxy_port").value = st.proxy_port;
  if (document.activeElement !== $("set-spoof_sni")) $("set-spoof_sni").value = st.spoof_sni || "";
}

async function saveSettings(patch) {
  if (!state) return;
  settingsDirty = true;
  const next = Object.assign({}, state.settings, patch);
  try {
    const saved = await api("POST", "/api/settings", next);
    state.settings = saved;
    fillSettings(saved);
    toast("ذخیره شد");
  } catch (err) {
    fillSettings(state.settings);
    if (err.code === "need_admin") $("admin-modal").classList.remove("hidden");
    else toast(err.message);
  } finally {
    settingsDirty = false;
  }
}

for (const k of toggles) $("set-" + k).addEventListener("change", (e) => saveSettings({ [k]: e.target.checked }));
for (const k of segments) {
  $("set-" + k).addEventListener("click", (e) => {
    const b = e.target.closest("button");
    if (b) saveSettings({ [k]: b.dataset.v });
  });
}
$("set-proxy_port").addEventListener("change", (e) => saveSettings({ proxy_port: parseInt(e.target.value, 10) || 10808 }));
$("set-spoof_sni").addEventListener("change", (e) => saveSettings({ spoof_sni: e.target.value.trim() }));

$("admin-cancel").addEventListener("click", () => $("admin-modal").classList.add("hidden"));
$("admin-ok").addEventListener("click", async () => {
  $("admin-modal").classList.add("hidden");
  try {
    await api("POST", "/api/settings", Object.assign({}, state.settings, { mode: "tun" })).catch(() => {});
    await api("POST", "/api/admin");
    toast("برنامه با دسترسی مدیر دوباره باز می‌شود…");
  } catch (err) { toast(err.message); }
});
$("quit-btn").addEventListener("click", () => api("POST", "/api/quit").catch(() => {}));

// ---------- Logs ----------

async function loadLogs() {
  let logs = [];
  try { logs = await api("GET", "/api/logs"); } catch (e) {}
  $("logs").innerHTML = logs.slice().reverse().map((l) => {
    const t = new Date(l.time).toLocaleTimeString("fa-IR", { hour12: false });
    return `<div class="log ${esc(l.level)}"><time>${esc(t)}</time><span>${esc(l.text)}</span></div>`;
  }).join("") || `<div class="empty">هنوز گزارشی نیست</div>`;
}

// ---------- Loop ----------

poll();
setInterval(poll, 1000);
setInterval(() => {
  if (view === "logs") loadLogs();
  if (view === "servers") loadServers();
}, 4000);
