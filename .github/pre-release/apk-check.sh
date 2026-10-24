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

# The Build run's APK (one file for every phone, ARM only since PR #38),
# 0.3.3 for the upgrade check, and the debuggable x86_64 build for Android
# versions whose emulators cannot run ARM code.
universal="$(ls apk/*.apk | grep -viE 'arm64-v8a|armeabi|x86|old-' | head -1 || true)"
x64="$(ls apk/*x86_64*.apk 2>/dev/null | head -1 || true)"
debug="$(ls apk/debug/*.apk 2>/dev/null | head -1 || true)"
apkfile="${universal:-$x64}"
adb wait-for-device
abis="$(sh_ getprop ro.product.cpu.abilist)"
log "INFO API $api, testing ${apkfile:-only the debuggable build $debug}; device ABIs: $abis"
log "INFO WebView: $(sh_ dumpsys webviewupdate | grep -m1 -i 'Current WebView package') $(sh_ dumpsys package com.android.webview | grep -m1 versionName) $(sh_ dumpsys package com.google.android.webview | grep -m1 versionName)"

pid_of() {
  local p
  p="$(sh_ pidof "$pkg" 2>/dev/null | grep -oE '^[0-9]+' || true)"
  [ -n "$p" ] || p="$(sh_ ps -A 2>/dev/null | awk -v n="$pkg" '$NF==n {print $2}' | head -1)"
  [ -n "$p" ] || p="$(sh_ ps | awk -v n="$pkg" '$NF==n {print $2}' | head -1)"
  echo "$p"
}

# Dialogs the app opens at first start would cover the screenshot.
grant() {
  [ "$api" -ge 23 ] && sh_ dumpsys deviceidle whitelist +"$pkg" > /dev/null
  [ "$api" -ge 33 ] && sh_ pm grant "$pkg" android.permission.POST_NOTIFICATIONS > /dev/null
  sh_ appops set "$pkg" ACTIVATE_VPN allow > /dev/null
}

launch() { # label
  local label="$1" pid crash abi js killed
  grant
  adb logcat -c
  sh_ am start -W -n "$pkg/.MainActivity" > "$out/$label-start.txt"
  sleep 30
  pid="$(pid_of)"
  adb exec-out screencap -p > "$out/$label.png" 2>/dev/null
  adb logcat -d > "$out/$label-logcat.txt" 2>&1
  # Only the app's own process counts (other apps on the image crash too).
  crash="$(grep -E 'FATAL EXCEPTION|Fatal signal|Cause: seccomp|UnsatisfiedLinkError' "$out/$label-logcat.txt" | grep -E 'p\.albatross\.vpn|app\.albatross|libgojni|seccomp' | head -4 | cut -c1-220 | tr '\n' ' ')"
  js="$(grep -E 'Uncaught (SyntaxError|ReferenceError|TypeError)' "$out/$label-logcat.txt" | grep -m2 -E 'app\.js|127\.0\.0\.1' | cut -c1-220 | tr '\n' ' ')"
  killed="$(grep -m1 -E 'Killing [0-9]+:app\.albatross\.vpn' "$out/$label-logcat.txt" | grep -vE 'crash|signal' | cut -c1-200)"
  abi="$(sh_ dumpsys package "$pkg" | grep -m1 primaryCpuAbi | tr -d ' ')"
  if [ -n "$crash" ]; then log "FAIL $label: app crashed ($abi): $crash"
  elif [ -n "$js" ]; then log "FAIL $label: the page's script does not run in this WebView, the UI is dead: $js"
  elif [ -n "$pid" ]; then log "PASS $label: app running, page script loaded (pid $pid, $abi)"
  elif [ -n "$killed" ]; then log "SKIP $label: the emulator killed the app for its own reasons: $killed"
  else log "FAIL $label: app not running after 30 s ($abi)"; fi
}

