#define MyAppName "SafeLink"
#define MyAppVersion "1.0.0"
#define MyAppPublisher "SafeLink"
#define MyAppURL "https://github.com/example/safelink"
#define MyAppExeName "safelink-tray.exe"

[Setup]
AppId={{A1B2C3D4-E5F6-7890-ABCD-EF1234567890}
AppName={#MyAppName}
AppVersion={#MyAppVersion}
AppPublisher={#MyAppPublisher}
AppPublisherURL={#MyAppURL}
DefaultDirName={autopf}\{#MyAppName}
DefaultGroupName={#MyAppName}
DisableProgramGroupPage=yes
LicenseFile=license.txt
OutputDir=..\dist
OutputBaseFilename=SafeLink-{#MyAppVersion}-Setup
SetupIconFile=icon.ico
Compression=lzma2/ultra64
SolidCompression=yes
WizardStyle=modern
PrivilegesRequired=admin
ArchitecturesAllowed=x64compatible
ArchitecturesInstallIn64BitMode=x64compatible
CloseApplications=yes
CloseApplicationsFilter=safelink*.exe

[Languages]
Name: "chinesesimplified"; MessagesFile: "compiler:Languages\ChineseSimplified.isl"
Name: "english"; MessagesFile: "compiler:Default.isl"

[Tasks]
Name: "autostart"; Description: "开机自动启动 SafeLink"; GroupDescription: "附加选项:"; Flags: checked
Name: "desktopicon"; Description: "创建桌面快捷方式"; GroupDescription: "附加选项:"; Flags: unchecked

[Files]
Source: "..\safelink.exe"; DestDir: "{app}"; Flags: ignoreversion
Source: "..\safelink-tray.exe"; DestDir: "{app}"; Flags: ignoreversion
Source: "..\wintun.dll"; DestDir: "{app}"; Flags: ignoreversion
Source: "..\configs\safelink.yaml"; DestDir: "{app}\configs"; Flags: onlyifdoesntexist uninsneveruninstall
Source: "..\configs\tunnels.json"; DestDir: "{app}\configs"; Flags: onlyifdoesntexist uninsneveruninstall skipifsourcedoesntexist

[Icons]
Name: "{group}\{#MyAppName}"; Filename: "{app}\{#MyAppExeName}"
Name: "{group}\打开控制面板"; Filename: "http://127.0.0.1:9090"
Name: "{group}\卸载 {#MyAppName}"; Filename: "{uninstallexe}"
Name: "{autodesktop}\{#MyAppName}"; Filename: "{app}\{#MyAppExeName}"; Tasks: desktopicon

[Run]
; Create scheduled task for auto-start (runs as admin without UAC prompt)
Filename: "schtasks"; Parameters: "/Create /TN ""SafeLink"" /TR """"""{app}\safelink-tray.exe"""""" /SC ONLOGON /RL HIGHEST /F"; Flags: runhidden; Tasks: autostart
; Launch after install
Filename: "{app}\{#MyAppExeName}"; Description: "立即启动 SafeLink"; Flags: nowait postinstall skipifsilent

[UninstallRun]
Filename: "taskkill"; Parameters: "/F /IM safelink-tray.exe"; Flags: runhidden
Filename: "taskkill"; Parameters: "/F /IM safelink.exe"; Flags: runhidden
Filename: "schtasks"; Parameters: "/Delete /TN ""SafeLink"" /F"; Flags: runhidden

[UninstallDelete]
Type: files; Name: "{app}\safelink.log"

[Code]
procedure CurStepChanged(CurStep: TSetupStep);
begin
  if CurStep = ssInstall then
  begin
    // Kill running instances before replacing files
    Exec('taskkill', '/F /IM safelink-tray.exe', '', SW_HIDE, ewWaitUntilTerminated, ResultCode);
    Exec('taskkill', '/F /IM safelink.exe', '', SW_HIDE, ewWaitUntilTerminated, ResultCode);
    Sleep(1000);
  end;
end;

var
  ResultCode: Integer;
