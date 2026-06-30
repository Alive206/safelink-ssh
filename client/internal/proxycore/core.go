// Package proxycore builds sing-box configs and manages the sing-box process.
package proxycore

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/example/safelink/pkg/config"
	"github.com/example/safelink/pkg/proxysubscription"
)

const (
	StateStopped = "stopped"
	StateRunning = "running"
	StateError   = "error"

	DefaultTestURL = "http://www.gstatic.com/generate_204"

	ProxyModeRule   = "rule"
	ProxyModeGlobal = "global"
	ProxyModeDirect = "direct"
)

var remoteProxyRuleSetURLs = map[string]string{
	config.ProxyRuleSetGeoIPCN:                 "https://raw.githubusercontent.com/SagerNet/sing-geoip/rule-set/geoip-cn.srs",
	config.ProxyRuleSetGeositeGeolocationCN:    "https://raw.githubusercontent.com/SagerNet/sing-geosite/rule-set/geosite-geolocation-cn.srs",
	config.ProxyRuleSetGeositeGeolocationNotCN: "https://raw.githubusercontent.com/SagerNet/sing-geosite/rule-set/geosite-geolocation-!cn.srs",
}

var testURLs = []string{
	"https://www.gstatic.com/generate_204",
	"https://connectivitycheck.gstatic.com/generate_204",
	"https://cp.cloudflare.com/generate_204",
	"http://cp.cloudflare.com/generate_204",
	"http://www.gstatic.com/generate_204",
	"http://connectivitycheck.gstatic.com/generate_204",
	"http://connectivitycheck.platform.hicloud.com/generate_204",
	"http://www.msftconnecttest.com/connecttest.txt",
}

type Options struct {
	CorePath      string
	WorkDir       string
	SOCKSAddr     string
	HTTPAddr      string
	ClashAPIAddr  string
	Mode          string
	RuleModeRules []config.ProxyRule
}

type Status struct {
	State              string `json:"state"`
	NodeName           string `json:"node_name,omitempty"`
	SOCKSAddr          string `json:"socks_addr"`
	HTTPAddr           string `json:"http_addr"`
	Mode               string `json:"mode"`
	CorePath           string `json:"core_path,omitempty"`
	CoreAvailable      bool   `json:"core_available"`
	LastError          string `json:"last_error,omitempty"`
	StartedAt          string `json:"started_at,omitempty"`
	UploadSpeedBps     uint64 `json:"upload_speed_bps"`
	DownloadSpeedBps   uint64 `json:"download_speed_bps"`
	UploadTotalBytes   uint64 `json:"upload_total_bytes"`
	DownloadTotalBytes uint64 `json:"download_total_bytes"`
}

type TestResult struct {
	NodeName  string  `json:"node_name"`
	OK        bool    `json:"ok"`
	LatencyMS int64   `json:"latency_ms,omitempty"`
	SpeedMbps float64 `json:"speed_mbps,omitempty"`
	Error     string  `json:"error,omitempty"`
	TestedAt  string  `json:"tested_at"`
}

type Runner struct {
	mu              sync.Mutex
	opts            Options
	cmd             *exec.Cmd
	cancel          context.CancelFunc
	done            chan struct{}
	state           string
	nodeName        string
	lastError       string
	startedAt       time.Time
	lastTraffic     connectionTraffic
	lastTrafficAt   time.Time
	lastTrafficNode string
}

func New(opts Options) *Runner {
	opts = normalizeOptions(opts)
	return &Runner{opts: opts, state: StateStopped}
}

func (r *Runner) SetMode(mode string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.opts.Mode = NormalizeMode(mode)
}

func (r *Runner) SetRuleModeRules(rules []config.ProxyRule) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.opts.RuleModeRules = normalizeRuleModeRules(rules)
}

