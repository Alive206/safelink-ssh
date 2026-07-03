# SafeLink

SafeLink is split into a Docker-friendly VPN server, a Windows desktop client,
and an Android client. The server exposes a QUIC VPN gateway and subscription
endpoint. The clients import VPN/proxy subscriptions, manage VPN and proxy
connections, and provide SSH tunnel forwarding as a separate feature area.

## Features

- **Server VPN gateway**: QUIC transport, TUN device, optional NAT, Basic-style
  VPN authentication, and Docker deployment support.
- **Server subscriptions**: `GET /api/subscription` and `GET /subscription`
  export the running VPN node as SafeLink JSON or Clash YAML.
- **Client VPN**: import SafeLink JSON or Clash YAML subscriptions, create VPN
  tunnel configs, and manage route/driver state from the VPN page.
- **Client SSH tunnels**: local (`-L`), remote (`-R`), and dynamic SOCKS
  (`-D`) forwarding are configured from a dedicated SSH tunnel page.
- **Client SSH terminal**: open an interactive SSH PTY terminal, type commands
  in real time, and view remote output directly in the desktop client.
- **Android client**: Flutter UI with encrypted local storage, subscription
  import, proxy nodes, rule/global/direct proxy modes, local sing-box proxy,
  Android `VpnService` global proxy through tun2socks, SafeLink VPN, SSH
  tunnels, SSH terminal, logs, and settings.
- **Shared protocol package**: `pkg/` contains tunnel config validation,
  QUIC/frame helpers, and subscription parsing/encoding.

## Project Layout

```text
server/              VPN server, web API, Docker deployment
client/              Wails desktop client, frontend, SSH/VPN runtime
android/             Flutter Android client and Android native services
pkg/                 shared config, protocol, transport and subscription code
scripts/             build helpers
go.work              multi-module workspace
```

## Server

Build and run locally on Linux/macOS:

```bash
cd server
go build -o safelink-server ./cmd/safelink-server
VPN_PASS=changeme VPN_PUBLIC_ADDR=vpn.example.com:1562 ./safelink-server --nat-iface eth0
```

Docker Compose example:

```bash
cd server
docker compose up -d --build
```

Important server environment variables:

- `VPN_USER`: VPN auth username, default `admin`.
- `VPN_PASS`: VPN auth password, required.
- `VPN_SUBNET`: server TUN subnet, default `10.0.8.0/24`.
- `VPN_CLIENT_SUBNET`: client TUN subnet advertised in subscriptions, default `10.8.0.2/24`.
- `VPN_PUBLIC_ADDR`: public VPN endpoint advertised to clients, for example `vpn.example.com:1562`.
- `SUBSCRIPTION_TOKEN`: optional token required to download subscriptions.

Subscription URLs:

```text
http://server:8080/api/subscription?format=json&token=SUBSCRIPTION_TOKEN
http://server:8080/api/subscription?format=clash&token=SUBSCRIPTION_TOKEN
```

`format=json` returns SafeLink JSON. `format=clash` returns Clash-compatible
YAML with `type: safelink-vpn` proxy entries.

## Client

Build the Windows client:

```powershell
.\scripts\build-client.bat
```

Build the Android client APK:

```powershell
.\scripts\build-android.bat
```

The Android build creates a Flutter client under `android/`, compiles the Go
mobile core as an arm64 AAR, and writes the internal test APK to
`android\build\app\outputs\flutter-apk\app-release.apk`. By default the script
expects Flutter at `D:\env\flutter` and the Android SDK at
`D:\env\android-sdk`; pass `-FlutterRoot`, `-AndroidSdk`, or `-AndroidNdk` to
`scripts\build-android.ps1` if your local paths differ.

The Android APK is arm64-first. It bundles:

- `mobilecore.aar`: gomobile bridge for subscription parsing, SSH sessions,
  SSH tunnels, SafeLink VPN, and Android TUN-to-SOCKS control.
- `libsing-box.so`: Android arm64 sing-box runtime used by local proxy and
  proxy VPN modes.

Use `-RebuildSingBox` on `scripts\build-android.ps1` to rebuild the bundled
sing-box binary from source:

```powershell
.\scripts\build-android.ps1 -RebuildSingBox
```

Android proxy modes:

- **本地代理** starts sing-box inside a foreground service and listens on
  `127.0.0.1:10808` SOCKS and `127.0.0.1:10809` HTTP.
- **全局 VPN** starts the same sing-box config, creates an Android
  `VpnService` TUN, excludes SafeLink's own UID to avoid routing loops, and
  forwards TUN packets to the local SOCKS listener through tun2socks.
- **SafeLink VPN** passes the Android TUN fd into the Go QUIC VPN runtime and
  uses the existing SafeLink server protocol.

In the desktop UI:

1. Open **订阅** and import a server subscription URL.
2. Open **VPN** to view imported VPN nodes, start/stop connections, and manage routing.
3. Open **SSH 隧道** to configure local, remote, or SOCKS forwarding separately from VPN.
4. Open **SSH 终端** to connect to a host and run commands in an interactive PTY session.
5. Open **驱动** to check or install the TUN driver required by VPN mode.

Client data is stored under `%APPDATA%\SafeLink`.

## Subscription Formats

SafeLink JSON:

```json
{
  "version": 1,
  "tunnels": [
    {
      "name": "demo",
      "mode": "vpn",
      "forward": "vpn.example.com:1562",
      "ssh": { "user": "admin", "password": "changeme" },
      "tun": { "subnet": "10.8.0.2/24", "dns": ["1.1.1.1"], "auto_route": true }
    }
  ]
}
```

Clash YAML:

```yaml
proxies:
  - name: demo
    type: safelink-vpn
    server: vpn.example.com
    port: 1562
    username: admin
    password: changeme
    subnet: 10.8.0.2/24
    dns:
      - 1.1.1.1
    auto-route: true
```

## Verification

Useful checks while developing:

```bash
cd pkg && go test ./...
cd server && go test ./...
cd client && go test ./...
cd client/frontend && npm run build
cd android && flutter analyze
cd android && flutter test
.\scripts\build-android.bat
```

## License

This project is licensed under the [GNU General Public License v3.0](LICENSE).
