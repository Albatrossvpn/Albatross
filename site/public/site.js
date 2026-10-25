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
      // The site's own copy (or the account service's); never a GitHub address.
      var own = (f.urls || []).filter(function (u) { return u.indexOf("https://github.com/") !== 0; })[0];
      if (a && own) a.href = own;
      var s = document.querySelector('[data-sha="' + key + '"]');
      if (s && f.sha256) s.textContent = "SHA-256 " + f.sha256;
    });
    if (v) document.querySelectorAll("[data-ver]").forEach(function (e) { e.textContent = "Version " + v + " · " + e.textContent; });
  }
  tryNext(0);
})();
