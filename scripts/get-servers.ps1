$session = New-Object Microsoft.PowerShell.Commands.WebRequestSession
$loginBody = '{"username":"admin","password":"ms-pmyZ2SZrrt17yj_"}'
$r = Invoke-WebRequest -Uri 'http://127.0.0.1:9090/api/login' -Method POST -Body $loginBody -ContentType 'application/json' -UseBasicParsing -WebSession $session
Write-Host "Login: $($r.StatusCode)"

# Get VPN servers list
$r = Invoke-WebRequest -Uri 'http://127.0.0.1:9090/api/vpn/servers' -UseBasicParsing -WebSession $session
Write-Host "Servers: $($r.Content)"
