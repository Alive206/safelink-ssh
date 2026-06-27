[System.Reflection.Assembly]::LoadWithPartialName('System.Drawing') | Out-Null
$bmp = New-Object System.Drawing.Bitmap(32,32)
$g = [System.Drawing.Graphics]::FromImage($bmp)
$g.SmoothingMode = 'AntiAlias'
$g.Clear([System.Drawing.Color]::FromArgb(30,64,175))
$brush = New-Object System.Drawing.SolidBrush([System.Drawing.Color]::White)
$font = New-Object System.Drawing.Font('Segoe UI',13,[System.Drawing.FontStyle]::Bold)
$g.DrawString('SL',$font,$brush,2,5)
$g.Dispose()
$icon = [System.Drawing.Icon]::FromHandle($bmp.GetHicon())
$fs = [System.IO.File]::Create("$PSScriptRoot\..\cmd\tray\icon.ico")
$icon.Save($fs)
$fs.Close()
$bmp.Dispose()
# Copy for installer too
Copy-Item "$PSScriptRoot\..\cmd\tray\icon.ico" "$PSScriptRoot\..\installer\icon.ico"
Write-Host "Icon created successfully"
