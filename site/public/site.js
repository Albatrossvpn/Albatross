// Fills the download buttons from the release manifest the apps read, so the
// links always point at the newest version and show its checksum. Without it
// (or while it loads) the buttons lead to the list of all downloads on this
// site. Visitors are never sent to GitHub.
(function () {
  "use strict";
  document.querySelectorAll("[data-year]").forEach(function (e) { e.textContent = String(new Date().getFullYear()); });
  var urls = [
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
      // Only a file this site stores itself, so visitors never leave it.
      if (a && /^(Albatross-\d+\.\d+\.\d+-android\.apk|Albatross-Setup-\d+\.\d+\.\d+\.exe)$/.test(name)) {
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
