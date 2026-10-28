"use strict";

// Plain ES5 throughout: Android 6's stock WebView (Chrome 44) must run this.

// The token arrives in the URL fragment so it never reaches a server log.
var token = (function () {
  var h = location.hash.slice(1);
  if (h) {
    try { sessionStorage.setItem("t", h); } catch (e) {}
    history.replaceState(null, "", location.pathname);
    return h;
  }
  try { return sessionStorage.getItem("t") || ""; } catch (e) { return ""; }
})();

function $(id) { return document.getElementById(id); }
function num(n) { return Number(n || 0).toLocaleString("en-US"); }
function esc(s) {
  return String(s == null ? "" : s).replace(/[&<>"']/g, function (c) {
    return { "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" }[c];
  });
}

// Chrome 44 has no NodeList.forEach, Array.prototype.find or Object.assign.
function each(list, fn) { Array.prototype.forEach.call(list, fn); }
function first(list, fn) {
  for (var i = 0; i < list.length; i++) if (fn(list[i])) return list[i];
}
function assign(target) {
  for (var i = 1; i < arguments.length; i++) {
    var src = arguments[i];
    for (var k in src) if (Object.prototype.hasOwnProperty.call(src, k)) target[k] = src[k];
  }
  return target;
}
// Runs fn once p settles, either way, and keeps p's outcome: a finally block.
function always(p, fn) {
  return p.then(function (v) { fn(); return v; }, function (e) { fn(); throw e; });
}

function api(method, path, body) {
  var init = { method: method, headers: { "X-Token": token, "Content-Type": "application/json" } };
  if (body !== undefined) init.body = JSON.stringify(body);
  return fetch(path, init).then(function (res) {
    return res.json().catch(function () { return {}; }).then(function (data) {
      if (!res.ok) {
        var err = new Error(data.error || res.statusText);
        err.code = data.code;
        err.feature = data.feature;
        throw err;
      }
      return data;
    });
  });
}

var toastTimer;
function toast(text) {
  var t = $("toast");
  t.textContent = text;
  t.classList.remove("hidden");
  clearTimeout(toastTimer);
  toastTimer = setTimeout(function () { t.classList.add("hidden"); }, 3200);
}

function pingClass(ms) {
  if (!ms) return "plain";
  if (ms < 350) return "good";
  if (ms < 900) return "mid";
  return "slow";
}

// "Fastest location" wears a gold bolt where a country shows its flag.
var BOLT = '<svg class="bolt" viewBox="0 0 24 24"><path d="M13 2 4 14h7l-1 8 9-12h-7z"/></svg>';

var statusTitles = {
  idle: "Not connected",
  searching: "Looking for a server",
  connecting: "Connecting",
  reconnecting: "Reconnecting",
  connected: "Connected"
};

var state = null;
var view = "home";

// ---------- Connect ----------

