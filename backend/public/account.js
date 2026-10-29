import { $, ago, api, esc, fmtBytes, fmtDate, fmtTime, frame, meter, planBadge, toast } from "/site.js";

let token = "";
let info;
let acct; // the account as last shown

const reasons = {
  disabled: "This account is turned off.",
  expired: "This account has expired.",
  quota: "The data allowance is used up.",
  time: "The time allowance is used up.",
  devices: "All the devices your plan allows are connected. Disconnect one, or remove it below.",
};

const planName = (tier) => (info.tiers && info.tiers[tier] ? info.tiers[tier].name : tier);
const planPill = (tier, cls) => planBadge(tier, planName(tier), cls);

function showTab(name) {
  $("#login-form").classList.toggle("hidden", name !== "login");
  $("#signup-form").classList.toggle("hidden", name !== "signup");
  for (const b of document.querySelectorAll("#tabs button")) b.classList.toggle("on", b.dataset.tab === name);
  $("#auth-sub").textContent = name === "login" ? "Sign in to see your usage." : "Create an account to get started.";
  if (name === "signup") renderTurnstile();
}

let turnstileId;
function renderTurnstile() {
  if (!info.turnstile_sitekey || turnstileId !== undefined) return;
  const s = document.createElement("script");
  s.src = "https://challenges.cloudflare.com/turnstile/v0/api.js?render=explicit";
  s.onload = () => { turnstileId = window.turnstile.render("#turnstile", { sitekey: info.turnstile_sitekey }); };
  document.head.append(s);
}

function signedOut() {
  token = "";
  $("#auth").classList.remove("hidden");
  $("#dash").classList.add("hidden");
  $("#logout").classList.add("hidden");
}

// Connection apps for Apple devices: where to get each, and the address
// that adds a subscription to it with one tap.
const APPS = [
  { id: "v2box", name: "V2Box", store: "https://apps.apple.com/app/id6446814690",
    link: (u, n) => `v2box://install-sub?url=${encodeURIComponent(u)}&name=${encodeURIComponent(n)}` },
  { id: "streisand", name: "Streisand", store: "https://apps.apple.com/app/id6450534064", link: (u) => `streisand://import/${u}` },
  { id: "shadowrocket", name: "Shadowrocket", store: "https://apps.apple.com/app/id932747118",
    link: (u, n) => `shadowrocket://add/sub://${btoa(u).replace(/\+/g, "-").replace(/\//g, "_").replace(/=+$/, "")}?remark=${encodeURIComponent(n)}` },
  { id: "hiddify", name: "Hiddify", store: "https://apps.apple.com/app/id6596777532", link: (u, n) => `hiddify://import/${u}#${encodeURIComponent(n)}` },
  // sing-box loads a whole config, which the subscription gives with f=singbox.
  { id: "singbox", name: "sing-box", store: "https://apps.apple.com/app/id6673731168",
    link: (u, n) => `sing-box://import-remote-profile?url=${encodeURIComponent(u + (u.includes("?") ? "&" : "?") + "f=singbox")}#${encodeURIComponent(n)}` },
];
let app = APPS.find((a) => a.id === localStorage.getItem("app")) || APPS[0];
let sub;

function renderApps() {
  $("#apps").innerHTML = APPS.map((a) => `<button type="button" data-app="${a.id}" class="${a === app ? "on" : ""}">${a.name}</button>`).join("");
  $("#app-get").innerHTML = `Not installed yet? <a href="${app.store}" target="_blank" rel="noopener">Get ${esc(app.name)} from the App Store</a>, then come back here.`;
}

function renderSub() {
  const el = $("#sub-list");
  if (!sub) return;
  const brand = sub.brand || "Albatross";
  const rows = [];
  const row = (label, count, choice) => {
    const u = sub.url + (choice ? "?c=" + choice : "");
    const name = `${brand} ${label.replace(/^\S+\s/, "")}`;
    // A country's servers come with choosing a location, the edge's with the edge.
    const need = choice === "edge" ? "edge" : choice ? "locations" : "";
    const add = need && !has(need) ? planPill(acct.plans[need], "small") : `<a class="btn small primary" href="${esc(app.link(u, name))}">Add to ${esc(app.name)}</a>`;
    rows.push(`<div class="row between wrap-row"><span>${esc(label)} <span class="muted small">· ${count} ${count === 1 ? "server" : "servers"}</span></span>${add}</div>`);
  };
  row("All servers", sub.servers + sub.edge, "");
  if (sub.edge) row(`${brand} Edge`, sub.edge, "edge");
  for (const c of sub.countries) row(`${c.flag} ${c.name}`, c.count, c.code);
  el.innerHTML = rows.join("") || `<span class="muted note">No servers are listed right now. Try again in a few minutes.</span>`;
  $("#sub-url").textContent = sub.url;
}

