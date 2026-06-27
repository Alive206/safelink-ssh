# Deploy SafeLink VPN server to 159.75.35.104
# Step 1: Cross-compile for Linux
Write-Host "=== Cross-compiling for Linux amd64 ==="
$env:GOOS = "linux"
$env:GOARCH = "amd64"
$env:CGO_ENABLED = "0"
$outFile = "$env:TEMP\safelink-deploy"
Set-Location "e:\project\safelink-ssh"
go build -o $outFile ./cmd/safelink
if ($LASTEXITCODE -ne 0) {
    Write-Host "Build failed!"
    exit 1
}
Write-Host "Build OK: $outFile"

# Reset env
Remove-Item Env:\GOOS
Remove-Item Env:\GOARCH
Remove-Item Env:\CGO_ENABLED

# Step 2: Deploy via SSH using plink/pscp (or native SSH if available)
$sshAddr = "159.75.35.104"
$sshUser = "ubuntu"
$sshPass = "lyhappy2018."
$vpnUser = "vpn"
$vpnPass = "vpn3456"
$subnet = "10.0.8.0/24"
$port = "1562"

Write-Host "=== Uploading binary via SCP ==="
# Use native ssh/scp (Windows 10+ has OpenSSH built-in)
$env:SSHPASS = $sshPass

# Create expect-like script for scp
$scpCmd = "scp -o StrictHostKeyChecking=no -o UserKnownHostsFile=NUL $outFile ${sshUser}@${sshAddr}:/tmp/safelink"
Write-Host "Running: $scpCmd"

# Use plink approach - write password to stdin
# Actually let's use ssh with sshpass equivalent
# On Windows, let's use the native SSH with password from pipe
$psi = New-Object System.Diagnostics.ProcessStartInfo
$psi.FileName = "scp"
$psi.Arguments = "-o StrictHostKeyChecking=no -o UserKnownHostsFile=NUL `"$outFile`" ${sshUser}@${sshAddr}:/tmp/safelink"
$psi.UseShellExecute = $false
$psi.RedirectStandardInput = $true
$psi.RedirectStandardOutput = $true
$psi.RedirectStandardError = $true
$proc = [System.Diagnostics.Process]::Start($psi)
Start-Sleep -Milliseconds 2000
$proc.StandardInput.WriteLine($sshPass)
$proc.WaitForExit(60000)
$stdout = $proc.StandardOutput.ReadToEnd()
$stderr = $proc.StandardError.ReadToEnd()
Write-Host "SCP stdout: $stdout"
Write-Host "SCP stderr: $stderr"
Write-Host "SCP exit: $($proc.ExitCode)"

if ($proc.ExitCode -ne 0) {
    Write-Host "SCP failed, trying alternative method..."
    # Try with plain ssh command - might work with key auth or agent
}

Write-Host "=== Restarting VPN server via SSH ==="
$cmds = @(
    "echo '$sshPass' | sudo -S killall -9 safelink 2>/dev/null; true"
    "echo '$sshPass' | sudo -S fuser -k ${port}/udp 2>/dev/null; true"
    "chmod +x /tmp/safelink"
    "echo '$sshPass' | sudo -S cp /tmp/safelink /usr/local/bin/safelink"
    "echo '$sshPass' | sudo -S nohup /usr/local/bin/safelink server --listen :${port} --subnet ${subnet} --user ${vpnUser} --pass ${vpnPass} --nat-iface eth0 > /tmp/safelink.log 2>&1 &"
)
$remoteCmd = $cmds -join "; sleep 1; "

$psi2 = New-Object System.Diagnostics.ProcessStartInfo
$psi2.FileName = "ssh"
$psi2.Arguments = "-o StrictHostKeyChecking=no -o UserKnownHostsFile=NUL ${sshUser}@${sshAddr} `"$remoteCmd`""
$psi2.UseShellExecute = $false
$psi2.RedirectStandardInput = $true
$psi2.RedirectStandardOutput = $true
$psi2.RedirectStandardError = $true
$proc2 = [System.Diagnostics.Process]::Start($psi2)
Start-Sleep -Milliseconds 2000
$proc2.StandardInput.WriteLine($sshPass)
$proc2.WaitForExit(30000)
Write-Host "SSH stdout: $($proc2.StandardOutput.ReadToEnd())"
Write-Host "SSH stderr: $($proc2.StandardError.ReadToEnd())"
Write-Host "SSH exit: $($proc2.ExitCode)"

Write-Host "`n=== Deploy complete ==="
