#!/usr/bin/env bash
# Tells installed copies about a release (update.json): stores its files in
# the project's own storage, writes each platform's installer and SHA-256
# into the manifest and, when FORCE is true, makes the release the version
# older copies must update to; then signs the manifest, commits it to main
# and publishes it into storage. The release job of
# .github/workflows/build.yml runs it for a new release, require-update.yml
# for the latest one after the fact, with no build:
#
#   VERSION=1.0.4 FORCE=true|false [MESSAGE=...] bash build/update-manifest.sh
#
# in a checkout of main, with the release job's credentials in the
# environment (GH_TOKEN, TOKEN_SECRET, and CLOUDFLARE_API_TOKEN with
# CLOUDFLARE_ACCOUNT_ID or ADMIN_USER with ADMIN_PASSWORD).
set -eu
root="$(cd "$(dirname "$0")/.." && pwd)"
work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT
cd "$work"
gh api "repos/${RELEASE_REPO:-$GITHUB_REPOSITORY}/releases/tags/v$VERSION" > rel.json
# The files go into the project's own storage and are served by
# the website (/download/) and the account service (/dl/), so no
# download depends on GitHub, whose downloads break off or are
# filtered on many networks.
ACCOUNT_URL="$(bash "$root/build/account-url.sh")"
SITE_URL="https://$(sed -n 's/.*pattern *= *"\([^"]*\)", *custom_domain *= *true.*/\1/p' "$root/site/wrangler.toml" | head -1)"
export ACCOUNT_URL SITE_URL
# A file that could not be stored is left out of the website's
# addresses (the account service's /dl/ still fetches it from the
# release), so a storage problem never blocks the update.
: > stored.txt
jq -r '.assets[] | select(.digest != null) | "\(.name) \(.browser_download_url) \(.digest | ltrimstr("sha256:"))"' rel.json > assets.txt
while read -r name url sha; do
  if bash "$root/deploy/files/push.sh" "${ACCOUNT_URL%%,*}" "$SITE_URL" "$name" "$url" "$sha"; then
    echo "$name" >> stored.txt
  else
    echo "::warning::$name could not be stored; the website will not list it until a deploy stores it."
  fi
done < assets.txt
python3 - "$root/update.json" <<'PY'
import json, os, re, sys
v = os.environ["VERSION"]
rel = json.load(open("rel.json"))
site = os.environ["SITE_URL"].rstrip("/")
stored = set(open("stored.txt").read().split())
mirrors = [m.strip().rstrip("/") for m in os.environ.get("ACCOUNT_URL", "").split(",") if m.strip()]
# The one APK serves every ABI; 0.3.3 and earlier read only the
# arm64-v8a entry. The website comes first, then the account
# service, then GitHub as the last resort for the app alone; the
# SHA-256 guards the file either way.
keys = {"-android.apk": ["android-arm64-v8a", "android-armeabi-v7a"], "Setup-": ["windows"]}
files = {}
for a in rel["assets"]:
    for part, names in keys.items():
        if part in a["name"] and (a.get("digest") or "").startswith("sha256:"):
            urls = ([f"{site}/download/{a['name']}"] if a["name"] in stored else []) + [f"{m}/dl/v{v}/{a['name']}" for m in mirrors] + [a["browser_download_url"]]
            for key in names:
                files[key] = {"urls": urls, "sha256": a["digest"][7:]}
path = sys.argv[1]
m = json.load(open(path))
m["latest_version"] = v
# 0.3.3 and later download the new version through their own
# connection before they stop (require). Older versions read only
# min_version: they stop at once and open download_url, so the
# message tells them, in one paragraph (they show it as plain
# text), how to reinstall.
apk = files.get("android-arm64-v8a", {}).get("urls", [""])[0]
# Every platform opens download_url when it cannot update itself
# (0.3.0 and 0.3.1 always, Windows without files.windows), so it
# is the website's download page, never one platform's file.
page = site + "/#download"
m["download_url"] = page
if os.environ["FORCE"] == "true":
    # A forced update with a platform's file missing would cut
    # those users off with nothing to update to.
    bad = [k for k in ("android-arm64-v8a", "android-armeabi-v7a", "windows")
           if not re.fullmatch(r"[0-9a-fA-F]{64}", files.get(k, {}).get("sha256", ""))
           or not files[k]["urls"][0].startswith("https://")]
    if bad:
        print("::error::Not forcing the update: no file or SHA-256 for " + ", ".join(bad))
        sys.exit(1)
    m["require"] = v
    m["min_version"] = v
steps = ("1) Uninstall Albatross. "
         f"2) Download the new version with Get the update, or open {apk or page} in your browser "
         f"(Windows: Albatross-Setup-{v}.exe from {page}). "
         "3) Install it and sign in with the username and password you were given. "
         "Later updates install from inside the app.")
# The newest version still required, by this release or an earlier
# forced one: copies older than it no longer connect.
ver = lambda s: tuple(map(int, s.split("."))) if re.fullmatch(r"\d+\.\d+\.\d+", s or "") else (0, 0, 0)
need = max(m.get("min_version"), m.get("require"), key=ver)
if os.environ.get("MESSAGE"):
    m["message"] = os.environ["MESSAGE"]
elif os.environ["FORCE"] == "true":
    m["message"] = (f"Albatross {v} is required and this version no longer connects. "
                    "If the app does not update by itself: " + steps)
elif ver(need) > (0, 0, 0):
    # The copies an earlier release cut off still need the steps;
    # the others see this with an offer they may skip.
    m["message"] = (f"Albatross {v} is out. Versions older than {need} no longer connect; "
                    "if yours is one of them and does not update by itself: " + steps)
else:
    # The last release's words would describe the wrong version.
    m.pop("message", None)
m["files"] = files
with open(path, "w") as f:
    json.dump(m, f, indent=2)
    f.write("\n")
print(json.dumps(m, indent=2))
PY
cd "$root"
# Copies from 1.0.0 on trust only the signed contents (signed,
# sig); the plain fields serve the older copies.
go run ./cmd/signupdate sign update.json
go run ./cmd/signupdate verify update.json
git config user.name "github-actions[bot]"
git config user.email "41898282+github-actions[bot]@users.noreply.github.com"
git commit -am "Tell installed copies about $VERSION$([ "$FORCE" != true ] || echo ", required")" && git push origin HEAD:main
# Installed copies read the manifest from the project's own storage
# first, and copies of 0.4.0 and older reach it there through the
# account service once the repository is private.
# The manifest on the main branch is already in force (GitHub and
# its mirrors); a storage problem must not fail the release.
bash "$root/deploy/files/publish-lists.sh" "${ACCOUNT_URL%%,*}" main "$root/update.json" ||
  echo "::warning::update.json could not be stored; copies read it from GitHub until the next deploy stores it."
echo "- update.json points at v$VERSION (forced: $FORCE)." >> "$GITHUB_STEP_SUMMARY"
