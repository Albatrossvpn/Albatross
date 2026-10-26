import { $, ago, api, esc, fmtBytes, fmtDate, fmtTime, frame, planBadge, toast } from "/site.js";

const GB = 1024 ** 3;
const KEY = "admin-session";
let token = "";
try { token = sessionStorage.getItem(KEY) || ""; } catch {}
let offset = 0;
const LIMIT = 25;
let users = [];
let editing = null;

const call = (method, path, body) => api(method, "admin/" + path, { token, body });

function signedOut() {
  token = "";
  try { sessionStorage.removeItem(KEY); } catch {}
  $("#auth").classList.remove("hidden");
  $("#panel").classList.add("hidden");
  $("#logout").classList.add("hidden");
}

/** Runs an administrator call; a refused session goes back to the sign-in. */
async function guard(fn, errEl) {
  try {
    return await fn();
  } catch (e) {
    if (e.status === 401) signedOut();
    else if (errEl) errEl.textContent = e.message;
    else toast(e.message);
  }
}

// ---------- Limits fields ----------

const toDate = (unix) => (unix ? new Date(unix * 1000).toISOString().slice(0, 10) : "");
const fromDate = (s) => (s ? Math.floor(new Date(s + "T23:59:59Z").getTime() / 1000) : 0);
const num = (v) => (v === "" || v == null ? 0 : Number(v));

function limitsFrom(p) {
  return {
    tier: $(`#${p}-tier`).value,
    quota_bytes: Math.round(num($(`#${p}-quota`).value) * GB),
    max_seconds: Math.round(num($(`#${p}-hours`).value) * 3600),
    reset_days: Math.round(num($(`#${p}-reset`).value)),
    expires_at: fromDate($(`#${p}-expires`).value),
    note: $(`#${p}-note`).value.trim(),
  };
}

function fillLimits(p, u) {
  $(`#${p}-tier`).value = u.tier || "bronze";
  $(`#${p}-quota`).value = u.quota_bytes ? +(u.quota_bytes / GB).toFixed(3) : "";
  $(`#${p}-hours`).value = u.max_seconds ? +(u.max_seconds / 3600).toFixed(2) : "";
  $(`#${p}-reset`).value = u.reset_days || "";
  $(`#${p}-expires`).value = toDate(u.expires_at);
  $(`#${p}-note`).value = u.note || "";
}

// ---------- Overview and list ----------

async function loadStats() {
  const s = await guard(() => call("GET", "stats"));
  if (!s) return;
  const cards = [
    [s.users, "Users"], [s.active_day, "Active in 24 h"], [s.active_month, "Active in 30 days"],
    [s.disabled, "Turned off"], [fmtBytes(s.total_bytes), "Total data"], [fmtTime(s.total_seconds), "Total time"],
  ];
  $("#stats").innerHTML = cards.map(([v, k]) => `<div class="stat"><b>${esc(v)}</b><span>${esc(k)}</span></div>`).join("");
}

function usageCell(used, limit, fmt) {
  if (!limit) return `${esc(fmt(used))}<div class="muted small">no limit</div>`;
  const frac = Math.min(1, used / limit);
  const cls = frac >= 1 ? "full" : frac >= 0.85 ? "warn" : "";
  return `${esc(fmt(Math.min(used, limit)))} <span class="muted">/ ${esc(fmt(limit))}</span><div class="bar mini"><i class="${cls}" data-w="${(frac * 100).toFixed(0)}"></i></div>`;
}

const statusPill = (u) =>
  u.status === "active" ? `<span class="pill">Active</span>`
    : `<span class="pill ${u.status === "disabled" ? "bad" : "warn"}">${esc({ disabled: "Off", expired: "Expired", quota: "Data used", time: "Time used", devices: "Devices full" }[u.status] || u.status)}</span>`;

let tiers = {};
const planName = (tier) => (tiers[tier] ? tiers[tier].name : tier);
const planCell = (u) => `${planBadge(u.tier, planName(u.tier))}<div class="muted small">${u.max_devices ? `${u.devices} of ${u.max_devices}` : u.devices} ${u.devices === 1 && !u.max_devices ? "device" : "devices"}</div>`;

/** Fills every plan selector from the service's list of plans. */
function fillPlans() {
  const opts = Object.entries(tiers).map(([k, t]) => `<option value="${esc(k)}">${esc(t.name)} · ${t.devices ? `${t.devices} ${t.devices === 1 ? "device" : "devices"}` : "any number of devices"}</option>`).join("");
  for (const id of ["u-tier", "b-tier", "t-tier"]) $("#" + id).innerHTML = opts;
}

