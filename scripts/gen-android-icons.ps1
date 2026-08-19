# Generates Android launcher icon resources from the shared SafeLink app icon.
#
# The source artwork (client/build/appicon.png) is also the Wails desktop icon,
# so all platforms stay visually identical. Run this again after replacing it.
param(
    [string]$Source = "",
    # Adaptive icon background. The foreground artwork has a transparent
    # background, so this colour is what shows around the shield.
    [string]$BackgroundColor = "#EEF8FB",
    # Render preview tiles (circle / squircle / square masks) instead of only
    # writing resources.
    [switch]$Preview,
    [string]$PreviewPath = ""
)

$ErrorActionPreference = "Stop"
Add-Type -AssemblyName System.Drawing

$RepoRoot = Split-Path -Parent $PSScriptRoot
if (-not $Source) { $Source = Join-Path $RepoRoot "client\build\appicon.png" }
$ResRoot = Join-Path $RepoRoot "android\android\app\src\main\res"

if (-not (Test-Path $Source)) { throw "Source icon not found: $Source" }

# Density buckets: adaptive foreground layers are 108dp, legacy icons are 48dp.
$Densities = [ordered]@{
    "mdpi"    = 1.0
    "hdpi"    = 1.5
    "xhdpi"   = 2.0
    "xxhdpi"  = 3.0
    "xxxhdpi" = 4.0
}

$src = New-Object System.Drawing.Bitmap($Source)
Write-Host "Source: $Source ($($src.Width)x$($src.Height))"

# --- Measure the visible artwork ------------------------------------------
# The adaptive foreground must fit inside the 72dp safe zone of the 108dp
# layer, and circular masks clip to a 72dp *circle*. Fitting the bounding box
# would slice off the diagonal lightning tips, so we fit the smallest circle
# that encloses every visible pixel instead.
$w = $src.Width
$h = $src.Height
$lockRect = New-Object System.Drawing.Rectangle 0, 0, $w, $h
$data = $src.LockBits($lockRect, [System.Drawing.Imaging.ImageLockMode]::ReadOnly, [System.Drawing.Imaging.PixelFormat]::Format32bppArgb)
$stride = $data.Stride
$bytes = New-Object byte[] ($stride * $h)
[System.Runtime.InteropServices.Marshal]::Copy($data.Scan0, $bytes, 0, $bytes.Length)
$src.UnlockBits($data)

$alphaFloor = 32
$minX = $w; $minY = $h; $maxX = -1; $maxY = -1
for ($y = 0; $y -lt $h; $y++) {
    $row = $y * $stride
    for ($x = 0; $x -lt $w; $x++) {
        if ($bytes[$row + $x * 4 + 3] -lt $alphaFloor) { continue }
        if ($x -lt $minX) { $minX = $x }
        if ($x -gt $maxX) { $maxX = $x }
        if ($y -lt $minY) { $minY = $y }
        if ($y -gt $maxY) { $maxY = $y }
    }
}
if ($maxX -lt 0) { throw "Source icon has no visible pixels." }

$centerX = ($minX + $maxX) / 2.0
$centerY = ($minY + $maxY) / 2.0
$radiusSq = 0.0
for ($y = $minY; $y -le $maxY; $y++) {
    $row = $y * $stride
    $dy = $y - $centerY
    $dySq = $dy * $dy
    for ($x = $minX; $x -le $maxX; $x++) {
        if ($bytes[$row + $x * 4 + 3] -lt $alphaFloor) { continue }
        $dx = $x - $centerX
        $d = $dx * $dx + $dySq
        if ($d -gt $radiusSq) { $radiusSq = $d }
    }
}
$radius = [math]::Sqrt($radiusSq)
$boxW = $maxX - $minX + 1
$boxH = $maxY - $minY + 1
Write-Host ("Artwork bounds: {0}x{1} at ({2},{3}); enclosing radius {4:N1}px" -f $boxW, $boxH, $minX, $minY, $radius)

