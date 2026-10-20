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
const num = (n) => Number(n || 0).toLocaleString("en-US");
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

function pingClass(ms) {
  if (!ms) return "plain";
  if (ms < 350) return "good";
  if (ms < 900) return "mid";
  return "slow";
}

const statusTitles = {
  idle: "Not connected",
  searching: "Looking for a server",
  connecting: "Connecting",
  reconnecting: "Reconnecting",
  connected: "Connected",
};

let state = null;
let view = "home";

// ---------- Connect ----------

function render(s) {
  state = s;
  document.body.classList.toggle("mobile", !!s.mobile);

  const p = $("power");
  const busy = s.status === "searching" || s.status === "connecting" || s.status === "reconnecting";
  p.classList.toggle("busy", busy);
  p.classList.toggle("on", s.status === "connected");
  p.setAttribute("aria-label", s.status === "idle" ? "Connect" : "Disconnect");

  $("status-title").textContent = statusTitles[s.status] || "";
  let sub = s.message || "";
  if (s.status === "idle" && !sub) sub = s.servers > 0 ? "Tap the button to connect" : "Getting ready…";
  if (s.status === "connected" && s.conn) sub = `${s.conn.flag} ${s.conn.country_name}`;
  $("status-sub").textContent = sub;

  const c = s.conn;
  $("conn-stats").classList.toggle("hidden", !c);
  if (c) $("st-ping").textContent = c.delay ? `${num(c.delay)} ms` : "—";

  const locations = s.countries.length;
  const target = s.countries.find((x) => x.code === s.target);
  $("cc-flag").textContent = s.target ? s.target_flag : "⚡";
  $("cc-name").textContent = s.target ? s.target_name : "Fastest location";
  if (!s.target) {
    $("cc-meta").textContent = s.servers ? `${num(s.servers)} servers in ${num(locations)} locations` : "Looking for servers…";
  } else if (target) {
    $("cc-meta").textContent = `${num(target.servers)} servers · ${num(target.best_delay)} ms`;
  } else {
    $("cc-meta").textContent = "No servers available yet";
  }
  $("n-servers").textContent = num(s.servers);
  $("n-countries").textContent = num(locations);

  const u = s.update;
  $("update-banner").classList.toggle("hidden", !u || u.required);
  $("update-modal").classList.toggle("hidden", !u || !u.required);
  if (u) {
    $("update-banner").textContent = `Version ${u.latest} is available. Tap to get it.${u.message ? " " + u.message : ""}`;
    if (u.message) $("update-text").textContent = u.message;
  }

  $("admin-chip").classList.toggle("hidden", !s.admin);
  if (!settingsBusy) fillSettings(s.settings);
  $("version").textContent = s.version ? "v" + s.version : "";
  if (sheetKind === "country") renderSheet();
}

