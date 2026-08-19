param(
    [string]$FlutterRoot = "D:\env\flutter",
    [string]$AndroidSdk = "D:\env\Androidsdk",
    [string]$AndroidNdk = "D:\env\Androidsdk\ndk\28.2.13676358",
    [string]$SingBoxVersion = "v1.13.14",
    # Mirrors sing-box's release/DEFAULT_BUILD_TAGS_OTHERS, which is the default
    # TAGS value of the upstream Makefile on non-Windows targets. Enables the
    # full protocol/feature set (QUIC-based hysteria/hysteria2/tuic, uTLS
    # fingerprints, WireGuard, Clash API, gVisor stack, ...).
    # 1.13 已将 with_reality_server 合入 with_utls，不能再单独传。
    [string]$SingBoxTags = "with_gvisor,with_quic,with_dhcp,with_wireguard,with_utls,with_acme,with_clash_api,with_tailscale,with_ccm,with_ocm,badlinkname,tfogo_checklinkname0",
    [switch]$RebuildSingBox
)

$ErrorActionPreference = "Stop"

$RepoRoot = Split-Path -Parent $PSScriptRoot
$ClientDir = Join-Path $RepoRoot "client"
$AndroidDir = Join-Path $RepoRoot "android"
$AARPath = Join-Path $AndroidDir "android\app\libs\mobilecore.aar"
$SingBoxLibPath = Join-Path $AndroidDir "android\app\src\main\jniLibs\arm64-v8a\libsing-box.so"

$env:ANDROID_HOME = $AndroidSdk
$env:ANDROID_SDK_ROOT = $AndroidSdk
$env:ANDROID_NDK_HOME = $AndroidNdk
$env:PUB_HOSTED_URL = "https://pub.flutter-io.cn"
$env:FLUTTER_STORAGE_BASE_URL = "https://storage.flutter-io.cn"
$env:PATH = "$FlutterRoot\bin;$AndroidSdk\platform-tools;$AndroidSdk\cmdline-tools\latest\bin;$(go env GOPATH)\bin;$env:PATH"

if ($RebuildSingBox -or -not (Test-Path $SingBoxLibPath)) {
    Write-Host "Building sing-box $SingBoxVersion for Android arm64..."
    $SingBoxBuildDir = Join-Path ([System.IO.Path]::GetTempPath()) "safelink-singbox-build"
    if (Test-Path $SingBoxBuildDir) {
        Remove-Item -Recurse -Force $SingBoxBuildDir
    }
    New-Item -ItemType Directory -Force -Path $SingBoxBuildDir | Out-Null
    New-Item -ItemType Directory -Force -Path (Split-Path -Parent $SingBoxLibPath) | Out-Null
    Push-Location $SingBoxBuildDir
    $OldGOOS = $env:GOOS
    $OldGOARCH = $env:GOARCH
    $OldCGO_ENABLED = $env:CGO_ENABLED
    $OldCC = $env:CC
    try {
        go mod init safelink-singbox-build | Out-Host
        go get "github.com/sagernet/sing-box/cmd/sing-box@$SingBoxVersion"
        $env:GOOS = "android"
        $env:GOARCH = "arm64"
        $env:CGO_ENABLED = "1"
        $env:CC = Join-Path $AndroidNdk "toolchains\llvm\prebuilt\windows-x86_64\bin\aarch64-linux-android29-clang.cmd"
        # The badlinkname / tfogo_checklinkname0 tags require -checklinkname=0,
        # and the multipathtcp default mirrors sing-box's release/LDFLAGS.
        $SingBoxLdflags = "-X internal/godebug.defaultGODEBUG=multipathtcp=0 -checklinkname=0 -X github.com/sagernet/sing-box/constant.Version=$($SingBoxVersion.TrimStart('v')) -s -w -buildid="
        go build -trimpath -tags "$SingBoxTags" -ldflags "$SingBoxLdflags" -o $SingBoxLibPath github.com/sagernet/sing-box/cmd/sing-box
        if ($LASTEXITCODE -ne 0) { throw "sing-box build failed with exit code $LASTEXITCODE" }
    } finally {
        if ($null -eq $OldGOOS) { Remove-Item Env:GOOS -ErrorAction SilentlyContinue } else { $env:GOOS = $OldGOOS }
        if ($null -eq $OldGOARCH) { Remove-Item Env:GOARCH -ErrorAction SilentlyContinue } else { $env:GOARCH = $OldGOARCH }
        if ($null -eq $OldCGO_ENABLED) { Remove-Item Env:CGO_ENABLED -ErrorAction SilentlyContinue } else { $env:CGO_ENABLED = $OldCGO_ENABLED }
        if ($null -eq $OldCC) { Remove-Item Env:CC -ErrorAction SilentlyContinue } else { $env:CC = $OldCC }
        Pop-Location
        Remove-Item -Recurse -Force $SingBoxBuildDir -ErrorAction SilentlyContinue
    }
} else {
    Write-Host "Using bundled sing-box Android arm64 binary: $SingBoxLibPath"
}

Write-Host "Building SafeLink mobilecore AAR..."
Push-Location $ClientDir
try {
    gomobile bind -target=android/arm64 -androidapi 29 -o $AARPath github.com/example/safelink/client/mobilecore
    if ($LASTEXITCODE -ne 0) { throw "gomobile bind failed with exit code $LASTEXITCODE" }
} finally {
    Pop-Location
}

Write-Host "Building SafeLink Android arm64 APK..."
Push-Location $AndroidDir
try {
    flutter pub get
    if ($LASTEXITCODE -ne 0) { throw "flutter pub get failed with exit code $LASTEXITCODE" }
    flutter build apk --release --target-platform android-arm64
    if ($LASTEXITCODE -ne 0) { throw "flutter build apk failed with exit code $LASTEXITCODE" }
} finally {
    Pop-Location
}

Write-Host "Done! Output: android\build\app\outputs\flutter-apk\app-release.apk"
