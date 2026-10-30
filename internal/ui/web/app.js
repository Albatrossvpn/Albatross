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

// A part of the page that fails to draw must not stop the others, least of
// all the update notice: each runs on its own.
function safely(fn) {
  try { fn(); } catch (e) { if (window.console && console.error) console.error(e); }
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
var PING_WORD = { good: "Good", mid: "Fair", slow: "Slow", plain: "" };

// Signal bars by ping: four for a fast server, one for a slow one.
function bars(ms) {
  var lit = !ms ? 0 : ms < 150 ? 4 : ms < 350 ? 3 : ms < 900 ? 2 : 1;
  var html = '<span class="bars ' + pingClass(ms) + '" aria-hidden="true">';
  for (var i = 0; i < 4; i++) html += "<i" + (i < lit ? ' class="lit"' : "") + "></i>";
  return html + "</span>";
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
var kickers = { idle: "Not protected", searching: "Securing", connecting: "Securing", reconnecting: "Securing" };

var state = null;
var view = "home";
var wasOn = false;

function isBusy(s) { return s.status === "searching" || s.status === "connecting" || s.status === "reconnecting"; }

// Where the user's own connection starts, for the route on the map: the
// country whose sites they open directly, else Iran, where Albatross is
// mostly used.
function homeCode() {
  var b = state && state.settings && state.settings.bypass;
  return b && PLACES[b] ? b : "IR";
}

// ---------- Connect ----------

function render(s) {
  state = s;
  var body = document.body;
  var busy = isBusy(s), on = s.status === "connected" && !!s.conn;
  body.classList.toggle("mobile", !!s.mobile);
  body.classList.toggle("busy", busy);
  body.classList.toggle("on", on);
  if (on && !wasOn) {
    connectedAt = Date.now();
    rememberLast(s);
  }
  if (!on) resetChart();
  wasOn = on;

  var p = $("power");
  p.setAttribute("aria-label", s.status === "idle" ? "Connect" : "Disconnect");
  $("mascot").setAttribute("class", "mascot " + (on ? "glide" : busy ? "fly" : "sleep"));

  $("status").classList.toggle("hidden", on);
  $("status-kicker").lastChild.textContent = kickers[s.status] || "";
  $("status-title").textContent = statusTitles[s.status] || "";
  var sub = s.message || "";
  if (s.status === "idle" && !sub) sub = s.servers > 0 ? "Tap the button to connect" : "Getting ready…";
  $("status-sub").textContent = sub;

  $("smart").classList.toggle("hidden", busy || on);
  safely(function () { renderSteps(s, busy); });
  safely(function () { renderSmart(s); });
  safely(function () { renderConnected(s, on); });

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

  safely(function () { renderUpdate(s.update); });
  safely(function () { renderAccount(s.account); });
  safely(renderLocks);
  safely(function () { renderPrivacy(s); });
  $("admin-chip").classList.toggle("hidden", !s.admin);
  if (!settingsBusy) fillSettings(s.settings);
  safely(function () { renderGps(s); });
  $("version").textContent = s.version ? "v" + s.version : "";
  safely(function () {
    if (view === "locations") renderLocations();
    if (view === "plans") renderPlans();
    if (view === "speed") renderSpeedWhere();
    if (sheetKind === "protocol") renderSheet();
  });
}

function renderSteps(s, busy) {
  $("steps").classList.toggle("hidden", !busy);
  $("cancel-btn").classList.toggle("hidden", !busy);
  if (!busy) return;
  var found = s.status !== "searching";
  $("step-find").className = found ? "done" : "active";
  $("step-connect").className = found ? "active" : "";
  $("step-check").className = "";
  $("step-connect-text").textContent = s.status === "reconnecting" ? "Moving to a working server" : "Connecting";
}

// ---------- The smart card ----------

// The last place connected to, kept on this device: the card offers it back.
function rememberLast(s) {
  var name = s.target ? s.target_name : "Fastest location";
  try { localStorage.setItem("last", JSON.stringify({ code: s.target || "", name: name, flag: s.target_flag || "" })); } catch (e) {}
}
function lastUsed() {
  try { return JSON.parse(localStorage.getItem("last") || "null"); } catch (e) { return null; }
}

var altChoice = null;
function renderSmart(s) {
  var locations = s.countries.length;
  var target = first(s.countries, function (x) { return x.code === s.target; });
  var flag = $("cc-flag");
  flag.classList.toggle("fast", !s.target);
  if (s.target) flag.textContent = s.target_flag;
  else flag.innerHTML = BOLT;
  $("cc-label").textContent = s.target ? "Your location" : "Chosen for you";
  $("cc-name").textContent = s.target ? s.target_name : "Fastest location";
  if (!s.target) {
    $("cc-meta").textContent = s.servers ? num(s.servers) + " servers in " + num(locations) + " locations" : "Looking for servers…";
  } else if (target) {
    $("cc-meta").innerHTML = bars(target.best_delay) + esc(PING_WORD[pingClass(target.best_delay)] + " · " + num(target.servers) + " servers · " + num(target.best_delay) + " ms");
  } else {
    $("cc-meta").textContent = "No servers available yet";
  }
  // Beneath it, one suggestion: where the user was last, else the best
  // country right now.
  altChoice = null;
  var last = lastUsed();
  if (last && last.code !== (s.target || "") && (!last.code || first(s.countries, function (x) { return x.code === last.code; }))) {
    altChoice = { code: last.code, text: "Last used: " + (last.flag ? last.flag + " " : "") + last.name };
  } else if (hasFeature("locations") && s.countries.length && s.countries[0].code !== s.target) {
    var b = s.countries[0];
    altChoice = { code: b.code, text: "Best right now: " + b.flag + " " + b.name + " · " + num(b.best_delay) + " ms" };
  }
  $("smart-alt").classList.toggle("hidden", !altChoice);
  if (altChoice) $("smart-alt-text").textContent = altChoice.text;

  var a = s.account || {};
  var showData = a.signed_in && a.quota_bytes;
  if (showData) meter("home-data", a.used_bytes, a.quota_bytes, fmtBytes);
  $("home-data").classList.toggle("hidden", !showData || isBusy(s) || s.status === "connected");
}

$("smart-alt").addEventListener("click", function () {
  if (!altChoice) return;
  if (altChoice.code && !hasFeature("locations")) return showPlanModal("locations");
  api("POST", "/api/target", { country: altChoice.code }).then(function () { poll(); }).catch(function (e) {
    if (e.code === "plan") showPlanModal(e.feature);
    else toast(e.message);
  });
});

// ---------- Connected: the dashboard and the live chart ----------

var connectedAt = 0;
function renderConnected(s, on) {
  each(["on-row", "dash", "chart-card", "mini-label"], function (id) { $(id).classList.toggle("hidden", !on); });
  if (!on) return;
  var c = s.conn;
  $("on-country").textContent = c.flag + " " + c.country_name;
  $("mini-label").textContent = c.country_name;
  $("d-proto").textContent = c.protocol || "—";
  $("d-time").textContent = c.since ? clock(Math.max(0, Date.now() / 1000 - c.since)) : "—";
  $("st-ping").innerHTML = c.delay ? esc(num(c.delay) + " ms") + "<em>" + esc(PING_WORD[pingClass(c.delay)]) + "</em>" : "—";
  $("d-data").textContent = fmtBytes((c.up || 0) + (c.down || 0));
  $("rate-down").textContent = fmtRate(c.down_rate);
  $("rate-up").textContent = fmtRate(c.up_rate);
  pushChart(c.down_rate || 0, c.up_rate || 0);
}

function clock(sec) {
  sec = Math.floor(sec);
  var h = Math.floor(sec / 3600), m = Math.floor((sec % 3600) / 60), x = sec % 60;
  return (h < 10 ? "0" : "") + h + ":" + (m < 10 ? "0" : "") + m + ":" + (x < 10 ? "0" : "") + x;
}
// Bytes a second as megabits, the way line speeds are told.
function fmtRate(bps) {
  var mb = (bps || 0) * 8 / 1e6;
  if (mb < 0.1) return Math.round((bps || 0) * 8 / 1e3) + " kb/s";
  return (mb < 10 ? mb.toFixed(1) : Math.round(mb)) + " Mb/s";
}

var chartDown = [], chartUp = [];
var CHART_N = 60;
function resetChart() { chartDown = []; chartUp = []; }
function pushChart(d, u) {
  chartDown.push(d); chartUp.push(u);
  if (chartDown.length > CHART_N) { chartDown.shift(); chartUp.shift(); }
  drawChart();
}

// A canvas sized for the screen's pixels; returns its context and size in
// CSS pixels.
function fitCanvas(cv) {
  var dpr = window.devicePixelRatio || 1, w = cv.clientWidth, h = cv.clientHeight;
  if (!w || !h) return null;
  if (cv.width !== Math.round(w * dpr) || cv.height !== Math.round(h * dpr)) {
    cv.width = Math.round(w * dpr);
    cv.height = Math.round(h * dpr);
  }
  var ctx = cv.getContext("2d");
  ctx.setTransform(dpr, 0, 0, dpr, 0, 0);
  ctx.clearRect(0, 0, w, h);
  return { ctx: ctx, w: w, h: h };
}

function drawChart() {
  var f = fitCanvas($("chart"));
  if (!f) return;
  var ctx = f.ctx, w = f.w, h = f.h, n = chartDown.length;
  ctx.strokeStyle = "#f0ede6";
  ctx.lineWidth = 1;
  for (var g = 1; g <= 3; g++) { ctx.beginPath(); ctx.moveTo(0, h * g / 3.4); ctx.lineTo(w, h * g / 3.4); ctx.stroke(); }
  if (n < 2) return;
  var top = 50000;
  for (var i = 0; i < n; i++) top = Math.max(top, chartDown[i], chartUp[i]);
  top *= 1.2;
  var x = function (i) { return w - (n - 1 - i) * (w / (CHART_N - 1)); };
  var y = function (v) { return h - 4 - (v / top) * (h - 10); };
  var path = function (list) {
    ctx.beginPath();
    ctx.moveTo(x(0), y(list[0]));
    for (var i = 1; i < n; i++) {
      var mx = (x(i - 1) + x(i)) / 2;
      ctx.bezierCurveTo(mx, y(list[i - 1]), mx, y(list[i]), x(i), y(list[i]));
    }
  };
  path(chartDown);
  ctx.lineTo(x(n - 1), h);
  ctx.lineTo(x(0), h);
  ctx.closePath();
  var grad = ctx.createLinearGradient(0, 0, 0, h);
  grad.addColorStop(0, "rgba(47, 95, 224, 0.24)");
  grad.addColorStop(1, "rgba(47, 95, 224, 0)");
  ctx.fillStyle = grad;
  ctx.fill();
  ctx.lineWidth = 2.4;
  ctx.lineCap = "round";
  path(chartDown);
  ctx.strokeStyle = "#2f5fe0";
  ctx.stroke();
  path(chartUp);
  ctx.strokeStyle = "#c49a48";
  ctx.stroke();
}

$("test-speed").addEventListener("click", function () { show("speed"); startSpeed(); });

// ---------- The globe and the maps ----------

// The land as dots, from map.js: each with its longitude and latitude.
var DOTS = (function () {
  var out = [];
  for (var r = 0; r < WORLD.rows.length; r++) {
    var hex = WORLD.rows[r], lat = WORLD.top - WORLD.step * r - WORLD.step / 2;
    for (var d = 0; d < hex.length; d++) {
      var v = parseInt(hex.charAt(d), 16);
      for (var b = 0; b < 4; b++) {
        if (v & (8 >> b)) {
          var lon = -180 + WORLD.step * (d * 4 + b) + WORLD.step / 2;
          out.push({ lon: lon, lat: lat, cl: Math.cos(lat * Math.PI / 180), sl: Math.sin(lat * Math.PI / 180), lr: lon * Math.PI / 180 });
        }
      }
    }
  }
  return out;
})();
// An angle in degrees as the equivalent from -180 up to 180.
function wrap180(d) { return (((d % 360) + 540) % 360) - 180; }
function place(code) { return code && PLACES[code] ? { lon: PLACES[code][0], lat: PLACES[code][1] } : null; }

// The globe spins slowly while off; while connecting it turns to the
// country and draws closer; once connected it gives way to the map of the
// route.
var globe = { lon: 20, lat: 18, zoom: 1 };

// dt is the seconds since the last frame; every motion is per second, so
// it looks the same at any frame rate.
function animStep(dt) {
  if (!state || document.hidden || view !== "home") return;
  var on = document.body.classList.contains("on");
  if (on) return drawMini();
  var busy = document.body.classList.contains("busy");
  var aim = busy ? place(state.target) : null;
  var ease = function (rate) { return 1 - Math.exp(-rate * dt); };
  if (aim) {
    // Turn toward the country, then lean in.
    var dl = wrap180(aim.lon - globe.lon);
    globe.lon += dl * ease(2.6);
    globe.lat += (Math.max(-40, Math.min(50, aim.lat)) * 0.8 - globe.lat) * ease(2.6);
    globe.zoom += ((Math.abs(dl) < 8 ? 1.45 : 1) - globe.zoom) * ease(2.4);
  } else {
    globe.lon += (busy ? 28 : 2) * dt;
    globe.lat += (18 - globe.lat) * ease(1.2);
    globe.zoom += (1 - globe.zoom) * ease(3);
  }
  globe.lon = wrap180(globe.lon);
  drawGlobe(aim, busy);
}

function drawGlobe(aim, busy) {
  var f = fitCanvas($("globe"));
  if (!f) return;
  var ctx = f.ctx, cx = f.w / 2, cy = f.h / 2, R = Math.min(f.w, f.h) * 0.47 * globe.zoom;
  var l0 = globe.lon * Math.PI / 180, p0 = globe.lat * Math.PI / 180, cp = Math.cos(p0), sp = Math.sin(p0);
  // The sphere's edge and a soft light on it.
  var glow = ctx.createRadialGradient(cx - R * 0.3, cy - R * 0.35, R * 0.1, cx, cy, R);
  glow.addColorStop(0, busy ? "rgba(255, 255, 255, 0.9)" : "rgba(255, 255, 255, 0.7)");
  glow.addColorStop(1, busy ? "rgba(214, 226, 250, 0.9)" : "rgba(232, 228, 218, 0.55)");
  ctx.beginPath();
  ctx.arc(cx, cy, R, 0, Math.PI * 2);
  ctx.fillStyle = glow;
  ctx.fill();
  ctx.strokeStyle = busy ? "rgba(47, 95, 224, 0.22)" : "rgba(196, 154, 72, 0.28)";
  ctx.lineWidth = 1;
  ctx.stroke();
  var near = [], far = [];
  var size = Math.max(1.2, R / 110);
  for (var i = 0; i < DOTS.length; i++) {
    var d = DOTS[i], dl = d.lr - l0, cd = Math.cos(dl);
    var z = sp * d.sl + cp * d.cl * cd;
    if (z <= 0) continue;
    var x = cx + R * d.cl * Math.sin(dl), y = cy - R * (cp * d.sl - sp * d.cl * cd);
    (z > 0.45 ? near : far).push(x, y);
  }
  ctx.fillStyle = busy ? "rgba(47, 95, 224, 0.75)" : "rgba(15, 27, 61, 0.38)";
  for (var n = 0; n < near.length; n += 2) ctx.fillRect(near[n] - size / 2, near[n + 1] - size / 2, size, size);
  ctx.fillStyle = busy ? "rgba(47, 95, 224, 0.35)" : "rgba(15, 27, 61, 0.16)";
  for (var m = 0; m < far.length; m += 2) ctx.fillRect(far[m] - size / 2, far[m + 1] - size / 2, size, size);
  if (aim) {
    var al = aim.lon * Math.PI / 180 - l0, ap = aim.lat * Math.PI / 180;
    var az = sp * Math.sin(ap) + cp * Math.cos(ap) * Math.cos(al);
    if (az > 0.2) {
      var px = cx + R * Math.cos(ap) * Math.sin(al), py = cy - R * (cp * Math.sin(ap) - sp * Math.cos(ap) * Math.cos(al));
      var t = (Date.now() % 1600) / 1600;
      ctx.beginPath();
      ctx.arc(px, py, 6 + 18 * t, 0, Math.PI * 2);
      ctx.strokeStyle = "rgba(196, 154, 72, " + (0.7 * (1 - t)) + ")";
      ctx.lineWidth = 2;
      ctx.stroke();
      ctx.beginPath();
      ctx.arc(px, py, 6, 0, Math.PI * 2);
      ctx.fillStyle = "#0f1b3d";
      ctx.fill();
      ctx.lineWidth = 2.5;
      ctx.strokeStyle = "#d6ab55";
      ctx.stroke();
    }
  }
}

// A flat map: x from longitude, y from latitude, scale pixels a degree.
function flatDots(ctx, w, h, v, color, most) {
  var r = Math.max(0.6, Math.min(most || 2, v.scale * WORLD.step * 0.2));
  ctx.fillStyle = color;
  ctx.beginPath();
  for (var i = 0; i < DOTS.length; i++) {
    var d = DOTS[i];
    var x = (d.lon - v.lon) * v.scale + w / 2, y = (v.lat - d.lat) * v.scale + h / 2;
    if (x < -6 || y < -6 || x > w + 6 || y > h + 6) continue;
    ctx.moveTo(x + r, y);
    ctx.arc(x, y, r, 0, Math.PI * 2);
  }
  ctx.fill();
}
function toXY(v, w, h, p) { return { x: (p.lon - v.lon) * v.scale + w / 2, y: (v.lat - p.lat) * v.scale + h / 2 }; }

// The route, drawn from home to the server once connected: a glowing gold
// line, then data running along it.
function drawMini() {
  var f = fitCanvas($("globe"));
  if (!f || !state.conn) return;
  var ctx = f.ctx, w = f.w, h = f.h;
  var from = place(homeCode()), to = place(state.conn.country) || from;
  if (!from) from = to;
  var span = Math.max(36, Math.abs(to.lon - from.lon) * 1.7, Math.abs(to.lat - from.lat) * 2.6);
  var v = { lon: (from.lon + to.lon) / 2, lat: (from.lat + to.lat) / 2 + span * 0.05, scale: w / span };
  flatDots(ctx, w, h, v, "rgba(130, 156, 222, 0.6)", 1.8);
  var a = toXY(v, w, h, from), b = toXY(v, w, h, to);
  var c = { x: (a.x + b.x) / 2, y: Math.min(a.y, b.y) - Math.max(30, Math.abs(a.x - b.x) * 0.35) };
  var t = Math.min(1, (Date.now() - connectedAt) / 1600);
  t = 1 - Math.pow(1 - t, 3);
  var at = function (k) {
    return { x: (1 - k) * (1 - k) * a.x + 2 * (1 - k) * k * c.x + k * k * b.x, y: (1 - k) * (1 - k) * a.y + 2 * (1 - k) * k * c.y + k * k * b.y };
  };
  ctx.lineCap = "round";
  if (a.x !== b.x || a.y !== b.y) {
    // The glow, then the line, as far as it has been drawn.
    for (var pass = 0; pass < 2; pass++) {
      ctx.beginPath();
      ctx.moveTo(a.x, a.y);
      for (var k = 0.02; k <= t + 0.001; k += 0.02) { var q = at(k); ctx.lineTo(q.x, q.y); }
      ctx.strokeStyle = pass ? "#f1d9a0" : "rgba(232, 196, 110, 0.28)";
      ctx.lineWidth = pass ? 2 : 8;
      ctx.stroke();
    }
    if (t >= 1) {
      var run = ((Date.now() - connectedAt) % 2000) / 2000, p = at(run);
      ctx.beginPath();
      ctx.arc(p.x, p.y, 3.5, 0, Math.PI * 2);
      ctx.fillStyle = "#fff";
      ctx.fill();
    }
    ctx.beginPath();
    ctx.arc(a.x, a.y, 4, 0, Math.PI * 2);
    ctx.fillStyle = "rgba(255, 255, 255, 0.85)";
    ctx.fill();
    ctx.font = "700 11px system-ui, sans-serif";
    ctx.fillStyle = "#aeb9d6";
    ctx.fillText("You", a.x + 8, a.y + 14);
  }
  var pulse = (Date.now() % 1800) / 1800;
  ctx.beginPath();
  ctx.arc(b.x, b.y, 5 + 14 * pulse, 0, Math.PI * 2);
  ctx.strokeStyle = "rgba(214, 171, 85, " + (0.7 * (1 - pulse)) + ")";
  ctx.lineWidth = 2;
  ctx.stroke();
  ctx.beginPath();
  ctx.arc(b.x, b.y, 5, 0, Math.PI * 2);
  ctx.fillStyle = "#d6ab55";
  ctx.fill();
  ctx.strokeStyle = "#0f1b3d";
  ctx.lineWidth = 2;
  ctx.stroke();
}

// One frame loop, only while it shows: smooth while something moves fast,
// a few frames a second while the globe only turns slowly.
var lastFrame = 0;
var raf = window.requestAnimationFrame || function (fn) { return setTimeout(function () { fn(Date.now()); }, 33); };
function frame(now) {
  raf(frame);
  var every = document.body.classList.contains("busy") ? 30 : document.body.classList.contains("on") ? 45 : 100;
  if (now - lastFrame < every) return;
  var dt = Math.min(0.25, (now - lastFrame) / 1000);
  lastFrame = now;
  safely(function () { animStep(dt); });
}
raf(frame);

$("mascot").addEventListener("click", function () {
  var m = $("mascot"), cls = m.getAttribute("class");
  m.setAttribute("class", cls + " hop");
  setTimeout(function () { m.setAttribute("class", cls); }, 520);
});

// ---------- Locations: list and map ----------

// Countries for each use: the big streaming libraries, places that let
// file sharing be, and for games whatever answers fastest from here.
var CATS = {
  stream: { codes: ["US", "GB", "CA", "DE", "FR", "NL", "JP", "AU", "IT", "ES", "SE", "CH", "KR", "IN", "BR"], hint: "Countries with the largest streaming libraries." },
  p2p: { codes: ["NL", "CH", "SE", "RO", "ES", "CA", "DE", "FI", "NO", "IS", "LU", "MD", "BG", "CZ", "PL"], hint: "Countries usually relaxed about file sharing. Mind your local law." },
  game: { ms: 350, hint: "The lowest ping from where you are, for games and calls." }
};
var cat = "";
var STREAM_ICON = '<svg viewBox="0 0 24 24" aria-label="Streaming"><rect x="3" y="5" width="18" height="14" rx="3"/><path d="m10 9 5 3-5 3z"/></svg>';
var GAME_ICON = '<svg viewBox="0 0 24 24" aria-label="Gaming"><path d="M7 8h10a4 4 0 0 1 4 4v1a3 3 0 0 1-5.4 1.8L14.5 14h-5l-1.1.8A3 3 0 0 1 3 13v-1a4 4 0 0 1 4-4z"/></svg>';
var P2P_ICON = '<svg class="p2p" viewBox="0 0 24 24" aria-label="Torrent"><path d="M8 4v12M4 12l4 4 4-4M16 20V8M12 12l4-4 4 4"/></svg>';
function inCat(c, k) {
  if (k === "game") return c.best_delay && c.best_delay < CATS.game.ms;
  return CATS[k].codes.indexOf(c.code) >= 0;
}

var locMode = "list";
var locKey = "";
function renderLocations() {
  if (!state) return;
  var q = $("country-search").value.trim().toLowerCase();
  var lock = !hasFeature("locations");
  // The page asks every second: a list rebuilt for nothing would swallow a
  // tap that lands while it is.
  var key = [locMode, q, cat, lock, planFor("locations"), state.target || "", state.servers, JSON.stringify(state.countries)].join("|");
  if (key === locKey) return;
  locKey = key;
  if (!mapTouched) mapFitted = false;
  $("cat-hint").textContent = cat ? CATS[cat].hint : "";
  $("cat-hint").classList.toggle("hidden", !cat);
  $("loc-note").innerHTML = lock && planFor("locations") ? '<div class="lock-note" id="loc-lock">' + medal(planFor("locations"), true) +
    "<span>Choosing a location comes with the " + PLAN_NAMES[planFor("locations")] + " plan. Tap to see it.</span></div>" : "";
  var list = state.countries.filter(function (c) {
    return (!q || c.name.toLowerCase().indexOf(q) >= 0 || c.code.toLowerCase() === q) && (!cat || inCat(c, cat));
  });
  if (locMode === "map") return drawWorld();
  var html = "";
  if (!cat && (!q || "fastest".indexOf(q) >= 0)) {
    html += countryItem({ code: "", name: "Fastest location", meta: num(state.servers) + " servers in " + num(state.countries.length) + " locations" });
  }
  list.forEach(function (c) { html += countryItem(c, lock); });
  $("loc-list").innerHTML = html || '<div class="empty">No locations found</div>';
}

function countryItem(c, locked) {
  var sel = ((state.target || "") === c.code ? " selected" : "") + (locked ? " dim" : "");
  var icons = c.code ? (inCat(c, "stream") ? STREAM_ICON : "") + (inCat(c, "game") ? GAME_ICON : "") + (inCat(c, "p2p") ? P2P_ICON : "") : "";
  var right = c.code ? '<span class="q"><span>' + bars(c.best_delay) + num(c.best_delay) + " ms</span></span>" :
    ((state.target || "") === "" ? '<svg class="check" viewBox="0 0 24 24"><path d="M5 13l4 4L19 7"/></svg>' : "");
  return '<button class="item' + sel + '" data-value="' + esc(c.code) + '">' +
    '<span class="flag big' + (c.code ? "" : " fast") + '">' + (c.code ? esc(c.flag) : BOLT) + "</span>" +
    '<span class="main"><span class="title">' + esc(c.name) + '</span><span class="sub">' +
    esc(c.code ? num(c.servers) + (c.servers === 1 ? " server" : " servers") : c.meta) + icons + "</span></span>" + right + "</button>";
}

// Picking a location connects to it right away, and shows it connecting.
function connectTo(code) {
  if (code && !hasFeature("locations")) return showPlanModal("locations");
  show("home");
  api("POST", "/api/connect", { country: code }).then(function () { poll(); }).catch(function (err) {
    if (err.code === "plan") showPlanModal(err.feature);
    else if (err.code === "need_admin") {
      return api("POST", "/api/target", { country: code }).catch(function () {}).then(function () {
        $("admin-modal").classList.remove("hidden");
      });
    } else toast(err.message);
  });
}

$("loc-list").addEventListener("click", function (e) {
  var item = e.target.closest(".item");
  if (item) connectTo(item.dataset.value);
});
$("loc-note").addEventListener("click", function () { showPlanModal("locations"); });
$("country-search").addEventListener("input", renderLocations);
each(document.querySelectorAll("#chips button"), function (b) {
  b.addEventListener("click", function () {
    cat = b.dataset.cat;
    each(document.querySelectorAll("#chips button"), function (x) { x.classList.toggle("on", x === b); });
    renderLocations();
  });
});
function setLocMode(m) {
  locMode = m;
  $("loc-list-btn").classList.toggle("on", m === "list");
  $("loc-map-btn").classList.toggle("on", m === "map");
  $("loc-list").classList.toggle("hidden", m !== "list");
  $("loc-map").classList.toggle("hidden", m !== "map");
  renderLocations();
}
$("loc-list-btn").addEventListener("click", function () { setLocMode("list"); });
$("loc-map-btn").addEventListener("click", function () { setLocMode("map"); });

// The world map: drag to move, the buttons, the wheel or two fingers to
// zoom; each country with servers is a pin, haloed by how fast it answers.
var mapView = { lon: 35, lat: 38, scale: 0 };
var picked = null;
var mapFitted = false;
var mapTouched = false; // the user moved or zoomed it: it stays where they put it
// The first time the map shows, it frames the user and the places with
// servers (at least a stretch the size of Europe).
function fitMap() {
  var cv = $("worldmap"), w = cv.clientWidth, h = cv.clientHeight;
  if (!w || !h) return;
  mapFitted = true;
  var pts = state.countries.filter(function (c) { return PLACES[c.code]; }).map(function (c) { return place(c.code); });
  if (place(homeCode())) pts.push(place(homeCode()));
  if (!pts.length) return;
  var lo = { lon: 999, lat: 999 }, hi = { lon: -999, lat: -999 };
  pts.forEach(function (p) {
    lo.lon = Math.min(lo.lon, p.lon); hi.lon = Math.max(hi.lon, p.lon);
    lo.lat = Math.min(lo.lat, p.lat); hi.lat = Math.max(hi.lat, p.lat);
  });
  var spanLon = Math.max(40, (hi.lon - lo.lon) * 1.35), spanLat = Math.max(24, (hi.lat - lo.lat) * 1.5);
  mapView.scale = Math.min(w / spanLon, h / spanLat);
  mapView.lon = (lo.lon + hi.lon) / 2;
  mapView.lat = (lo.lat + hi.lat) / 2;
}
function mapPins() {
  var q = $("country-search").value.trim().toLowerCase();
  return state.countries.filter(function (c) {
    return PLACES[c.code] && (!q || c.name.toLowerCase().indexOf(q) >= 0) && (!cat || inCat(c, cat));
  });
}
function drawWorld() {
  var cv = $("worldmap"), f = fitCanvas(cv);
  if (!f) return;
  var ctx = f.ctx, w = f.w, h = f.h;
  if (picked) {
    // The pings change every few seconds: the pin keeps up with them.
    showPick(state.countries.filter(function (c) { return c.code === picked.code; })[0] || null);
  }
  if (!mapFitted) fitMap();
  if (!mapView.scale) mapView.scale = w / 70;
  mapView.scale = Math.max(w / 360, Math.min(w / 12, mapView.scale));
  mapView.lat = Math.max(-55, Math.min(75, mapView.lat));
  mapView.lon = Math.max(-180, Math.min(180, mapView.lon));
  flatDots(ctx, w, h, mapView, "#9db0d8", 3);
  var heat = $("heat-toggle").checked;
  var pins = mapPins();
  var colors = { good: "47, 95, 224", mid: "143, 169, 242", slow: "227, 154, 43", plain: "152, 161, 183" };
  if (heat) {
    pins.forEach(function (c) {
      var p = toXY(mapView, w, h, place(c.code)), r = Math.max(26, mapView.scale * 9);
      var g = ctx.createRadialGradient(p.x, p.y, 0, p.x, p.y, r);
      g.addColorStop(0, "rgba(" + colors[pingClass(c.best_delay)] + ", 0.38)");
      g.addColorStop(1, "rgba(" + colors[pingClass(c.best_delay)] + ", 0)");
      ctx.fillStyle = g;
      ctx.fillRect(p.x - r, p.y - r, r * 2, r * 2);
    });
  }
  var home = place(homeCode());
  if (home) {
    var hp = toXY(mapView, w, h, home);
    ctx.beginPath();
    ctx.arc(hp.x, hp.y, 5, 0, Math.PI * 2);
    ctx.fillStyle = "#2f5fe0";
    ctx.fill();
    ctx.fillStyle = "#2449b5";
    ctx.font = "700 11px system-ui, sans-serif";
    ctx.fillText("You", hp.x + 8, hp.y + 4);
  }
  pins.forEach(function (c) {
    var p = toXY(mapView, w, h, place(c.code)), sel = picked && picked.code === c.code;
    ctx.beginPath();
    ctx.arc(p.x, p.y, sel ? 9 : 6, 0, Math.PI * 2);
    ctx.fillStyle = sel ? "#0f1b3d" : "#fff";
    ctx.fill();
    ctx.lineWidth = 3;
    ctx.strokeStyle = sel ? "#d6ab55" : "rgb(" + colors[pingClass(c.best_delay)] + ")";
    ctx.stroke();
  });
  if (picked) {
    var pp = toXY(mapView, w, h, place(picked.code)), label = picked.name + "  " + num(picked.best_delay) + " ms";
    ctx.font = "700 12px system-ui, sans-serif";
    var tw = ctx.measureText(label).width + 20;
    ctx.fillStyle = "#0f1b3d";
    roundRect(ctx, pp.x + 14, pp.y - 14, tw, 28, 14);
    ctx.fill();
    ctx.fillStyle = "#fff";
    ctx.fillText(label, pp.x + 24, pp.y + 4);
  }
}
function roundRect(ctx, x, y, w, h, r) {
  ctx.beginPath();
  ctx.moveTo(x + r, y);
  ctx.arcTo(x + w, y, x + w, y + h, r);
  ctx.arcTo(x + w, y + h, x, y + h, r);
  ctx.arcTo(x, y + h, x, y, r);
  ctx.arcTo(x, y, x + w, y, r);
  ctx.closePath();
}
// The card under the map shows the pin picked.
function showPick(c) {
  picked = c;
  $("map-card").classList.toggle("hidden", !c);
  if (!c) return;
  $("mc-flag").textContent = c.flag;
  $("mc-name").textContent = c.name;
  $("mc-meta").textContent = num(c.servers) + " servers · " + num(c.best_delay) + " ms · " + PING_WORD[pingClass(c.best_delay)];
}
function pickAt(x, y) {
  var cv = $("worldmap"), w = cv.clientWidth, h = cv.clientHeight, best = null, bestD = 24 * 24;
  mapPins().forEach(function (c) {
    var p = toXY(mapView, w, h, place(c.code)), d = (p.x - x) * (p.x - x) + (p.y - y) * (p.y - y);
    if (d < bestD) { bestD = d; best = c; }
  });
  showPick(best);
  drawWorld();
}
function zoomBy(k, x, y) {
  var cv = $("worldmap"), w = cv.clientWidth, h = cv.clientHeight;
  mapTouched = true;
  if (x === undefined) { x = w / 2; y = h / 2; }
  var lon = mapView.lon + (x - w / 2) / mapView.scale, lat = mapView.lat - (y - h / 2) / mapView.scale;
  mapView.scale = Math.max(w / 360, Math.min(w / 12, mapView.scale * k));
  mapView.lon = lon - (x - w / 2) / mapView.scale;
  mapView.lat = lat + (y - h / 2) / mapView.scale;
  drawWorld();
}
$("zoom-in").addEventListener("click", function () { zoomBy(1.5); });
$("zoom-out").addEventListener("click", function () { zoomBy(1 / 1.5); });
$("heat-toggle").addEventListener("change", drawWorld);
$("map-go").addEventListener("click", function () { if (picked) connectTo(picked.code); });
(function () {
  var cv = $("worldmap"), drag = null, moved = false, pinch = 0;
  var pos = function (e) { var r = cv.getBoundingClientRect(); return { x: e.clientX - r.left, y: e.clientY - r.top }; };
  var start = function (p) { drag = p; moved = false; };
  var move = function (p) {
    if (!drag) return;
    var dx = p.x - drag.x, dy = p.y - drag.y;
    if (Math.abs(dx) + Math.abs(dy) > 3) moved = true;
    mapTouched = true;
    mapView.lon -= dx / mapView.scale;
    mapView.lat += dy / mapView.scale;
    drag = p;
    drawWorld();
  };
  var end = function (p) {
    if (drag && !moved && p) pickAt(p.x, p.y);
    drag = null;
  };
  cv.addEventListener("mousedown", function (e) { start(pos(e)); });
  window.addEventListener("mousemove", function (e) { if (drag) move(pos(e)); });
  window.addEventListener("mouseup", function (e) { if (drag) end(pos(e)); });
  cv.addEventListener("wheel", function (e) { e.preventDefault(); var p = pos(e); zoomBy(e.deltaY < 0 ? 1.2 : 1 / 1.2, p.x, p.y); });
  var dist = function (t) { var dx = t[0].clientX - t[1].clientX, dy = t[0].clientY - t[1].clientY; return Math.sqrt(dx * dx + dy * dy); };
  cv.addEventListener("touchstart", function (e) {
    if (e.touches.length === 2) { pinch = dist(e.touches); drag = null; } else start(pos(e.touches[0]));
  });
  cv.addEventListener("touchmove", function (e) {
    e.preventDefault();
    if (e.touches.length === 2 && pinch) {
      var d = dist(e.touches);
      zoomBy(d / pinch);
      pinch = d;
    } else if (e.touches.length === 1) move(pos(e.touches[0]));
  });
  cv.addEventListener("touchend", function (e) {
    if (e.touches.length < 2) pinch = 0;
    if (!e.touches.length && drag) end(moved ? null : drag);
  });
})();
window.addEventListener("resize", function () {
  if (view === "locations" && locMode === "map") drawWorld();
  drawChart();
});

// ---------- Speed test ----------

var speedTimer = null;
var gaugeLen = 0;
var GAUGE_MARKS = [0, 10, 50, 100, 250, 500, 1000];
function gaugeFrac(mbps) {
  for (var i = 1; i < GAUGE_MARKS.length; i++) {
    if (mbps <= GAUGE_MARKS[i]) return (i - 1 + (mbps - GAUGE_MARKS[i - 1]) / (GAUGE_MARKS[i] - GAUGE_MARKS[i - 1])) / (GAUGE_MARKS.length - 1);
  }
  return 1;
}
function setGauge(mbps, label) {
  if (!gaugeLen) { try { gaugeLen = $("g-fill").getTotalLength(); } catch (e) { gaugeLen = 498; } }
  $("g-fill").style.strokeDasharray = (gaugeLen * gaugeFrac(mbps)).toFixed(1) + " 999";
  $("g-value").textContent = mbps ? (mbps < 10 ? mbps.toFixed(1) : Math.round(mbps)) : "—";
  $("g-label").textContent = label;
}
function mbps(bits) { return (bits || 0) / 1e6; }
function renderSpeedWhere() {
  var c = state && state.conn;
  $("sp-where").textContent = c ? c.flag + " " + c.country_name : "";
}
function renderSpeed(t) {
  var running = t.phase === "ping" || t.phase === "download" || t.phase === "upload";
  $("sp-down").textContent = t.down ? mbps(t.down).toFixed(1) : "—";
  $("sp-up").textContent = t.up ? mbps(t.up).toFixed(1) : "—";
  $("sp-ping").textContent = t.ping ? t.ping + " ms" : "—";
  $("sp-jitter").textContent = t.ping ? t.jitter + " ms" : "—";
  if (t.phase === "upload") setGauge(mbps(t.up), "Mb/s · upload");
  else if (t.phase === "ping") setGauge(0, "Measuring ping");
  else setGauge(mbps(t.down), t.phase && t.phase !== "stopped" ? "Mb/s · download" : "Mb/s");
  $("sp-note").textContent = t.error || (running ? { ping: "Measuring ping…", download: "Measuring download…", upload: "Measuring upload…" }[t.phase] :
    t.phase === "done" ? "Done." : t.phase === "stopped" ? "Stopped." : (state && state.conn ? "" : "Connect first, then test the speed."));
  $("sp-go").textContent = running ? "Stop test" : t.phase ? "Test again" : "Start test";
  $("sp-go").dataset.running = running ? "1" : "";
  return running;
}
function pollSpeed() {
  clearTimeout(speedTimer);
  api("GET", "/api/speedtest").then(function (t) {
    if (renderSpeed(t)) speedTimer = setTimeout(pollSpeed, 250);
  }).catch(function () {});
}
function startSpeed() {
  api("POST", "/api/speedtest").then(function (t) { renderSpeed(t); pollSpeed(); }).catch(function (e) { toast(e.message); });
}
$("sp-go").addEventListener("click", function () {
  if ($("sp-go").dataset.running) api("POST", "/api/speedtest/stop").then(pollSpeed).catch(function () {});
  else startSpeed();
});

// ---------- Update ----------

function renderUpdate(u) {
  // A required update the app downloads itself blocks the page only once
  // it is downloaded; until then the user can still connect, so the
  // download can go through the VPN.
  var blocking = !!u && u.required && (!u.in_app || u.progress === 100);
  $("update-banner").classList.toggle("hidden", !u || blocking);
  $("update-modal").classList.toggle("hidden", !blocking);
  if (!u) return;
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

// ---------- Plans ----------

var PLAN_NAMES = { bronze: "Bronze", silver: "Silver", gold: "Gold", vip: "VIP" };
var PLAN_STAR = '<svg viewBox="0 0 24 24"><path d="M12 2.5l2.9 6 6.6.9-4.8 4.6 1.2 6.5L12 17.4l-5.9 3.1 1.2-6.5L2.5 9.4l6.6-.9z"/></svg>';
var PLAN_CROWN = '<svg viewBox="0 0 24 24"><path d="M3 7.5l4.6 4.2L12 4.5l4.4 7.2L21 7.5l-1.6 11.3H4.6z"/></svg>';
var EMBLEM = '<svg viewBox="0 0 34 34"><circle cx="17" cy="17" r="16"/><path d="M6 19c4-1 7-4 11-4s7 3 11 4c-4 0-7 1-11 4-4-3-7-4-11-4z"/></svg>';

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

/** A plan as a card of its metal. */
function metalCard(tier, small, yours) {
  var devices = ((state.account && state.account.plan_devices) || {})[tier];
  var foot = devices === undefined ? "" : devices === 0 ? "Unlimited devices" : devices === 1 ? "1 device" : devices + " devices";
  return '<div class="metal-card ' + tier + (small ? " small" : "") + '"><span class="sheen"></span><div class="mc-top">' + EMBLEM +
    (yours ? '<span class="mc-yours">YOUR PLAN</span>' : tier === "vip" ? '<span class="mc-yours">THE HIGHEST</span>' : "") + "</div>" +
    '<div class="tier-name">' + esc(PLAN_NAMES[tier].toUpperCase()) + '</div><div class="mc-foot"><span>' + esc(foot) + "</span><span>ALBATROSS</span></div></div>";
}

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
  edge: "Albatross Edge servers",
  own: "Albatross's own servers",
  lan: "Share with other devices",
  priority: "Our private servers first",
  support: "Direct support"
};
// Why a feature is worth having, for the moment a user reaches for it.
var FEATURE_PITCH = {
  locations: ["Pick any country you like", "For sites and apps that only open in one country."],
  adblock: ["No more ads or trackers", "Cleaner, faster pages in every app."],
  bypass_apps: ["Bank apps keep working", "Your country's own apps go around the VPN."],
  protocols: ["Choose exactly how you connect", "Pick the protocol that works best on your network."],
  tun: ["Protect the whole device", "Every app and game goes through Albatross."],
  gps: ["GPS that matches your VPN", "Apps that read GPS see the same country as everything else."],
  edge: ["Albatross Edge", "Routes through Cloudflare that are hard to block."],
  own: ["Our own servers", "The fastest servers, run by us, not shared with public lists."],
  lan: ["Share with every device", "Phones, laptops and TVs on your Wi-Fi use your connection."],
  priority: ["Our private servers come first", "Your connection prefers our own servers, which public lists do not crowd."],
  support: ["A direct line to us", "Talk to our team whenever you need help."]
};
var TICK = '<svg viewBox="0 0 24 24"><path d="M5 13l4 4L19 7"/></svg>';
// Features only phones or only computers have.
var FEATURE_ON = { tun: "desktop", gps: "mobile", bypass_apps: "mobile" };
var PLAN_BASICS = "Fastest location, every protocol";

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
function tierRank(t) { return TIER_ORDER.indexOf(t); }
function nextTier(t) { return TIER_ORDER[tierRank(t) + 1] || ""; }
function tierFeatures(tier) {
  var plans = (state.account && state.account.plans) || {};
  return Object.keys(FEATURE_NAMES).filter(function (f) { return plans[f] === tier && featureHere(f); });
}

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
      title.parentNode.insertBefore(tag, title.nextSibling);
    }
    if (tag && (!lock || !tier)) {
      tag.parentNode.removeChild(tag);
    } else if (tag && tag.dataset.tier !== tier) {
      tag.dataset.tier = tier;
      tag.innerHTML = medal(tier, true);
    }
  });
}

