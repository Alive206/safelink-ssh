package proxycore_test

import (
	"encoding/json"
	"testing"

	"github.com/example/safelink/client/internal/proxycore"
	"github.com/example/safelink/pkg/proxysubscription"
)

func TestBuildConfigCreatesLocalInboundsAndSelectedOutbound(t *testing.T) {
	node := proxysubscription.ProxyNode{
		Name:     "vless-sg",
		Protocol: proxysubscription.ProtocolVLESS,
		Server:   "vless.example.com",
		Port:     443,
		UUID:     "11111111-1111-1111-1111-111111111111",
		TLS: &proxysubscription.TLSOptions{
			Enabled:    true,
			ServerName: "edge.example.com",
		},
		Transport: proxysubscription.TransportOptions{
			Type: "ws",
			Path: "/ws",
			Host: "edge.example.com",
		},
	}

	data, err := proxycore.BuildConfig(node, proxycore.Options{
		SOCKSAddr: "127.0.0.1:10808",
		HTTPAddr:  "127.0.0.1:10809",
	})
	if err != nil {
		t.Fatalf("BuildConfig returned error: %v", err)
	}

	var cfg map[string]any
	if err := json.Unmarshal(data, &cfg); err != nil {
		t.Fatalf("config is not JSON: %v\n%s", err, string(data))
	}
	inbounds := cfg["inbounds"].([]any)
	if len(inbounds) != 2 {
		t.Fatalf("len(inbounds) = %d, want 2", len(inbounds))
	}
	outbounds := cfg["outbounds"].([]any)
	selected := outbounds[0].(map[string]any)
	if selected["type"] != "vless" || selected["server"] != "vless.example.com" || selected["server_port"].(float64) != 443 {
		t.Fatalf("unexpected outbound: %+v", selected)
	}
	if selected["tls"].(map[string]any)["server_name"] != "edge.example.com" {
		t.Fatalf("TLS server name missing: %+v", selected["tls"])
	}
	if selected["transport"].(map[string]any)["path"] != "/ws" {
		t.Fatalf("WS transport missing: %+v", selected["transport"])
	}
}

func TestBuildConfigRejectsUnsupportedNodes(t *testing.T) {
	_, err := proxycore.BuildConfig(proxysubscription.ProxyNode{Name: "bad", Protocol: "unknown"}, proxycore.Options{})
	if err == nil {
		t.Fatalf("BuildConfig should reject unsupported protocols")
	}
}
