#!/usr/bin/env bash
# Installs the debuggable x86_64 APK on a running emulator, lets it use the
# VPN, asks it to connect (SelfTest.java) and passes once it reports a
# working connection. Prints the app's log either way.
set -uo pipefail
summary() { [ -n "${GITHUB_STEP_SUMMARY:-}" ] && echo "- $1" >> "$GITHUB_STEP_SUMMARY"; echo "$1"; }
apk="$(ls android/app/build/outputs/apk/debug/*.apk | head -1)"
adb install -r "$apk"
adb shell appops set app.albatross.vpn ACTIVATE_VPN allow
adb logcat -c
adb shell am start -a app.albatross.vpn.SELF_TEST -n app.albatross.vpn/.MainActivity
ok=1
for i in $(seq 1 60); do
  sleep 5
  if adb logcat -d -s AlbatrossTest:I | grep -q '"status":"connected"'; then ok=0; break; fi
done
if [ $ok -eq 0 ]; then
  summary "Android emulator: connected through the VPN."
  adb shell ip -o link | grep -E ': tun[0-9]+:' || true
  # SelfTest disconnects 15 s after connecting; the tunnel must then go
  # (a core that inherited it used to keep the VPN up).
  for i in $(seq 1 12); do
    sleep 5
    if adb logcat -d -s AlbatrossTest:I | grep -q 'disconnect: 200'; then break; fi
  done
  sleep 10
  if adb shell ip -o link | grep -qE ': tun[0-9]+:'; then
    summary "Android emulator: FAIL, the VPN is still up after Disconnect." >&2
    adb shell ip -o link | grep -E ': tun[0-9]+:'
    ok=1
  else
    summary "Android emulator: Disconnect took the VPN down."
  fi
fi
adb logcat -d -s AlbatrossTest:I | tail -c 60000
# Each core must start on this Android version: one built with the wrong Go
# panics before doing anything, and WARP and Tor are not used above.
libdir="$(adb shell pm dump app.albatross.vpn | tr -d '\r' | sed -n 's/.*legacyNativeLibraryDir=//p' | head -1)/x86_64"
for c in "libsingbox.so version" "libxray.so version" "libwarpplus.so --help" "libtor.so --version"; do
  out="$(adb shell run-as app.albatross.vpn "$libdir/${c% *}" "${c#* }" 2>&1 | tr -d '\r' | head -c 4000)"
  echo "== $c: $(printf '%s' "$out" | head -3)"
  if printf '%s' "$out" | grep -qE 'panic:|fatal error:|SIGSYS'; then
    summary "Android emulator: FAIL, ${c% *} crashes at start: $(printf '%s' "$out" | grep -m1 -E 'panic:|fatal error:|SIGSYS')" >&2
    ok=1
  fi
done
[ $ok -eq 0 ] || summary "Android emulator: FAIL (see the log above)." >&2
exit $ok