function render(s) {
  state = s;
  document.body.classList.toggle("mobile", !!s.mobile);

  var p = $("power");
  var busy = s.status === "searching" || s.status === "connecting" || s.status === "reconnecting";
  p.classList.toggle("busy", busy);
  p.classList.toggle("on", s.status === "connected");
  p.setAttribute("aria-label", s.status === "idle" ? "Connect" : "Disconnect");

  $("status-title").textContent = statusTitles[s.status] || "";
  var sub = s.message || "";
  if (s.status === "idle" && !sub) sub = s.servers > 0 ? "Tap the button to connect" : "Getting ready…";
  if (s.status === "connected" && s.conn) sub = s.conn.flag + " " + s.conn.country_name;
  $("status-sub").textContent = sub;

  var c = s.conn;
  $("conn-stats").classList.toggle("hidden", !c);
  if (c) $("st-ping").textContent = c.delay ? num(c.delay) + " ms" : "—";

  var locations = s.countries.length;
  var target = first(s.countries, function (x) { return x.code === s.target; });
  if (s.target) $("cc-flag").textContent = s.target_flag;
  else $("cc-flag").innerHTML = BOLT;
  $("cc-name").textContent = s.target ? s.target_name : "Fastest location";
  if (!s.target) {
    $("cc-meta").textContent = s.servers ? num(s.servers) + " servers in " + num(locations) + " locations" : "Looking for servers…";
  } else if (target) {
    $("cc-meta").textContent = num(target.servers) + " servers · " + num(target.best_delay) + " ms";
  } else {
    $("cc-meta").textContent = "No servers available yet";
  }
  $("n-servers").textContent = num(s.servers);
  $("n-countries").textContent = num(locations);

  $("help-row").classList.toggle("hidden", !s.help_available && !s.settings.help_others);
  $("crowd-row").classList.toggle("hidden", !s.crowd_available);
  $("share-note").textContent = s.share && s.share.length
    ? "On the other device, set the Wi-Fi proxy to " + s.share.join(" or ") +
      (s.share_pac && s.share_pac.length ? ", or its automatic setup (PAC) address to " + s.share_pac[0] : "")
    : "Phones, laptops and TVs on your hotspot or Wi-Fi can use this connection by setting an HTTP or SOCKS proxy";
  $("help-note").textContent = s.helping ? "Helping now. Thank you!" :
    "Lets people in censored countries reach Tor through this computer. Runs only outside Iran on an open network.";
  $("notice").classList.toggle("hidden", !s.notice);
  $("notice").textContent = s.notice || "";
  var networkText = {
    national: "Only addresses inside the country can be reached right now. Using the relays and carriers that still get out; a share code from a friend who is connected helps.",
    allowlist: "Only a few foreign services are reachable right now. Connections go through what still passes.",
    offline: "No network. Will connect as soon as there is one."
  }[s.network];
  $("network").classList.toggle("hidden", !networkText || s.status === "connected");
  $("network").textContent = networkText || "";

  var u = s.update;
  // A required update the app downloads itself blocks the page only once
  // it is downloaded; until then the user can still connect, so the
  // download can go through the VPN.
  var blocking = !!u && u.required && (!u.in_app || u.progress === 100);
  $("update-banner").classList.toggle("hidden", !u || blocking);
  $("update-modal").classList.toggle("hidden", !blocking);
  if (u) {
    var downloading = u.in_app && u.progress > 0 && u.progress < 100;
    // What the download is doing: the percentage once bytes flow over a
    // line of known length, else the step (connecting, another address,
    // megabytes so far), so the user sees it is at work.
    var doing = u.step || (u.progress + "%");
    // A badge, the version and one button: the manifest's message is for
    // copies too old to update themselves.
    $("update-badge").textContent = u.required ? "Required" : "Optional";
    $("update-badge").className = "badge " + (u.required ? "slow" : "good");
    $("update-version").textContent = "Version " + u.latest;
    $("update-modal-version").textContent = "Version " + u.latest;
    var label = downloading ? "Downloading… " + (u.step ? "" : u.progress + "%") : u.error ? "Try again" : u.in_app ? "Install" : "Download";
    ["update-install", "update-get"].forEach(function (id) {
      $(id).textContent = label;
      $(id).disabled = downloading;
    });
    ["update-error", "update-modal-error"].forEach(function (id) {
      $(id).textContent = u.error || "";
      $(id).classList.toggle("hidden", !u.error);
    });
    $("update-progress").classList.toggle("hidden", !downloading);
    $("update-bar").style.width = (u.step && u.progress <= 1 ? 3 : u.progress) + "%";
    $("update-bar").classList.toggle("busy", !!u.step);
    $("update-step").textContent = downloading ? doing : "";
  }

  renderAccount(s.account);
  renderLocks();
  if (sheetKind === "plans") renderSheet();
  $("admin-chip").classList.toggle("hidden", !s.admin);
  if (!settingsBusy) fillSettings(s.settings);
  renderGps(s);
  $("version").textContent = s.version ? "v" + s.version : "";
  if (sheetKind === "country") renderSheet();
}

var GB = Math.pow(1024, 3);
function fmtBytes(n) {
  if (n >= GB) return (n / GB).toFixed(n >= 10 * GB ? 0 : 1) + " GB";
  if (n >= Math.pow(1024, 2)) return (n / Math.pow(1024, 2)).toFixed(0) + " MB";
  return Math.max(0, Math.round(n / 1024)) + " KB";
}
function fmtTime(sec) {
  var h = Math.floor(sec / 3600), m = Math.floor((sec % 3600) / 60);
  return h ? (m ? h + " h " + m + " min" : h + " h") : m + " min";
}
function fmtDate(unix) {
  return new Date(unix * 1000).toLocaleDateString("en-GB", { day: "numeric", month: "short", year: "numeric" });
}