async function poll() {
  try {
    render(await api("GET", "/api/state"));
  } catch (e) {
    $("status-sub").textContent = "Connection to the app was lost…";
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

// ---------- Pickers ----------

let sheetKind = null;

function openSheet(kind) {
  sheetKind = kind;
  const titles = { country: "Choose a location", protocol: "Protocol", bypass: "Bypass local sites", privacy: "Privacy" };
  $("sheet-title").textContent = titles[kind];
  $("country-search").classList.toggle("hidden", kind !== "country");
  $("country-search").value = "";
  $("sheet").classList.remove("hidden");
  $("sheet-backdrop").classList.remove("hidden");
  renderSheet();
  if (kind === "country") setTimeout(() => $("country-search").focus(), 50);
}

function closeSheet() {
  sheetKind = null;
  $("sheet").classList.add("hidden");
  $("sheet-backdrop").classList.add("hidden");
}

function optionItem(value, label, selected, sub) {
  return `<button class="item${selected ? " selected" : ""}" data-value="${esc(value)}">
    <span class="main"><span class="title">${esc(label)}</span>${sub ? `<span class="sub">${esc(sub)}</span>` : ""}</span>
    ${selected ? `<svg class="check" viewBox="0 0 24 24"><path d="M5 13l4 4L19 7"/></svg>` : ""}</button>`;
}

function renderSheet() {
  if (!state || !sheetKind) return;
  let html = "";
  if (sheetKind === "country") {
    const q = $("country-search").value.trim().toLowerCase();
    const match = (c) => !q || c.name.toLowerCase().includes(q) || c.code.toLowerCase() === q;
    if (!q || "fastest".includes(q)) {
      html += countryItem({ code: "", flag: "⚡", name: "Fastest location", meta: "Lowest ping", badge: "" });
    }
    for (const c of state.countries.filter(match)) {
      html += countryItem({ code: c.code, flag: c.flag, name: c.name, meta: `${num(c.servers)} servers`, badge: `<span class="badge ${pingClass(c.best_delay)}">${num(c.best_delay)} ms</span>` });
    }
    if (!html) html = `<div class="empty">No locations found</div>`;
  } else if (sheetKind === "protocol") {
    html = state.protocols.map((p) => optionItem(p.value, p.label, p.value === state.settings.protocol,
      p.value === "" ? "Picks the best one for you" : "")).join("");
  } else if (sheetKind === "privacy") {
    html = privacyNotes.map(([t, d]) => `<div class="note"><b>${esc(t)}</b>${esc(d)}</div>`).join("");
  } else if (sheetKind === "bypass") {
    html = optionItem("", "Off", !state.settings.bypass, "Open every site through Skyroad") +
      state.regions.map((r) => optionItem(r.code, r.name, r.code === state.settings.bypass, `Open ${r.name} sites directly`)).join("");
  }
  $("sheet-list").innerHTML = html;
}

// What the app protects and what it cannot: stated plainly, without promises
// that no VPN can keep.
const privacyNotes = [
  ["Encrypted tunnel", "Your traffic is encrypted between your device and the server you connect to, so your network or provider cannot see what you open."],
  ["Secure sites stay private", "Pages, passwords and messages on sites that use HTTPS stay encrypted all the way to the site. The server carrying them cannot read them."],
  ["Unencrypted traffic is blocked", "Anything unencrypted can be read by whoever carries it, so plain HTTP, FTP, Telnet and unprotected mail ports are refused. You can turn this off in Settings."],
  ["DNS stays inside the tunnel", "Name lookups go through the connection, not through your network."],
  ["What a server can still see", "The server you connect through can see which sites you reach and how much data you send, as with any VPN. Do not rely on it to make you anonymous."],
  ["Your account", "It records only your username, how much data you have used and how long you have been connected. The app does not send the sites you visit."],
];

function countryItem(c) {
  const sel = (state.target || "") === c.code ? " selected" : "";
  return `<button class="item${sel}" data-value="${esc(c.code)}" data-country="1">
    <span class="flag big">${esc(c.flag)}</span>
    <span class="main"><span class="title">${esc(c.name)}</span><span class="sub">${esc(c.meta)}</span></span>
    ${c.badge}</button>`;
}

$("country-btn").addEventListener("click", () => openSheet("country"));
$("row-protocol").addEventListener("click", () => openSheet("protocol"));
$("row-bypass").addEventListener("click", () => openSheet("bypass"));
$("row-privacy").addEventListener("click", () => openSheet("privacy"));
$("row-killswitch").addEventListener("click", () => api("POST", "/api/vpn-settings").catch((e) => toast(e.message)));
$("sheet-backdrop").addEventListener("click", closeSheet);
$("country-search").addEventListener("input", renderSheet);
document.addEventListener("keydown", (e) => {
  if (e.key === "Escape") { closeSheet(); $("admin-modal").classList.add("hidden"); }
});

$("sheet-list").addEventListener("click", async (e) => {
  const item = e.target.closest(".item");
  if (!item) return;
  const value = item.dataset.value;
  const kind = sheetKind;
  if (kind === "privacy") return;
  closeSheet();
  if (kind === "protocol") return saveSettings({ protocol: value });
  if (kind === "bypass") return saveSettings({ bypass: value });
  try {
    // Picking a location connects to it right away.
    await api("POST", "/api/connect", { country: value });
    poll();
  } catch (err) {
    if (err.code === "need_admin") {
      await api("POST", "/api/target", { country: value }).catch(() => {});
      $("admin-modal").classList.remove("hidden");
    } else toast(err.message);
  }
});

// ---------- Tabs ----------

function show(v) {
  view = v;
  document.querySelectorAll(".view").forEach((el) => el.classList.toggle("active", el.id === "view-" + v));
  document.querySelectorAll(".tabs button").forEach((b) => b.classList.toggle("active", b.dataset.view === v));
  if (v === "logs") loadLogs();
}
document.querySelectorAll(".tabs button").forEach((b) => b.addEventListener("click", () => show(b.dataset.view)));

// ---------- Settings ----------

let settingsBusy = false;
const toggles = ["secure_only", "block_ads", "auto_connect", "start_with_system"];

function fillSettings(st) {
  for (const k of toggles) $("set-" + k).checked = !!st[k];
  $("set-tun").checked = st.mode === "tun";
  const proto = state.protocols.find((p) => p.value === st.protocol);
  $("protocol-value").textContent = proto ? proto.label : "Automatic";
  const region = state.regions.find((r) => r.code === st.bypass);
  $("bypass-value").textContent = region ? region.name : "Off";
}

async function saveSettings(patch) {
  if (!state) return;
  settingsBusy = true;
  const next = Object.assign({}, state.settings, patch);
  try {
    state.settings = await api("POST", "/api/settings", next);
    fillSettings(state.settings);
    toast("Saved");
  } catch (err) {
    fillSettings(state.settings);
    if (err.code === "need_admin") $("admin-modal").classList.remove("hidden");
    else toast(err.message);
  } finally {
    settingsBusy = false;
  }
}

for (const k of toggles) $("set-" + k).addEventListener("change", (e) => saveSettings({ [k]: e.target.checked }));
$("set-tun").addEventListener("change", (e) => saveSettings({ mode: e.target.checked ? "tun" : "proxy" }));

$("admin-cancel").addEventListener("click", () => $("admin-modal").classList.add("hidden"));
$("admin-ok").addEventListener("click", async () => {
  $("admin-modal").classList.add("hidden");
  try {
    await api("POST", "/api/settings", Object.assign({}, state.settings, { mode: "tun" })).catch(() => {});
    await api("POST", "/api/admin");
    toast("Restarting with administrator rights…");
  } catch (err) { toast(err.message); }
});
$("quit-btn").addEventListener("click", () => api("POST", "/api/quit").catch(() => {}));

const getUpdate = () => state && state.update && api("POST", "/api/open", { url: state.update.url }).catch((e) => toast(e.message));
$("update-banner").addEventListener("click", getUpdate);
$("update-get").addEventListener("click", getUpdate);

// ---------- Diagnostics ----------

// Seven quick taps on the version number open the activity log, which
// support can ask for.
let taps = [];
$("version").addEventListener("click", () => {
  const now = Date.now();
  taps = taps.filter((t) => now - t < 4000).concat(now);
  if (taps.length >= 7) { taps = []; show("logs"); }
});

async function loadLogs() {
  let logs = [];
  try { logs = await api("GET", "/api/logs"); } catch (e) {}
  $("logs").innerHTML = logs.slice().reverse().map((l) => {
    const t = new Date(l.time).toLocaleTimeString("en-GB");
    return `<div class="log ${esc(l.level)}"><time>${esc(t)}</time><span>${esc(l.text)}</span></div>`;
  }).join("") || `<div class="empty">Nothing yet</div>`;
}

// The Android back button: close what is open, then go home; false lets the
// app go to the background.
window.skyroadBack = () => {
  if (!$("sheet").classList.contains("hidden")) { closeSheet(); return true; }
  if (!$("admin-modal").classList.contains("hidden")) { $("admin-modal").classList.add("hidden"); return true; }
  if (view === "logs") { show("settings"); return true; }
  if (view !== "home") { show("home"); return true; }
  return false;
};

// ---------- Loop ----------

// Once a second while visible; a hidden window (minimized, in the tray, or
// an app in the background) checks in rarely, which lets the engine skip
// work nobody would see.
async function loop() {
  await poll();
  setTimeout(loop, document.hidden ? 5000 : 1000);
}
loop();
document.addEventListener("visibilitychange", () => { if (!document.hidden) poll(); });
setInterval(() => { if (!document.hidden && view === "logs") loadLogs(); }, 4000);
