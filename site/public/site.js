// Fills the download buttons from the release manifest the apps read, so the
// links always point at the newest version and show its checksum. Without it
// (or while it loads) the buttons lead to the releases page.
(function () {
  "use strict";
  document.querySelectorAll("[data-year]").forEach(function (e) { e.textContent = String(new Date().getFullYear()); });
  var urls = [
    "https://raw.githubusercontent.com/cezomagoanlace29861-jpg/Albatross/main/update.json",
    "https://cdn.jsdelivr.net/gh/cezomagoanlace29861-jpg/Albatross@main/update.json"
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
      if (a && f.urls && f.urls[0]) a.href = f.urls[0];
      var s = document.querySelector('[data-sha="' + key + '"]');
      if (s && f.sha256) s.textContent = "SHA-256 " + f.sha256;
    });
    if (v) document.querySelectorAll("[data-ver]").forEach(function (e) { e.textContent = "Version " + v + " · " + e.textContent; });
  }
  tryNext(0);
})();