function meter(id, used, limit, fmt) {
  $(id).classList.toggle("hidden", !limit);
  if (!limit) return;
  var frac = Math.min(1, used / limit);
  $(id + "-text").textContent = fmt(Math.min(used, limit)) + " of " + fmt(limit);
  var bar = $(id + "-bar");
  bar.style.width = (frac * 100).toFixed(1) + "%";
  bar.className = frac >= 1 ? "full" : frac >= 0.85 ? "warn" : "";
}

var PLAN_NAMES = { bronze: "Bronze", silver: "Silver", gold: "Gold", vip: "VIP" };
var PLAN_STAR = '<svg viewBox="0 0 24 24"><path d="M12 2.5l2.9 6 6.6.9-4.8 4.6 1.2 6.5L12 17.4l-5.9 3.1 1.2-6.5L2.5 9.4l6.6-.9z"/></svg>';
var PLAN_CROWN = '<svg viewBox="0 0 24 24"><path d="M3 7.5l4.6 4.2L12 4.5l4.4 7.2L21 7.5l-1.6 11.3H4.6z"/></svg>';

/** Shows the plan's badge in el, or hides el for an account without one. */
function planBadge(el, tier) {
  var name = PLAN_NAMES[tier];
  el.className = "plan-badge" + (name ? " " + tier : " hidden");
  el.innerHTML = name ? (tier === "vip" ? PLAN_CROWN : PLAN_STAR) + name : "";
}

/** A plan's medal as HTML; mini for the one beside a locked feature. */
function medal(tier, mini) {
  if (!PLAN_NAMES[tier]) return "";
  return '<span class="plan-badge ' + (mini ? "mini " : "") + tier + '">' + (tier === "vip" ? PLAN_CROWN : PLAN_STAR) + PLAN_NAMES[tier] + "</span>";
}

// ---------- Plans ----------

// What each plan has beyond connecting; the service says which plan
// brings each (account.plans) and which the account's has
// (account.features). Every plan has PLAN_BASICS.
var TIER_ORDER = ["bronze", "silver", "gold", "vip"];
var FEATURE_NAMES = {
  locations: "Choose a location",
  adblock: "Ad & tracker blocker",
  bypass_apps: "Local apps outside the VPN",
  protocols: "Choose the protocol",
  tun: "Protect the whole device",
  gps: "Override GPS location",
  edge: "Albatross Edge servers, through Cloudflare",
  own: "Albatross's own servers, the fastest",
  lan: "Share with other devices on your Wi-Fi or hotspot"
};
// Features only phones or only computers have.
var FEATURE_ON = { tun: "desktop", gps: "mobile", bypass_apps: "mobile" };
var PLAN_BASICS = ["The fastest location, found for you", "Every protocol, picked for your network", "Relays inside the country in a shutdown",
  "Block unencrypted connections", "Local sites opened directly", "Servers shared with friends"];

function hasFeature(f) {
  var a = state && state.account;
  // A service that names no features (null) is one from before plans had them.
  return !(a && a.signed_in && a.features) || a.features.indexOf(f) >= 0;
}
function featureHere(f) {
  var on = FEATURE_ON[f];
  return !on || (on === "mobile") === !!state.mobile;
}
function planFor(f) { return ((state.account && state.account.plans) || {})[f] || ""; }

// Marks each feature the plan lacks with the medal of the plan that has
// it; its switch stays off and a tap tells which plan to get.
function renderLocks() {
  each(document.querySelectorAll("[data-feature]"), function (el) {
    var f = el.dataset.feature, lock = !hasFeature(f), tier = planFor(f);
    el.classList.toggle("locked", lock);
    var input = el.querySelector("input");
    if (input) input.disabled = lock;
    var title = el.querySelector(".t-body b"), tag = el.querySelector(".lock-tag");
    if (lock && tier && !tag) {
      tag = document.createElement("span");
      tag.className = "lock-tag";
      title.appendChild(tag);
    }
    if (tag && (!lock || !tier)) {
      tag.parentNode.removeChild(tag);
    } else if (tag && tag.dataset.tier !== tier) {
      tag.dataset.tier = tier;
      tag.innerHTML = medal(tier, true);
    }
  });
}

