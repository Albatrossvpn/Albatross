#!/usr/bin/env bash
# Installs the debuggable x86_64 APK on a running emulator, lets it use the
# VPN, asks it to connect (SelfTest.java) and passes once it reports a
# working connection. Prints the app's log either way.
set -uo pipefail
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
adb logcat -d -s AlbatrossTest:I | tail -c 60000
if [ $ok -eq 0 ]; then
  echo "Connected through the VPN on the emulator."
  adb shell dumpsys connectivity | grep -m3 -i "VPN" || true
else
  echo "The app did not connect on the emulator." >&2
fi
exit $ok
