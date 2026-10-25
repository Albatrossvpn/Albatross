# A tester inside the country

The collector (`cmd/collect`, run by CI abroad) finds and tests thousands of
servers, but it tests from outside and cannot see the filtering users face.
This tester runs on a machine on an Iranian network, connects through the
candidate servers for real from there, twice, and reports which ones work.

Its report, `servers/iran.json` in the project's own storage, is read by the
collector (`-crowd`): servers confirmed from inside come first in `hot.txt`
and servers that failed there are left out; while the report is fresh (12
hours) and confirms at least 100 servers, `hot.txt` holds confirmed servers
only. The tester also publishes `hot.txt` itself after such a run, so the
apps get the list at once rather than at the next run abroad.

## Setting one up

A small Ubuntu 22.04/24.04 or Debian 12 machine (x86-64 or ARM64) on an
Iranian provider, with the ordinary internet access users have. Then:

```sh
curl -fsSL https://accounts.example.com/list/tools/tester-install.sh -o install.sh
sudo ACCOUNT_URL=https://accounts.example.com ADMIN_USER=... ADMIN_PASSWORD=... bash install.sh
```

`ACCOUNT_URL` is the account service; `ADMIN_USER` and `ADMIN_PASSWORD` are
its administrator (`backend/README.md`), which the tester uses to publish.
The installer downloads the collector built by CI and the cores (sing-box,
Xray; set `CORES_URL` to a mirror if GitHub is not reachable), and installs
a systemd timer that runs every two hours (`EVERY=1h` changes that; `REAL`
is how many servers a run connects through, 1500 by default).

Follow a run with `journalctl -fu albatross-tester`. A run takes a few
minutes; it tests the current `hot.txt` and the top of `fresh.txt`.

Several testers (different providers, mobile and fixed) can report in turn;
the latest report wins.