function showPlanModal(f) {
  var tier = planFor(f), name = FEATURE_NAMES[f] || "This";
  $("plan-modal-title").innerHTML = tier ? medal(tier) + " plan" : "Not in your plan";
  $("plan-modal-text").textContent = name + (tier ? " comes with the " + PLAN_NAMES[tier] + " plan" + (tier === "vip" ? "." : " and above.") : " is not in your plan.") +
    " To get it, ask for that plan where you got your account.";
  $("plan-modal").classList.remove("hidden");
}
$("plan-close").addEventListener("click", function () { $("plan-modal").classList.add("hidden"); });
$("plan-see").addEventListener("click", function () { $("plan-modal").classList.add("hidden"); openSheet("plans"); });
$("plans-btn").addEventListener("click", function () { openSheet("plans"); });
$("plan-chip").addEventListener("click", function () { openSheet("plans"); });
// A locked switch tells which plan has it instead of turning on.
each(document.querySelectorAll("label[data-feature]"), function (el) {
  el.addEventListener("click", function (e) {
    if (!hasFeature(el.dataset.feature)) {
      e.preventDefault();
      showPlanModal(el.dataset.feature);
    }
  });
});

/** The plans, each with what it adds to the one below it, the account's marked. */
function plansHTML() {
  var a = state.account, plans = a.plans || {}, devices = a.plan_devices || {};
  return TIER_ORDER.map(function (tier, i) {
    var lines = Object.keys(FEATURE_NAMES).filter(function (f) { return plans[f] === tier && featureHere(f); }).map(function (f) { return FEATURE_NAMES[f]; });
    if (i === 0) lines = PLAN_BASICS.concat(lines);
    var n = devices[tier];
    if (n !== undefined) lines.push(n === 0 ? "As many devices as you like" : n === 1 ? "1 device at a time" : n + " devices at a time");
    return '<div class="tier-card' + (a.tier === tier ? " current" : "") + '"><div class="tier-head">' + medal(tier) +
      (a.tier === tier ? '<span class="tier-yours">Your plan</span>' : "") + "</div>" +
      (i > 0 ? '<small class="tier-also">Everything in ' + PLAN_NAMES[TIER_ORDER[i - 1]] + ", and:</small>" : "") +
      "<ul>" + lines.map(function (l) { return "<li>" + esc(l) + "</li>"; }).join("") + "</ul></div>";
  }).join("");
}

function renderAccount(a) {
  var needLogin = a.enabled && !a.signed_in;
  // While signing in, what it is doing: finding a server to go through
  // can take a while, and the user should know it is still at work.
  $("login-progress").classList.toggle("hidden", !a.progress);
  $("login-progress-text").textContent = a.progress || "";
  planBadge($("plan-chip"), a.signed_in ? a.tier : "");
  planBadge($("acc-plan"), a.signed_in ? a.tier : "");
  $("login").classList.toggle("hidden", !needLogin);
  $("account-card").classList.toggle("hidden", !a.signed_in);
  if (!a.signed_in) return;
  $("acc-name").textContent = a.username;
  $("acc-note").textContent = a.message || "";
  meter("acc-data", a.used_bytes, a.quota_bytes, fmtBytes);
  meter("acc-time", a.used_seconds, a.max_seconds, fmtTime);
  var dates = [];
  if (a.max_devices) dates.push(a.devices + " of " + a.max_devices + (a.max_devices === 1 ? " device" : " devices") + " connected");
  else if (a.tier) dates.push(a.devices + (a.devices === 1 ? " device" : " devices") + " connected · no limit");
  if (a.resets_at) dates.push("Renews " + fmtDate(a.resets_at));
  if (a.expires_at) dates.push("Expires " + fmtDate(a.expires_at));
  if (!a.quota_bytes && !a.max_seconds && !a.expires_at) dates.push("No limits on this account");
  $("acc-dates").textContent = dates.join(" · ");
}

