#!/usr/bin/env bash
# Connects the debuggable build through the VPN (SelfTest.java) on the running
# emulator, like build/android/selftest.sh, for the emulator's own ABI and,
# on Android 11+, for each ARM ABI in the APK through ARM translation.
set -u
api="$1"
out=out
mkdir -p "$out"
pkg=app.albatross.vpn
log() { echo "$*" | tee -a "$out/result.txt"; }

universal="$(ls apk/*.apk | grep -viE 'arm64-v8a|armeabi|x86' | head -1 || true)"
x64="$(ls apk/*x86_64*.apk 2>/dev/null | head -1 || true)"
adb wait-for-device

connect() { # label apk [install args]
  local label="$1" apkfile="$2" res ok=1 t
  shift 2
  adb uninstall "$pkg" > /dev/null 2>&1
  res="$(adb install "$@" "$apkfile" 2>&1 | tr -d '\r' | tail -1)"
  if ! echo "$res" | grep -q Success; then log "FAIL $label install: $res"; return; fi
  adb shell appops set "$pkg" ACTIVATE_VPN allow
  [ "$api" -ge 23 ] && adb shell dumpsys deviceidle whitelist +"$pkg" > /dev/null
  [ "$api" -ge 33 ] && adb shell pm grant "$pkg" android.permission.POST_NOTIFICATIONS > /dev/null
  adb logcat -c
  adb shell am start -a app.albatross.vpn.SELF_TEST -n "$pkg/.MainActivity"
  for t in $(seq 1 72); do
    sleep 5
    if adb logcat -d -s AlbatrossTest:I | grep -q '"status":"connected"'; then ok=0; break; fi
  done
  adb logcat -d -s AlbatrossTest:I > "$out/$label-selftest.txt" 2>&1
  adb logcat -d > "$out/$label-logcat.txt" 2>&1
  if [ $ok -eq 0 ]; then
    log "PASS $label: connected through the VPN after about $((t * 5)) s"
    for t in $(seq 1 12); do
      sleep 5
      adb logcat -d -s AlbatrossTest:I | grep -q 'disconnect: 200' && break
    done
    sleep 10
    if adb shell ip -o link 2>/dev/null | grep -qE ': tun[0-9]+:'; then log "FAIL $label: VPN still up after Disconnect"; else log "PASS $label: Disconnect took the VPN down"; fi
  else
    log "FAIL $label: no connection in 6 minutes; last lines: $(tail -c 600 "$out/$label-selftest.txt" | tr '\n' ' ')"
    grep -E 'FATAL EXCEPTION|UnsatisfiedLinkError|Fatal signal|Cause: seccomp' "$out/$label-logcat.txt" | grep -E 'albatross|libgojni|seccomp' | head -5 | cut -c1-220 | sed "s/^/INFO $label crash: /" | tee -a "$out/result.txt" > /dev/null
  fi
}

own="${universal:-$x64}"
abis="$(adb shell getprop ro.product.cpu.abilist | tr -d '\r')"
if [ -n "$own" ]; then connect native "$own"; else log "FAIL no debug APK for x86_64"; fi
if [ "$api" -ge 30 ] && [ -n "$universal" ]; then
  for abi in armeabi-v7a arm64-v8a; do
    unzip -l "$universal" | grep -q "lib/$abi/" || continue
    if echo "$abis" | grep -q "$abi"; then connect "$abi" "$universal" --abi "$abi"; else log "SKIP $abi: this emulator image cannot run $abi code"; fi
  done
fi
exit 0