func (r *Runner) Start(ctx context.Context, node proxysubscription.ProxyNode) error {
	r.mu.Lock()
	if r.cmd != nil {
		r.mu.Unlock()
		return errors.New("proxy core is already running")
	}
	opts := normalizeOptions(r.opts)
	if opts.CorePath == "" {
		opts.CorePath = FindCorePath("")
	}
	if opts.CorePath == "" {
		r.mu.Unlock()
		return errors.New("sing-box executable not found")
	}
	if err := os.MkdirAll(opts.WorkDir, 0o755); err != nil {
		r.mu.Unlock()
		return fmt.Errorf("create proxy work dir: %w", err)
	}
	if opts.ClashAPIAddr == "" {
		if addr, err := freeLoopbackAddr(); err == nil {
			opts.ClashAPIAddr = addr
		}
	}
	cfg, err := BuildConfig(node, opts)
	if err != nil {
		r.mu.Unlock()
		return err
	}
	configPath := filepath.Join(opts.WorkDir, "sing-box.json")
	if err := os.WriteFile(configPath, cfg, 0o600); err != nil {
		r.mu.Unlock()
		return fmt.Errorf("write sing-box config: %w", err)
	}
	checkCtx, checkCancel := context.WithTimeout(ctx, 5*time.Second)
	if err := checkCoreConfig(checkCtx, opts.CorePath, configPath, opts.WorkDir); err != nil {
		checkCancel()
		r.mu.Unlock()
		return err
	}
	checkCancel()

	runCtx, cancel := context.WithCancel(ctx)
	cmd := exec.CommandContext(runCtx, opts.CorePath, "run", "-c", configPath)
	cmd.Dir = opts.WorkDir
	hideCommandWindow(cmd)
	if err := cmd.Start(); err != nil {
		cancel()
		r.mu.Unlock()
		return fmt.Errorf("start sing-box: %w", err)
	}
	r.opts = opts
	r.cmd = cmd
	r.cancel = cancel
	r.done = make(chan struct{})
	r.state = StateRunning
	r.nodeName = node.Name
	r.lastError = ""
	r.startedAt = time.Now()
	r.lastTraffic = connectionTraffic{}
	r.lastTrafficAt = time.Time{}
	r.lastTrafficNode = node.Name
	done := r.done
	readyAddrs := proxyReadyAddrs(opts)
	r.mu.Unlock()

	go func() {
		err := cmd.Wait()
		r.mu.Lock()
		defer r.mu.Unlock()
		if r.cmd == cmd {
			r.cmd = nil
			r.cancel = nil
			r.state = StateStopped
			if err != nil && runCtx.Err() == nil {
				r.state = StateError
				r.lastError = err.Error()
			}
			r.startedAt = time.Time{}
			r.lastTraffic = connectionTraffic{}
			r.lastTrafficAt = time.Time{}
			r.lastTrafficNode = ""
		}
		close(done)
	}()

	readyCtx, readyCancel := context.WithTimeout(ctx, 5*time.Second)
	defer readyCancel()
	for _, addr := range readyAddrs {
		if err := waitForLocalTCPWithDone(readyCtx, addr, done, "proxy listener"); err != nil {
			cancel()
			if done != nil {
				<-done
			}
			r.mu.Lock()
			r.state = StateError
			r.nodeName = node.Name
			r.lastError = err.Error()
			r.startedAt = time.Time{}
			r.lastTraffic = connectionTraffic{}
			r.lastTrafficAt = time.Time{}
			r.lastTrafficNode = ""
			r.mu.Unlock()
			return err
		}
	}
	return nil
}

func (r *Runner) Stop() error {
	r.mu.Lock()
	cancel := r.cancel
	done := r.done
	r.mu.Unlock()
	if cancel == nil {
		return nil
	}
	cancel()
	if done != nil {
		<-done
	}
	return nil
}

func (r *Runner) Status() Status {
	r.mu.Lock()
	opts := normalizeOptions(r.opts)
	if opts.CorePath == "" {
		opts.CorePath = FindCorePath("")
	}
	status := Status{
		State:         r.state,
		NodeName:      r.nodeName,
		SOCKSAddr:     opts.SOCKSAddr,
		HTTPAddr:      opts.HTTPAddr,
		Mode:          opts.Mode,
		CorePath:      opts.CorePath,
		CoreAvailable: opts.CorePath != "",
		LastError:     r.lastError,
	}
	if !r.startedAt.IsZero() {
		status.StartedAt = r.startedAt.Format(time.RFC3339)
	}
	apiAddr := opts.ClashAPIAddr
	r.mu.Unlock()

	if status.State != StateRunning || apiAddr == "" {
		return status
	}
	ctx, cancel := context.WithTimeout(context.Background(), 600*time.Millisecond)
	defer cancel()
	traffic, err := queryConnectionTraffic(ctx, apiAddr)
	if err != nil {
		return status
	}
	r.applyTraffic(&status, traffic, time.Now())
	return status
}

