Orbot برای ویندوز (x64)
=======================

اجرا: روی Orbot.exe دوبار کلیک کنید (یا از نصب‌کننده استفاده کنید).
فایل‌های داخل این پوشه (tor، data، wintun.dll) را جابه‌جا یا حذف نکنید.

حالت‌ها:
  • پروکسی سیستم: Edge، Chrome و بیشتر برنامه‌ها از Tor عبور می‌کنند. ادمین لازم نیست.
  • VPN کامل: همه‌ی ترافیک TCP و DNS ویندوز از Tor عبور می‌کند. برنامه یک بار
    با دسترسی ادمین دوباره اجرا می‌شود. (Tor فقط TCP را منتقل می‌کند؛ UDP غیر از DNS
    مسدود می‌شود، و برنامه‌ها خودشان به TCP برمی‌گردند.)
  • فقط پورت‌ها: SOCKS5 روی 127.0.0.1:9050 و HTTP روی 127.0.0.1:8118.

پل‌ها (ایران): ابتدا Snowflake را امتحان کنید. اگر کار نکرد، از ربات تلگرام
@GetBridgesBot یا ایمیل bridges@torproject.org پل obfs4 یا webtunnel بگیرید
و در «پل‌های سفارشی» بچسبانید.

نکته: برای ناشناس‌ماندن کامل در وب، مرورگر Tor Browser امن‌تر از مرورگر معمولی
پشت پروکسی است؛ Orbot ترافیک را رمز و مخفی می‌کند اما اثر انگشت مرورگر را پاک نمی‌کند.

تنظیمات و گزارش‌ها: %APPDATA%\OrbotWindows

----------------------------------------------------------------

Orbot for Windows (x64)

Run Orbot.exe (or use the installer). Keep the tor, data folders and
wintun.dll next to it.

Modes:
  * System proxy: Edge, Chrome and most apps go through Tor. No admin needed.
  * Full VPN: all TCP and DNS traffic goes through Tor via a wintun adapter.
    The app restarts once as administrator. Tor carries TCP only, so other
    UDP is dropped (apps such as browsers fall back from QUIC to TCP).
  * Local ports only: SOCKS5 127.0.0.1:9050 and HTTP 127.0.0.1:8118.

Bridges: try Snowflake first. Otherwise get obfs4/webtunnel bridges from
Telegram @GetBridgesBot or bridges@torproject.org and paste them under
"Custom bridges".

For anonymous browsing, Tor Browser is still safer than a normal browser
behind a proxy: Orbot hides your IP but does not remove browser fingerprints.

Settings and logs: %APPDATA%\OrbotWindows
