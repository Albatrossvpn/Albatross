# Upgrades a per-user 0.3.3 install, with 0.3.3 running, to the Build run's
# installer; checks the new app answers; checks an all-users 0.3.3 install
# is upgraded in place; and checks uninstall turns off a leftover system
# proxy and the autostart entry.
$ErrorActionPreference = 'Continue'
$fails = New-Object System.Collections.ArrayList
New-Item -ItemType Directory -Force win-out | Out-Null
function Check([bool]$ok, [string]$msg) {
  if ($ok) { "PASS $msg" } else { "FAIL $msg"; [void]$script:fails.Add($msg) }
}
$appId = '{3D7A91C2-5B48-4E6F-A0D3-7C12E9B84F65}_is1'
$userKey = "HKCU:\Software\Microsoft\Windows\CurrentVersion\Uninstall\$appId"
$machineKey = "HKLM:\Software\Microsoft\Windows\CurrentVersion\Uninstall\$appId"
$new = (Get-ChildItem setup\Albatross-Setup-*.exe | Select-Object -First 1).FullName
$newVersion = [regex]::Match($new, 'Setup-(.+)\.exe$').Groups[1].Value
"New installer: $new ($newVersion)"

function Run-Setup([string]$exe, [string[]]$extra, [string]$logName) {
  $a = @('/VERYSILENT', '/SUPPRESSMSGBOXES', '/NORESTART', "/LOG=$logName") + $extra
  $sw = [Diagnostics.Stopwatch]::StartNew()
  $p = Start-Process $exe -ArgumentList $a -Wait -PassThru
  Write-Host "  $([IO.Path]::GetFileName($exe)) $($extra -join ' '): exit $($p.ExitCode) in $([int]$sw.Elapsed.TotalSeconds) s"
  return [int]$p.ExitCode
}

function Uninstall-From([string]$key) {
  $u = Get-ItemProperty $key -ErrorAction SilentlyContinue
  if (-not $u) { return }
  $exe = $u.UninstallString.Trim('"')
  Start-Process $exe -ArgumentList '/VERYSILENT', '/SUPPRESSMSGBOXES', '/NORESTART' -Wait | Out-Null
  for ($i = 0; $i -lt 90 -and (Test-Path $key); $i++) { Start-Sleep 1 }
}

function Start-Headless([string]$exe, [int]$port, [string]$name) {
  $data = Join-Path $env:RUNNER_TEMP "alb-$name"
  $out = Join-Path $env:RUNNER_TEMP "alb-$name.txt"
  $p = Start-Process $exe -ArgumentList '--headless', '--ui-port', "$port", '--data', $data -RedirectStandardOutput $out -PassThru
  $url = $null
  for ($i = 0; $i -lt 30 -and -not $url; $i++) { Start-Sleep 1; $url = Get-Content $out -ErrorAction SilentlyContinue | Select-Object -First 1 }
  return @{ Process = $p; Url = $url }
}

