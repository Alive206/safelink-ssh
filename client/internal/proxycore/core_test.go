package proxycore

import (
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/example/safelink/pkg/proxysubscription"
)

func TestStatusReportsProxyTraffic(t *testing.T) {
	var uploadTotal atomic.Uint64
	var downloadTotal atomic.Uint64
	uploadTotal.Store(1024)
	downloadTotal.Store(4096)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/connections" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w, `{"uploadTotal":%d,"downloadTotal":%d,"connections":[]}`, uploadTotal.Load(), downloadTotal.Load())
	}))
	defer server.Close()

	parsed, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	runner := New(Options{ClashAPIAddr: parsed.Host})
	runner.mu.Lock()
	runner.state = StateRunning
	runner.nodeName = "ss-hk"
	runner.startedAt = time.Now()
	runner.mu.Unlock()

	first := runner.Status()
	if first.UploadTotalBytes != 1024 || first.DownloadTotalBytes != 4096 {
		t.Fatalf("first traffic totals = %d/%d, want 1024/4096", first.UploadTotalBytes, first.DownloadTotalBytes)
	}

	uploadTotal.Store(3072)
	downloadTotal.Store(12288)
	time.Sleep(20 * time.Millisecond)
	second := runner.Status()
	if second.UploadTotalBytes != 3072 || second.DownloadTotalBytes != 12288 {
		t.Fatalf("second traffic totals = %d/%d, want 3072/12288", second.UploadTotalBytes, second.DownloadTotalBytes)
	}
	if second.UploadSpeedBps == 0 || second.DownloadSpeedBps == 0 {
		t.Fatalf("traffic speed should be non-zero after totals increase: %+v", second)
	}
}

func TestStartReturnsErrorWhenCoreExitsBeforeReady(t *testing.T) {
	runner := New(Options{
		CorePath:  writeExitingCore(t),
		WorkDir:   t.TempDir(),
		HTTPAddr:  freeTestAddr(t),
		SOCKSAddr: freeTestAddr(t),
	})

	err := runner.Start(t.Context(), proxysubscription.ProxyNode{
		Name:     "ss-hk",
		Protocol: proxysubscription.ProtocolShadowsocks,
		Server:   "198.51.100.10",
		Port:     8388,
		Method:   "aes-128-gcm",
		Password: "secret",
	})
	if err == nil {
		t.Fatal("Start returned nil, want startup readiness error")
	}
	if !strings.Contains(err.Error(), "proxy listener exited before") {
		t.Fatalf("Start error = %q, want proxy listener readiness error", err)
	}
	status := runner.Status()
	if status.State != StateError {
		t.Fatalf("Status.State = %q, want %q", status.State, StateError)
	}
	if status.LastError == "" {
		t.Fatalf("Status.LastError should describe the startup failure: %+v", status)
	}
}