async function loadUsers() {
  const q = $("#q").value.trim();
  const r = await guard(() => call("GET", `users?q=${encodeURIComponent(q)}&limit=${LIMIT}&offset=${offset}`));
  if (!r) return;
  users = r.users;
  $("#rows").innerHTML = users.map((u) => `<tr>
    <td><b>${esc(u.username)}</b>${u.note ? `<div class="muted small">${esc(u.note)}</div>` : ""}</td>
    <td>${statusPill(u)}</td>
    <td>${planCell(u)}</td>
    <td>${usageCell(u.used_bytes, u.quota_bytes, fmtBytes)}</td>
    <td>${usageCell(u.used_seconds, u.max_seconds, fmtTime)}</td>
    <td>${u.expires_at ? esc(fmtDate(u.expires_at)) : "—"}</td>
    <td>${esc(ago(u.last_seen))}</td>
    <td><button class="btn small" data-edit="${u.id}">Edit</button></td></tr>`).join("") ||
    `<tr><td colspan="8" class="muted">No users.</td></tr>`;
  for (const i of document.querySelectorAll("#rows .bar i")) i.style.width = i.dataset.w + "%";
  const from = r.total ? offset + 1 : 0;
  $("#count").textContent = `${from}–${offset + users.length} of ${r.total}`;
  $("#prev").disabled = offset === 0;
  $("#next").disabled = offset + LIMIT >= r.total;
}

const refresh = () => Promise.all([loadStats(), loadUsers()]);

function enter(t) {
  token = t;
  try { sessionStorage.setItem(KEY, t); } catch {}
  $("#auth").classList.add("hidden");
  $("#panel").classList.remove("hidden");
  $("#logout").classList.remove("hidden");
  refresh();
}

// ---------- Sign in ----------

$("#login-form").addEventListener("submit", async (e) => {
  e.preventDefault();
  $("#a-error").textContent = "";
  try {
    const r = await api("POST", "admin/login", { body: { username: $("#a-user").value, password: $("#a-pass").value } });
    $("#a-pass").value = "";
    enter(r.token);
  } catch (err) {
    $("#a-error").textContent = err.message;
  }
});
$("#logout").addEventListener("click", signedOut);

// ---------- A password to hand over ----------

function showSecret(title, secret) {
  $("#x-title").textContent = title;
  $("#x-secret").textContent = secret;
  $("#secret-dialog").showModal();
}
$("#x-close").addEventListener("click", () => $("#secret-dialog").close());
$("#x-copy").addEventListener("click", () => copy($("#x-secret").textContent));

async function copy(text) {
  try { await navigator.clipboard.writeText(text); toast("Copied."); } catch { toast("Select the text and copy it."); }
}

// ---------- Create or edit a user ----------

function openUser(u) {
  editing = u;
  $("#u-error").textContent = "";
  $("#u-title").innerHTML = u ? `Edit ${esc(u.username)} ${planBadge(u.tier, planName(u.tier))}` : "New user";
  $("#u-name-field").classList.toggle("hidden", !!u);
  $("#u-pass-field").classList.toggle("hidden", !!u);
  $("#u-extra").classList.toggle("hidden", !u);
  $("#u-save").textContent = u ? "Save" : "Create";
  $("#u-name").value = "";
  $("#u-pass").value = "";
  fillLimits("u", u || { reset_days: 0, quota_bytes: 5 * GB, tier: "bronze" });
  if (u) $("#u-toggle").textContent = u.disabled ? "Turn on" : "Turn off";
  $("#user-dialog").showModal();
}

$("#new").addEventListener("click", () => openUser(null));
$("#rows").addEventListener("click", (e) => {
  const id = e.target.dataset && e.target.dataset.edit;
  if (id) openUser(users.find((u) => u.id === Number(id)));
});
$("#u-cancel").addEventListener("click", () => $("#user-dialog").close());

$("#user-form").addEventListener("submit", async (e) => {
  e.preventDefault();
  const err = $("#u-error");
  err.textContent = "";
  if (editing) {
    const r = await guard(() => call("PATCH", `users/${editing.id}`, limitsFrom("u")), err);
    if (r) { $("#user-dialog").close(); toast("Saved."); refresh(); }
  } else {
    const r = await guard(() => call("POST", "users", { username: $("#u-name").value, password: $("#u-pass").value || undefined, ...limitsFrom("u") }), err);
    if (r) {
      $("#user-dialog").close();
      showSecret(`Password for ${r.user.username}`, r.password);
      refresh();
    }
  }
});