func (r *Runner) applyTraffic(status *Status, traffic connectionTraffic, now time.Time) {
	r.mu.Lock()
	defer r.mu.Unlock()

	status.UploadTotalBytes = traffic.UploadTotal
	status.DownloadTotalBytes = traffic.DownloadTotal
	if r.state != StateRunning || r.nodeName != status.NodeName {
		return
	}
	if r.lastTrafficNode != status.NodeName {
		r.lastTraffic = connectionTraffic{}
		r.lastTrafficAt = time.Time{}
		r.lastTrafficNode = status.NodeName
	}
	if !r.lastTrafficAt.IsZero() {
		elapsed := now.Sub(r.lastTrafficAt).Seconds()
		if elapsed > 0 {
			if traffic.UploadTotal >= r.lastTraffic.UploadTotal {
				status.UploadSpeedBps = uint64(float64(traffic.UploadTotal-r.lastTraffic.UploadTotal) / elapsed)
			}
			if traffic.DownloadTotal >= r.lastTraffic.DownloadTotal {
				status.DownloadSpeedBps = uint64(float64(traffic.DownloadTotal-r.lastTraffic.DownloadTotal) / elapsed)
			}
		}
	}
	r.lastTraffic = traffic
	r.lastTrafficAt = now
	r.lastTrafficNode = status.NodeName
}

func (r *Runner) Test(ctx context.Context, node proxysubscription.ProxyNode, timeout time.Duration) TestResult {
	r.mu.Lock()
	opts := normalizeOptions(r.opts)
	r.mu.Unlock()
	return TestNode(ctx, node, opts, timeout)
}

func TestNode(ctx context.Context, node proxysubscription.ProxyNode, opts Options, timeout time.Duration) TestResult {
	result := TestResult{
		NodeName: node.Name,
		TestedAt: time.Now().Format(time.RFC3339),
	}
	if node.Server == "" || node.Port <= 0 {
		result.Error = "proxy node has invalid server or port"
		return result
	}
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	opts = normalizeOptions(opts)
	if opts.CorePath == "" {
		opts.CorePath = FindCorePath("")
	}
	if opts.CorePath == "" {
		result.Error = "sing-box executable not found"
		return result
	}
	if err := os.MkdirAll(opts.WorkDir, 0o755); err != nil {
		result.Error = fmt.Sprintf("create proxy work dir: %v", err)
		return result
	}

	testCtx, cancel := context.WithTimeout(ctx, timeout+3*time.Second)
	defer cancel()

	httpAddr, err := freeLoopbackAddr()
	if err != nil {
		result.Error = err.Error()
		return result
	}
	socksAddr, err := freeLoopbackAddr()
	if err != nil {
		result.Error = err.Error()
		return result
	}
	clashAPIAddr, err := freeLoopbackAddr()
	if err != nil {
		result.Error = err.Error()
		return result
	}
	testDir, err := os.MkdirTemp(opts.WorkDir, "test-*")
	if err != nil {
		result.Error = fmt.Sprintf("create test work dir: %v", err)
		return result
	}
	defer func() { _ = os.RemoveAll(testDir) }()

	cfg, err := BuildConfig(node, Options{
		CorePath:     opts.CorePath,
		WorkDir:      testDir,
		SOCKSAddr:    socksAddr,
		HTTPAddr:     httpAddr,
		ClashAPIAddr: clashAPIAddr,
	})
	if err != nil {
		result.Error = err.Error()
		return result
	}
	configPath := filepath.Join(testDir, "sing-box-test.json")
	if err := os.WriteFile(configPath, cfg, 0o600); err != nil {
		result.Error = fmt.Sprintf("write test config: %v", err)
		return result
	}
	if err := checkCoreConfig(testCtx, opts.CorePath, configPath, testDir); err != nil {
		result.Error = err.Error()
		return result
	}

	runCtx, stopCore := context.WithCancel(testCtx)
	defer stopCore()
	var coreOutput bytes.Buffer
	cmd := exec.CommandContext(runCtx, opts.CorePath, "run", "-c", configPath)
	cmd.Dir = testDir
	cmd.Stdout = &coreOutput
	cmd.Stderr = &coreOutput
	hideCommandWindow(cmd)
	if err := cmd.Start(); err != nil {
		result.Error = fmt.Sprintf("start sing-box: %v", err)
		return result
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	defer func() {
		stopCore()
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			if cmd.Process != nil {
				_ = cmd.Process.Kill()
			}
			<-done
		}
	}()

	if err := waitForLocalTCP(testCtx, clashAPIAddr); err != nil {
		output := strings.TrimSpace(coreOutput.String())
		if output != "" {
			result.Error = fmt.Sprintf("%v: %s", err, output)
		} else {
			result.Error = err.Error()
		}
		return result
	}

	latency, err := testCoreDelay(testCtx, clashAPIAddr, timeout)
	if err != nil {
		fallbackLatency, fallbackErr := testHTTPInboundDelay(testCtx, httpAddr, timeout)
		if fallbackErr != nil {
			result.Error = fmt.Sprintf("%v; fallback proxy request failed: %v", err, fallbackErr)
			return result
		}
		latency = fallbackLatency
	}
	result.OK = true
	result.LatencyMS = latency
	return result
}

