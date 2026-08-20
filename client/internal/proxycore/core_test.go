package proxycore

import (
	"bytes"
	"context"
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

	"github.com/example/safelink/pkg/config"
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

// TestNodeDoesNotUseTCPConnectFallbackForProxyDelay 盯的是仍然走 core 探测的 QUIC 协议：
// hysteria2 的端口能 TCP 连上也不代表代理可用（它跑在 UDP 上），不能拿 TCP 握手兜底。
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
		Protocol: proxysubscription.ProtocolHysteria2,
		Server:   "127.0.0.1",
		Port:     port,
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
		Protocol: proxysubscription.ProtocolHysteria2,
		Server:   "198.51.100.10",
		Port:     8388,
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

func TestNodesMeasureDelayThroughOneBatchCore(t *testing.T) {
	nodes := []proxysubscription.ProxyNode{
		{
			Name:     "hy2-hk",
			Protocol: proxysubscription.ProtocolHysteria2,
			Server:   "198.51.100.10",
			Port:     8388,
			Password: "secret",
		},
		{
			Name:     "hy2-sg",
			Protocol: proxysubscription.ProtocolHysteria2,
			Server:   "198.51.100.11",
			Port:     8389,
			Password: "secret",
		},
	}
	var reported []string
	results := TestNodes(t.Context(), nodes, Options{
		CorePath: writeBatchDelayCore(t),
		WorkDir:  t.TempDir(),
	}, time.Second, func(result TestResult) {
		reported = append(reported, result.NodeName)
	})
	if len(results) != len(nodes) {
		t.Fatalf("len(results) = %d, want %d", len(results), len(nodes))
	}
	for i, result := range results {
		if !result.OK {
			t.Fatalf("result[%d] failed: %+v", i, result)
		}
		if result.LatencyMS <= 0 {
			t.Fatalf("result[%d].LatencyMS = %d, want > 0", i, result.LatencyMS)
		}
	}
	if len(reported) != len(nodes) {
		t.Fatalf("reported %d results, want %d", len(reported), len(nodes))
	}
}

func TestRunnerTestHonorsContextWhenTestSlotsAreBusy(t *testing.T) {
	runner := New(Options{})
	for i := 0; i < defaultNodeTestConcurrency; i++ {
		runner.testSlots <- struct{}{}
	}
	defer func() {
		for i := 0; i < defaultNodeTestConcurrency; i++ {
			<-runner.testSlots
		}
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()

	result := runner.Test(ctx, proxysubscription.ProxyNode{Name: "queued-node"}, time.Second)
	if result.OK {
		t.Fatalf("Test returned OK while all test slots were busy: %+v", result)
	}
	if !strings.Contains(result.Error, context.DeadlineExceeded.Error()) {
		t.Fatalf("Error = %q, want context deadline exceeded", result.Error)
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

func writeBatchDelayCore(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	source := filepath.Join(dir, "main.go")
	bin := filepath.Join(dir, "sing-box-batch"+executableSuffix())
	content := `package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "check" {
		return
	}
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
		Outbounds []struct {
			Tag string ` + "`json:\"tag\"`" + `
		} ` + "`json:\"outbounds\"`" + `
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
	tags := map[string]bool{}
	for _, outbound := range cfg.Outbounds {
		tags[outbound.Tag] = true
	}
	controllerAddr := cfg.Experimental.ClashAPI.ExternalController
	if controllerAddr == "" {
		fmt.Fprintln(os.Stderr, "clash api controller not found")
		os.Exit(2)
	}
	server := &http.Server{
		Addr: controllerAddr,
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
			if len(parts) != 3 || parts[0] != "proxies" || parts[2] != "delay" {
				http.NotFound(w, r)
				return
			}
			tag := parts[1]
			if !tags[tag] {
				http.NotFound(w, r)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = fmt.Fprintf(w, ` + "`{\"delay\":%d}`" + `, 20+len(tag))
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
		t.Fatalf("build batch core: %v\n%s", err, out)
	}
	return bin
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

// 降级启动时不能只删 rule_set 定义：路由规则里还引用着它的话 sing-box 会直接
// 报 rule-set not found，降级一样起不来。
func TestBuildRouteWithoutRuleSetsDropsDefinitionsAndReferences(t *testing.T) {
	ruleSetDir := t.TempDir()
	route, err := buildRoute(ProxyModeRule, nil, ruleSetDir, false)
	if err != nil {
		t.Fatalf("buildRoute returned error: %v", err)
	}
	if _, ok := route["rule_set"]; ok {
		t.Fatalf("route.rule_set should be absent, got %+v", route["rule_set"])
	}
	for _, raw := range route["rules"].([]map[string]any) {
		if _, ok := raw["rule_set"]; ok {
			t.Fatalf("route.rules still references a rule_set: %+v", raw)
		}
	}
	if route["final"] != "selected" {
		t.Fatalf("route.final = %q, want selected", route["final"])
	}
	// 降级时不需要规则集，也就不应该白铺一遍文件
	if entries, err := os.ReadDir(ruleSetDir); err == nil && len(entries) > 0 {
		t.Fatalf("rule set files were written during fallback start: %v", entries)
	}

	// 对照：默认仍然带规则集，降级只能是启动失败后的退路
	withRuleSets, err := buildRoute(ProxyModeRule, nil, ruleSetDir, true)
	if err != nil {
		t.Fatalf("buildRoute returned error: %v", err)
	}
	if _, ok := withRuleSets["rule_set"]; !ok {
		t.Fatal("route.rule_set missing when rule sets are enabled")
	}
}

// 内置的 .srs 必须真的能铺出来，且已存在的文件不重写：批量测速会反复调用。
func TestEnsureLocalRuleSetFilesWritesOnceAndKeepsContent(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "rule-sets")
	tags := []string{
		config.ProxyRuleSetGeoIPCN,
		config.ProxyRuleSetGeositeGeolocationCN,
		config.ProxyRuleSetGeositeGeolocationNotCN,
	}
	paths, err := ensureLocalRuleSetFiles(dir, tags)
	if err != nil {
		t.Fatalf("ensureLocalRuleSetFiles returned error: %v", err)
	}
	if len(paths) != len(tags) {
		t.Fatalf("got %d paths, want %d: %+v", len(paths), len(tags), paths)
	}
	modTimes := make(map[string]time.Time, len(paths))
	for tag, path := range paths {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatalf("rule set %s not written: %v", tag, err)
		}
		if info.Size() < minRuleSetBytes {
			t.Fatalf("rule set %s is only %d bytes, embedded file looks broken", tag, info.Size())
		}
		modTimes[tag] = info.ModTime()
	}

	// 第二次调用不应该重写：每测一个节点重写几百 KB 没必要
	time.Sleep(10 * time.Millisecond)
	again, err := ensureLocalRuleSetFiles(dir, tags)
	if err != nil {
		t.Fatalf("second ensureLocalRuleSetFiles returned error: %v", err)
	}
	for tag, path := range again {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatalf("rule set %s disappeared: %v", tag, err)
		}
		if !info.ModTime().Equal(modTimes[tag]) {
			t.Fatalf("rule set %s was rewritten, mod time changed %v -> %v",
				tag, modTimes[tag], info.ModTime())
		}
	}
}

// 下载回来的东西要把关住：写进去一份损坏的 .srs 会让下次启动直接 FATAL。
func TestDownloadRuleSetRejectsBadResponses(t *testing.T) {
	good := bytes.Repeat([]byte{0x53, 0x52, 0x53}, minRuleSetBytes)
	cases := []struct {
		name    string
		status  int
		body    []byte
		wantErr bool
	}{
		{name: "ok", status: http.StatusOK, body: good},
		{name: "not found", status: http.StatusNotFound, body: good, wantErr: true},
		{name: "server error", status: http.StatusServiceUnavailable, body: good, wantErr: true},
		{name: "too small", status: http.StatusOK, body: []byte("nope"), wantErr: true},
		{name: "html error page", status: http.StatusOK,
			body:    append([]byte("<!DOCTYPE html>"), bytes.Repeat([]byte(" "), minRuleSetBytes)...),
			wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tc.status)
				_, _ = w.Write(tc.body)
			}))
			defer srv.Close()
			data, err := downloadRuleSet(context.Background(), srv.Client(), srv.URL)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error, got %d bytes", len(data))
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !bytes.Equal(data, tc.body) {
				t.Fatal("downloaded content does not match response body")
			}
		})
	}
}

func TestIsRuleSetInitFailure(t *testing.T) {
	// sing-box 真实日志：规则集拉不下来时它不是降级，而是直接 FATAL 退出
	const realLog = `FATAL start service: (initialize rule-set[2]: initial rule-set: ` +
		`geoip-cn: Get "https://raw.githubusercontent.com/SagerNet/sing-geoip/rule-set/geoip-cn.srs": ` +
		`unexpected HTTP response status: 503 Service Unavailable)`
	if !isRuleSetInitFailure(realLog) {
		t.Fatal("real sing-box rule-set FATAL log was not recognized")
	}
	for _, unrelated := range []string{
		"",
		"proxy listener exited before 127.0.0.1:10808 became ready",
		"FATAL start service: (initialize inbound/socks[socks-in]: listen tcp: address already in use)",
	} {
		if isRuleSetInitFailure(unrelated) {
			t.Fatalf("unrelated failure treated as rule-set failure: %q", unrelated)
		}
	}
}

// 日志缓冲得封顶，但对 sing-box 必须报写成功，否则它会把短写当成管道错误。
func TestCoreLogTailCapsSizeButReportsFullWrite(t *testing.T) {
	logs := newCoreLogTail()
	chunk := make([]byte, maxCoreLogTailBytes)
	for i := range chunk {
		chunk[i] = 'a'
	}
	if n, err := logs.Write(chunk); n != len(chunk) || err != nil {
		t.Fatalf("Write = (%d, %v), want (%d, nil)", n, err, len(chunk))
	}
	if n, err := logs.Write([]byte("overflow")); n != len("overflow") || err != nil {
		t.Fatalf("Write past cap = (%d, %v), want (%d, nil)", n, err, len("overflow"))
	}
	if got := len(logs.String()); got > maxCoreLogTailBytes {
		t.Fatalf("buffered %d bytes, want at most %d", got, maxCoreLogTailBytes)
	}
}