// The upgrade sheet: the plan's card, why the feature is worth it, what
// else the plan brings, and the way to it.
var upTier = "", upNext = "";
function showPlanModal(f) {
  if (!state) return;
  var tier = planFor(f), pitch = FEATURE_PITCH[f] || [FEATURE_NAMES[f] || "Not in your plan", ""];
  upTier = tier;
  $("up-card").innerHTML = tier ? metalCard(tier, true) : "";
  $("up-card").className = "";
  $("plan-modal-title").textContent = pitch[0];
  $("plan-modal-text").textContent = pitch[1] + (tier ? " It comes with " + PLAN_NAMES[tier] + (tier === "vip" ? "." : " and above.") : "");
  var lines = tierFeatures(tier).filter(function (x) { return x !== f; }).slice(0, 3).map(function (x) {
    return "<li><span>" + TICK + "</span><span><b>" + esc(FEATURE_NAMES[x]) + "</b><small>" + esc((FEATURE_PITCH[x] || ["", ""])[1]) + "</small></span></li>";
  });
  var n = ((state.account && state.account.plan_devices) || {})[tier];
  if (n !== undefined) lines.push("<li><span>" + TICK + "</span><span><b>" + (n === 0 ? "Any number of devices" : n + (n === 1 ? " device" : " devices") + " at once") + "</b></span></li>");
  $("up-list").innerHTML = lines.join("");
  $("up-go").textContent = tier ? "Upgrade to " + PLAN_NAMES[tier] : "See the plans";
  upNext = tier ? nextTier(tier) : "";
  $("up-next").classList.toggle("hidden", !upNext);
  if (upNext) {
    var more = tierFeatures(upNext).slice(0, 2).map(function (x) { return FEATURE_NAMES[x]; });
    $("up-next").textContent = "Or go " + PLAN_NAMES[upNext] + (more.length ? ": " + more.join(" · ") : "");
  }
  $("plan-modal").classList.remove("hidden");
}
// Plans come from where the account came from: the way up is asking there,
// so the request is written and copied, ready to send.
function copyText(text) {
  var ta = document.createElement("textarea");
  ta.value = text;
  ta.setAttribute("readonly", "");
  ta.style.cssText = "position:fixed;left:-1000px;top:0";
  document.body.appendChild(ta);
  ta.select();
  var ok = false;
  try { ok = document.execCommand("copy"); } catch (e) {}
  document.body.removeChild(ta);
  if (ok || !navigator.clipboard) return Promise.resolve(ok);
  return navigator.clipboard.writeText(text).then(function () { return true; }, function () { return false; });
}
// The store is the service's Telegram bot, where data is paid with
// Stars: the app shows what is on sale and opens the bot, set to recharge
// the signed-in account (renew_url) or to buy a new one (buy_url).
function storeLink() {
  var a = (state && state.account) || {};
  return a.renew_url || a.buy_url || "";
}
function openStore(note) {
  var url = storeLink();
  if (!url) return false;
  api("POST", "/api/open", { url: url }).then(function () { toast(note || "Opening the store in Telegram…"); }, function (e) { toast(e.message); });
  return true;
}
function devicesText(n) { return n === 0 ? "Unlimited devices" : n === 1 ? "1 device" : n + " devices"; }
var shopShown = "";
function renderShop(a) {
  var s = a.store;
  var key = JSON.stringify(s || null) + (a.renew_url || "");
  if (key === shopShown) return;
  shopShown = key;
  $("shop").classList.toggle("hidden", !s);
  $("plans-hint").textContent = !s ? "Plans are sold where you got your account. Your account, its data and devices carry over." :
    a.signed_in ? "Paid with Telegram Stars in our Telegram bot. The data is added to " + a.username + " at once; its devices carry over." :
      "Paid with Telegram Stars in our Telegram bot, which sends the account's username and password at once.";
  if (!s) return;
  var days = s.days ? s.days + " days" : "No end";
  var rows = (s.packages || []).map(function (p) {
    return '<button type="button" class="shop-item" data-gb="' + p.gb + '"><span class="si-gb">' + p.gb + "<small>GB</small></span>" +
      '<span class="si-mid">' + medal(String(p.plan || "").toLowerCase(), true) + "<small>" + esc(devicesText(p.devices) + " · " + days) + "</small></span>" +
      '<span class="si-price">⭐ ' + p.stars + (p.save ? '<small class="si-save">Save ' + p.save + "%</small>" : "") + "</span></button>";
  });
  var c = s.custom;
  if (c && c.months && c.months.length) {
    var months = c.months.length > 1 ? c.months.slice(0, -1).join(", ") + " or " + c.months[c.months.length - 1] : String(c.months[0]);
    rows.push('<button type="button" class="shop-item own"><span class="si-gb">+</span><span class="si-mid"><b>Your own plan</b><small>' +
      esc(c.min_gb + " to " + c.max_gb + " GB a month, for " + months + " months. More data and more months cost less a GB.") + '</small></span><span class="si-price">›</span></button>');
  }
  $("shop-list").innerHTML = rows.join("");
}
$("shop-list").addEventListener("click", function (e) {
  var item = e.target.closest(".shop-item");
  if (!item) return;
  openStore(item.dataset.gb ? "Choose " + item.dataset.gb + " GB in Telegram and pay with Stars." : "Choose “Build your own plan” in Telegram.");
});
function onRenew() { openStore("Choose a package in Telegram: it is added to your account."); }
$("acc-renew").addEventListener("click", onRenew);
$("acc-buy").addEventListener("click", onRenew);
$("login-buy").addEventListener("click", function () { openStore("Choose a package in Telegram: the bot sends your username and password."); });

