# sshtunneld

A small, cross-platform SSH tunnel daemon written in Go.  A single binary
maintains any number of `-L` / `-R` / `-D` (SOCKS5) tunnels described in a
YAML file, with auto-reconnect, keepalive, and strict `known_hosts`
verification.

## Features

- **Three forwarding modes** in one process:
  - `local`  → `ssh -L`
  - `remote` → `ssh -R`
  - `dynamic` → `ssh -D` (SOCKS5)
- **Authentication**: private key (PKCS#1 / PKCS#8 / OpenSSH, optional
  passphrase) and/or account password.  When both are configured, the public
  key is offered first and the password is used as a fallback.
- **Strict host key verification** against `known_hosts` (no insecure mode).
- **Auto-reconnect** with capped exponential backoff and ±20% jitter.
- **Keepalive** equivalent to OpenSSH's `ServerAliveInterval` /
  `ServerAliveCountMax` (`SendRequest("keepalive@openssh.com")`).
- **Cross-platform**: Windows / Linux / macOS, single binary.
- Structured JSON logging via `log/slog`.

## Build

```powershell
go mod tidy
go build -o sshtunneld.exe .\cmd\sshtunneld
```

## Configure

Copy [configs/sshtunneld.yaml](configs/sshtunneld.yaml) and edit it.  Anything
written as `${VAR}` is substituted from the process environment at load time,
so secrets need not live in the file.

Minimum viable example:

```yaml
known_hosts: ~/.ssh/known_hosts
tunnels:
  - name: db
    mode: local
    listen: 127.0.0.1:5432
    forward: db.internal:5432
    ssh:
      addr: jump.example.com:22
      user: alice
      identity_file: ~/.ssh/id_ed25519
```

## Run

```powershell
$env:ID_ED25519_PASS = "super-secret"
.\sshtunneld.exe -config .\configs\sshtunneld.yaml
```

Stop with Ctrl+C; on Windows you can also use
`taskkill /PID <pid>` (sends `SIGTERM`).

## Security notes

- `known_hosts` must contain the SSH server's fingerprint **before** the first
  run.  Seed it once with `ssh-keyscan -H host >> ~/.ssh/known_hosts`.  There
  is intentionally no "insecure ignore host key" option.
- Prefer `${ENV_VAR}` interpolation over hard-coding `password` /
  `passphrase` in YAML.  On Linux / macOS, restrict the file with
  `chmod 600`; on Windows, set ACLs so only the running account can read it.
- Account-password auth uses the SSH `password` method.  If a server only
  enables `keyboard-interactive` (multi-step challenge), you must use a
  private key instead.

## End-to-end verification

See [test/README.md](test/README.md) for a Docker-based sshd test rig that
exercises all three modes plus reconnect / keepalive / graceful-shutdown
paths.

## Project layout

```
cmd/sshtunneld/    main package
internal/config/   YAML schema, loader, validation
internal/sshclient/ auth, host-key, keepalive, supervisor
internal/tunnel/   local / remote / dynamic forwarders
internal/daemon/   signal handling and supervisor wiring
internal/logging/  slog initialization
configs/           example configuration
test/              docker-compose sshd for verification
```

## Dependencies

- `golang.org/x/crypto/ssh` – SSH protocol and key parsing
- `golang.org/x/crypto/ssh/knownhosts` – strict host-key verification
- `gopkg.in/yaml.v3` – YAML parsing
- `github.com/things-go/go-socks5` – SOCKS5 server (used for `-D`)
