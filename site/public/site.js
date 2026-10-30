// Fills the download buttons from the release manifest the apps read, so the
// links always point at the newest version and show its checksum. Without it
// (or while it loads) the buttons lead to the list of all downloads on this
// site. Visitors are never sent to GitHub, except on the site's own mirror
// on GitHub Pages (for networks where this site's name is filtered), where
// the files come from the public copies of the releases (alba-data, which
// deploy/data-repo/mirror.sh keeps, along with the mirror itself).
(function () {
  "use strict";
  document.querySelectorAll("[data-year]").forEach(function (e) { e.textContent = String(new Date().getFullYear()); });
  var mirror = /\.github\.io$/.test(location.hostname);
  var releases = "https://github.com/Albatrossvpn/alba-data/releases";
  var base = "/" + (location.pathname.split("/")[1] || "");
  if (mirror) {
    // The mirror holds every page of the site under the repository's name
    // (mirror.sh puts it in front of each root-relative address), so its
    // links stay on it; only the list of downloads is the releases there.
    document.querySelectorAll('a[href^="/download/"], a[href^="' + base + '/download/"]').forEach(function (a) { a.href = releases; });
  }
  var urls = mirror ? [base + "/update.json"] : [
    "/update.json"
  ];
  function tryNext(i) {
    if (i >= urls.length) return;
    fetch(urls[i], { cache: "no-store" }).then(function (r) { return r.json(); }).then(apply).catch(function () { tryNext(i + 1); });
  }
  function apply(m) {
    var v = m.latest_version;
    Object.keys(m.files || {}).forEach(function (key) {
      var f = m.files[key];
      var a = document.querySelector('[data-dl="' + key + '"]');
      var name = f.urls && f.urls[0] ? f.urls[0].split("/").pop() : "";
      if (a && mirror) {
        // On the mirror: the same file from the public copy of the release.
        if (v && /^(Albatross-\d+\.\d+\.\d+-android\.apk|Albatross-Setup-\d+\.\d+\.\d+\.exe)$/.test(name)) a.href = releases + "/download/v" + v + "/" + name;
      } else if (a && /^(Albatross-\d+\.\d+\.\d+-android\.apk|Albatross-Setup-\d+\.\d+\.\d+\.exe)$/.test(name)) {
        // Only a file this site stores itself, so visitors never leave it.
        var href = "/download/" + name;
        fetch(href, { method: "HEAD" }).then(function (r) {
          if (!r.ok) return;
          a.href = href;
          var n = +r.headers.get("content-length");
          if (n) a.textContent += " (" + Math.round(n / 1048576) + " MB)";
        }).catch(function () {});
      }
      var s = document.querySelector('[data-sha="' + key + '"]');
      if (s && f.sha256) { s.textContent = f.sha256; s.closest("details").hidden = false; }
    });
    if (v) document.querySelectorAll("[data-ver]").forEach(function (e) { e.textContent += " · version " + v; });
  }
  // How to buy: the packages on sale in the Telegram bot, with their prices, from the account
  // service (they follow its settings). The block is shown only while data is on sale.
  var buy = document.getElementById("buy");
  if (buy) {
    fetch("https://accounts.albatrossvpn.online/api/v1/support/info").then(function (r) { return r.json(); }).then(function (j) {
      var st = j && j.stars;
      if (!st || !/^[A-Za-z0-9_]{5,32}$/.test(st.bot || "") || !st.packages || !st.packages.length) return;
      var packs = document.getElementById("buy-packs");
      st.packages.forEach(function (p) {
        var d = document.createElement("div");
        d.className = "pack";
        var gb = document.createElement("b");
        gb.textContent = (+p.gb) + " GB";
        var stars = document.createElement("span");
        stars.textContent = "\u2B50 " + (+p.stars);
        var plan = document.createElement("small");
        plan.textContent = String(p.plan || "").slice(0, 20) + (+p.save > 0 ? " \u00B7 " + (+p.save) + "% off" : "");
        d.appendChild(gb);
        d.appendChild(stars);
        d.appendChild(plan);
        packs.appendChild(d);
      });
      document.getElementById("buy-days").textContent = st.days > 0 ? (+st.days) + " days" : "without an end date";
      // What a longer plan saves: the months' discounts, largest first.
      var c = st.custom || {}, steps = (c.month_discounts || []).slice().reverse().filter(function (s) { return +s[1] > 0; });
      if (steps.length) document.getElementById("buy-save").textContent = "Longer plans cost less: " + steps.map(function (s) { return (+s[0]) + " months " + (+s[1]) + "% off"; }).join(", ") + ".";
      document.getElementById("buy-link").href = "https://t.me/" + st.bot + "?start=buy";
      buy.hidden = false;
    }).catch(function () {});
  }
  document.querySelectorAll(".menu-panel a").forEach(function (a) {
    a.addEventListener("click", function () { a.closest("details").open = false; });
  });
  tryNext(0);
})();