func checkCoreConfig(ctx context.Context, corePath, configPath, workDir string) error {
	cmd := exec.CommandContext(ctx, corePath, "check", "-c", configPath)
	cmd.Dir = workDir
	hideCommandWindow(cmd)
	output, err := cmd.CombinedOutput()
	if err == nil {
		return nil
	}
	message := strings.TrimSpace(string(output))
	if message != "" {
		return fmt.Errorf("check sing-box config: %s", message)
	}
	return fmt.Errorf("check sing-box config: %w", err)
}

func testHTTPThroughProxy(ctx context.Context, client *http.Client) (int64, error) {
	var attempts []string
	for _, testURL := range testURLs {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, testURL, nil)
		if err != nil {
			attempts = append(attempts, fmt.Sprintf("%s: %v", testURL, err))
			continue
		}
		req.Header.Set("User-Agent", "SafeLink/1.0")

		start := time.Now()
		resp, err := client.Do(req)
		if err != nil {
			attempts = append(attempts, fmt.Sprintf("%s: %v", testURL, err))
			continue
		}
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1024))
		_ = resp.Body.Close()
		if resp.StatusCode >= 500 {
			attempts = append(attempts, fmt.Sprintf("%s: HTTP %d", testURL, resp.StatusCode))
			continue
		}
		return max(time.Since(start).Milliseconds(), 1), nil
	}
	if len(attempts) == 0 {
		return 0, errors.New("no test URL available")
	}
	return 0, fmt.Errorf("proxy test failed: %s", strings.Join(attempts, "; "))
}

func testHTTPInboundDelay(ctx context.Context, httpAddr string, timeout time.Duration) (int64, error) {
	if err := waitForLocalTCP(ctx, httpAddr); err != nil {
		return 0, err
	}
	dialTimeout := timeout
	if dialTimeout <= 0 || dialTimeout > 3*time.Second {
		dialTimeout = 3 * time.Second
	}
	proxyURL := &url.URL{Scheme: "http", Host: httpAddr}
	transport := &http.Transport{
		Proxy:                 http.ProxyURL(proxyURL),
		DialContext:           (&net.Dialer{Timeout: dialTimeout}).DialContext,
		TLSHandshakeTimeout:   dialTimeout,
		ResponseHeaderTimeout: timeout,
	}
	defer transport.CloseIdleConnections()
	return testHTTPThroughProxy(ctx, &http.Client{
		Timeout:   timeout,
		Transport: transport,
	})
}

