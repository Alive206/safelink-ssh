Set-Location "e:\project\safelink-ssh"
# Generate bcrypt hash for password "admin123"
$proc = New-Object System.Diagnostics.ProcessStartInfo
$proc.FileName = ".\safelink.exe"
$proc.Arguments = "passwd admin"
$proc.UseShellExecute = $false
$proc.RedirectStandardInput = $true
$proc.RedirectStandardOutput = $true
$proc.RedirectStandardError = $true
$p = [System.Diagnostics.Process]::Start($proc)
Start-Sleep -Milliseconds 500
$p.StandardInput.WriteLine("admin123")
$p.WaitForExit(5000)
Write-Host "stdout:"
Write-Host $p.StandardOutput.ReadToEnd()
Write-Host "stderr:"
Write-Host $p.StandardError.ReadToEnd()
