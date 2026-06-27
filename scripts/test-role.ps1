$r = Invoke-WebRequest -Uri 'http://127.0.0.1:9090/api/role' -UseBasicParsing -TimeoutSec 5
Write-Host "Status: $($r.StatusCode)"
Write-Host "Body: $($r.Content)"