function openUpgrade(tier) {
  var name = PLAN_NAMES[tier] || "a higher";
  var user = state && state.account && state.account.username;
  // A plan on sale comes with enough data: the smallest package that brings it.
  var store = state && state.account && state.account.store;
  var pack = store && (store.packages || []).filter(function (p) { return String(p.plan || "").toLowerCase() === tier; })[0];
  if (pack && storeLink()) {
    $("plan-modal").classList.add("hidden");
    openStore("Choose " + pack.gb + " GB or more in Telegram: " + name + " comes with it.");
    return;
  }
  var text = "Hello, please move my Albatross account" + (user ? " \u201c" + user + "\u201d" : "") + " to the " + name + " plan.";
  $("plan-modal").classList.add("hidden");
  copyText(text).then(function (ok) {
    toast(ok ? "Request copied. Send it where you got your account." : "Ask for the " + name + " plan where you got your account.");
  });
}
$("up-go").addEventListener("click", function () { if (upTier) openUpgrade(upTier); else { $("plan-modal").classList.add("hidden"); show("plans"); } });
$("up-next").addEventListener("click", function () { openUpgrade(upNext); });
$("plan-close").addEventListener("click", function () { $("plan-modal").classList.add("hidden"); });
$("plan-see").addEventListener("click", function () { $("plan-modal").classList.add("hidden"); show("plans"); });
$("plan-modal").addEventListener("click", function (e) { if (e.target === $("plan-modal")) $("plan-modal").classList.add("hidden"); });
$("plans-btn").addEventListener("click", function () { show("plans"); });
$("plan-chip").addEventListener("click", function () { show("plans"); });
$("plans-back").addEventListener("click", function () { show("settings"); });
// A locked switch tells which plan has it instead of turning on.
each(document.querySelectorAll("label[data-feature]"), function (el) {
  el.addEventListener("click", function (e) {
    if (!hasFeature(el.dataset.feature)) {
      e.preventDefault();
      showPlanModal(el.dataset.feature);
    }
  });
});

