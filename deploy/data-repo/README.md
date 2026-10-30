# The public repository alba-data

GitHub Actions minutes are free for public repositories, and GitHub and its
CDN mirrors (raw, jsDelivr, githack, gcore, the API) serve a public
repository's files to the apps when the project's own storage is out of
reach. So the heavy work runs in **Albatrossvpn/alba-data**, which is public,
while everything that is the project's own stays here, private:

| alba-data holds | it never holds |
|---|---|
| `.github/workflows/`: thin workflows that check this repository out with a token and run the scripts here | the code, the scripts, any comment describing how things work |
| `servers` branch: the sealed lists only (`*.sealed`) | statistics (meta.json, source-stats.json), the sources and channels, Tor bridges, reach.json, any plain file |
| releases: a copy of each release's APK and Windows installer (already public on the website) | |
| `gh-pages` branch: the public website's mirror, minified, with the signed `update.json` | |

- **Lists:** sealed with AES-256-GCM (`sources.SealKeyed`). The key changes every collection, and only signed-in users get it from the account service. The own servers are sealed with the build's key too.
- **Builds between jobs:** go through the cache sealed with gpg (AES-256, SHA-512 key derivation, a key derived from `TOKEN_SECRET`), and are opened only by the job that needs them.
- **Logs:**
  - The collector's and the discovery's output goes to a file that is not kept; the log shows counts.
  - The domestic mirrors are masked one by one; secrets are masked by GitHub.
- **No traces:** `cleanup.yml` deletes every finished run's caches and artifacts, and the run itself with its logs when it succeeded. A failed run keeps its log for a day, and the hourly sweep removes what is older.
- **No outside triggers:** nothing there runs on a push or a pull request, so no fork's workflow ever gets its secrets or its cache.

## Files

- **`collect.sh`, `discover.sh`:** one collection or discovery run, started by alba-data's `collect.yml` and `discover.yml`.
- **`mirror.sh`:** started by alba-data's `mirror.yml` every six hours. It copies the newest release's files into alba-data's releases, the public download fallback while this repository is private (`update.json` and the account service's `/dl/` name them). It also publishes the website's mirror on alba-data's GitHub Pages: Settings, then Pages, then "Deploy from a branch", `gh-pages`, `/ (root)`.
- **`deploy.sh`:** deploys the account service and the website, started by alba-data's `deploy.yml` (by hand). The token needs Workers, D1 and Routes; the Workers' secrets are left as they are.
- **`gen-release.py`:** writes alba-data's `release.yml` from `.github/workflows/build.yml`. Run it after every change to build.yml:

  ```sh
  python3 deploy/data-repo/gen-release.py > ../alba-data/.github/workflows/release.yml
  ```

## Setting it up (once)

In alba-data, under Settings → Secrets and variables → Actions:

- **Secrets:**
  - `SOURCE_TOKEN`: a fine-grained token for this repository only, with Contents read and write. It checks the code out, publishes releases and commits update.json.
  - With the same values as here:
    - `CLOUDFLARE_API_TOKEN`, `CLOUDFLARE_ACCOUNT_ID`
    - `ADMIN_USER`, `ADMIN_PASSWORD`
    - `OWN_SERVERS`, `OWN_RELAYS`, `OWN_KEY`
    - `GLOBALPING_TOKEN`, `GLOBALPING_TAGS`
    - `TOKEN_SECRET`
    - `ANDROID_KEYSTORE_BASE64`, `ANDROID_KEYSTORE_PASSWORD`, `ANDROID_KEY_ALIAS`, `ANDROID_KEY_PASSWORD`
    - `SITE_DOMAIN`, `DOMESTIC_MIRROR`: secrets there, not variables, so they are masked in its logs.
- **No variables:** there, collection and discovery start by themselves once `SOURCE_TOKEN` and `CLOUDFLARE_API_TOKEN` are set. Here they run only while the variable `COLLECT_IN_PRIVATE` is 1. Only one collector may run, or the two race over the list keys.

## Releasing

Run alba-data's **Release** workflow by hand:

- **ref:** the branch to build, normally `main`.
- **release:** the version.
- **force_update:** whether older versions stop connecting until they update.
- **update_message:** what installed copies show.

The release, its files and update.json land here, as before. With an empty `release`, the same workflow builds and tests a branch.
