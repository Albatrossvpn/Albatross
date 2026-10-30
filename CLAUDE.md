# Standing instructions

## Private and public

- **This repository is private and stays the only home of everything proprietary:** the code, the scripts, the workflows' logic, statistics, sources, keys, and anything readable about how the project works.
- **The heavy work runs in the public repository Albatrossvpn/alba-data:** collection, discovery, builds and releases, on free minutes. So do the files GitHub must serve to the apps. See `deploy/data-repo/README.md`.
- **The public copies:** alba-data's `mirror.yml` (`deploy/data-repo/mirror.sh`) copies each release's APK and installer into alba-data's releases, and publishes the public website's mirror (minified, no comments) on its GitHub Pages. Nothing else of this repository is public.
- **What may go into alba-data:**
  - thin workflows that check this repository out with `SOURCE_TOKEN`;
  - files sealed with strong encryption: AES-256-GCM lists, and gpg AES-256 for anything handed between jobs.
- **What must never go there:** plain data, sources, statistics, keys, the domestic mirrors' addresses, code or explanatory comments.
- **Logs there are public.**
  - Print counts, never addresses, links or sources.
  - Send tool output to a file that is not kept.
  - Mask anything sensitive (`::add-mask::`).
- **After every release, and every run there:** nothing of it may stay.
  - `cleanup.yml` deletes finished runs, their logs, caches and artifacts.
  - Keep it working.
  - If something sensitive ever reaches alba-data (a file, a log, a release asset), delete it at once: the run, the commit (force-push the branch's single commit) or the asset.
- **After any change to `.github/workflows/build.yml`:** regenerate alba-data's `release.yml` with `deploy/data-repo/gen-release.py` and push it there.

## The app's text

- **The app is in English only.** No Persian text goes into it: not in the UI, not in notifications, and not in an update message (update.json's `message` is shown in the app).

## Commits

- Commit with a noreply address and with `TZ=UTC` (`.github/workflows/identity.yml`).
