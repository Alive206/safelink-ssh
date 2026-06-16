# End-to-end verification with a Dockerized sshd

This directory contains a minimal `sshd` setup used to exercise all three
forwarding modes.  All commands assume PowerShell on Windows; replace `;`
with `&&` and the path style for bash.

## 1. Generate a test key and authorize it

```powershell
ssh-keygen -t ed25519 -f .\test\id_test -N '""'
Get-Content .\test\id_test.pub | Set-Content .\test\authorized_keys
```

## 2. Start the test sshd

```powershell
docker compose -f .\test\docker-compose.yml up -d
```

## 3. Trust the host key (strict known_hosts)

```powershell
ssh-keyscan -p 2222 -H 127.0.0.1 | Add-Content $env:USERPROFILE\.ssh\known_hosts
```

## 4. Run sshtunneld against the test sshd

Create `configs/sshtunneld.test.yaml` (adapt the example) with:

```yaml
log_level: debug
known_hosts: ${USERPROFILE}\.ssh\known_hosts
tunnels:
  - name: L-test
    mode: local
    listen: 127.0.0.1:5555
    forward: 127.0.0.1:2222     # the container's sshd, reached via SSH itself
    ssh:
      addr: 127.0.0.1:2222
      user: tester
      identity_file: ./test/id_test
  - name: D-test
    mode: dynamic
    listen: 127.0.0.1:1080
    ssh:
      addr: 127.0.0.1:2222
      user: tester
      identity_file: ./test/id_test
  - name: R-test
    mode: remote
    listen: 0.0.0.0:8080
    forward: 127.0.0.1:3000     # local python http server below
    ssh:
      addr: 127.0.0.1:2222
      user: tester
      identity_file: ./test/id_test
```

Run it:
```powershell
.\sshtunneld.exe -config .\configs\sshtunneld.test.yaml
```

## 5. Verify each mode

| Mode | Command | Expected |
|------|---------|----------|
| `-L` | `ssh -p 5555 -i .\test\id_test tester@127.0.0.1` | Connects through the local listener back to the container sshd |
| `-R` | start `python -m http.server 3000`, then `docker exec sshtunneld-sshd curl -s http://127.0.0.1:8080` | HTTP listing from the host |
| `-D` | `curl --socks5-hostname 127.0.0.1:1080 http://127.0.0.1:2222 -v` | TCP 200 / SSH banner via SOCKS proxy |

## 6. Reliability checks

- **Reconnect**: `docker restart sshtunneld-sshd` and watch the daemon log
  exponential backoff and recovery.
- **Keepalive**: drop traffic with `docker exec sshtunneld-sshd iptables -I OUTPUT -p tcp --sport 2222 -j DROP`;
  the supervisor should detect loss within ~90s and reconnect once you remove
  the rule.
- **Graceful shutdown**: Ctrl+C or `taskkill /PID <pid>`; all listening ports
  must be released and the process must exit with code 0.
