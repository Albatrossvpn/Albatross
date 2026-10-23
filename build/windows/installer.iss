; Azadrah VPN installer. Everything the app needs ships inside: the app,
; sing-box, Xray, warp-plus and the WebView2 bootstrapper (run only when
; WebView2 is missing). Build with:
;   iscc /DAppVersion=0.1.0 /DSourceDir=..\..\dist\Azadrah /DOutputDir=..\..\dist build\windows\installer.iss

#ifndef AppVersion
  #define AppVersion "0.0.0"
#endif
#ifndef SourceDir
  #define SourceDir "..\..\dist\Azadrah"
#endif
#ifndef OutputDir
  #define OutputDir "..\..\dist"
#endif

[Setup]
AppId={{6E3C2B1A-8F4D-4B5E-9C7A-A2D5E8F1B3C4}
AppName=Azadrah VPN
AppVersion={#AppVersion}
AppVerName=Azadrah VPN {#AppVersion}
AppPublisher=Azadrah
DefaultDirName={autopf}\Azadrah
DefaultGroupName=Azadrah VPN
DisableProgramGroupPage=yes
; Install for the current user by default so no UAC prompt is needed.
PrivilegesRequired=lowest
PrivilegesRequiredOverridesAllowed=dialog
OutputDir={#OutputDir}
OutputBaseFilename=Azadrah-Setup-{#AppVersion}
SetupIconFile=icon.ico
UninstallDisplayIcon={app}\Azadrah.exe
Compression=lzma2/ultra64
SolidCompression=yes
WizardStyle=modern
ArchitecturesAllowed=x64compatible
ArchitecturesInstallIn64BitMode=x64compatible
MinVersion=10.0
CloseApplications=force
RestartApplications=no

[Languages]
Name: "english"; MessagesFile: "compiler:Default.isl"

[Tasks]
Name: "desktopicon"; Description: "{cm:CreateDesktopIcon}"; GroupDescription: "{cm:AdditionalIcons}"

[Files]
Source: "{#SourceDir}\Azadrah.exe"; DestDir: "{app}"; Flags: ignoreversion
Source: "{#SourceDir}\core\*"; DestDir: "{app}\core"; Flags: ignoreversion recursesubdirs
Source: "{#SourceDir}\licenses\*"; DestDir: "{app}\licenses"; Flags: ignoreversion recursesubdirs
Source: "{#SourceDir}\README.txt"; DestDir: "{app}"; Flags: ignoreversion isreadme
Source: "MicrosoftEdgeWebview2Setup.exe"; DestDir: "{tmp}"; Flags: deleteafterinstall; Check: NeedsWebView2

[Icons]
Name: "{group}\Azadrah VPN"; Filename: "{app}\Azadrah.exe"
Name: "{group}\{cm:UninstallProgram,Azadrah VPN}"; Filename: "{uninstallexe}"
Name: "{autodesktop}\Azadrah VPN"; Filename: "{app}\Azadrah.exe"; Tasks: desktopicon

[Run]
Filename: "{tmp}\MicrosoftEdgeWebview2Setup.exe"; Parameters: "/silent /install"; StatusMsg: "Installing Microsoft Edge WebView2..."; Check: NeedsWebView2; Flags: waituntilterminated
Filename: "{app}\Azadrah.exe"; Description: "{cm:LaunchProgram,Azadrah VPN}"; Flags: nowait postinstall skipifsilent

[UninstallDelete]
Type: filesandordirs; Name: "{localappdata}\Azadrah\cache"
Type: filesandordirs; Name: "{localappdata}\Azadrah\run"
Type: files; Name: "{localappdata}\Azadrah\pool.dat"

[Code]
const
  WebView2Key = 'SOFTWARE\Microsoft\EdgeUpdate\Clients\{F3017226-FE2A-4295-8BDF-00C3A9A7E4C5}';
  WebView2KeyWow = 'SOFTWARE\WOW6432Node\Microsoft\EdgeUpdate\Clients\{F3017226-FE2A-4295-8BDF-00C3A9A7E4C5}';
  ProxyKey = 'Software\Microsoft\Windows\CurrentVersion\Internet Settings';

function HasVersion(Root: Integer; Key: String): Boolean;
var
  V: String;
begin
  Result := RegQueryStringValue(Root, Key, 'pv', V) and (V <> '') and (V <> '0.0.0.0');
end;

function NeedsWebView2: Boolean;
begin
  Result := not (HasVersion(HKLM, WebView2KeyWow) or HasVersion(HKLM, WebView2Key) or HasVersion(HKCU, WebView2Key));
end;

// If the app was killed while connected, Windows may still point at its
// local proxy. Turn that off on uninstall so the internet keeps working.
procedure CurUninstallStepChanged(CurUninstallStep: TUninstallStep);
var
  Server: String;
begin
  if CurUninstallStep = usUninstall then
  begin
    if RegQueryStringValue(HKCU, ProxyKey, 'ProxyServer', Server) and (Pos('127.0.0.1:', Server) = 1) then
      RegWriteDWordValue(HKCU, ProxyKey, 'ProxyEnable', 0);
    RegDeleteValue(HKCU, 'Software\Microsoft\Windows\CurrentVersion\Run', 'Azadrah');
  end;
end;