type clashDelayResponse struct {
	Delay int64  `json:"delay"`
	Error string `json:"error"`
}

func testCoreDelay(ctx context.Context, clashAPIAddr string, timeout time.Duration) (int64, error) {
	if len(testURLs) == 0 {
		return 0, errors.New("no delay test URL available")
	}
	testCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	client := &http.Client{Timeout: timeout}
	results := make(chan delayAttemptResult, len(testURLs))
	for _, testURL := range testURLs {
		go func(testURL string) {
			delay, err := testCoreDelayURL(testCtx, client, clashAPIAddr, timeout, testURL)
			if err != nil {
				results <- delayAttemptResult{attempt: fmt.Sprintf("%s: %v", testURL, err)}
				return
			}
			results <- delayAttemptResult{delay: delay}
		}(testURL)
	}

	var attempts []string
	for range testURLs {
		select {
		case <-ctx.Done():
			return 0, ctx.Err()
		case result := <-results:
			if result.attempt == "" {
				cancel()
				return result.delay, nil
			}
			attempts = append(attempts, result.attempt)
		}
	}
	return 0, fmt.Errorf("proxy delay test failed: %s", strings.Join(attempts, "; "))
}

type delayAttemptResult struct {
	delay   int64
	attempt string
}

type connectionTraffic struct {
	UploadTotal   uint64 `json:"uploadTotal"`
	DownloadTotal uint64 `json:"downloadTotal"`
}

func queryConnectionTraffic(ctx context.Context, clashAPIAddr string) (connectionTraffic, error) {
	endpoint := url.URL{
		Scheme: "http",
		Host:   clashAPIAddr,
		Path:   "/connections",
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return connectionTraffic{}, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return connectionTraffic{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return connectionTraffic{}, fmt.Errorf("query sing-box connections: HTTP %d %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var payload connectionTraffic
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&payload); err != nil {
		return connectionTraffic{}, fmt.Errorf("parse sing-box connections: %w", err)
	}
	return payload, nil
}

func testCoreDelayURL(ctx context.Context, client *http.Client, clashAPIAddr string, timeout time.Duration, testURL string) (int64, error) {
	endpoint := url.URL{
		Scheme: "http",
		Host:   clashAPIAddr,
		Path:   "/proxies/selected/delay",
	}
	q := endpoint.Query()
	q.Set("timeout", fmt.Sprintf("%d", timeout.Milliseconds()))
	q.Set("url", testURL)
	endpoint.RawQuery = q.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return 0, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return 0, err
	}
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	_ = resp.Body.Close()
	if resp.StatusCode >= 500 {
		return 0, fmt.Errorf("HTTP %d %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var payload clashDelayResponse
	if err := json.Unmarshal(body, &payload); err != nil {
		return 0, fmt.Errorf("parse delay response: %w", err)
	}
	if payload.Error != "" {
		return 0, errors.New(payload.Error)
	}
	if payload.Delay <= 0 {
		return 0, fmt.Errorf("invalid delay %d", payload.Delay)
	}
	return payload.Delay, nil
}

var speedTestURLs = []string{
	"https://speed.cloudflare.com/__down?bytes=524288",
	"https://cachefly.cachefly.net/1mb.test",
	"https://proof.ovh.net/files/1Mb.dat",
}

func testDownloadSpeedThroughProxy(ctx context.Context, client *http.Client) (float64, error) {
	const minBytes = 32 * 1024
	const maxBytes = 512 * 1024
	var attempts []string
	for _, testURL := range speedTestURLs {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, testURL, nil)
		if err != nil {
			attempts = append(attempts, fmt.Sprintf("%s: %v", testURL, err))
			continue
		}
		req.Header.Set("User-Agent", "SafeLink/1.0")

		start := time.Now()
		resp, err := client.Do(req)
		if err != nil {
			attempts = append(attempts, fmt.Sprintf("%s: %v", testURL, err))
			continue
		}
		n, copyErr := io.Copy(io.Discard, io.LimitReader(resp.Body, maxBytes))
		_ = resp.Body.Close()
		if copyErr != nil {
			attempts = append(attempts, fmt.Sprintf("%s: %v", testURL, copyErr))
			continue
		}
		if resp.StatusCode >= 500 {
			attempts = append(attempts, fmt.Sprintf("%s: HTTP %d", testURL, resp.StatusCode))
			continue
		}
		if n < minBytes {
			attempts = append(attempts, fmt.Sprintf("%s: downloaded only %d bytes", testURL, n))
			continue
		}
		elapsed := time.Since(start).Seconds()
		if elapsed <= 0 {
			return 0, errors.New("speed test duration is zero")
		}
		return float64(n) * 8 / elapsed / 1_000_000, nil
	}
	if len(attempts) == 0 {
		return 0, errors.New("no speed test URL available")
	}
	return 0, fmt.Errorf("proxy speed test failed: %s", strings.Join(attempts, "; "))
}

func freeLoopbackAddr() (string, error) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", fmt.Errorf("allocate local test port: %w", err)
	}
	defer listener.Close()
	return listener.Addr().String(), nil
}

