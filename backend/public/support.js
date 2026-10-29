// The support chat: a button that opens a conversation with Albatross's AI
// assistant (the account service's /api/v1/support). Whatever the visitor
// writes goes to that service only; when the chat ends, a summary of it is
// mailed to the team. Nothing is kept in the page but this tab's session.
(function () {
  "use strict";
  var script = document.currentScript;
  // The website calls the account service across origins; its own pages call it directly.
  var API = script && script.getAttribute("data-api") ? script.getAttribute("data-api").replace(/\/$/, "") : "";
  // The logo sits beside this script, wherever the page keeps it (the mirror on GitHub Pages lives under a path).
  var HERE = script && script.src ? script.src.replace(/[^\/]*$/, "") : "/";
  var STORE = "albatross-support";
  var SEND_LAST = 16; // messages of the conversation sent with a question
  var IDLE_MS = 8 * 60 * 1000; // a conversation left alone this long is over

  var TEXT = {
    fab: "Support",
    title: "Albatross Support",
    sub: "AI assistant. Usually answers in seconds.",
    placeholder: "Type your question…",
    send: "Send",
    close: "Close",
    end: "End chat",
    hello: "Hi! I'm Albatross's support assistant. Tell me what is going wrong and I will help you step by step. If I cannot solve it, a summary goes to our team.",
    chips: ["I can't connect", "Install on iPhone", "Plans and upgrading", "Account or data problem"],
    contact: "Want our team to reach you? Leave a Telegram @name, an email or a phone number (optional).",
    contactPlaceholder: "Telegram @name, email or phone",
    toTeam: "Send to our team",
    sent: "Thank you. Our team has a summary of this chat.",
    failed: "The message did not go through. Check your connection and try again.",
    note: "An AI assistant can be wrong. Do not write passwords. When the chat ends, a summary goes to our team.",
  };

  function randomId() {
    var a = new Uint8Array(12);
    try { (window.crypto || window.msCrypto).getRandomValues(a); } catch (e) { for (var i = 0; i < a.length; i++) a[i] = Math.floor(Math.random() * 256); }
    var s = "";
    for (var j = 0; j < a.length; j++) s += ("0" + a[j].toString(16)).slice(-2);
    return s;
  }

  function fresh() {
    return { sid: randomId(), messages: [], contact: "", contactSent: "", sentAt: 0, offered: false };
  }

  function load() {
    try {
      var v = JSON.parse(sessionStorage.getItem(STORE) || "null");
      if (v && Array.isArray(v.messages) && typeof v.sid === "string") return v;
    } catch (e) {}
    return fresh();
  }

  var state = load();
  function save() { try { sessionStorage.setItem(STORE, JSON.stringify(state)); } catch (e) {} }
  var t = function (k) { return TEXT[k]; };

  // ---------- Elements ----------

  var SVG_NS = "http://www.w3.org/2000/svg";
  function icon(d, fill) {
    var s = document.createElementNS(SVG_NS, "svg");
    s.setAttribute("viewBox", "0 0 24 24");
    s.setAttribute("aria-hidden", "true");
    var p = document.createElementNS(SVG_NS, "path");
    p.setAttribute("d", d);
    if (fill) p.setAttribute("fill", "currentColor");
    else { p.setAttribute("fill", "none"); p.setAttribute("stroke", "currentColor"); p.setAttribute("stroke-width", "2"); p.setAttribute("stroke-linecap", "round"); p.setAttribute("stroke-linejoin", "round"); }
    s.appendChild(p);
    return s;
  }
  function el(tag, cls, text) {
    var e = document.createElement(tag);
    if (cls) e.className = cls;
    if (text !== undefined) e.textContent = text;
    return e;
  }

  var root = el("div", "sp-root");
  var fab = el("button", "sp-fab");
  fab.type = "button";
  fab.setAttribute("aria-haspopup", "dialog");
  var fabText = el("span");
  fab.appendChild(icon("M21 12a8 8 0 0 1-11.6 7.1L4 20.5l1.5-4.7A8 8 0 1 1 21 12z"));
  fab.appendChild(fabText);
  fab.appendChild(el("span", "sp-dot"));

  // Plain divs: a page that styles section, header or p must not restyle the chat.
  var panel = el("div", "sp-panel");
  panel.setAttribute("role", "dialog");
  panel.setAttribute("aria-modal", "false");
  panel.hidden = true;

  var head = el("div", "sp-head");
  var avatar = el("div", "sp-avatar");
  var logo = document.createElement("img");
  logo.src = HERE + "icon.svg";
  logo.alt = "";
  avatar.appendChild(logo);
  var titles = el("div", "sp-titles");
  var title = el("div", "sp-title");
  var sub = el("div", "sp-sub");
  titles.appendChild(title);
  titles.appendChild(sub);
  var endBtn = el("button", "sp-icon-btn");
  endBtn.type = "button";
  var closeBtn = el("button", "sp-icon-btn");
  closeBtn.type = "button";
  closeBtn.appendChild(icon("M6 6l12 12M18 6L6 18"));
  head.appendChild(avatar);
  head.appendChild(titles);
  head.appendChild(endBtn);
  head.appendChild(closeBtn);

  var log_ = el("div", "sp-log");
  log_.setAttribute("role", "log");
  log_.setAttribute("aria-live", "polite");
  var chips = el("div", "sp-chips");

  var team = el("div", "sp-team");
  team.hidden = true;
  var teamLabel = el("label");
  var teamInput = document.createElement("input");
  teamInput.type = "text";
  teamInput.maxLength = 120;
  teamInput.autocomplete = "off";
  teamInput.id = "sp-contact";
  teamLabel.setAttribute("for", "sp-contact");
  var teamGo = el("button", "sp-go");
  teamGo.type = "button";
  var teamRow = el("div", "sp-team-row");
  teamRow.appendChild(teamInput);
  teamRow.appendChild(teamGo);
  team.appendChild(teamLabel);
  team.appendChild(teamRow);

  var form = el("form", "sp-form");
  var box = document.createElement("textarea");
  box.rows = 1;
  box.maxLength = 800;
  box.setAttribute("enterkeyhint", "send");
  var sendBtn = el("button", "sp-send");
  sendBtn.type = "submit";
  sendBtn.appendChild(icon("M5 12h14M13 6l6 6-6 6"));
  form.appendChild(box);
  form.appendChild(sendBtn);
  var note = el("div", "sp-note");

  panel.appendChild(head);
  panel.appendChild(log_);
  panel.appendChild(chips);
  panel.appendChild(team);
  panel.appendChild(form);
  panel.appendChild(note);
  panel.hidden = true;
  root.appendChild(fab);
  root.appendChild(panel);

  // ---------- Text of a message ----------

  var SAFE_HOST = /^(?:[a-z0-9-]+\.)*albatrossvpn\.online$/i;
  // Links the assistant writes are shown as links only to our own addresses.
  function inline(parent, text) {
    var re = /(\*\*[^*]+\*\*)|(https?:\/\/[^\s<>()"']+)|([A-Za-z0-9._-]+@albatrossvpn\.online)/g;
    var last = 0;
    var m;
    while ((m = re.exec(text))) {
      if (m.index > last) parent.appendChild(document.createTextNode(text.slice(last, m.index)));
      if (m[1]) {
        parent.appendChild(el("strong", "", m[1].slice(2, -2)));
      } else if (m[2]) {
        var url = m[2].replace(/[.,;:!?]+$/, "");
        var rest = m[2].slice(url.length);
        var host = "";
        try { host = new URL(url).hostname; } catch (e) {}
        if (/^https:/i.test(url) && SAFE_HOST.test(host)) {
          var a = el("a", "", url.replace(/^https:\/\//i, ""));
          a.href = url;
          a.target = "_blank";
          a.rel = "noopener";
          parent.appendChild(a);
        } else {
          parent.appendChild(document.createTextNode(url));
        }
        if (rest) parent.appendChild(document.createTextNode(rest));
      } else {
        var mail = el("a", "", m[3]);
        mail.href = "mailto:" + m[3];
        parent.appendChild(mail);
      }
      last = m.index + m[0].length;
    }
    if (last < text.length) parent.appendChild(document.createTextNode(text.slice(last)));
  }

  // Paragraphs, "- " and "1." lists, bold and our links: all built as elements, never as markup.
  function body(container, text) {
    var lines = String(text).replace(/\r/g, "").split("\n");
    var list = null;
    lines.forEach(function (line) {
      var item = /^\s*(?:[-*•]|\d+[.)])\s+(.*)$/.exec(line);
      if (item) {
        var ordered = /^\s*\d/.test(line);
        if (!list || list.tagName !== (ordered ? "OL" : "UL")) {
          list = document.createElement(ordered ? "ol" : "ul");
          container.appendChild(list);
        }
        var li = document.createElement("li");
        inline(li, item[1]);
        list.appendChild(li);
        return;
      }
      list = null;
      if (!line.trim()) return;
      var p = document.createElement("p");
      inline(p, line.trim());
      container.appendChild(p);
    });
  }

  function bubble(kind, text) {
    var row = el("div", "sp-msg sp-" + kind);
    var inner = document.createElement("div");
    inner.dir = "auto";
    body(inner, text);
    row.appendChild(inner);
    log_.appendChild(row);
    log_.scrollTop = log_.scrollHeight;
    return row;
  }

  var typing = null;
  function showTyping(on) {
    if (on && !typing) {
      typing = el("div", "sp-msg sp-bot sp-typing");
      var inner = document.createElement("div");
      for (var i = 0; i < 3; i++) inner.appendChild(document.createElement("i"));
      typing.appendChild(inner);
      log_.appendChild(typing);
      log_.scrollTop = log_.scrollHeight;
    } else if (!on && typing) {
      typing.remove();
      typing = null;
    }
  }

  // ---------- Showing it all ----------

  function draw() {
    fabText.textContent = t("fab");
    fab.setAttribute("aria-label", t("fab"));
    panel.setAttribute("aria-label", t("title"));
    title.textContent = t("title");
    sub.textContent = t("sub");
    endBtn.textContent = t("end");
    closeBtn.setAttribute("aria-label", t("close"));
    box.placeholder = t("placeholder");
    sendBtn.setAttribute("aria-label", t("send"));
    teamLabel.textContent = t("contact");
    teamInput.placeholder = t("contactPlaceholder");
    teamGo.textContent = t("toTeam");
    note.textContent = t("note");
    chips.textContent = "";
    if (!state.messages.length) {
      t("chips").forEach(function (q) {
        var b = el("button", "", q);
        b.type = "button";
        b.addEventListener("click", function () { ask(q); });
        chips.appendChild(b);
      });
    }
    chips.hidden = !!state.messages.length;
    log_.textContent = "";
    typing = null;
    bubble("bot", t("hello"));
    state.messages.forEach(function (m) { bubble(m.role === "user" ? "me" : "bot", m.content); });
    team.hidden = !state.offered;
    teamInput.value = state.contact;
    endBtn.hidden = !state.messages.length;
  }

  function setOpen(on) {
    root.classList.toggle("sp-open", on);
    panel.hidden = !on;
    fab.setAttribute("aria-expanded", String(on));
    if (on) {
      draw();
      setTimeout(function () { box.focus(); }, 50);
    } else {
      conclude("closed");
    }
  }

  // ---------- Talking to the service ----------

  function request(path, data, keepalive) {
    var controller = typeof AbortController === "function" ? new AbortController() : null;
    var timer = controller ? setTimeout(function () { controller.abort(); }, 50000) : 0;
    var headers = { "Content-Type": "text/plain;charset=UTF-8" }; // not preflighted across origins
    return fetch(API + "/api/v1/support/" + path, {
      method: "POST",
      headers: headers,
      body: JSON.stringify(data),
      keepalive: !!keepalive,
      credentials: "omit",
      signal: controller ? controller.signal : undefined,
    }).then(function (r) {
      clearTimeout(timer);
      return r.json().then(function (j) { j.status = r.status; return j; }, function () { return { status: r.status }; });
    }, function (e) { clearTimeout(timer); throw e; });
  }

  var busy = false;
  var idleTimer = 0;
  function bumpIdle() {
    clearTimeout(idleTimer);
    idleTimer = setTimeout(function () { conclude("idle"); }, IDLE_MS);
  }

  function history() {
    return state.messages.slice(-SEND_LAST).map(function (m) {
      return { role: m.role, content: m.content.slice(0, m.role === "user" ? 800 : 1500) };
    });
  }

  function ask(text) {
    text = String(text || "").trim();
    if (!text || busy) return;
    busy = true;
    sendBtn.disabled = true;
    state.messages.push({ role: "user", content: text.slice(0, 800) });
    chips.hidden = true;
    endBtn.hidden = false;
    bubble("me", text.slice(0, 800));
    box.value = "";
    grow();
    save();
    showTyping(true);
    bumpIdle();
    request("chat", { messages: history(), page: location.pathname }).then(function (r) {
      showTyping(false);
      if (r && r.reply) {
        state.messages.push({ role: "assistant", content: r.reply });
        bubble("bot", r.reply);
      } else {
        bubble("info", t("failed"));
        state.messages.pop(); // so the question can be asked again
        box.value = text;
      }
      var users = state.messages.filter(function (m) { return m.role === "user"; }).length;
      // After a hand-over, or once it is clear this is more than a quick question, offer a way to be reached.
      if (r && (r.handoff || users >= 3) && !state.offered) {
        state.offered = true;
        team.hidden = false;
      }
      save();
    }, function () {
      showTyping(false);
      bubble("info", t("failed"));
      state.messages.pop();
      box.value = text;
      save();
    }).then(function () {
      busy = false;
      sendBtn.disabled = false;
      box.focus();
    });
  }

  // The conversation is over (closed, left alone, or page left): the team gets its summary, once for what was said
  // (again when more was said since, or when a way to reach the visitor was added).
  function conclude(why, contact) {
    clearTimeout(idleTimer);
    var said = state.messages.length;
    if (typeof contact === "string") contact = contact.trim().slice(0, 120);
    var newContact = typeof contact === "string" && contact !== "" && contact !== state.contactSent;
    if (!said || (said <= state.sentAt && !newContact)) return;
    if (typeof contact === "string") state.contact = contact;
    // A conversation that went on after its summary was sent gets a new summary of its own.
    if (state.sentAt) state.sid = randomId();
    var data = { messages: history(), sid: state.sid, contact: state.contact, page: location.pathname, why: why };
    state.sentAt = said;
    state.contactSent = state.contact;
    save();
    var leaving = why === "leave";
    var sent = false;
    if (leaving && navigator.sendBeacon) {
      try { sent = navigator.sendBeacon(API + "/api/v1/support/close", new Blob([JSON.stringify(data)], { type: "text/plain;charset=UTF-8" })); } catch (e) {}
    }
    if (!sent) request("close", data, leaving).catch(function () {});
  }

  // ---------- Events ----------

  function grow() {
    box.style.height = "auto";
    box.style.height = Math.min(box.scrollHeight, 110) + "px";
  }
  box.addEventListener("input", grow);
  box.addEventListener("keydown", function (e) {
    if (e.key === "Enter" && !e.shiftKey && !e.isComposing) {
      e.preventDefault();
      ask(box.value);
    }
  });
  form.addEventListener("submit", function (e) { e.preventDefault(); ask(box.value); });
  fab.addEventListener("click", function () { setOpen(true); });
  closeBtn.addEventListener("click", function () { setOpen(false); });
  document.addEventListener("keydown", function (e) { if (e.key === "Escape" && !panel.hidden) setOpen(false); });
  endBtn.addEventListener("click", function () {
    conclude("ended", teamInput.value);
    bubble("info", t("sent"));
    state = fresh();
    save();
    setTimeout(function () { if (!panel.hidden) draw(); }, 1800);
  });
  teamGo.addEventListener("click", function () {
    state.contact = teamInput.value.trim().slice(0, 120);
    save();
    // Ending the conversation here, with its contact, is what sends it.
    conclude("handoff", state.contact);
    bubble("info", t("sent"));
    team.hidden = true;
  });
  window.addEventListener("pagehide", function () { conclude("leave"); });

  function start() {
    document.body.appendChild(root);
    draw();
    if (state.messages.length) bumpIdle();
  }
  if (document.body) start();
  else document.addEventListener("DOMContentLoaded", start);
})();