cores() { # label abi-dir
  local label="$1" dir="$2" libdir res c bin arg
  adb root > /dev/null 2>&1
  sleep 4
  adb wait-for-device
  libdir="$(sh_ pm path "$pkg" | head -1 | sed 's/^package://; s#/base\.apk$##')/lib/$dir"
  for c in "libsingbox.so version" "libxray.so version" "libtor.so --version" "libwarpplus.so --help" "liblyrebird.so -version"; do
    bin="${c%% *}"
    arg="${c#* }"
    res="$(timeout 60 adb shell "cd $libdir && ./$bin $arg" 2>&1 | tr -d '\r' | grep -v 'unsupported flags' | head -3 | tr '\n' ' ')"
    if echo "$res" | grep -qiE 'not found|inaccessible|denied|cannot|can.t execute|exec format|CANNOT LINK|No such file|Bad system call|Segmentation|panic:|fatal error:|SIGSYS'; then
      log "FAIL $label core $bin: ${res:0:200}"
    elif echo "$res" | grep -qiE 'version|usage|commands|flags|^Xray [0-9]|^lyrebird [0-9]'; then
      log "PASS $label core $bin runs: ${res:0:100}"
    else
      log "FAIL $label core $bin: unexpected output: ${res:0:200}"
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
if [ "$api" -ge 30 ] && [ -n "$apkfile" ] && [ -f apk/old-0.3.3.apk ] && echo "$abis" | grep -q arm64-v8a; then
  res="$(install old033 apk/old-0.3.3.apk)"
  if echo "$res" | grep -q Success; then
    # Baseline: does the released 0.3.3 (arm64) run under this emulator's
    # ARM translation? If it crashes too, an arm64 crash below is the
    # translator's, not the new build's.
    launch old033-arm64
    res="$(install over033 -r "$apkfile")"
    if echo "$res" | grep -q VERSION_DOWNGRADE; then
      log "INFO over 0.3.3: test build has a lower versionCode; retrying with -d"
      res="$(install over033 -r -d "$apkfile")"
    fi
    if echo "$res" | grep -q Success; then
      log "PASS installs over 0.3.3 (same signing key)"
      launch over033
    elif echo "$res" | grep -qE 'UPDATE_INCOMPATIBLE|signatures do not match'; then log "FAIL installs over 0.3.3: signature mismatch: $res"
    elif echo "$res" | grep -q VERSION_DOWNGRADE; then log "SKIP installs over 0.3.3: downgrade refused for a test build ($res)"
    else log "FAIL installs over 0.3.3: $res"; fi
  else
    log "SKIP installs over 0.3.3: 0.3.3 does not install here: $res"
  fi
  adb uninstall "$pkg" > /dev/null 2>&1
fi

# 2. Fresh install for this emulator's own ABI: the Build run's APK when it
# has x86_64 code, otherwise the debuggable x86_64 build of the same code.
if [ -n "$apkfile" ] && unzip -l "$apkfile" | grep -q 'lib/x86_64/'; then
  own="$apkfile" ownlabel=native
else
  own="$debug" ownlabel=debug-x86_64
  [ -n "$own" ] && log "INFO $(basename "$apkfile") has no x86_64 code; the x86_64 checks use the debuggable build $(basename "$own")"
fi
if [ -n "$own" ]; then
  adb uninstall "$pkg" > /dev/null 2>&1
  res="$(install "$ownlabel" "$own")"
  if echo "$res" | grep -q Success; then
    log "PASS $ownlabel install"
    launch "$ownlabel"
    cores "$ownlabel" x86_64
  else
    log "FAIL $ownlabel install: $res"
  fi
fi

# 3. Each ARM ABI of the Build run's APK, through the emulator's ARM
# translation (Android 11+ images that have it).
if [ "$api" -ge 30 ] && [ -n "$apkfile" ]; then
  for abi in arm64-v8a armeabi-v7a; do
    unzip -l "$apkfile" | grep -q "lib/$abi/" || { log "INFO no $abi code in $(basename "$apkfile")"; continue; }
    if ! echo "$abis" | grep -q "$abi"; then log "SKIP $abi: this emulator image cannot run $abi code"; continue; fi
    adb uninstall "$pkg" > /dev/null 2>&1
    res="$(install "$abi" --abi "$abi" "$apkfile")"
    if echo "$res" | grep -q Success; then
      log "PASS install as $abi"
      launch "$abi"
      cores "$abi" "$(echo "$abi" | sed 's/armeabi-v7a/arm/; s/arm64-v8a/arm64/')"
    else
      log "FAIL install as $abi: $res"
    fi
  done
elif [ -n "$apkfile" ] && ! unzip -l "$apkfile" | grep -q 'lib/x86_64/'; then
  log "INFO $(basename "$apkfile") itself cannot run on an x86_64 emulator before Android 11 (no ARM translation)"
fi
exit 0