$("login-form").addEventListener("submit", function (e) {
  e.preventDefault();
  $("login-error").textContent = "";
  $("login-btn").disabled = true;
  // Where the account service is filtered, signing in first finds a
  // server to go through, which can take a while.
  $("login-btn").textContent = "Signing in…";
  var signIn = api("POST", "/api/login", { username: $("login-user").value, password: $("login-pass").value }).then(function () {
    $("login-pass").value = "";
    return poll();
  }).catch(function (err) {
    $("login-error").textContent = err.message;
  });
  always(signIn, function () {
    $("login-btn").disabled = false;
    $("login-btn").textContent = "Sign in";
  });
});
$("logout-btn").addEventListener("click", function () {
  api("POST", "/api/logout").catch(function (e) { toast(e.message); }).then(function () { poll(); });
});

function poll() {
  // look=1 while the page shows: the engine then looks for a new version,
  // unless it did a few minutes ago.
  return api("GET", "/api/state" + (document.hidden ? "" : "?look=1")).then(render).catch(function () {
    $("status-sub").textContent = "Connection to the app was lost…";
  });
}

$("power").addEventListener("click", function () {
  if (!state) return;
  var req;
  if (state.status === "idle") {
    req = api("POST", "/api/connect", { country: state.target || "" });
  } else {
    req = api("POST", "/api/disconnect");
  }
  req.then(function () { poll(); }).catch(function (e) {
    if (e.code === "need_admin") $("admin-modal").classList.remove("hidden");
    else toast(e.message);
  });
});

// ---------- Pickers ----------

var sheetKind = null;

function openSheet(kind) {
  sheetKind = kind;
  var titles = { country: "Choose a location", protocol: "Protocol", bypass: "Bypass local sites", privacy: "Privacy", plans: "Plans" };
  $("sheet-title").textContent = titles[kind];
  $("country-search").classList.toggle("hidden", kind !== "country");
  $("country-search").value = "";
  $("sheet").classList.remove("hidden");
  $("sheet-backdrop").classList.remove("hidden");
  renderSheet();
  if (kind === "country") setTimeout(function () { $("country-search").focus(); }, 50);
}

function closeSheet() {
  sheetKind = null;
  $("sheet").classList.add("hidden");
  $("sheet-backdrop").classList.add("hidden");
}

function optionItem(value, label, selected, sub) {
  return '<button class="item' + (selected ? " selected" : "") + '" data-value="' + esc(value) + '">\n' +
    '    <span class="main"><span class="title">' + esc(label) + "</span>" + (sub ? '<span class="sub">' + esc(sub) + "</span>" : "") + "</span>\n" +
    "    " + (selected ? '<svg class="check" viewBox="0 0 24 24"><path d="M5 13l4 4L19 7"/></svg>' : "") + "</button>";
}

function renderSheet() {
  if (!state || !sheetKind) return;
  var html = "";
  if (sheetKind === "country") {
    var q = $("country-search").value.trim().toLowerCase();
    var match = function (c) { return !q || c.name.toLowerCase().includes(q) || c.code.toLowerCase() === q; };
    var lock = !hasFeature("locations");
    if (lock && planFor("locations")) {
      html += '<div class="sheet-note">' + medal(planFor("locations"), true) + "<span>Choosing a location comes with the " + PLAN_NAMES[planFor("locations")] + " plan and above.</span></div>";
    }
    if (!q || "fastest".includes(q)) {
      html += countryItem({ code: "", name: "Fastest location", meta: "Lowest ping", badge: "" });
    }
    state.countries.filter(match).forEach(function (c) {
      html += countryItem({ code: c.code, flag: c.flag, name: c.name, meta: num(c.servers) + " servers", badge: '<span class="badge ' + pingClass(c.best_delay) + '">' + num(c.best_delay) + " ms</span>" }, lock);
    });
    if (!html) html = '<div class="empty">No locations found</div>';
  } else if (sheetKind === "protocol") {
    html = state.protocols.map(function (p) {
      return optionItem(p.value, p.label, p.value === state.settings.protocol, protocolNote(p));
    }).join("");
  } else if (sheetKind === "plans") {
    html = plansHTML();
  } else if (sheetKind === "privacy") {
    html = privacyNotes.map(function (n) { return '<div class="note"><b>' + esc(n[0]) + "</b>" + esc(n[1]) + "</div>"; }).join("");
  } else if (sheetKind === "bypass") {
    html = optionItem("", "Off", !state.settings.bypass, "Open every site through Albatross") +
      state.regions.map(function (r) { return optionItem(r.code, r.name, r.code === state.settings.bypass, "Open " + r.name + " sites directly"); }).join("");
  }
  $("sheet-list").innerHTML = html;
}

