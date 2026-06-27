Set-Location "e:\project\safelink-ssh"
$output = & .\safelink.exe -config configs\safelink.yaml -no-open 2>&1
Write-Host "Exit code: $LASTEXITCODE"
Write-Host "Output:"
$output | ForEach-Object { Write-Host $_ }
