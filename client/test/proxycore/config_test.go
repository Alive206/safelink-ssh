package proxycore_test

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

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

func TestBuildConfigAppliesProxyModeRoute(t *testing.T) {
	node := proxysubscription.ProxyNode{
		Name:     "ss-hk",
		Protocol: proxysubscription.ProtocolShadowsocks,
		Server:   "ss.example.com",
		Port:     8388,
		Method:   "2022-blake3-aes-128-gcm",
		Password: "secret",
	}
	tests := []struct {
		name string
		mode string
		want string
	}{
		{name: "global", mode: proxycore.ProxyModeGlobal, want: "selected"},
		{name: "direct", mode: proxycore.ProxyModeDirect, want: "direct"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data, err := proxycore.BuildConfig(node, proxycore.Options{Mode: tt.mode})
			if err != nil {
				t.Fatalf("BuildConfig returned error: %v", err)
			}
			var cfg map[string]any
			if err := json.Unmarshal(data, &cfg); err != nil {
				t.Fatalf("config is not JSON: %v\n%s", err, string(data))
			}
			route := cfg["route"].(map[string]any)
			if route["final"] != tt.want {
				t.Fatalf("route.final = %q, want %q", route["final"], tt.want)
			}
		})
	}
}

func TestBuildConfigOmitsAnyTLSTransportAndKeepsUTLSFingerprint(t *testing.T) {
	node := proxysubscription.ProxyNode{
		Name:      "anytls-hk",
		Protocol:  proxysubscription.ProtocolAnyTLS,
		Server:    "anytls.example.com",
		Port:      443,
		Password:  "secret",
		Transport: proxysubscription.TransportOptions{Type: "tcp"},
		TLS: &proxysubscription.TLSOptions{
			Enabled:     true,
			ServerName:  "sni.example.com",
			Fingerprint: "chrome",
		},
	}

	data, err := proxycore.BuildConfig(node, proxycore.Options{})
	if err != nil {
		t.Fatalf("BuildConfig returned error: %v", err)
	}
	var cfg map[string]any
	if err := json.Unmarshal(data, &cfg); err != nil {
		t.Fatalf("config is not JSON: %v\n%s", err, string(data))
	}
	selected := cfg["outbounds"].([]any)[0].(map[string]any)
	if _, ok := selected["transport"]; ok {
		t.Fatalf("AnyTLS tcp transport should be omitted: %+v", selected["transport"])
	}
	tls := selected["tls"].(map[string]any)
	utls := tls["utls"].(map[string]any)
	if utls["fingerprint"] != "chrome" {
		t.Fatalf("utls fingerprint missing: %+v", tls)
	}
}

func TestFindCorePathReturnsAbsolutePath(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "sing-box.exe")
	if err := os.WriteFile(bin, []byte(""), 0o755); err != nil {
		t.Fatal(err)
	}
	got := proxycore.FindCorePath(dir)
	if got == "" {
		t.Fatal("FindCorePath returned empty path")
	}
	if !filepath.IsAbs(got) {
		t.Fatalf("FindCorePath returned relative path %q", got)
	}
	if !strings.EqualFold(got, bin) {
		t.Fatalf("FindCorePath = %q, want %q", got, bin)
	}
}

func TestNodeMeasuresDelayThroughCoreAPI(t *testing.T) {
	helper := writeHelperCore(t)
	node := proxysubscription.ProxyNode{
		Name:     "ss-hk",
		Protocol: proxysubscription.ProtocolShadowsocks,
		Server:   "198.51.100.10",
		Port:     8388,
		Method:   "aes-128-gcm",
		Password: "secret",
	}

	result := proxycore.TestNode(t.Context(), node, proxycore.Options{
		CorePath: helper,
		WorkDir:  t.TempDir(),
	}, 3*time.Second)
	if !result.OK {
		t.Fatalf("TestNode failed: %+v", result)
	}
	if result.LatencyMS <= 0 {
		t.Fatalf("LatencyMS = %d, want > 0", result.LatencyMS)
	}
}

func writeHelperCore(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	source := filepath.Join(dir, "main.go")
	helper := filepath.Join(dir, "sing-box-helper.exe")
	content := `package main

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"strconv"
)

func main() {
	configPath := ""
	for i, arg := range os.Args {
		if arg == "-c" && i+1 < len(os.Args) {
			configPath = os.Args[i+1]
			break
		}
	}
	if configPath == "" {
		fmt.Fprintln(os.Stderr, "missing -c")
		os.Exit(2)
	}
	data, err := os.ReadFile(configPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	var cfg struct {
		Inbounds []struct {
			Type       string ` + "`json:\"type\"`" + `
			Listen     string ` + "`json:\"listen\"`" + `
			ListenPort int    ` + "`json:\"listen_port\"`" + `
		} ` + "`json:\"inbounds\"`" + `
		Experimental struct {
			ClashAPI struct {
				ExternalController string ` + "`json:\"external_controller\"`" + `
			} ` + "`json:\"clash_api\"`" + `
		} ` + "`json:\"experimental\"`" + `
	}
	if err := json.Unmarshal(data, &cfg); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	if len(os.Args) > 1 && os.Args[1] == "check" {
		return
	}
	addr := ""
	for _, inbound := range cfg.Inbounds {
		if inbound.Type == "http" {
			addr = net.JoinHostPort(inbound.Listen, strconv.Itoa(inbound.ListenPort))
			break
		}
	}
	if addr == "" {
		fmt.Fprintln(os.Stderr, "http inbound not found")
		os.Exit(2)
	}
	inboundServer := &http.Server{
		Addr: addr,
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusNoContent)
		}),
	}
	go func() {
		if err := inboundServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(2)
		}
	}()
	controllerAddr := cfg.Experimental.ClashAPI.ExternalController
	if controllerAddr == "" {
		fmt.Fprintln(os.Stderr, "clash api controller not found")
		os.Exit(2)
	}
	controller := &http.Server{
		Addr: controllerAddr,
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/proxies/selected/delay" {
				http.NotFound(w, r)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(` + "`{\"delay\":42}`" + `))
		}),
	}
	if err := controller.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
}
`
	if err := os.WriteFile(source, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("go", "build", "-o", helper, source)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build helper core: %v\n%s", err, out)
	}
	return helper
}