// What the app protects and what it cannot: stated plainly, without promises
// that no VPN can keep.
var privacyNotes = [
  ["Encrypted tunnel", "Your traffic is encrypted between your device and the server you connect to, so your network or provider cannot see what you open."],
  ["Secure sites stay private", "Pages, passwords and messages on sites that use HTTPS stay encrypted all the way to the site. The server carrying them cannot read them."],
  ["Unencrypted traffic is blocked", "Anything unencrypted can be read by whoever carries it, so plain HTTP, FTP, Telnet and unprotected mail ports are refused. You can turn this off in Settings."],
  ["DNS stays inside the tunnel", "Name lookups for sites the VPN carries go through the connection, not through your network."],
  ["What a server can still see", "The server you connect through can see which sites you reach and how much data you send, as with any VPN. Do not rely on it to make you anonymous."],
  ["Your account", "Your account records only your username, how much data you have used and how long you have been connected. The app does not send the sites you visit."]
];

function countryItem(c, locked) {
  var sel = ((state.target || "") === c.code ? " selected" : "") + (locked ? " dim" : "");
  return '<button class="item' + sel + '" data-value="' + esc(c.code) + '" data-country="1">\n' +
    '    <span class="flag big">' + (c.code ? esc(c.flag) : BOLT) + "</span>\n" +
    '    <span class="main"><span class="title">' + esc(c.name) + '</span><span class="sub">' + esc(c.meta) + "</span></span>\n" +
    "    " + c.badge + "</button>";
}

$("country-btn").addEventListener("click", function () { openSheet("country"); });
// What the protocol sheet says under a protocol: how many servers it has.
function protocolNote(p) {
  if (p.value === "") return "Picks the best one for you";
  if (p.value === "warp" || p.value === "tor") return "Built in";
  if (!p.servers) return "No servers yet";
  return num(p.servers) + (p.servers === 1 ? " server" : " servers") + (p.working ? " \u00b7 " + num(p.working) + " worked" : "");
}

$("row-protocol").addEventListener("click", function () {
  if (hasFeature("protocols")) openSheet("protocol");
  else showPlanModal("protocols");
});
$("row-bypass").addEventListener("click", function () { openSheet("bypass"); });
$("row-privacy").addEventListener("click", function () { openSheet("privacy"); });
$("row-killswitch").addEventListener("click", function () { api("POST", "/api/vpn-settings").catch(function (e) { toast(e.message); }); });
$("sheet-backdrop").addEventListener("click", closeSheet);

// ---------- Share ----------

$("row-share").addEventListener("click", function () {
  $("share-code").value = "";
  $("share-modal").classList.remove("hidden");
});
$("share-close").addEventListener("click", function () { $("share-modal").classList.add("hidden"); });
$("share-send").addEventListener("click", function () {
  api("POST", "/api/share").then(function (r) {
    $("share-code").value = r.code;
    if (r.sent) return; // the system share sheet took it
    $("share-code").select();
    var told = function (copied) {
      toast(copied ? "Code copied. Paste it to your friend." : "Copy the code and send it to your friend.");
    };
    if (!navigator.clipboard) return told(document.execCommand("copy"));
    return navigator.clipboard.writeText(r.code).then(function () { return true; }, function () { return false; }).then(function (ok) {
      told(ok || document.execCommand("copy"));
    });
  }).catch(function (e) {
    toast(e.message);
  });
});
$("share-add").addEventListener("click", function () {
  api("POST", "/api/share/import", { code: $("share-code").value }).then(function (r) {
    toast(r.added > 0 ? r.added + " servers added; testing them now." : "Those servers are already known.");
    if (r.added > 0) $("share-modal").classList.add("hidden");
  }).catch(function (e) {
    toast(e.message);
  });
});
$("row-share-app").addEventListener("click", function () { api("POST", "/api/share/app").catch(function (e) { toast(e.message); }); });
$("row-save-installer").addEventListener("click", function () {
  toast("Getting the installer\u2026 this takes a minute");
  api("POST", "/api/share/installer").then(function (r) { toast("Saved to " + r.path); }).catch(function (e) { toast(e.message); });
});
$("country-search").addEventListener("input", renderSheet);
document.addEventListener("keydown", function (e) {
  // KeyboardEvent.key needs Chrome 51; keyCode covers older WebViews.
  if (e.key === "Escape" || e.keyCode === 27) {
    closeSheet();
    each(["admin-modal", "share-modal", "gps-modal", "plan-modal"], function (id) { $(id).classList.add("hidden"); });
  }
});

