import { $, api, esc, fmtBytes, fmtDate, fmtTime, frame, meter, toast } from "/site.js";

const KEY = "session";
const store = {
  get() { try { return localStorage.getItem(KEY) || ""; } catch { return ""; } },
  set(v) { try { v ? localStorage.setItem(KEY, v) : localStorage.removeItem(KEY); } catch {} },
};
let token = store.get();
let info;

const reasons = {
  disabled: "This account is turned off.",
  expired: "This account has expired.",
  quota: "The data allowance is used up.",
  time: "The time allowance is used up.",
};

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
  store.set("");
  token = "";
  $("#auth").classList.remove("hidden");
  $("#dash").classList.add("hidden");
  $("#logout").classList.add("hidden");
}

function render(a) {
  $("#auth").classList.add("hidden");
  $("#dash").classList.remove("hidden");
  $("#logout").classList.remove("hidden");
  $("#d-name").textContent = a.username;
  const pill = $("#d-status");
  pill.textContent = a.allowed ? "Active" : { disabled: "Off", expired: "Expired" }[a.reason] || "Limit reached";
  pill.className = "pill" + (a.allowed ? "" : " bad");
  $("#d-sub").textContent = a.allowed ? "You can connect." : "You cannot connect right now.";
  const n = $("#notice");
  n.classList.toggle("hidden", a.allowed);
  n.textContent = a.allowed ? "" : (reasons[a.reason] || "") + (a.resets_at && (a.reason === "quota" || a.reason === "time") ? ` It renews on ${fmtDate(a.resets_at)}.` : "");
  meter($("#m-data"), "Data", a.used_bytes, a.quota_bytes, fmtBytes);
  meter($("#m-time"), "Time", a.used_seconds, a.max_seconds, fmtTime);
  const rows = [];
  if (a.resets_at) rows.push(["Allowance renews", fmtDate(a.resets_at)]);
  rows.push(["Account ends", a.expires_at ? fmtDate(a.expires_at) : "Never"]);
  $("#d-kv").innerHTML = rows.map(([k, v]) => `<dt>${esc(k)}</dt><dd>${esc(v)}</dd>`).join("");
}

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
  store.set(token);
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

if (token) load();
