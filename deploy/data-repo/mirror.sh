#!/usr/bin/env bash
# Keeps the public copies in alba-data that need no part of this
# repository's code (alba-data's mirror.yml checks this repository out into
# src/ and runs this, every few hours):
#
# - The newest release's files (the APK and the Windows installer) as a
#   release of alba-data, the public fallback for downloads (update.json and
#   the account service's /dl/ name it) while this repository is private.
# - The website's mirror on alba-data's GitHub Pages (its gh-pages branch),
#   for networks where the site's own name is filtered: the public site,
#   minified (no comments), with the signed update.json, its addresses under
#   /alba-data.
#
# Environment: SOURCE_GH_TOKEN reads this repository's releases; GH_TOKEN
# writes alba-data's (the workflow's own token). The log shows versions and
# counts only.
set -uo pipefail
root="$(cd "$(dirname "$0")/../.." && pwd)"
tmp="${RUNNER_TEMP:-$(mktemp -d)}"
log="$tmp/mirror.log"
src="Albatrossvpn/Albatross"
pub="${GITHUB_REPOSITORY:-Albatrossvpn/alba-data}"

# The release.
tag="$(GH_TOKEN="$SOURCE_GH_TOKEN" gh release list --repo "$src" --exclude-drafts --exclude-pre-releases --limit 1 --json tagName --jq '.[0].tagName' 2>>"$log")"
if [[ ! "$tag" =~ ^v[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
  echo "::warning::no release to copy"
elif gh release view "$tag" --repo "$pub" >/dev/null 2>&1; then
  echo "release $tag: already copied"
else
  mkdir -p "$tmp/rel"
  GH_TOKEN="$SOURCE_GH_TOKEN" gh release download "$tag" --repo "$src" --dir "$tmp/rel" \
    --pattern 'Albatross-*-android.apk' --pattern 'Albatross-Setup-*.exe' >>"$log" 2>&1 || { echo "::error::the release files could not be read"; exit 1; }
  notes="$(GH_TOKEN="$SOURCE_GH_TOKEN" gh release view "$tag" --repo "$src" --json body --jq .body 2>>"$log")"
  gh release create "$tag" "$tmp"/rel/* --repo "$pub" --title "$tag" --notes "$notes" >>"$log" 2>&1 || { echo "::error::the release could not be made"; exit 1; }
  echo "release $tag: copied, $(ls "$tmp/rel" | wc -l) files"
fi

# The website's mirror.
site="$tmp/site"
rm -rf "$site" && cp -r "$root/site/public" "$site"
base="/${pub#*/}"
find "$site" -name '*.html' -print0 | xargs -0 sed -i -E "s#(href|src|srcset|content)=\"/([^/\"][^\"]*)?\"#\1=\"$base/\2\"#g; s#(srcset=\"[^\"]*), /#\1, $base/#g; s#<!--([^-]|-[^-])*-->##g"
find "$site" -name '*.css' -print0 | xargs -0 sed -i -E "s#url\\(/#url($base/#g"
(cd "$root/backend" && npm ci --silent >>"$log" 2>&1)
for f in $(find "$site" -name '*.js' -o -name '*.css'); do
  (cd "$root/backend" && npx esbuild "$f" --minify --legal-comments=none --outfile="$f" --allow-overwrite >>"$log" 2>&1) || { echo "::error::the site could not be minified"; exit 1; }
done
cp "$root/update.json" "$site/update.json"
touch "$site/.nojekyll"
cd "$site"
git init -q -b gh-pages
git config user.name "github-actions[bot]"
git config user.email "41898282+github-actions[bot]@users.noreply.github.com"
git add -A
TZ=UTC git commit -qm "Site mirror" >>"$log" 2>&1
git push -q --force "https://x-access-token:${GH_TOKEN}@github.com/${pub}.git" gh-pages >>"$log" 2>&1 || { echo "::error::the mirror could not be published"; exit 1; }
echo "mirror: $(git ls-files | wc -l) files published"