/** The plans as metal cards, and what each has, side by side. */
var plansShown = "";
function renderPlans() {
  var a = state.account || {}, mine = a.signed_in ? a.tier : "";
  renderShop(a);
  var devices = a.plan_devices || {}, plans = a.plans || {};
  var key = mine + JSON.stringify(plans) + JSON.stringify(devices) + state.mobile;
  if (key === plansShown) return;
  plansShown = key;
  $("plans-yours").innerHTML = mine ? "You are on " + medal(mine, true) : "";
  $("tier-cards").innerHTML = TIER_ORDER.map(function (t) { return metalCard(t, false, t === mine); }).join("");
  var col = function (t) { return t === "vip" ? "vipc" : t === mine ? "cur" : ""; };
  var head = "<thead><tr><th scope=\"col\"></th>" + TIER_ORDER.map(function (t) { return '<th scope="col" class="' + col(t) + '">' + medal(t, true) + "</th>"; }).join("") + "</tr></thead>";
  var row = function (name, cells, strong) {
    return '<tr><th scope="row"' + (strong ? ' style="font-weight:700"' : "") + ">" + esc(name) + "</th>" + cells.join("") + "</tr>";
  };
  var rows = [row("Devices at once", TIER_ORDER.map(function (t) {
    var n = devices[t];
    return '<td class="' + col(t) + '" style="font-weight:800">' + (n === undefined ? "–" : n === 0 ? "∞" : n) + "</td>";
  }), true), row(PLAN_BASICS, TIER_ORDER.map(function (t) { return '<td class="yes ' + col(t) + '">✓</td>'; }))];
  Object.keys(FEATURE_NAMES).filter(function (f) { return plans[f] && featureHere(f); }).sort(function (x, y) {
    return tierRank(plans[x]) - tierRank(plans[y]);
  }).forEach(function (f) {
    rows.push(row(FEATURE_NAMES[f], TIER_ORDER.map(function (t) {
      var has = tierRank(t) >= tierRank(plans[f]);
      return '<td class="' + (has ? "yes " : "no ") + col(t) + '">' + (has ? "✓" : "–") + "</td>";
    })));
  });
  $("compare").innerHTML = head + "<tbody>" + rows.join("") + "</tbody>";
  var up = mine ? nextTier(mine) : "";
  $("plans-cta").classList.toggle("hidden", !up);
  $("plans-cta").textContent = up ? "Become " + PLAN_NAMES[up] : "";
  $("plans-cta").dataset.tier = up;
  // The next plan up in view.
  var cards = $("tier-cards").children, at = up ? cards[tierRank(up)] : mine ? cards[tierRank(mine)] : null;
  if (at) $("tier-cards").scrollLeft = at.offsetLeft - 16;
}
$("plans-cta").addEventListener("click", function () { openUpgrade($("plans-cta").dataset.tier); });

