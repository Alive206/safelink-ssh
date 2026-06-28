package subscription_test

import (
	"strings"
	"testing"

	"github.com/example/safelink/pkg/config"
	"github.com/example/safelink/pkg/subscription"
)

func TestParseSafeLinkJSON(t *testing.T) {
	data := []byte(`{
		"version": 1,
		"tunnels": [
			{
				"name": "home-vpn",
				"mode": "vpn",
				"forward": "vpn.example.com:1562",
				"ssh": {"user": "alice", "password": "secret"},
				"tun": {"subnet": "10.8.0.2/24", "dns": ["1.1.1.1"], "auto_route": true, "sni": "front.example.com", "padding": true}
			}
		]
	}`)

	tunnels, detected, err := subscription.Parse(data, subscription.FormatAuto)
	if err != nil {
		t.Fatalf("Parse returned error: %v", err)
	}
	if detected != subscription.FormatSafeLinkJSON {
		t.Fatalf("detected format = %q, want %q", detected, subscription.FormatSafeLinkJSON)
	}
	if len(tunnels) != 1 {
		t.Fatalf("len(tunnels) = %d, want 1", len(tunnels))
	}

	got := tunnels[0]
	if got.Name != "home-vpn" || got.Mode != config.ModeVPN {
		t.Fatalf("unexpected tunnel identity: %+v", got)
	}
	if got.Forward != "vpn.example.com:1562" {
		t.Fatalf("Forward = %q", got.Forward)
	}
	if got.SSH.User != "alice" || got.SSH.Password != "secret" {
		t.Fatalf("unexpected auth: %+v", got.SSH)
	}
	if got.Tun.Subnet != "10.8.0.2/24" || !got.Tun.AutoRoute {
		t.Fatalf("unexpected tun settings: %+v", got.Tun)
	}
	if got.Tun.Padding == nil || !*got.Tun.Padding {
		t.Fatalf("padding was not parsed as true: %+v", got.Tun.Padding)
	}
}

func TestParseClashYAML(t *testing.T) {
	data := []byte(`
proxies:
  - name: hk-1
    type: safelink-vpn
    server: vpn.example.com
    port: 1562
    username: bob
    password: secret
    sni: edge.example.com
    subnet: 10.9.0.2/24
    dns:
      - 1.1.1.1
      - 8.8.8.8
    auto-route: true
    pin-sha256: abc123
    padding: false
  - name: ignored-ss
    type: ss
    server: example.com
    port: 8388
`)

	tunnels, detected, err := subscription.Parse(data, subscription.FormatAuto)
	if err != nil {
		t.Fatalf("Parse returned error: %v", err)
	}
	if detected != subscription.FormatClashYAML {
		t.Fatalf("detected format = %q, want %q", detected, subscription.FormatClashYAML)
	}
	if len(tunnels) != 1 {
		t.Fatalf("len(tunnels) = %d, want 1", len(tunnels))
	}

	got := tunnels[0]
	if got.Name != "hk-1" || got.Forward != "vpn.example.com:1562" {
		t.Fatalf("unexpected VPN endpoint: %+v", got)
	}
	if got.SSH.User != "bob" || got.SSH.Password != "secret" {
		t.Fatalf("unexpected auth: %+v", got.SSH)
	}
	if got.Tun.SNI != "edge.example.com" || got.Tun.PinSHA256 != "abc123" {
		t.Fatalf("unexpected TLS settings: %+v", got.Tun)
	}
	if got.Tun.Padding == nil || *got.Tun.Padding {
		t.Fatalf("padding was not parsed as false: %+v", got.Tun.Padding)
	}
}

func TestEncodeClashYAML(t *testing.T) {
	padding := true
	out, err := subscription.EncodeClashYAML([]config.TunnelCfg{{
		Name:    "demo",
		Mode:    config.ModeVPN,
		Forward: "vpn.example.com:1562",
		SSH: config.SSHCfg{
			User:     "admin",
			Password: "secret",
		},
		Tun: config.TunCfg{
			Subnet:    "10.8.0.2/24",
			DNS:       []string{"1.1.1.1"},
			AutoRoute: true,
			SNI:       "front.example.com",
			Padding:   &padding,
		},
	}})
	if err != nil {
		t.Fatalf("EncodeClashYAML returned error: %v", err)
	}

	text := string(out)
	for _, want := range []string{"type: safelink-vpn", "server: vpn.example.com", "port: 1562", "username: admin", "auto-route: true"} {
		if !strings.Contains(text, want) {
			t.Fatalf("encoded YAML missing %q:\n%s", want, text)
		}
	}
}