func waitForLocalTCP(ctx context.Context, addr string) error {
	return waitForLocalTCPWithDone(ctx, addr, nil, "sing-box test inbound")
}

func waitForLocalTCPWithDone(ctx context.Context, addr string, done <-chan struct{}, label string) error {
	deadline := time.NewTimer(3 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	for {
		dialer := net.Dialer{Timeout: 200 * time.Millisecond}
		conn, err := dialer.DialContext(ctx, "tcp", addr)
		if err == nil {
			_ = conn.Close()
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-done:
			return fmt.Errorf("%s exited before %s became ready", label, addr)
		case <-deadline.C:
			return fmt.Errorf("timed out waiting for %s %s", label, addr)
		case <-ticker.C:
		}
	}
}

func proxyReadyAddrs(opts Options) []string {
	addrs := make([]string, 0, 2)
	if opts.ClashAPIAddr != "" {
		addrs = append(addrs, opts.ClashAPIAddr)
	}
	if opts.HTTPAddr != "" {
		addrs = append(addrs, opts.HTTPAddr)
	} else if opts.SOCKSAddr != "" {
		addrs = append(addrs, opts.SOCKSAddr)
	}
	return addrs
}

func BuildConfig(node proxysubscription.ProxyNode, opts Options) ([]byte, error) {
	opts = normalizeOptions(opts)
	outbound, err := buildOutbound(node)
	if err != nil {
		return nil, err
	}
	route, err := buildRoute(opts.Mode, opts.RuleModeRules)
	if err != nil {
		return nil, err
	}
	cfg := map[string]any{
		"log": map[string]any{
			"level": "info",
		},
		"inbounds": []map[string]any{
			buildInbound("socks-in", "socks", opts.SOCKSAddr),
			buildInbound("http-in", "http", opts.HTTPAddr),
		},
		"outbounds": []map[string]any{
			outbound,
			{"type": "direct", "tag": "direct"},
			{"type": "block", "tag": "block"},
		},
		"route": route,
	}
	experimental := make(map[string]any)
	if _, ok := route["rule_set"]; ok {
		experimental["cache_file"] = map[string]any{
			"enabled": true,
		}
	}
	if opts.ClashAPIAddr != "" {
		experimental["clash_api"] = map[string]any{
			"external_controller": opts.ClashAPIAddr,
			"secret":              "",
		}
	}
	if len(experimental) > 0 {
		cfg["experimental"] = experimental
	}
	return json.MarshalIndent(cfg, "", "  ")
}

func buildRoute(mode string, rules []config.ProxyRule) (map[string]any, error) {
	switch NormalizeMode(mode) {
	case ProxyModeDirect:
		return map[string]any{"final": "direct"}, nil
	case ProxyModeGlobal:
		return map[string]any{"final": "selected"}, nil
	default:
		rules = normalizeRuleModeRules(rules)
		if err := config.ValidateProxyRules(rules); err != nil {
			return nil, err
		}
		routeRules := make([]map[string]any, 0, len(rules))
		ruleSets := make([]string, 0)
		for _, rule := range rules {
			if !rule.Enabled || strings.TrimSpace(rule.Value) == "" {
				continue
			}
			routeRule := map[string]any{
				proxyRuleRouteKey(rule.Type): []string{rule.Value},
				"outbound":                   rule.Outbound,
			}
			if rule.Type == config.ProxyRuleTypeRuleSet {
				ruleSets = append(ruleSets, rule.Value)
			}
			routeRules = append(routeRules, routeRule)
		}
		route := map[string]any{
			"rules": routeRules,
			"final": "selected",
		}
		if definitions := buildRemoteRuleSetDefinitions(ruleSets); len(definitions) > 0 {
			route["rule_set"] = definitions
		}
		return route, nil
	}
}

func proxyRuleRouteKey(ruleType string) string {
	switch config.NormalizeProxyRuleType(ruleType) {
	case config.ProxyRuleTypeDomain:
		return "domain"
	case config.ProxyRuleTypeDomainKeyword:
		return "domain_keyword"
	case config.ProxyRuleTypeIPCIDR:
		return "ip_cidr"
	case config.ProxyRuleTypeRuleSet:
		return "rule_set"
	default:
		return "domain_suffix"
	}
}

func buildRemoteRuleSetDefinitions(ruleSets []string) []map[string]any {
	seen := make(map[string]bool, len(ruleSets))
	definitions := make([]map[string]any, 0, len(ruleSets))
	for _, ruleSet := range ruleSets {
		tag := strings.ToLower(strings.TrimSpace(ruleSet))
		if seen[tag] {
			continue
		}
		url, ok := remoteProxyRuleSetURLs[tag]
		if !ok {
			continue
		}
		seen[tag] = true
		definitions = append(definitions, map[string]any{
			"type":            "remote",
			"tag":             tag,
			"format":          "binary",
			"url":             url,
			"download_detour": "selected",
			"update_interval": "24h",
		})
	}
	return definitions
}

func buildInbound(tag, inboundType, addr string) map[string]any {
	host, port := splitListenAddr(addr)
	return map[string]any{
		"type":        inboundType,
		"tag":         tag,
		"listen":      host,
		"listen_port": port,
	}
}

func buildOutbound(node proxysubscription.ProxyNode) (map[string]any, error) {
	out := map[string]any{
		"type":        singBoxProtocol(node.Protocol),
		"tag":         "selected",
		"server":      node.Server,
		"server_port": node.Port,
	}
	if out["type"] == "" {
		return nil, fmt.Errorf("unsupported proxy protocol %q", node.Protocol)
	}
	switch node.Protocol {
	case proxysubscription.ProtocolShadowsocks:
		out["method"] = node.Method
		out["password"] = node.Password
	case proxysubscription.ProtocolVMess:
		out["uuid"] = node.UUID
		out["security"] = firstNonEmpty(node.Security, "auto")
		if node.AlterID > 0 {
			out["alter_id"] = node.AlterID
		}
	case proxysubscription.ProtocolVLESS:
		out["uuid"] = node.UUID
		if node.Flow != "" {
			out["flow"] = node.Flow
		}
	case proxysubscription.ProtocolTrojan, proxysubscription.ProtocolHysteria, proxysubscription.ProtocolHysteria2, proxysubscription.ProtocolAnyTLS:
		out["password"] = node.Password
	case proxysubscription.ProtocolTUIC:
		out["uuid"] = node.UUID
		out["password"] = node.Password
	}
	if node.TLS != nil && node.TLS.Enabled {
		out["tls"] = buildTLS(node.TLS)
	}
	if supportsOutboundTransport(node.Protocol) && normalizedTransportType(node.Transport.Type) != "" {
		out["transport"] = buildTransport(node.Transport)
	}
	return out, nil
}

func buildTLS(tls *proxysubscription.TLSOptions) map[string]any {
	out := map[string]any{"enabled": tls.Enabled}
	if tls.ServerName != "" {
		out["server_name"] = tls.ServerName
	}
	if tls.Insecure {
		out["insecure"] = true
	}
	if len(tls.ALPN) > 0 {
		out["alpn"] = tls.ALPN
	}
	if tls.PublicKey != "" {
		out["reality"] = map[string]any{
			"enabled":    true,
			"public_key": tls.PublicKey,
			"short_id":   tls.ShortID,
		}
	}
	if tls.Fingerprint != "" {
		out["utls"] = map[string]any{
			"enabled":     true,
			"fingerprint": tls.Fingerprint,
		}
	}
	return out
}

func buildTransport(transport proxysubscription.TransportOptions) map[string]any {
	out := map[string]any{"type": normalizedTransportType(transport.Type)}
	if transport.Path != "" {
		out["path"] = transport.Path
	}
	if transport.Host != "" {
		out["headers"] = map[string]string{"Host": transport.Host}
	} else if len(transport.Headers) > 0 {
		out["headers"] = transport.Headers
	}
	return out
}

func supportsOutboundTransport(protocol string) bool {
	switch protocol {
	case proxysubscription.ProtocolVMess, proxysubscription.ProtocolVLESS, proxysubscription.ProtocolTrojan:
		return true
	default:
		return false
	}
}

func normalizedTransportType(transportType string) string {
	switch strings.ToLower(strings.TrimSpace(transportType)) {
	case "", "tcp":
		return ""
	case "websocket":
		return "ws"
	default:
		return strings.ToLower(strings.TrimSpace(transportType))
	}
}

func FindCorePath(baseDir string) string {
	candidates := []string{
		filepath.Join(baseDir, "bin", "sing-box.exe"),
		filepath.Join(baseDir, "sing-box.exe"),
		"sing-box.exe",
		"sing-box",
	}
	for _, candidate := range candidates {
		if path, err := exec.LookPath(candidate); err == nil {
			return absPath(path)
		}
		if _, err := os.Stat(candidate); err == nil {
			return absPath(candidate)
		}
	}
	return ""
}

func absPath(path string) string {
	abs, err := filepath.Abs(path)
	if err != nil {
		return path
	}
	return abs
}

func normalizeOptions(opts Options) Options {
	if opts.SOCKSAddr == "" {
		opts.SOCKSAddr = "127.0.0.1:10808"
	}
	if opts.HTTPAddr == "" {
		opts.HTTPAddr = "127.0.0.1:10809"
	}
	if opts.WorkDir == "" {
		opts.WorkDir = filepath.Join(os.TempDir(), "safelink-proxy")
	}
	opts.Mode = NormalizeMode(opts.Mode)
	opts.RuleModeRules = normalizeRuleModeRules(opts.RuleModeRules)
	return opts
}

func normalizeRuleModeRules(rules []config.ProxyRule) []config.ProxyRule {
	if rules == nil {
		rules = config.DefaultProxyRules()
	}
	normalized := config.NormalizeProxyRules(rules)
	result := make([]config.ProxyRule, len(normalized))
	copy(result, normalized)
	return result
}

func NormalizeMode(mode string) string {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case ProxyModeGlobal:
		return ProxyModeGlobal
	case ProxyModeDirect:
		return ProxyModeDirect
	default:
		return ProxyModeRule
	}
}

func splitListenAddr(addr string) (string, int) {
	host, portText, err := net.SplitHostPort(addr)
	if err != nil {
		return "127.0.0.1", 0
	}
	port, _ := strconvAtoi(portText)
	return host, port
}

func singBoxProtocol(protocol string) string {
	switch protocol {
	case proxysubscription.ProtocolShadowsocks:
		return "shadowsocks"
	case proxysubscription.ProtocolVMess:
		return "vmess"
	case proxysubscription.ProtocolVLESS:
		return "vless"
	case proxysubscription.ProtocolTrojan:
		return "trojan"
	case proxysubscription.ProtocolHysteria:
		return "hysteria"
	case proxysubscription.ProtocolHysteria2:
		return "hysteria2"
	case proxysubscription.ProtocolTUIC:
		return "tuic"
	case proxysubscription.ProtocolAnyTLS:
		return "anytls"
	default:
		return ""
	}
}

func strconvAtoi(text string) (int, error) {
	var n int
	for _, r := range strings.TrimSpace(text) {
		if r < '0' || r > '9' {
			return 0, fmt.Errorf("invalid integer %q", text)
		}
		n = n*10 + int(r-'0')
	}
	return n, nil
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
