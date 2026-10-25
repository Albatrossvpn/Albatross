// Says what to update when the WebView or browser is too old to run app.js
// (Android phones often keep the WebView they shipped with; without WebView2,
// Windows opens the page in the default browser). app.js sets
// albatrossStarted before the load event, so it is missing only when app.js
// could not run. Old syntax only, so any browser runs this; a file of its
// own because the page's security policy blocks inline scripts.
(function () {
  function check() {
    if (window.albatrossStarted) return;
    var android = /Android/i.test(navigator.userAgent);
    document.body.style.cssText = "background: #0a0f1d; color: #e9edf7; overflow: auto";
    document.body.innerHTML =
      '<div style="max-width: 420px; margin: 0 auto; padding: 48px 24px; text-align: center; font: 16px/1.5 sans-serif">' +
      '<h1 style="font-size: 22px; margin: 0 0 12px">' +
      (android ? "Please update Android System WebView" : "Please use a newer browser") + "</h1>" +
      "<p>" + (android ? "Albatross cannot run because this phone's Android System WebView is too old."
        : "Albatross cannot run in this browser because it is too old.") + "</p>" +
      "<p>" + (android ? "Update Android System WebView from Google Play or your phone's app store, then open Albatross again."
        : "Make a current Chrome, Edge or Firefox your default browser, or on Windows install the Microsoft Edge WebView2 Runtime, then open Albatross again.") + "</p>" +
      "</div>";
  }
  window.addEventListener("load", function () { setTimeout(check, 1000); }, false);
})();