async function patch(body, done) {
  const r = await guard(() => call("PATCH", `users/${editing.id}`, body), $("#u-error"));
  if (r) { editing = r.user; refresh(); done && done(r); }
}
$("#u-reset-usage").addEventListener("click", () => patch({ reset_usage: true }, () => toast("Recharged: the allowance is full again.")));
$("#u-signout").addEventListener("click", () => patch({ sign_out: true }, () => toast("Signed out everywhere.")));
$("#u-toggle").addEventListener("click", () => patch({ disabled: !editing.disabled }, (r) => {
  $("#u-toggle").textContent = r.user.disabled ? "Turn on" : "Turn off";
  toast(r.user.disabled ? "Turned off." : "Turned on.");
}));
$("#u-reset-pass").addEventListener("click", () => {
  const pw = Array.from(crypto.getRandomValues(new Uint8Array(14)), (b) => "abcdefghjkmnpqrstuvwxyzABCDEFGHJKLMNPQRSTUVWXYZ23456789"[b % 54]).join("");
  const name = editing.username;
  patch({ password: pw }, () => { $("#user-dialog").close(); showSecret(`New password for ${name}`, pw); });
});
$("#u-delete").addEventListener("click", async () => {
  if (!confirm(`Delete ${editing.username}? This cannot be undone.`)) return;
  const r = await guard(() => call("DELETE", `users/${editing.id}`), $("#u-error"));
  if (r) { $("#user-dialog").close(); toast("Deleted."); refresh(); }
});

// ---------- Create many ----------

let bulkRows = [];
$("#bulk").addEventListener("click", () => {
  $("#b-fields").classList.remove("hidden");
  $("#b-result").classList.add("hidden");
  for (const id of ["b-copy", "b-download"]) $("#" + id).classList.add("hidden");
  $("#b-create").classList.remove("hidden");
  $("#b-error").textContent = "";
  fillLimits("b", { reset_days: 0, quota_bytes: 5 * GB, tier: "bronze" });
  $("#bulk-dialog").showModal();
});
$("#b-cancel").addEventListener("click", () => $("#bulk-dialog").close());
$("#bulk-form").addEventListener("submit", async (e) => {
  e.preventDefault();
  const err = $("#b-error");
  err.textContent = "";
  const r = await guard(() => call("POST", "users/bulk", { prefix: $("#b-prefix").value, count: Number($("#b-count").value), ...limitsFrom("b") }), err);
  if (!r) return;
  bulkRows = r.users;
  $("#b-out").value = "username,password\n" + bulkRows.map((u) => `${u.username},${u.password}`).join("\n");
  $("#b-fields").classList.add("hidden");
  $("#b-result").classList.remove("hidden");
  $("#b-create").classList.add("hidden");
  for (const id of ["b-copy", "b-download"]) $("#" + id).classList.remove("hidden");
  refresh();
});
$("#b-copy").addEventListener("click", () => copy($("#b-out").value));
$("#b-download").addEventListener("click", () => {
  const a = document.createElement("a");
  a.href = URL.createObjectURL(new Blob([$("#b-out").value + "\n"], { type: "text/csv" }));
  a.download = "accounts.csv";
  a.click();
  URL.revokeObjectURL(a.href);
});

// ---------- Settings ----------

$("#settings").addEventListener("click", async () => {
  const s = await guard(() => call("GET", "settings"));
  if (!s) return;
  $("#t-signup").checked = s.signup_open === "1";
  $("#t-quota").value = +(Number(s.default_quota_bytes) / GB).toFixed(3);
  $("#t-hours").value = +(Number(s.default_max_seconds) / 3600).toFixed(2);
  $("#t-reset").value = s.default_reset_days;
  $("#t-valid").value = s.default_valid_days;
  $("#t-tier").value = s.default_tier || "bronze";
  $("#t-win").value = s.download_windows;
  $("#t-and").value = s.download_android;
  $("#t-error").textContent = "";
  $("#settings-dialog").showModal();
});
$("#t-cancel").addEventListener("click", () => $("#settings-dialog").close());
$("#settings-form").addEventListener("submit", async (e) => {
  e.preventDefault();
  const r = await guard(() => call("PUT", "settings", {
    signup_open: $("#t-signup").checked,
    default_quota_bytes: String(Math.round(num($("#t-quota").value) * GB)),
    default_max_seconds: String(Math.round(num($("#t-hours").value) * 3600)),
    default_reset_days: String(Math.round(num($("#t-reset").value))),
    default_valid_days: String(Math.round(num($("#t-valid").value))),
    default_tier: $("#t-tier").value,
    download_windows: $("#t-win").value.trim(),
    download_android: $("#t-and").value.trim(),
  }), $("#t-error"));
  if (r) { $("#settings-dialog").close(); toast("Saved."); }
});

// ---------- List controls ----------

let timer;
$("#q").addEventListener("input", () => { clearTimeout(timer); timer = setTimeout(() => { offset = 0; loadUsers(); }, 250); });
$("#prev").addEventListener("click", () => { offset = Math.max(0, offset - LIMIT); loadUsers(); });
$("#next").addEventListener("click", () => { offset += LIMIT; loadUsers(); });

tiers = (await frame()).tiers || {};
fillPlans();
if (token) enter(token);