async function loadSub() {
  try {
    sub = await api("GET", "sub", { token });
    renderSub();
  } catch (e) {
    $("#sub-list").innerHTML = `<span class="muted note">${esc(e.message)}</span>`;
  }
}

/** Whether the account's plan has a feature; a service naming none has every one. */
const has = (f) => !acct || !acct.features || acct.features.includes(f);

/** What the plan has (ticked) and what a higher plan brings (with its medal). */
function renderFeatures(a) {
  const names = info.features || {};
  $("#plan-card").classList.toggle("hidden", !a.features || !Object.keys(names).length);
  $("#d-features").innerHTML = Object.entries(names).map(([id, f]) =>
    has(id) ? `<li class="yes">${esc(f.name)}</li>` : `<li class="no">${esc(f.name)} ${planPill(a.plans[id], "small")}</li>`).join("");
}

// Data on sale in the Telegram bot, for an account with a data limit: a link to the bot's store, with the prices.
function renderBuy() {
  const st = info.stars;
  const show = !!st && /^[A-Za-z0-9_]{5,32}$/.test(st.bot || "") && acct.quota_bytes > 0 && !(acct.resets_at > 0); // an allowance that renews by itself is not recharged in the bot
  $("#buy").classList.toggle("hidden", !show);
  if (!show) return;
  $("#buy-text").textContent = `Need more data? Pay with Telegram Stars in our bot: ${st.packages.map((p) => `${p.gb} GB for ${p.stars} \u2B50`).join(", ")}. Say which account to add it to (${acct.username || "this one"}): what is left stays, and the days start again.`;
  $("#buy-link").href = `https://t.me/${st.bot}?start=buy`;
}

// VIP accounts reach support directly: the chat link and the address.
function renderSupport() {
  const links = [];
  if (info.support_url) links.push(`<a class="btn primary" href="${esc(info.support_url)}" target="_blank" rel="noopener">Message support</a>`);
  if (info.contact) links.push(`<a class="btn" href="mailto:${esc(info.contact)}">${esc(info.contact)}</a>`);
  const show = acct.features ? acct.features.includes("support") : false;
  $("#support").classList.toggle("hidden", !show || !links.length);
  $("#support-links").innerHTML = links.join("");
}

function render(a) {
  acct = a;
  $("#auth").classList.add("hidden");
  $("#dash").classList.remove("hidden");
  $("#logout").classList.remove("hidden");
  if (a.allowed) loadSub();
  else $("#sub-list").innerHTML = `<span class="muted note">The servers are available while the account can connect.</span>`;
  $("#d-name").textContent = a.username;
  $("#d-plan").innerHTML = planPill(a.tier, "big");
  $("#member").className = "card member " + (info.tiers && info.tiers[a.tier] ? a.tier : "");
  const pill = $("#d-status");
  pill.textContent = a.allowed ? "Active" : { disabled: "Off", expired: "Expired", devices: "Devices in use" }[a.reason] || "Limit reached";
  pill.className = "pill" + (a.allowed ? "" : " bad");
  $("#d-sub").textContent = a.allowed ? "You can connect." : "You cannot connect right now.";
  const n = $("#notice");
  n.classList.toggle("hidden", a.allowed);
  n.textContent = a.allowed ? "" : (reasons[a.reason] || "") + (a.resets_at && (a.reason === "quota" || a.reason === "time") ? ` It renews on ${fmtDate(a.resets_at)}.` : "");
  meter($("#m-data"), "Data", a.used_bytes, a.quota_bytes, fmtBytes);
  meter($("#m-time"), "Time", a.used_seconds, a.max_seconds, fmtTime);
  const rows = [];
  rows.push(["Plan", `${planPill(a.tier)} ${a.max_devices ? `up to ${a.max_devices} ${a.max_devices === 1 ? "device" : "devices"} at once` : "any number of devices"}`]);
  rows.push(["Connected now", esc(a.max_devices ? `${a.devices} of ${a.max_devices}` : `${a.devices}`)]);
  if (a.resets_at) rows.push(["Allowance renews", esc(fmtDate(a.resets_at))]);
  rows.push(["Account ends", esc(a.expires_at ? fmtDate(a.expires_at) : "Never")]);
  $("#d-kv").innerHTML = rows.map(([k, v]) => `<dt>${esc(k)}</dt><dd>${v}</dd>`).join("");
  renderFeatures(a);
  renderSupport();
  renderBuy();
  renderSub();
  loadDevices();
}

