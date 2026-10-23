#!/usr/bin/env bash
# Installs the debuggable x86_64 APK on a running emulator, lets it use the
# VPN, asks it to connect (SelfTest.java) and passes once it reports a
# working connection. Prints the app's log either way.
set -uo pipefail
summary() { [ -n "${GITHUB_STEP_SUMMARY:-}" ] && echo "- $1" >> "$GITHUB_STEP_SUMMARY"; echo "$1"; }
apk="$(ls android/app/build/outputs/apk/debug/*x86_64*.apk | head -1)"
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
  adb shell ip -o link | grep tun || true
  # SelfTest disconnects 15 s after connecting; the tunnel must then go
  # (a core that inherited it used to keep the VPN up).
  for i in $(seq 1 12); do
    sleep 5
    if adb logcat -d -s AlbatrossTest:I | grep -q 'disconnect: 200'; then break; fi
  done
  sleep 10
  if adb shell ip -o link | grep -q tun; then
    summary "Android emulator: FAIL, the VPN is still up after Disconnect." >&2
    adb shell ip -o link | grep tun
    ok=1
  else
    summary "Android emulator: Disconnect took the VPN down."
  fi
fi
adb logcat -d -s AlbatrossTest:I | tail -c 60000
[ $ok -eq 0 ] || summary "Android emulator: FAIL (see the log above)." >&2
exit $ok
