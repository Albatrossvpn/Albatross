// Helpers shared by the website's pages.

export const $ = (sel, el = document) => el.querySelector(sel);
export const $$ = (sel, el = document) => [...el.querySelectorAll(sel)];
export const esc = (s) => String(s ?? "").replace(/[&<>"']/g, (c) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" }[c]));

export async function api(method, path, { token, body } = {}) {
  let res;
  try {
    res = await fetch("/api/v1/" + path, {
      method,
      // These pages are not an app copy: the service never refuses them for an old version.
      headers: { "Content-Type": "application/json", "X-Albatross-Client": "web", ...(token ? { Authorization: "Bearer " + token } : {}) },
      body: body === undefined ? undefined : JSON.stringify(body),
    });
  } catch {
    throw Object.assign(new Error("Cannot reach the service. Check your connection."), { status: 0 });
  }
  const data = await res.json().catch(() => ({}));
  if (!res.ok) throw Object.assign(new Error(data.error || res.statusText), { status: res.status });
  return data;
}

const PLAN_STAR = '<path d="M12 2.5l2.9 6 6.6.9-4.8 4.6 1.2 6.5L12 17.4l-5.9 3.1 1.2-6.5L2.5 9.4l6.6-.9z"/>';
const PLAN_CROWN = '<path d="M3 7.5l4.6 4.2L12 4.5l4.4 7.2L21 7.5l-1.6 11.3H4.6z"/>';

/** A plan's badge: its medal and name. `cls` adds classes ("big"). */
export function planBadge(tier, name, cls = "") {
  const t = String(tier || "");
  return `<span class="plan-badge ${esc(t)} ${esc(cls)}"><svg viewBox="0 0 24 24" aria-hidden="true">${t === "vip" ? PLAN_CROWN : PLAN_STAR}</svg>${esc(name || t)}</span>`;
}

let infoPromise;
export const publicInfo = () => (infoPromise ||= api("GET", "public").catch(() => ({ name: "Albatross", downloads: {}, signup_open: false })));

const GB = 1024 ** 3;
export const gb = (n) => n / GB;
export function fmtBytes(n) {
  if (n >= GB) return (n / GB).toFixed(n >= 10 * GB ? 0 : 1) + " GB";
  if (n >= 1024 ** 2) return Math.round(n / 1024 ** 2) + " MB";
  return Math.round(n / 1024) + " KB";
}
export function fmtTime(sec) {
  const h = Math.floor(sec / 3600), m = Math.floor((sec % 3600) / 60);
  return h ? (m ? `${h} h ${m} min` : `${h} h`) : `${m} min`;
}
export const fmtDate = (unix) => (unix ? new Date(unix * 1000).toLocaleDateString("en-GB", { day: "numeric", month: "short", year: "numeric" }) : "—");
export function ago(unix) {
  if (!unix) return "never";
  const s = Math.floor(Date.now() / 1000) - unix;
  if (s < 90) return "just now";
  if (s < 5400) return Math.round(s / 60) + " min ago";
  if (s < 129600) return Math.round(s / 3600) + " h ago";
  return Math.round(s / 86400) + " d ago";
}

let toastTimer;
export function toast(text) {
  let t = $("#toast");
  if (!t) {
    t = document.createElement("div");
    t.id = "toast";
    t.className = "toast hidden";
    document.body.append(t);
  }
  t.textContent = text;
  t.classList.remove("hidden");
  clearTimeout(toastTimer);
  toastTimer = setTimeout(() => t.classList.add("hidden"), 3500);
}

/** Fills in the site's name and the footer from the service's public information. */
export async function frame() {
  const info = await publicInfo();
  $$("[data-name]").forEach((el) => (el.textContent = info.name));
  document.title = document.title.replace("{name}", info.name);
  const f = $("#footer");
  if (f) {
    const owner = [info.company, info.address].filter(Boolean);
    f.innerHTML = `<div class="wrap">
      <div><b>${esc(info.name)}</b>${owner.length ? `<address>${owner.map(esc).join("<br>")}</address>` : ""}
        ${info.contact ? `<div><a href="mailto:${esc(info.contact)}">${esc(info.contact)}</a></div>` : ""}</div>
      <div class="links"><a href="/privacy">Privacy</a><a href="/terms">Terms</a><a href="/account">Account</a></div>
    </div>`;
  }
  return info;
}

/** Shows how much of a limit is used. */
export function meter(el, label, used, limit, fmt) {
  if (!limit) {
    el.innerHTML = `<div class="top"><span>${esc(label)}</span><b>${esc(fmt(used))} · no limit</b></div>`;
    return;
  }
  const frac = Math.min(1, used / limit);
  el.innerHTML = `<div class="top"><span>${esc(label)}</span><b>${esc(fmt(Math.min(used, limit)))} of ${esc(fmt(limit))}</b></div><div class="bar"><i></i></div>`;
  const bar = $(".bar i", el);
  bar.style.width = (frac * 100).toFixed(1) + "%";
  bar.className = frac >= 1 ? "full" : frac >= 0.85 ? "warn" : "";
}
