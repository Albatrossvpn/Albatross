; NSIS installer for Orbot for Windows (x64)
Unicode true
!include "MUI2.nsh"
!include "x64.nsh"

Name "Orbot"
OutFile "${OUT}"
InstallDir "$PROGRAMFILES64\Orbot"
InstallDirRegKey HKLM "Software\OrbotWindows" "InstallDir"
RequestExecutionLevel admin
SetCompressor /SOLID lzma

!define MUI_ICON "${SRC}\..\..\winres\icon.ico"
!define MUI_UNICON "${SRC}\..\..\winres\icon.ico"
!insertmacro MUI_PAGE_DIRECTORY
!insertmacro MUI_PAGE_INSTFILES
!define MUI_FINISHPAGE_RUN "$INSTDIR\Orbot.exe"
!insertmacro MUI_PAGE_FINISH
!insertmacro MUI_UNPAGE_CONFIRM
!insertmacro MUI_UNPAGE_INSTFILES
!insertmacro MUI_LANGUAGE "Farsi"
!insertmacro MUI_LANGUAGE "English"

VIProductVersion "0.1.0.0"
VIAddVersionKey /LANG=${LANG_ENGLISH} "ProductName" "Orbot for Windows"
VIAddVersionKey /LANG=${LANG_ENGLISH} "FileVersion" "${VERSION}"
VIAddVersionKey /LANG=${LANG_ENGLISH} "FileDescription" "Orbot for Windows installer"
VIAddVersionKey /LANG=${LANG_ENGLISH} "LegalCopyright" "BSD-3-Clause"

Section "Install"
  ${IfNot} ${RunningX64}
    MessageBox MB_ICONSTOP "Orbot for Windows requires 64-bit Windows."
    Abort
  ${EndIf}
  SetRegView 64
  ; Make sure a running copy releases its files.
  nsExec::Exec 'taskkill /IM Orbot.exe /F'
  SetOutPath "$INSTDIR"
  File /r "${SRC}\*.*"
  WriteUninstaller "$INSTDIR\Uninstall.exe"
  CreateShortcut "$SMPROGRAMS\Orbot.lnk" "$INSTDIR\Orbot.exe"
  CreateShortcut "$DESKTOP\Orbot.lnk" "$INSTDIR\Orbot.exe"
  WriteRegStr HKLM "Software\OrbotWindows" "InstallDir" "$INSTDIR"
  WriteRegStr HKLM "Software\Microsoft\Windows\CurrentVersion\Uninstall\OrbotWindows" "DisplayName" "Orbot"
  WriteRegStr HKLM "Software\Microsoft\Windows\CurrentVersion\Uninstall\OrbotWindows" "DisplayVersion" "${VERSION}"
  WriteRegStr HKLM "Software\Microsoft\Windows\CurrentVersion\Uninstall\OrbotWindows" "DisplayIcon" "$INSTDIR\Orbot.exe"
  WriteRegStr HKLM "Software\Microsoft\Windows\CurrentVersion\Uninstall\OrbotWindows" "UninstallString" '"$INSTDIR\Uninstall.exe"'
  WriteRegDWORD HKLM "Software\Microsoft\Windows\CurrentVersion\Uninstall\OrbotWindows" "NoModify" 1
  WriteRegDWORD HKLM "Software\Microsoft\Windows\CurrentVersion\Uninstall\OrbotWindows" "NoRepair" 1
SectionEnd

Section "Uninstall"
  SetRegView 64
  nsExec::Exec 'taskkill /IM Orbot.exe /F'
  Delete "$SMPROGRAMS\Orbot.lnk"
  Delete "$DESKTOP\Orbot.lnk"
  RMDir /r "$INSTDIR"
  DeleteRegKey HKLM "Software\Microsoft\Windows\CurrentVersion\Uninstall\OrbotWindows"
  DeleteRegKey HKLM "Software\OrbotWindows"
SectionEnd