$("sheet-list").addEventListener("click", function (e) {
  var item = e.target.closest(".item");
  if (!item) return;
  var value = item.dataset.value;
  var kind = sheetKind;
  if (kind === "privacy" || kind === "plans") return;
  closeSheet();
  if (kind === "country" && value && !hasFeature("locations")) return showPlanModal("locations");
  if (kind === "protocol") return saveSettings({ protocol: value });
  if (kind === "bypass") return saveSettings({ bypass: value });
  // Picking a location connects to it right away.
  api("POST", "/api/connect", { country: value }).then(function () { poll(); }).catch(function (err) {
    if (err.code === "plan") showPlanModal(err.feature);
    else if (err.code === "need_admin") {
      return api("POST", "/api/target", { country: value }).catch(function () {}).then(function () {
        $("admin-modal").classList.remove("hidden");
      });
    } else toast(err.message);
  });
});

// ---------- Tabs ----------

function show(v) {
  view = v;
  each(document.querySelectorAll(".view"), function (el) { el.classList.toggle("active", el.id === "view-" + v); });
  each(document.querySelectorAll(".tabs button"), function (b) { b.classList.toggle("active", b.dataset.view === v); });
  if (v === "logs") loadLogs();
}
each(document.querySelectorAll(".tabs button"), function (b) { b.addEventListener("click", function () { show(b.dataset.view); }); });

// ---------- Settings ----------

var settingsBusy = false;
var toggles = ["secure_only", "block_ads", "auto_connect", "start_with_system", "help_others", "share_stats", "allow_lan", "override_gps", "bypass_apps"];

function fillSettings(st) {
  toggles.forEach(function (k) { $("set-" + k).checked = !!st[k]; });
  $("set-tun").checked = st.mode === "tun";
  var proto = first(state.protocols, function (p) { return p.value === st.protocol; });
  $("protocol-value").textContent = proto ? proto.label : "Automatic";
  var region = first(state.regions, function (r) { return r.code === st.bypass; });
  $("bypass-value").textContent = region ? region.name : "Off";
}

function saveSettings(patch) {
  if (!state) return;
  settingsBusy = true;
  // The answer is kept on the state this save started from, even if a poll
  // replaces it meanwhile.
  var saving = state;
  var next = assign({}, state.settings, patch);
  var saved = api("POST", "/api/settings", next).then(function (settings) {
    saving.settings = settings;
    fillSettings(state.settings);
    toast("Saved");
  }).catch(function (err) {
    fillSettings(state.settings);
    if (err.code === "need_admin") $("admin-modal").classList.remove("hidden");
    else if (err.code === "plan") showPlanModal(err.feature);
    else toast(err.message);
  });
  return always(saved, function () {
    settingsBusy = false;
  });
}

toggles.forEach(function (k) {
  $("set-" + k).addEventListener("change", function (e) {
    var patch = {};
    patch[k] = e.target.checked;
    saveSettings(patch);
  });
});
$("set-tun").addEventListener("change", function (e) { saveSettings({ mode: e.target.checked ? "tun" : "proxy" }); });

$("admin-cancel").addEventListener("click", function () { $("admin-modal").classList.add("hidden"); });
$("admin-ok").addEventListener("click", function () {
  $("admin-modal").classList.add("hidden");
  api("POST", "/api/settings", assign({}, state.settings, { mode: "tun" })).catch(function () {}).then(function () {
    return api("POST", "/api/admin");
  }).then(function () {
    toast("Restarting with administrator rights…");
  }).catch(function (err) { toast(err.message); });
});
$("quit-btn").addEventListener("click", function () { api("POST", "/api/quit").catch(function () {}); });