func TestCoreDelayReturnsFirstHealthyProbe(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/proxies/selected/delay" {
			http.NotFound(w, r)
			return
		}
		if strings.Contains(r.URL.Query().Get("url"), "cp.cloudflare.com") {
			time.Sleep(500 * time.Millisecond)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"delay":23}`))
	}))
	defer server.Close()

	parsed, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}

	start := time.Now()
	delay, err := testCoreDelay(t.Context(), parsed.Host, 2*time.Second)
	if err != nil {
		t.Fatalf("testCoreDelay failed: %v", err)
	}
	if delay != 23 {
		t.Fatalf("delay = %d, want 23", delay)
	}
	if elapsed := time.Since(start); elapsed >= 450*time.Millisecond {
		t.Fatalf("testCoreDelay took %s, want it to return before the slow probe", elapsed)
	}
}

func TestNodeDoesNotUseTCPConnectFallbackForProxyDelay(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()

	_, portText, err := net.SplitHostPort(listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	var port int
	if _, err := fmt.Sscanf(portText, "%d", &port); err != nil {
		t.Fatal(err)
	}

	result := TestNode(t.Context(), proxysubscription.ProxyNode{
		Name:     "tcp-is-not-proxy-delay",
		Protocol: proxysubscription.ProtocolShadowsocks,
		Server:   "127.0.0.1",
		Port:     port,
		Method:   "aes-128-gcm",
		Password: "secret",
	}, Options{
		CorePath: writeFailingDelayCore(t),
		WorkDir:  t.TempDir(),
	}, 2*time.Second)
	if result.OK {
		t.Fatalf("TestNode used a TCP-connect fallback: %+v", result)
	}
	if result.LatencyMS != 0 {
		t.Fatalf("LatencyMS = %d, want 0 when proxy delay fails", result.LatencyMS)
	}
	if !strings.Contains(result.Error, "probe failed") {
		t.Fatalf("Error = %q, want proxy delay error", result.Error)
	}
}

func TestNodeFallsBackToHTTPProxyWhenCoreDelayFails(t *testing.T) {
	result := TestNode(t.Context(), proxysubscription.ProxyNode{
		Name:     "http-proxy-fallback",
		Protocol: proxysubscription.ProtocolShadowsocks,
		Server:   "198.51.100.10",
		Port:     8388,
		Method:   "aes-128-gcm",
		Password: "secret",
	}, Options{
		CorePath: writeDelayFailingHTTPProxyCore(t),
		WorkDir:  t.TempDir(),
	}, 2*time.Second)
	if !result.OK {
		t.Fatalf("TestNode failed: %+v", result)
	}
	if result.LatencyMS <= 0 {
		t.Fatalf("LatencyMS = %d, want > 0", result.LatencyMS)
	}
}

func writeFailingDelayCore(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	source := filepath.Join(dir, "main.go")
	bin := filepath.Join(dir, "sing-box-helper"+executableSuffix())
	content := `package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
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
	if len(os.Args) > 1 && os.Args[1] == "check" {
		return
	}
	data, err := os.ReadFile(configPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	var cfg struct {
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
	if cfg.Experimental.ClashAPI.ExternalController == "" {
		fmt.Fprintln(os.Stderr, "clash api controller not found")
		os.Exit(2)
	}
	server := &http.Server{
		Addr: cfg.Experimental.ClashAPI.ExternalController,
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/proxies/selected/delay" {
				http.NotFound(w, r)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(` + "`{\"error\":\"probe failed\"}`" + `))
		}),
	}
	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
}
`
	if err := os.WriteFile(source, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("go", "build", "-o", bin, source)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build helper core: %v\n%s", err, out)
	}
	return bin
}

func writeExitingCore(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	source := filepath.Join(dir, "main.go")
	bin := filepath.Join(dir, "sing-box-exiting"+executableSuffix())
	content := `package main

import (
	"fmt"
	"os"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "check" {
		return
	}
	fmt.Fprintln(os.Stderr, "startup failed")
	os.Exit(2)
}
`
	if err := os.WriteFile(source, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("go", "build", "-o", bin, source)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build exiting core: %v\n%s", err, out)
	}
	return bin
}

func freeTestAddr(t *testing.T) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	return listener.Addr().String()
}

func writeDelayFailingHTTPProxyCore(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	source := filepath.Join(dir, "main.go")
	bin := filepath.Join(dir, "sing-box-helper"+executableSuffix())
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
	if len(os.Args) > 1 && os.Args[1] == "check" {
		return
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
	httpAddr := ""
	for _, inbound := range cfg.Inbounds {
		if inbound.Type == "http" {
			httpAddr = net.JoinHostPort(inbound.Listen, strconv.Itoa(inbound.ListenPort))
			break
		}
	}
	if httpAddr == "" {
		fmt.Fprintln(os.Stderr, "http inbound not found")
		os.Exit(2)
	}
	go func() {
		server := &http.Server{
			Addr: httpAddr,
			Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusNoContent)
			}),
		}
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
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
			_, _ = w.Write([]byte(` + "`{\"error\":\"probe failed\"}`" + `))
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
	cmd := exec.Command("go", "build", "-o", bin, source)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build helper core: %v\n%s", err, out)
	}
	return bin
}

func executableSuffix() string {
	if runtime.GOOS == "windows" {
		return ".exe"
	}
	return ""
}