function New-Canvas([int]$size) {
    $bmp = New-Object System.Drawing.Bitmap($size, $size, [System.Drawing.Imaging.PixelFormat]::Format32bppArgb)
    $bmp.SetResolution(72, 72)
    return $bmp
}

function New-Graphics($bmp) {
    $g = [System.Drawing.Graphics]::FromImage($bmp)
    $g.InterpolationMode = [System.Drawing.Drawing2D.InterpolationMode]::HighQualityBicubic
    $g.PixelOffsetMode = [System.Drawing.Drawing2D.PixelOffsetMode]::HighQuality
    $g.CompositingQuality = [System.Drawing.Drawing2D.CompositingQuality]::HighQuality
    $g.SmoothingMode = [System.Drawing.Drawing2D.SmoothingMode]::AntiAlias
    return $g
}

# Draws the artwork onto $g, scaled by $scale, with the artwork anchor point
# ($anchorX,$anchorY) landing on the centre of a $size canvas.
function Draw-Artwork($g, [double]$scale, [double]$anchorX, [double]$anchorY, [int]$size) {
    $destX = $size / 2.0 - $anchorX * $scale
    $destY = $size / 2.0 - $anchorY * $scale
    $destRect = New-Object System.Drawing.Rectangle(
        [int][math]::Round($destX),
        [int][math]::Round($destY),
        [int][math]::Round($w * $scale),
        [int][math]::Round($h * $scale))
    $attr = New-Object System.Drawing.Imaging.ImageAttributes
    $attr.SetWrapMode([System.Drawing.Drawing2D.WrapMode]::TileFlipXY)
    $g.DrawImage($src, $destRect, 0, 0, $w, $h, [System.Drawing.GraphicsUnit]::Pixel, $attr)
    $attr.Dispose()
}

# Adaptive foreground: enclosing circle -> 72dp safe circle of the 108dp layer.
function New-ForegroundLayer([double]$density) {
    $size = [int][math]::Round(108 * $density)
    $bmp = New-Canvas $size
    $g = New-Graphics $bmp
    $scale = (36.0 * $density) / $radius
    Draw-Artwork $g $scale $centerX $centerY $size
    $g.Dispose()
    return $bmp
}

# Legacy square icon: bounding box fills the tile minus a small margin.
function New-LegacyIcon([double]$density) {
    $size = [int][math]::Round(48 * $density)
    $bmp = New-Canvas $size
    $g = New-Graphics $bmp
    $scale = ($size * 0.96) / [math]::Max($boxW, $boxH)
    Draw-Artwork $g $scale $centerX $centerY $size
    $g.Dispose()
    return $bmp
}