function renderAccount(a) {
  var needLogin = a.enabled && !a.signed_in;
  // While signing in, what it is doing: finding a server to go through
  // can take a while, and the user should know it is still at work.
  $("login-progress").classList.toggle("hidden", !a.progress);
  $("login-progress-text").textContent = a.progress || "";
  planBadge($("plan-chip"), a.signed_in ? a.tier : "");
  planBadge($("acc-plan"), a.signed_in ? a.tier : "");
  $("login").classList.toggle("hidden", !needLogin);
  $("login-new").classList.toggle("hidden", !a.buy_url);
  $("account-card").classList.toggle("hidden", !a.signed_in);
  if (!a.signed_in) return;
  $("account-card").className = "member " + (PLAN_NAMES[a.tier] ? a.tier : "");
  $("acc-name").textContent = a.username;
  $("acc-note").textContent = a.message || "";
  $("plans-btn").textContent = a.tier && a.tier !== "vip" ? "Upgrade" : "Plans";
  meter("acc-data", a.used_bytes, a.quota_bytes, fmtBytes);
  meter("acc-time", a.used_seconds, a.max_seconds, fmtTime);
  var dates = [];
  if (a.max_devices) dates.push(a.devices + " of " + a.max_devices + (a.max_devices === 1 ? " device" : " devices"));
  else if (a.tier) dates.push(a.devices + (a.devices === 1 ? " device" : " devices") + " · no limit");
  if (a.resets_at) dates.push("Renews " + fmtDate(a.resets_at));
  if (a.expires_at) dates.push("Expires " + fmtDate(a.expires_at));
  if (!a.quota_bytes && !a.max_seconds && !a.expires_at) dates.push("No limits");
  $("acc-dates").textContent = dates.join(" · ");
  // Recharging: in plain sight when the account is at a limit, nearly out
  // of data or about to end; a link otherwise.
  var soon = a.expires_at && a.expires_at * 1000 - Date.now() < 3 * 86400000;
  var low = a.quota_bytes && a.used_bytes >= 0.8 * a.quota_bytes;
  var urgent = !!a.renew_url && !!(a.message || soon || low);
  $("acc-renew").classList.toggle("hidden", !urgent);
  $("acc-buy").classList.toggle("hidden", !a.renew_url || urgent);
}

