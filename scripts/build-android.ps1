param(
    [string]$FlutterRoot = "D:\env\flutter",
    [string]$AndroidSdk = "D:\env\android-sdk",
    [string]$AndroidNdk = "D:\env\android-sdk\ndk\28.2.13676358",
    [string]$SingBoxVersion = "v1.13.14",
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
        go build -trimpath -ldflags="-s -w" -o $SingBoxLibPath github.com/sagernet/sing-box/cmd/sing-box
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
} finally {
    Pop-Location
}

Write-Host "Building SafeLink Android arm64 APK..."
Push-Location $AndroidDir
try {
    flutter pub get
    flutter build apk --release --target-platform android-arm64
} finally {
    Pop-Location
}

Write-Host "Done! Output: android\build\app\outputs\flutter-apk\app-release.apk"
