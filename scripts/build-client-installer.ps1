param(
    [string]$Version = "3.0.0"
)

$ErrorActionPreference = "Stop"

$ScriptDir = Split-Path -Parent $MyInvocation.MyCommand.Path
$RepoRoot = Resolve-Path (Join-Path $ScriptDir "..")
$ClientDir = Join-Path $RepoRoot "client"
$FrontendDir = Join-Path $ClientDir "frontend"
$BuildBin = Join-Path $ClientDir "build\bin"
$PayloadDir = Join-Path $ClientDir "cmd\installer\payload"
$InstallerManifest = Join-Path $ClientDir "cmd\installer\installer.exe.manifest"
$InstallerIcon = Join-Path $ClientDir "build\windows\icon.ico"
$InstallerSyso = Join-Path $ClientDir "cmd\installer\rsrc_windows_amd64.syso"
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
    $built = $false
    if ($wails) {
        & wails build -platform windows/amd64
        if ($LASTEXITCODE -eq 0) {
            $built = $true
        } else {
            # Wails CLI 2.9.1 自带的 go.mod 解析器不认 Go 1.24 的 tool 指令，而
            # client/go.mod 里的 `tool golang.org/x/mobile/cmd/gobind` 是 Android 构建必需的。
            # 两边都不能动，所以 wails 失败就回退到 Go 直构：前端 dist 和 wailsjs 绑定
            # 都已经就位，wails 在这一步只是包一层资源。
            Write-Host "wails build failed with exit code $LASTEXITCODE; falling back to the Go build."
        }
    } else {
        Write-Host "Wails CLI was not found; using Go fallback build."
    }
    if (-not $built) {
        go run github.com/akavel/rsrc@v0.10.2 -manifest "build\windows\wails.exe.manifest" -ico "build\windows\icon.ico" -o "rsrc_windows_amd64.syso"
        if ($LASTEXITCODE -ne 0) {
            throw "rsrc failed with exit code $LASTEXITCODE"
        }
        go build -tags "desktop,production" -ldflags "-w -s -H windowsgui" -o "build\bin\SafeLink.exe" .
        if ($LASTEXITCODE -ne 0) {
            throw "go build failed with exit code $LASTEXITCODE"
        }
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
    go run github.com/akavel/rsrc@v0.10.2 -manifest $InstallerManifest -ico $InstallerIcon -o $InstallerSyso
    go build -tags installer -ldflags "-w -s -H windowsgui -X main.appVersion=$Version" -o $InstallerPath .\cmd\installer
} finally {
    Remove-Item -LiteralPath $InstallerSyso -Force -ErrorAction SilentlyContinue
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