// ---------- GPS override ----------

// Android lets one app set the phone's location, chosen in Developer
// options. location_setup names the step still to do; the guide follows it
// and closes once the phone is ready.
var gpsWanted = false;
function gpsSetup(s) { return (s.settings.override_gps && s.location_setup) || ""; }

function gpsSteps(setup) {
  $("gps-step-developer").classList.toggle("done", setup !== "developer");
  $("gps-step-mock_app").classList.toggle("done", !setup);
}

function openGpsGuide() {
  gpsSteps(gpsSetup(state));
  $("gps-modal").classList.remove("hidden");
}

function renderGps(s) {
  var setup = gpsSetup(s);
  $("gps-setup").classList.toggle("hidden", !setup);
  if (!$("gps-modal").classList.contains("hidden")) {
    if (setup) gpsSteps(setup);
    else {
      $("gps-modal").classList.add("hidden");
      if (s.settings.override_gps) toast("GPS override is ready");
    }
  }
  // Just turned on: the guide opens if the phone needs the setup.
  if (gpsWanted && s.settings.override_gps) {
    gpsWanted = false;
    if (setup) openGpsGuide();
  }
}

$("set-override_gps").addEventListener("change", function (e) { gpsWanted = e.target.checked; });
$("gps-setup").addEventListener("click", openGpsGuide);
$("gps-close").addEventListener("click", function () { $("gps-modal").classList.add("hidden"); });
each($("gps-modal").querySelectorAll("[data-step]"), function (b) {
  b.addEventListener("click", function () {
    api("POST", "/api/location-settings", { step: b.dataset.step }).catch(function (e) { toast(e.message); });
  });
});

// The app downloads and opens the new version itself where it can; else the
// download page opens.
function getUpdate() {
  if (!state || !state.update) return;
  api("POST", "/api/update").then(function () { poll(); }).catch(function (e) {
    if (e.code === "no_in_app") api("POST", "/api/open", { url: state.update.url }).catch(function (e2) { toast(e2.message); });
    else toast(e.message);
  });
}
$("update-install").addEventListener("click", getUpdate);
$("update-get").addEventListener("click", getUpdate);

// ---------- Diagnostics ----------

// Seven quick taps on the version number open the activity log, which
// support can ask for.
var taps = [];
$("version").addEventListener("click", function () {
  var now = Date.now();
  taps = taps.filter(function (t) { return now - t < 4000; }).concat(now);
  if (taps.length >= 7) { taps = []; show("logs"); }
});

function loadLogs() {
  return api("GET", "/api/logs").catch(function () { return []; }).then(function (logs) {
    $("logs").innerHTML = logs.slice().reverse().map(function (l) {
      var t = new Date(l.time).toLocaleTimeString("en-GB");
      return '<div class="log ' + esc(l.level) + '"><time>' + esc(t) + "</time><span>" + esc(l.text) + "</span></div>";
    }).join("") || '<div class="empty">Nothing yet</div>';
  });
}

// The Android back button: close what is open, then go home; false lets the
// app go to the background.
window.albatrossBack = function () {
  if (!$("sheet").classList.contains("hidden")) { closeSheet(); return true; }
  if (!$("admin-modal").classList.contains("hidden")) { $("admin-modal").classList.add("hidden"); return true; }
  if (!$("gps-modal").classList.contains("hidden")) { $("gps-modal").classList.add("hidden"); return true; }
  if (!$("plan-modal").classList.contains("hidden")) { $("plan-modal").classList.add("hidden"); return true; }
  if (view === "logs") { show("settings"); return true; }
  if (view !== "home") { show("home"); return true; }
  return false;
};

// ---------- Loop ----------

// Once a second while visible; a hidden window (minimized, in the tray, or
// an app in the background) checks in rarely, which lets the engine skip
// work nobody would see.
function loop() {
  return poll().then(function () {
    setTimeout(loop, document.hidden ? 5000 : 1000);
  });
}
loop();
document.addEventListener("visibilitychange", function () { if (!document.hidden) poll(); });
setInterval(function () { if (!document.hidden && view === "logs") loadLogs(); }, 4000);

// Everything above ran, so this WebView can run the app; old-webview.js
// shows its message only when this line is never reached.
window.albatrossStarted = true;