# 1. Per-user 0.3.3, the installer's default (no UAC prompt).
"== Per-user upgrade with 0.3.3 running"
[void](Run-Setup "$PWD\old-0.3.3.exe" @('/CURRENTUSER') 'old-user.log')
$u = Get-ItemProperty $userKey -ErrorAction SilentlyContinue
Check ($u.DisplayVersion -eq '0.3.3') "0.3.3 installed for the current user ($($u.DisplayVersion), $($u.InstallLocation))"
$dir = "$($u.InstallLocation)".TrimEnd('\')
$oldExe = (Get-FileHash "$dir\Albatross.exe").Hash
$oldCore = (Get-FileHash "$dir\core\sing-box.exe").Hash
$run = Start-Headless "$dir\Albatross.exe" 18090 'old'
Check ($null -ne $run.Url -and -not $run.Process.HasExited) "0.3.3 running before the upgrade"
$code = Run-Setup $new @('/CURRENTUSER') 'new-user.log'
Check ($code -eq 0) "upgrade installer exit code 0 (got $code)"
$u2 = Get-ItemProperty $userKey -ErrorAction SilentlyContinue
Check ($u2.DisplayVersion -eq $newVersion) "uninstall entry shows $newVersion (got $($u2.DisplayVersion))"
Check ("$($u2.InstallLocation)" -eq "$($u.InstallLocation)") "same install folder"
Check ((Get-FileHash "$dir\Albatross.exe").Hash -ne $oldExe) "Albatross.exe replaced"
Check (Test-Path "$dir\core\sing-box.exe") "cores present after upgrade (sing-box changed: $((Get-FileHash "$dir\core\sing-box.exe").Hash -ne $oldCore))"
Start-Sleep 3
$left = @(Get-Process -ErrorAction SilentlyContinue | Where-Object { $_.Path -and $_.Path.StartsWith($dir) })
Check ($left.Count -eq 0) "no 0.3.3 process left running from the install folder ($(($left | ForEach-Object { $_.Name }) -join ', '))"
Select-String -Path new-user.log -Pattern 'in use|Failed|RestartManager|Restart Manager|DeleteFile|MoveFile|error' | Select-Object -First 15 | ForEach-Object { "  log: $($_.Line)" }
$after = Start-Headless "$dir\Albatross.exe" 18091 'new'
if ($after.Url) {
  $h = @{ 'X-Token' = $after.Url.Split('#')[1] }
  try {
    $s = Invoke-RestMethod -Uri 'http://127.0.0.1:18091/api/state' -Headers $h
    Check ([bool]$s.status) "upgraded app answers (status $($s.status))"
    $s | ConvertTo-Json -Depth 4 | Out-File win-out\state-after-upgrade.json
  } catch { Check $false "upgraded app answers: $_" }
  Stop-Process -Id $after.Process.Id -Force -ErrorAction SilentlyContinue
} else { Check $false "upgraded app starts" }
Get-Process -ErrorAction SilentlyContinue | Where-Object { $_.Path -and $_.Path.StartsWith($dir) } | Stop-Process -Force -ErrorAction SilentlyContinue

# 2. Uninstall turns off a system proxy left by a crash and removes autostart.
"== Uninstall"
$inet = 'HKCU:\Software\Microsoft\Windows\CurrentVersion\Internet Settings'
Set-ItemProperty $inet -Name ProxyEnable -Value 1 -Type DWord
Set-ItemProperty $inet -Name ProxyServer -Value '127.0.0.1:10808'
New-ItemProperty 'HKCU:\Software\Microsoft\Windows\CurrentVersion\Run' -Name Albatross -Value "`"$dir\Albatross.exe`"" -Force | Out-Null
Uninstall-From $userKey
Check (-not (Test-Path $userKey)) "uninstall entry removed"
Check (-not (Test-Path "$dir\Albatross.exe")) "app files removed"
Check ((Get-ItemProperty $inet).ProxyEnable -eq 0) "system proxy turned off by uninstall"
Check ($null -eq (Get-ItemProperty 'HKCU:\Software\Microsoft\Windows\CurrentVersion\Run' -Name Albatross -ErrorAction SilentlyContinue)) "autostart entry removed"
Set-ItemProperty $inet -Name ProxyEnable -Value 0 -Type DWord

# 3. All-users 0.3.3, then the new installer with no mode chosen (silent default).
"== All-users upgrade"
[void](Run-Setup "$PWD\old-0.3.3.exe" @('/ALLUSERS') 'old-machine.log')
$m = Get-ItemProperty $machineKey -ErrorAction SilentlyContinue
Check ($m.DisplayVersion -eq '0.3.3') "0.3.3 installed for all users ($($m.InstallLocation))"
[void](Run-Setup $new @() 'new-default.log')
$m2 = Get-ItemProperty $machineKey -ErrorAction SilentlyContinue
$u3 = Get-ItemProperty $userKey -ErrorAction SilentlyContinue
"  all-users entry: $($m2.DisplayVersion) $($m2.InstallLocation); per-user entry: $($u3.DisplayVersion) $($u3.InstallLocation)"
Check ($m2.DisplayVersion -eq $newVersion -and -not $u3) "all-users install upgraded in place, no second copy"
Uninstall-From $machineKey
Uninstall-From $userKey

if ($fails.Count) { "FAILED: $($fails.Count)"; exit 1 }
"All Windows checks passed"
