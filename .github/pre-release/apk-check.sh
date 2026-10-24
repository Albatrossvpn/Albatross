#!/usr/bin/env bash
# Installs the Build run's APK on the running emulator, starts it, takes a
# screenshot and the log, runs each core once, and on Android 11+ also
# installs it over 0.3.3 and runs its 32-bit and 64-bit ARM code through the
# emulator's ARM translation. One PASS/FAIL/SKIP line per check in
# out/result.txt.
set -u
api="$1"
out=out
mkdir -p "$out"
pkg=app.albatross.vpn
log() { echo "$*" | tee -a "$out/result.txt"; }
sh_() { adb shell "$@" 2>&1 | tr -d '\r'; }

universal="$(ls apk/*.apk | grep -viE 'arm64-v8a|armeabi|x86|old-' | head -1 || true)"
x64="$(ls apk/*x86_64*.apk 2>/dev/null | head -1 || true)"
apkfile="${universal:-$x64}"
adb wait-for-device
log "INFO API $api, testing $(basename "$apkfile"); device ABIs: $(sh_ getprop ro.product.cpu.abilist)"
log "INFO WebView: $(sh_ dumpsys webviewupdate | grep -m1 -i 'Current WebView package') $(sh_ dumpsys package com.android.webview | grep -m1 versionName) $(sh_ dumpsys package com.google.android.webview | grep -m1 versionName)"

pid_of() {
  local p
  p="$(sh_ pidof "$pkg" 2>/dev/null | grep -E '^[0-9 ]+$' || true)"
  [ -n "$p" ] || p="$(sh_ ps | awk -v n="$pkg" '$NF==n {print $2}' | head -1)"
  echo "$p"
}

launch() { # label
  local label="$1" pid crash abi
  adb logcat -c
  sh_ appops set "$pkg" ACTIVATE_VPN allow > /dev/null
  sh_ am start -W -n "$pkg/.MainActivity" > "$out/$label-start.txt"
  sleep 35
  pid="$(pid_of)"
  adb exec-out screencap -p > "$out/$label.png" 2>/dev/null || sh_ screencap -p /sdcard/s.png
  [ -s "$out/$label.png" ] || adb pull /sdcard/s.png "$out/$label.png" > /dev/null 2>&1
  adb logcat -d > "$out/$label-logcat.txt" 2>&1
  crash="$(grep -E 'FATAL EXCEPTION|UnsatisfiedLinkError|Process app.albatross.vpn .*has died|ANR in app.albatross|Fatal signal|chromium.*Uncaught|Uncaught SyntaxError|Uncaught ReferenceError|Uncaught TypeError' "$out/$label-logcat.txt" | head -5 | tr '\n' ' ')"
  abi="$(sh_ dumpsys package "$pkg" | grep -m1 primaryCpuAbi | tr -d ' ')"
  if [ -n "$pid" ] && [ -z "$crash" ]; then
    log "PASS $label: app running (pid $pid, $abi)"
  else
    log "FAIL $label: pid='$pid' $abi; $crash"
  fi
  grep -iE 'chromium|console|webview' "$out/$label-logcat.txt" | grep -iE 'error|uncaught|syntax' | head -10 | sed "s/^/INFO $label web: /" | tee -a "$out/result.txt" > /dev/null
}

cores() { # label abi-dir
  local label="$1" dir="$2" libdir res c bin arg
  adb root > /dev/null 2>&1
  sleep 4
  adb wait-for-device
  libdir="$(sh_ dumpsys package "$pkg" | grep -m1 legacyNativeLibraryDir | sed 's/.*=//')"
  for c in "libsingbox.so version" "libxray.so version" "libtor.so --version" "libwarpplus.so --help" "liblyrebird.so -version"; do
    bin="${c%% *}"
    arg="${c#* }"
    res="$(timeout 60 adb shell "$libdir/$dir/$bin $arg" 2>&1 | tr -d '\r' | head -3 | tr '\n' ' ')"
    if echo "$res" | grep -qiE 'version|usage|xray|tor |sing-box|lyrebird|warp'; then
      log "PASS $label core $bin runs: ${res:0:120}"
    else
      log "FAIL $label core $bin: ${res:0:200}"
    fi
  done
}

install() { # label args...
  local label="$1" res
  shift
  res="$(adb install "$@" 2>&1 | tr -d '\r' | tail -2 | tr '\n' ' ')"
  echo "$res" > "$out/$label-install.txt"
  echo "$res"
}

# 1. In place over 0.3.3 (arm64, runs through ARM translation on 11+).
if [ "$api" -ge 30 ] && [ -f apk/old-0.3.3.apk ]; then
  res="$(install old033 apk/old-0.3.3.apk)"
  if echo "$res" | grep -q Success; then
    res="$(install over033 -r "$apkfile")"
    if echo "$res" | grep -q VERSION_DOWNGRADE; then
      log "INFO over 0.3.3: test build has a lower versionCode; retrying with -d"
      res="$(install over033 -r -d "$apkfile")"
    fi
    if echo "$res" | grep -q Success; then log "PASS installs over 0.3.3 (same signing key)"
    elif echo "$res" | grep -qE 'UPDATE_INCOMPATIBLE|signatures do not match'; then log "FAIL installs over 0.3.3: signature mismatch: $res"
    elif echo "$res" | grep -q VERSION_DOWNGRADE; then log "SKIP installs over 0.3.3: downgrade refused for a test build ($res)"
    else log "FAIL installs over 0.3.3: $res"; fi
  else
    log "SKIP installs over 0.3.3: 0.3.3 does not install here: $res"
  fi
  adb uninstall "$pkg" > /dev/null 2>&1
fi

# 2. Fresh install of the APK for this emulator's own ABI.
res="$(install fresh "$apkfile")"
if echo "$res" | grep -q Success; then
  log "PASS fresh install"
  launch native
  cores native x86_64
else
  log "FAIL fresh install: $res"
fi

# 3. The ARM code of a universal APK, through ARM translation (Android 11+).
if [ "$api" -ge 30 ] && [ -n "$universal" ]; then
  for abi in armeabi-v7a arm64-v8a; do
    if unzip -l "$universal" | grep -q "lib/$abi/"; then
      adb uninstall "$pkg" > /dev/null 2>&1
      res="$(install "$abi" --abi "$abi" "$universal")"
      if echo "$res" | grep -q Success; then
        log "PASS install as $abi"
        launch "$abi"
        cores "$abi" "$(echo "$abi" | sed 's/armeabi-v7a/arm/; s/arm64-v8a/arm64/')"
      else
        log "FAIL install as $abi: $res"
      fi
    else
      log "INFO no $abi libraries in $(basename "$universal")"
    fi
  done
fi
exit 0