// The devices the account has been used on; removing one frees its slot.
async function loadDevices() {
  const el = $("#dev-list");
  try {
    const r = await api("GET", "devices", { token });
    $("#dev-plan").textContent = r.max_devices
      ? `Your ${planName(r.tier)} plan lets ${r.max_devices === 1 ? "one device" : `${r.max_devices} devices`} connect at the same time. A device keeps its place for a few minutes after it disconnects; the subscription for Apple devices keeps it for six hours after an app refreshes it.`
      : `Your ${planName(r.tier)} plan lets any number of devices connect at the same time. These are the devices this account has been used on.`;
    el.innerHTML = r.devices.map((d) => `<div class="row between wrap-row">
      <span>${esc(d.name)}<span class="muted small"> · ${d.active ? "connected" : "last seen " + esc(ago(d.last_seen))}</span></span>
      <button class="btn small" type="button" data-remove="${esc(d.id)}">Remove</button></div>`).join("") ||
      `<span class="muted note">No device has connected with this account yet.</span>`;
  } catch (e) {
    el.innerHTML = `<span class="muted note">${esc(e.message)}</span>`;
  }
}
$("#dev-list").addEventListener("click", async (e) => {
  const id = e.target.dataset && e.target.dataset.remove;
  if (!id) return;
  e.target.disabled = true;
  try {
    await api("DELETE", "devices/" + id, { token });
    toast("Removed.");
    load();
  } catch (err) {
    toast(err.message);
    e.target.disabled = false;
  }
});

async function load() {
  try {
    const r = await api("GET", "me", { token });
    render(r.account);
  } catch (e) {
    if (e.status === 401) signedOut();
    else toast(e.message);
  }
}

function start(r) {
  token = r.token;
  render(r.account);
}

async function submit(form, btn, err, path, body) {
  err.textContent = "";
  btn.disabled = true;
  try {
    start(await api("POST", path, { body }));
    form.reset();
    if (window.turnstile && turnstileId !== undefined) window.turnstile.reset(turnstileId);
  } catch (e) {
    err.textContent = e.message;
  } finally {
    btn.disabled = false;
  }
}

info = await frame();
const tabs = $("#tabs");
if (info.signup_open) {
  tabs.classList.remove("hidden");
  tabs.innerHTML = `<button type="button" data-tab="login">Sign in</button><button type="button" data-tab="signup">Create account</button>`;
  tabs.addEventListener("click", (e) => e.target.dataset.tab && showTab(e.target.dataset.tab));
}
showTab(info.signup_open && location.hash === "#signup" ? "signup" : "login");

$("#login-form").addEventListener("submit", (e) => {
  e.preventDefault();
  submit(e.target, $("#l-btn"), $("#l-error"), "login", { username: $("#l-user").value, password: $("#l-pass").value });
});
$("#signup-form").addEventListener("submit", (e) => {
  e.preventDefault();
  const t = window.turnstile && turnstileId !== undefined ? window.turnstile.getResponse(turnstileId) : undefined;
  submit(e.target, $("#s-btn"), $("#s-error"), "signup", { username: $("#s-user").value, password: $("#s-pass").value, turnstile: t });
});
$("#logout").addEventListener("click", signedOut);
$("#pw-form").addEventListener("submit", async (e) => {
  e.preventDefault();
  $("#p-error").textContent = "";
  try {
    start(await api("POST", "password", { token, body: { old_password: $("#p-old").value, new_password: $("#p-new").value } }));
    e.target.reset();
    toast("Password changed.");
  } catch (err) {
    $("#p-error").textContent = err.message;
  }
});

const dl = $("#d-dl");
for (const [k, label] of [["windows", "Windows"], ["android", "Android"]]) {
  const url = info.downloads && info.downloads[k];
  if (url) dl.insertAdjacentHTML("beforeend", `<a class="btn" href="${esc(url)}">Download for ${label}</a>`);
}
if (!dl.children.length) dl.innerHTML = `<span class="muted note">Downloads will appear here.</span>`;

// Apple devices: the page can live on the Home Screen like an app.
const apple = /iPhone|iPad|iPod|Macintosh/.test(navigator.userAgent) && "ontouchend" in document;
$("#a2hs").classList.toggle("hidden", !apple || window.navigator.standalone === true || matchMedia("(display-mode: standalone)").matches);
renderApps();
$("#apps").addEventListener("click", (e) => {
  const id = e.target.dataset.app;
  if (!id) return;
  app = APPS.find((a) => a.id === id) || app;
  localStorage.setItem("app", app.id);
  renderApps();
  renderSub();
});
$("#sub-copy").addEventListener("click", async () => {
  if (!sub) return;
  try {
    await navigator.clipboard.writeText(sub.url);
    toast("Copied.");
  } catch {
    toast("Select the address and copy it.");
  }
});

if (token) load();
