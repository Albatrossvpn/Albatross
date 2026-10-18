<div dir="rtl">

# آزادراه VPN

برنامهٔ ویندوزی VPN رایگان که خودش از صدها منبع عمومی سرورهای رایگان را جمع می‌کند، دائم آزمایششان می‌کند و با یک دکمه به **کم‌پینگ‌ترین سرور سالم در هر کشوری که بخواهید** وصل می‌شود.

## دانلود و نصب

از بخش [Releases](../../releases) یا از خروجی‌های [Actions](../../actions) (artifact با نام `Azadrah-Setup-…`) فایل نصبی را بگیرید و اجرا کنید. **همه پیش‌نیازها داخل همین یک فایل است**: خود برنامه، هسته‌های sing-box و Xray، warp-plus (برای WARP و سایفون)، فهرست سایت‌های ایرانی و فونت‌ها. اگر WebView2 روی ویندوز نباشد، نصب‌کننده خودش نصبش می‌کند. نسخهٔ پرتابل (zip) هم هست که نصب نمی‌خواهد.

نیازمندی: ویندوز ۱۰ یا ۱۱ (۶۴ بیتی).

## استفاده

۱. برنامه را باز کنید.
۲. روی کارت کشور بزنید و کشور را انتخاب کنید (یا «سریع‌ترین کشور»).
۳. تمام. برنامه به کم‌پینگ‌ترین سرورهای سالم آن کشور وصل می‌شود و اگر سرور کند یا قطع شود، خودکار جابه‌جا می‌شود.

## چه چیزهایی دارد

- **همهٔ منابع:** ۱۰۴ فهرست اولویت‌دار (آزمایش‌شده و مخصوص ایران)، ۳۳۸ فهرست عمیق که همهٔ کانفیگ‌های یکتای پیدا‌شده از بین ۵٬۰۶۰ منبع زنده را پوشش می‌دهند (حدود ۶۹۴ هزار کانفیگ یکتا)، ۲۳۷ کانال تلگرام، VPN Gate (اوپن‌وی‌پی‌ان رایگان)، Cloudflare WARP، WARP در WARP و سایفون در ۳۲ کشور. لینک اشتراک، کانال تلگرام یا خود کانفیگ‌ها را هم می‌توانید اضافه کنید.
- **همهٔ پروتکل‌ها:** VLESS (Reality، Vision، xhttp، رمزنگاری پساکوانتومی)، VMess، Trojan، Shadowsocks (همهٔ رمزها و پلاگین‌ها)، Hysteria 1 و 2 (با obfs و port hopping)، TUIC، AnyTLS، WireGuard، SOCKS، OpenVPN، و فرمت Clash. هر چه sing-box اجرا کند با sing-box و بقیه با Xray.
- **به‌روزرسانی دائم:** منابع هر ۲۰ دقیقه تا ۴ ساعت دوباره خوانده می‌شوند و سرورها پشت سر هم آزمایش می‌شوند؛ هر آزمایش یک درخواست واقعی از داخل سرور است که پینگ، آی‌پی خروجی و **کشور واقعی خروجی** را می‌دهد.
- **اولویت با کمترین پینگ:** ۱۲ سرور سریع‌تر کشور انتخابی آماده نگه داشته می‌شوند و همیشه سریع‌ترینشان استفاده می‌شود.
- **سایت‌های ایرانی بدون VPN:** بیش از ۵۷ هزار دامنه و ۲٬۸۰۰ محدودهٔ آی‌پی ایران مستقیم باز می‌شوند.
- **عبور از فیلترینگ:** تکه‌تکه کردن TLS (خودکار برای سرورهایی که بدون آن کار نمی‌کنند)، تشخیص DNS مسموم (۱۰.۱۰.۳۴.x) و حل دوباره با DNS-over-HTTPS، پیدا کردن آی‌پی تمیز کلودفلر برای سرورهای پشت CDN، جعل SNI برای اینترنت ملی و لیست سفید، uTLS، ECH، و روش‌های پشتیبان WARP و سایفون.
- **دو حالت اتصال:** پروکسی سیستم (بدون نیاز به دسترسی مدیر) یا تونل کامل TUN برای همهٔ برنامه‌ها و بازی‌ها.
- اشتراک پروکسی در شبکهٔ محلی برای گوشی، مسدود کردن تبلیغات، اجرا با ویندوز، اتصال خودکار.

## نکتهٔ امنیتی

سرورها عمومی و رایگان‌اند و گردانندگانشان ناشناس هستند. اطلاعات حساس را فقط روی سایت‌های HTTPS وارد کنید.

</div>

---

## English

Azadrah is a Windows VPN client that collects free public servers from hundreds of sources, tests them continuously, and connects to the lowest-latency working server in the country you pick. It is built for networks with heavy censorship (Iran in particular).

### How it works

| Part | Package |
|---|---|
| Share-link and Clash parsing, sing-box/Xray outbound generation | `internal/node` |
| Source lists, GitHub CDN mirrors, Telegram previews, VPN Gate, ETag cache | `internal/sources` |
| Disk-backed candidate pool (hundreds of thousands of servers in tens of MB) | `internal/pool` |
| Runs sing-box with Xray and warp-plus sidecars; drops configs a core rejects | `internal/core` |
| TCP probe plus a real HTTPS request through each server (delay, exit IP, exit country) | `internal/tester` |
| DNS unpoisoning over DoH, Cloudflare clean-IP scanner, IP pinning | `internal/antiblock` |
| Scheduler, connect (sing-box `urltest` group), health checks and failover | `internal/engine` |
| Local API and the embedded Persian UI | `internal/ui` |
| System proxy, elevation, autostart, single instance | `internal/platform` |

Every server runs through sing-box. Servers only Xray supports (xhttp, mKCP, VLESS post-quantum encryption, ECH, pinned certificates) are served by an Xray sidecar on a local SOCKS port that sing-box connects to, and warp-plus methods (WARP, WARP in WARP, Psiphon) work the same way.

### Build

```sh
go test ./...
VERSION=0.1.0 bash build/package.sh      # dist/Azadrah + portable zip (needs curl, unzip, zip)
iscc /DAppVersion=0.1.0 build/windows/installer.iss   # on Windows, needs Inno Setup 6
```

CI (`.github/workflows/build.yml`) runs the tests including end-to-end runs against real local servers, builds the app, bundles the cores, smoke-tests it on Windows, builds and installs the installer, and publishes a release for `v*` tags.

Opt-in validation against real data:

```sh
AZADRAH_LINKS=links.txt SING_BOX=/path/sing-box XRAY=/path/xray go test -run RealData ./internal/node
```

Bundled third-party components keep their own licenses: sing-box (GPL-3.0-or-later), Xray-core (MPL-2.0), warp-plus (MIT), Vazirmatn (OFL-1.1), Twemoji flags (CC-BY 4.0), Iran rule sets by Chocolate4U.
