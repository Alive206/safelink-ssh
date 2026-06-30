param(
    [string]$Version = "1.0.0"
)

$ErrorActionPreference = "Stop"

$ScriptDir = Split-Path -Parent $MyInvocation.MyCommand.Path
$RepoRoot = Resolve-Path (Join-Path $ScriptDir "..")
$ClientDir = Join-Path $RepoRoot "client"
$FrontendDir = Join-Path $ClientDir "frontend"
$BuildBin = Join-Path $ClientDir "build\bin"
$PayloadDir = Join-Path $ClientDir "cmd\installer\payload"
$DistDir = Join-Path $RepoRoot "dist"
$InstallerName = "SafeLink-Setup-$Version-windows-amd64.exe"
$InstallerPath = Join-Path $DistDir $InstallerName

function Resolve-ChildPath {
    param(
        [Parameter(Mandatory = $true)][string]$Parent,
        [Parameter(Mandatory = $true)][string]$Child
    )
    $parentFull = [System.IO.Path]::GetFullPath($Parent)
    $childFull = [System.IO.Path]::GetFullPath($Child)
    if (-not $childFull.StartsWith($parentFull, [System.StringComparison]::OrdinalIgnoreCase)) {
        throw "Refusing to operate outside $parentFull`: $childFull"
    }
    return $childFull
}

Write-Host "Building SafeLink client frontend..."
Push-Location $FrontendDir
try {
    npm install
    npm run build
} finally {
    Pop-Location
}

Write-Host "Building SafeLink Windows binary..."
Push-Location $ClientDir
try {
    New-Item -ItemType Directory -Force -Path $BuildBin | Out-Null
    $wails = Get-Command wails -ErrorAction SilentlyContinue
    if ($wails) {
        & wails build -platform windows/amd64
        if ($LASTEXITCODE -ne 0) {
            throw "wails build failed with exit code $LASTEXITCODE"
        }
    } else {
        Write-Host "Wails CLI was not found; using Go fallback build."
        go run github.com/akavel/rsrc@v0.10.2 -manifest "build\windows\wails.exe.manifest" -ico "build\windows\icon.ico" -o "rsrc_windows_amd64.syso"
        go build -tags "desktop,production" -ldflags "-w -s -H windowsgui" -o "build\bin\SafeLink.exe" .
    }
} finally {
    Pop-Location
}

$safeLinkExe = Join-Path $BuildBin "SafeLink.exe"
if (-not (Test-Path $safeLinkExe)) {
    throw "SafeLink.exe was not built at $safeLinkExe"
}

$singBoxSource = Join-Path $ClientDir "bin\sing-box.exe"
if (Test-Path $singBoxSource) {
    Copy-Item -Force $singBoxSource (Join-Path $BuildBin "sing-box.exe")
} elseif (-not (Test-Path (Join-Path $BuildBin "sing-box.exe"))) {
    throw "sing-box.exe was not found. Put it at client\bin\sing-box.exe before building the installer."
}

$wintunBuild = Join-Path $BuildBin "wintun.dll"
$wintunRoot = Join-Path $RepoRoot "wintun.dll"
if (Test-Path $wintunBuild) {
    Write-Host "Using existing build\bin\wintun.dll"
} elseif (Test-Path $wintunRoot) {
    Copy-Item -Force $wintunRoot $wintunBuild
} else {
    throw "wintun.dll was not found. Put it at repo root or client\build\bin\wintun.dll before building the installer."
}

$PayloadDir = Resolve-ChildPath -Parent $ClientDir -Child $PayloadDir
if (Test-Path $PayloadDir) {
    Remove-Item -LiteralPath $PayloadDir -Recurse -Force
}
New-Item -ItemType Directory -Force -Path $PayloadDir | Out-Null

Write-Host "Preparing installer payload..."
Copy-Item -Force $safeLinkExe (Join-Path $PayloadDir "SafeLink.exe")
Copy-Item -Force (Join-Path $BuildBin "sing-box.exe") (Join-Path $PayloadDir "sing-box.exe")
Copy-Item -Force (Join-Path $BuildBin "wintun.dll") (Join-Path $PayloadDir "wintun.dll")

New-Item -ItemType Directory -Force -Path $DistDir | Out-Null

Write-Host "Building Windows installer..."
Push-Location $ClientDir
try {
    go build -tags installer -ldflags "-w -s -H windowsgui -X main.appVersion=$Version" -o $InstallerPath .\cmd\installer
} finally {
    Pop-Location
}

$payloadFiles = Get-ChildItem -File $PayloadDir | Select-Object -ExpandProperty Name
$unexpected = $payloadFiles | Where-Object { $_ -notin @("SafeLink.exe", "sing-box.exe", "wintun.dll") }
if ($unexpected) {
    throw "Unexpected files in installer payload: $($unexpected -join ', ')"
}

Write-Host ""
Write-Host "Installer created: $InstallerPath"
Write-Host "Payload files:"
Get-ChildItem -File $PayloadDir | Select-Object Name, Length | Format-Table -AutoSize
