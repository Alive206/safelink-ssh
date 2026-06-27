$session = New-Object Microsoft.PowerShell.Commands.WebRequestSession
$loginBody = '{"username":"admin","password":"admin123"}'
$r = Invoke-WebRequest -Uri 'http://127.0.0.1:9090/api/login' -Method POST -Body $loginBody -ContentType 'application/json' -UseBasicParsing -WebSession $session
Write-Host "Login: $($r.StatusCode)"

# Get tunnel status
$r = Invoke-WebRequest -Uri 'http://127.0.0.1:9090/api/tunnels' -UseBasicParsing -WebSession $session
Write-Host "Tunnels:"
Write-Host $r.Content

# Check Windows routes
Write-Host "`n=== Current Routes (default) ==="
route print 0.0.0.0 | Select-String "0.0.0.0"

Write-Host "`n=== TUN interface ==="
Get-NetAdapter | Where-Object { $_.InterfaceDescription -like "*Wintun*" -or $_.Name -like "*SafeLink*" } | Format-Table Name, Status, ifIndex