// What the connection protects right now, from what the app knows.
function renderPrivacy(s) {
  var on = s.status === "connected" && !!s.conn;
  $("privacy-card").classList.toggle("ok", on);
  $("pc-sub").textContent = on ? "Sites see you in " + s.conn.country_name : "Connect to hide your address";
  $("pc-pill").textContent = on ? "Protected" : "Not protected";
  var items = [["IP hidden", on], ["DNS private", on], ["HTTP blocked", on && s.settings.secure_only]];
  $("pc-list").innerHTML = items.map(function (x) { return '<li class="' + (x[1] ? "yes" : "") + '"><i></i>' + esc(x[0]) + "</li>"; }).join("");
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

function toggleConnection() {
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
}
$("power").addEventListener("click", toggleConnection);
$("cancel-btn").addEventListener("click", toggleConnection);
$("power-off").addEventListener("click", toggleConnection);
$("country-btn").addEventListener("click", function () { show("locations"); });

// ---------- Pickers ----------

var sheetKind = null;

function openSheet(kind) {
  sheetKind = kind;
  sheetKey = "";
  var titles = { protocol: "Protocol", bypass: "Bypass local sites", privacy: "Privacy" };
  $("sheet-title").textContent = titles[kind];
  $("sheet").classList.remove("hidden");
  $("sheet-backdrop").classList.remove("hidden");
  renderSheet();
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

var sheetKey = "";
function renderSheet() {
  if (!state || !sheetKind) return;
  var key = sheetKind + "|" + JSON.stringify(state.protocols) + "|" + state.settings.protocol + "|" + state.settings.bypass;
  if (key === sheetKey) return;
  sheetKey = key;
  var html = "";
  if (sheetKind === "protocol") {
    html = state.protocols.map(function (p) {
      return optionItem(p.value, p.label, p.value === state.settings.protocol, protocolNote(p));
    }).join("");
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

// What the protocol sheet says under a protocol: how many servers it has.
function protocolNote(p) {
  if (p.value === "") return "Picks the best one for you";
  if (p.missing) return "Not included on this device";
  if (p.value === "warp" || p.value === "tor") return "Built in";
  if (!p.servers) return p.failed ? "None worked yet (" + num(p.failed) + " tried)" : "No servers yet";
  return num(p.servers) + (p.servers === 1 ? " server" : " servers") + (p.working ? " · " + num(p.working) + " worked" : "");
}

$("row-protocol").addEventListener("click", function () {
  if (hasFeature("protocols")) openSheet("protocol");
  else showPlanModal("protocols");
});
$("row-bypass").addEventListener("click", function () { openSheet("bypass"); });
$("row-privacy").addEventListener("click", function () { openSheet("privacy"); });
$("row-killswitch").addEventListener("click", function () { api("POST", "/api/vpn-settings").catch(function (e) { toast(e.message); }); });
$("row-support").addEventListener("click", function () {
  if (!hasFeature("support")) return showPlanModal("support");
  var site = state.account && state.account.site_url;
  if (!site) return toast("Write to us where you got your account.");
  api("POST", "/api/open", { url: site.replace(/\/+$/, "") + "/account#support" }).catch(function (e) { toast(e.message); });
});
$("sheet-backdrop").addEventListener("click", closeSheet);

$("sheet-list").addEventListener("click", function (e) {
  var item = e.target.closest(".item");
  if (!item) return;
  var value = item.dataset.value;
  var kind = sheetKind;
  if (kind === "privacy") return;
  closeSheet();
  if (kind === "protocol") return saveSettings({ protocol: value });
  if (kind === "bypass") return saveSettings({ bypass: value });
});

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
  toast("Getting the installer… this takes a minute");
  api("POST", "/api/share/installer").then(function (r) { toast("Saved to " + r.path); }).catch(function (e) { toast(e.message); });
});
document.addEventListener("keydown", function (e) {
  // KeyboardEvent.key needs Chrome 51; keyCode covers older WebViews.
  if (e.key === "Escape" || e.keyCode === 27) {
    closeSheet();
    each(["admin-modal", "share-modal", "gps-modal", "plan-modal"], function (id) { $(id).classList.add("hidden"); });
  }
});

// ---------- Tabs ----------

function show(v) {
  view = v;
  locKey = "";
  document.body.classList.toggle("at-home", v === "home");
  each(document.querySelectorAll(".view"), function (el) { el.classList.toggle("active", el.id === "view-" + v); });
  var tab = v === "plans" ? "settings" : v === "logs" ? "settings" : v;
  each(document.querySelectorAll(".tabs button"), function (b) { b.classList.toggle("active", b.dataset.view === tab); });
  if (v === "logs") loadLogs();
  if (v === "locations") renderLocations();
  if (v === "plans") { plansShown = ""; renderPlans(); }
  if (v === "speed") { renderSpeedWhere(); pollSpeed(); }
  if (v === "home") drawChart();
  $("app").querySelector("main").scrollTop = 0;
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

// The Android back button: close what is open, then go back; false lets the
// app go to the background.
window.albatrossBack = function () {
  if (!$("sheet").classList.contains("hidden")) { closeSheet(); return true; }
  var open = first(["admin-modal", "gps-modal", "plan-modal", "share-modal"], function (id) { return !$(id).classList.contains("hidden"); });
  if (open) { $(open).classList.add("hidden"); return true; }
  if (view === "logs" || view === "plans") { show("settings"); return true; }
  if (view !== "home") { show("home"); return true; }
  return false;
};

// The fine engraving behind the sky, drawn once.
(function () {
  var g = "";
  for (var r = 30; r <= 230; r += 14) g += '<circle cx="230" cy="230" r="' + r + '"/><circle cx="' + (230 + Math.round(r / 6)) + '" cy="230" r="' + r + '"/>';
  try { $("guilloche").innerHTML = g; } catch (e) {}
})();

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