if ($Preview) {
    if (-not $PreviewPath) { $PreviewPath = Join-Path $RepoRoot ".icontmp\preview.png" }
    New-Item -ItemType Directory -Force -Path (Split-Path -Parent $PreviewPath) | Out-Null

    $tile = 256
    $gap = 24
    $sheet = New-Object System.Drawing.Bitmap(($tile * 3 + $gap * 4), ($tile + $gap * 2), [System.Drawing.Imaging.PixelFormat]::Format32bppArgb)
    $pg = New-Graphics $sheet
    $pg.Clear([System.Drawing.ColorTranslator]::FromHtml("#9AA3AB"))

    # 256px tile == the 72dp visible area, so render the 108dp layer at 384px
    # and centre-crop it, exactly like a launcher does.
    $layerSize = [int]($tile * 108 / 72)
    $layer = New-Canvas $layerSize
    $lg = New-Graphics $layer
    $sourceLayer = New-ForegroundLayer 4.0
    $lg.DrawImage($sourceLayer, 0, 0, $layerSize, $layerSize)
    $sourceLayer.Dispose()
    $lg.Dispose()
    $crop = New-Object System.Drawing.Rectangle ([int](($layerSize - $tile) / 2)), ([int](($layerSize - $tile) / 2)), $tile, $tile

    $bg = [System.Drawing.ColorTranslator]::FromHtml($BackgroundColor)
    $brush = New-Object System.Drawing.SolidBrush($bg)
    for ($i = 0; $i -lt 3; $i++) {
        $x = $gap + $i * ($tile + $gap)
        $path = New-Object System.Drawing.Drawing2D.GraphicsPath
        switch ($i) {
            0 { $path.AddEllipse($x, $gap, $tile, $tile) }
            1 {
                $r = [int]($tile * 0.28)
                $path.AddArc($x, $gap, $r, $r, 180, 90)
                $path.AddArc(($x + $tile - $r), $gap, $r, $r, 270, 90)
                $path.AddArc(($x + $tile - $r), ($gap + $tile - $r), $r, $r, 0, 90)
                $path.AddArc($x, ($gap + $tile - $r), $r, $r, 90, 90)
                $path.CloseFigure()
            }
            2 { $path.AddRectangle((New-Object System.Drawing.Rectangle $x, $gap, $tile, $tile)) }
        }
        $state = $pg.Save()
        $pg.SetClip($path)
        $pg.FillPath($brush, $path)
        $pg.DrawImage($layer, (New-Object System.Drawing.Rectangle $x, $gap, $tile, $tile), $crop.X, $crop.Y, $crop.Width, $crop.Height, [System.Drawing.GraphicsUnit]::Pixel)
        $pg.Restore($state)
        $path.Dispose()
    }
    $brush.Dispose()
    $pg.Dispose()
    $layer.Dispose()
    $sheet.Save($PreviewPath, [System.Drawing.Imaging.ImageFormat]::Png)
    $sheet.Dispose()
    Write-Host "Preview written: $PreviewPath"
}

foreach ($bucket in $Densities.Keys) {
    $density = $Densities[$bucket]
    $dir = Join-Path $ResRoot "mipmap-$bucket"
    New-Item -ItemType Directory -Force -Path $dir | Out-Null

    $fg = New-ForegroundLayer $density
    $fgPath = Join-Path $dir "ic_launcher_foreground.png"
    $fg.Save($fgPath, [System.Drawing.Imaging.ImageFormat]::Png)
    Write-Host "  wrote mipmap-$bucket\ic_launcher_foreground.png ($($fg.Width)px)"
    $fg.Dispose()

    $legacy = New-LegacyIcon $density
    $legacyPath = Join-Path $dir "ic_launcher.png"
    $legacy.Save($legacyPath, [System.Drawing.Imaging.ImageFormat]::Png)
    Write-Host "  wrote mipmap-$bucket\ic_launcher.png ($($legacy.Width)px)"
    $legacy.Dispose()
}

$src.Dispose()

# aapt2 prefers XML without a byte order mark, which Set-Content -Encoding UTF8
# would add on PowerShell 5.
$utf8NoBom = New-Object System.Text.UTF8Encoding($false)

$anyDpi = Join-Path $ResRoot "mipmap-anydpi-v26"
New-Item -ItemType Directory -Force -Path $anyDpi | Out-Null
$adaptiveXml = @"
<?xml version="1.0" encoding="utf-8"?>
<adaptive-icon xmlns:android="http://schemas.android.com/apk/res/android">
    <background android:drawable="@color/ic_launcher_background" />
    <foreground android:drawable="@mipmap/ic_launcher_foreground" />
</adaptive-icon>
"@
[System.IO.File]::WriteAllText((Join-Path $anyDpi "ic_launcher.xml"), ($adaptiveXml + "`r`n"), $utf8NoBom)

$values = Join-Path $ResRoot "values"
New-Item -ItemType Directory -Force -Path $values | Out-Null
$colorXml = @"
<?xml version="1.0" encoding="utf-8"?>
<resources>
    <color name="ic_launcher_background">$BackgroundColor</color>
</resources>
"@
[System.IO.File]::WriteAllText((Join-Path $values "ic_launcher_background.xml"), ($colorXml + "`r`n"), $utf8NoBom)

Write-Host "Done. Adaptive icon background: $BackgroundColor"
