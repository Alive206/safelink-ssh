Start-Sleep -Seconds 3
try {
    $r = Invoke-WebRequest -Uri 'http://127.0.0.1:9090/api/role' -UseBasicParsing -TimeoutSec 5
    Write-Host "OK: $($r.Content)"
} catch {
    Write-Host "FAIL: $($_.Exception.Message)"
    # Check if process is running
    $p = Get-Process safelink -ErrorAction SilentlyContinue
    if ($p) {
        Write-Host "Process running: PID $($p.Id)"
    } else {
        Write-Host "Process NOT running"
    }
}
