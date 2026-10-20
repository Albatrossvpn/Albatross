import { $, frame } from "/site.js";

const info = await frame();
$("#signup-cta").classList.toggle("hidden", !info.signup_open);
for (const [key, note] of [["windows", "Installer for Windows 10 and 11"], ["android", "APK for Android 7 and newer"]]) {
  const url = info.downloads && info.downloads[key];
  const a = $("#dl-" + key);
  if (url) {
    a.href = url;
    a.classList.remove("off");
    $("#dl-" + key + "-note").textContent = note;
  }
}
