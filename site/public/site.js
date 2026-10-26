// Fills the download buttons from the release manifest the apps read, so the
// links always point at the newest version and show its checksum. Without it
// (or while it loads) the buttons lead to the list of all downloads on this
// site. Visitors are never sent to GitHub, except on the site's own mirror
// on GitHub Pages (for networks where this site's name is filtered), where
// the files come from the releases there.
(function () {
  "use strict";
  document.querySelectorAll("[data-year]").forEach(function (e) { e.textContent = String(new Date().getFullYear()); });
  var mirror = /\.github\.io$/.test(location.hostname);
  var releases = "https://github.com/Albatrossvpn/Albatross/releases";
  if (mirror) {
    document.querySelectorAll('a[href^="/download/"]').forEach(function (a) { a.href = releases; });
    document.querySelectorAll('a[href^="/"]').forEach(function (a) {
      // The main site's own pages: send visitors to the site itself.
      if (a.getAttribute("href") !== "/" && !/^\/(download|#)/.test(a.getAttribute("href"))) a.href = "https://albatrossvpn.online" + a.getAttribute("href");
    });
  }
  var urls = mirror ? ["https://raw.githubusercontent.com/Albatrossvpn/Albatross/main/update.json"] : [
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
        // On the mirror: the same file from the release on GitHub.
        var gh = (f.urls || []).filter(function (u) { return /^https:\/\/github\.com\//.test(u); })[0];
        if (gh) a.href = gh;
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
  document.querySelectorAll(".menu-panel a").forEach(function (a) {
    a.addEventListener("click", function () { a.closest("details").open = false; });
  });
  tryNext(0);
})();
