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
	"net/netip"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
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

	// 每个测速操作都要单独拉起一个 sing-box 进程，进程级并发保持保守
	defaultNodeTestConcurrency = 8

	// 批量测速里所有节点共用同一个 sing-box，worker 只是并发查 Clash API，
	// 属于纯 I/O 等待，可以比进程级并发开大很多
	defaultBatchTestConcurrency = 32

	// TCP 握手测试不用拉进程，纯网络等待，并发可以再开大一些
	defaultTCPTestConcurrency = 64

	// 单次 TCP 握手容易受瞬时抖动影响，多量几次取最小值
	defaultTCPTestAttempts = 2

	// hysteria v1 refuses to start without bandwidth hints
	defaultHysteriaMbps = 100

	// sing-box 的 REALITY 客户端必须开 uTLS，链接里没给 fp 时用这个默认指纹
	defaultUTLSFingerprint = "chrome"
	// vmess/vless 的 UDP 封装默认用 xudp：机场服务端多为 Xray，不开的话 UDP 转发会异常
	defaultPacketEncoding = proxysubscription.PacketEncodingXUDP

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

// dnsDirectTag 是配置里直连 DNS 服务器的 tag，同时用作 default_domain_resolver。
const dnsDirectTag = "dns-direct"

// 系统没给出可用 DNS 时的兜底，公共 DNS 在部分园区网会被拦，所以优先用系统下发的。
var fallbackDNSServers = []string{"223.5.5.5", "119.29.29.29"}

var fastTestURLs = []string{
	"http://cp.cloudflare.com/generate_204",
	"http://www.gstatic.com/generate_204",
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
	// DNSServers 是解析节点服务器域名用的 DNS。Android 上没有 /etc/resolv.conf，
	// sing-box 默认的 local DNS 会退化成查 127.0.0.1:53 直接失败，必须显式给出。
	DNSServers []string
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
	testSlots       chan struct{}
}

func New(opts Options) *Runner {
	opts = normalizeOptions(opts)
	return &Runner{opts: opts, state: StateStopped, testSlots: make(chan struct{}, defaultNodeTestConcurrency)}
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
	if ctx == nil {
		ctx = context.Background()
	}
	testedAt := time.Now().Format(time.RFC3339)
	slot, err := r.acquireTestSlot(ctx)
	if err != nil {
		return TestResult{NodeName: node.Name, OK: false, Error: err.Error(), TestedAt: testedAt}
	}
	defer func() { <-slot }()

	r.mu.Lock()
	opts := normalizeOptions(r.opts)
	r.mu.Unlock()
	return TestNode(ctx, node, opts, timeout)
}

func (r *Runner) TestNodes(ctx context.Context, nodes []proxysubscription.ProxyNode, timeout time.Duration, onResult func(TestResult)) []TestResult {
	if ctx == nil {
		ctx = context.Background()
	}
	slot, err := r.acquireTestSlot(ctx)
	if err != nil {
		results := make([]TestResult, len(nodes))
		testedAt := time.Now().Format(time.RFC3339)
		for i, node := range nodes {
			results[i] = TestResult{NodeName: node.Name, OK: false, Error: err.Error(), TestedAt: testedAt}
			emitTestResult(onResult, results[i])
		}
		return results
	}
	defer func() { <-slot }()

	r.mu.Lock()
	opts := normalizeOptions(r.opts)
	r.mu.Unlock()
	return TestNodes(ctx, nodes, opts, timeout, onResult)
}

func (r *Runner) acquireTestSlot(ctx context.Context) (chan struct{}, error) {
	r.mu.Lock()
	if r.testSlots == nil {
		r.testSlots = make(chan struct{}, defaultNodeTestConcurrency)
	}
	slots := r.testSlots
	r.mu.Unlock()

	select {
	case slots <- struct{}{}:
		return slots, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func emitTestResult(onResult func(TestResult), result TestResult) {
	if onResult != nil {
		onResult(result)
	}
}

// TestNode 测单个节点的延迟。TCP 协议的节点直接量 TCP 握手耗时（tcping）：不用拉起
// sing-box，也不依赖 generate_204 这类探测域名，探测站点被墙时不会把好节点判成不可用。
// hysteria/tuic 这类跑在 QUIC(UDP) 上的节点没有 TCP 端口可握手，仍旧交给 core 探测。
func TestNode(ctx context.Context, node proxysubscription.ProxyNode, opts Options, timeout time.Duration) TestResult {
	if ctx == nil {
		ctx = context.Background()
	}
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
	if !tcpHandshakeProtocol(node.Protocol) {
		return testNodeWithCore(ctx, node, opts, timeout)
	}
	latency, err := testTCPHandshake(ctx, node, timeout)
	if err != nil {
		result.Error = err.Error()
		return result
	}
	result.OK = true
	result.LatencyMS = latency
	return result
}

// tcpHandshakeProtocol 判断协议是不是跑在 TCP 上。hysteria/hysteria2/tuic 只监听 UDP，
// 对它们做 TCP 握手必然失败，不能用 tcping 判断可用性。
func tcpHandshakeProtocol(protocol string) bool {
	switch protocol {
	case proxysubscription.ProtocolShadowsocks, proxysubscription.ProtocolVMess,
		proxysubscription.ProtocolVLESS, proxysubscription.ProtocolTrojan,
		proxysubscription.ProtocolAnyTLS:
		return true
	default:
		return false
	}
}

// testTCPHandshake 连续量几次 TCP 握手取最小值：第一次会带上 DNS 解析耗时，
// 取最小值能把解析开销和瞬时抖动抹掉。
func testTCPHandshake(ctx context.Context, node proxysubscription.ProxyNode, timeout time.Duration) (int64, error) {
	addr := net.JoinHostPort(node.Server, strconv.Itoa(node.Port))
	var best int64
	var lastErr error
	for attempt := 0; attempt < defaultTCPTestAttempts; attempt++ {
		if err := ctx.Err(); err != nil {
			lastErr = err
			break
		}
		latency, err := dialTCPLatency(ctx, addr, timeout)
		if err != nil {
			lastErr = err
			continue
		}
		if best == 0 || latency < best {
			best = latency
		}
	}
	if best > 0 {
		return best, nil
	}
	if lastErr == nil {
		lastErr = errors.New("no tcp handshake attempt succeeded")
	}
	return 0, fmt.Errorf("tcp connect %s failed: %w", addr, lastErr)
}

func dialTCPLatency(ctx context.Context, addr string, timeout time.Duration) (int64, error) {
	dialCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	start := time.Now()
	conn, err := (&net.Dialer{}).DialContext(dialCtx, "tcp", addr)
	if err != nil {
		return 0, err
	}
	latency := time.Since(start)
	_ = conn.Close()
	return max(latency.Milliseconds(), 1), nil
}

func testNodeWithCore(ctx context.Context, node proxysubscription.ProxyNode, opts Options, timeout time.Duration) TestResult {
	result := TestResult{
		NodeName: node.Name,
		TestedAt: time.Now().Format(time.RFC3339),
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
	readyDone := make(chan struct{})
	go func() {
		done <- cmd.Wait()
		close(readyDone)
	}()
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

	if err := waitForLocalTCPWithDone(testCtx, clashAPIAddr, readyDone, "sing-box test inbound"); err != nil {
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

func SpeedTestNode(ctx context.Context, node proxysubscription.ProxyNode, opts Options, timeout time.Duration) TestResult {
	if ctx == nil {
		ctx = context.Background()
	}
	result := TestResult{
		NodeName: node.Name,
		TestedAt: time.Now().Format(time.RFC3339),
	}
	if node.Server == "" || node.Port <= 0 {
		result.Error = "proxy node has invalid server or port"
		return result
	}
	if timeout <= 0 {
		timeout = 12 * time.Second
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
	testDir, err := os.MkdirTemp(opts.WorkDir, "speed-test-*")
	if err != nil {
		result.Error = fmt.Sprintf("create speed test work dir: %v", err)
		return result
	}
	defer func() { _ = os.RemoveAll(testDir) }()

	cfg, err := BuildConfig(node, Options{
		CorePath:      opts.CorePath,
		WorkDir:       testDir,
		Mode:          opts.Mode,
		SOCKSAddr:     socksAddr,
		HTTPAddr:      httpAddr,
		ClashAPIAddr:  clashAPIAddr,
		RuleModeRules: opts.RuleModeRules,
	})
	if err != nil {
		result.Error = err.Error()
		return result
	}
	configPath := filepath.Join(testDir, "sing-box-speed-test.json")
	if err := os.WriteFile(configPath, cfg, 0o600); err != nil {
		result.Error = fmt.Sprintf("write speed test config: %v", err)
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
	readyDone := make(chan struct{})
	go func() {
		done <- cmd.Wait()
		close(readyDone)
	}()
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

	if err := waitForLocalTCPWithDone(testCtx, httpAddr, readyDone, "sing-box speed test inbound"); err != nil {
		output := strings.TrimSpace(coreOutput.String())
		if output != "" {
			result.Error = fmt.Sprintf("%v: %s", err, output)
		} else {
			result.Error = err.Error()
		}
		return result
	}

	// 顺带量一次延迟，测速成功后列表可以直接显示延迟。口径和 TestNode 保持一致：
	// TCP 协议量握手耗时，QUIC 协议才走 core 探测。
	delayTimeout := timeout
	if delayTimeout > 5*time.Second {
		delayTimeout = 5 * time.Second
	}
	if tcpHandshakeProtocol(node.Protocol) {
		if latency, delayErr := testTCPHandshake(testCtx, node, delayTimeout); delayErr == nil {
			result.LatencyMS = latency
		}
	} else if latency, delayErr := testCoreDelay(testCtx, clashAPIAddr, delayTimeout); delayErr == nil {
		result.LatencyMS = latency
	} else if latency, delayErr := testHTTPInboundDelay(testCtx, httpAddr, delayTimeout); delayErr == nil {
		result.LatencyMS = latency
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
	speed, err := testDownloadSpeedThroughProxy(testCtx, &http.Client{
		Timeout:   timeout,
		Transport: transport,
	})
	if err != nil {
		result.Error = err.Error()
		return result
	}
	result.OK = true
	result.SpeedMbps = speed
	return result
}

type batchTestOutbound struct {
	index    int
	tag      string
	node     proxysubscription.ProxyNode
	outbound map[string]any
}

type batchTestAttempt struct {
	index  int
	result TestResult
}

// TestNodes 批量测延迟：先按协议分流，TCP 协议的节点并发量 TCP 握手（不用拉 sing-box），
// 只有 QUIC 协议的节点才共用一个 core 进程去探测。
func TestNodes(ctx context.Context, nodes []proxysubscription.ProxyNode, opts Options, timeout time.Duration, onResult func(TestResult)) []TestResult {
	if ctx == nil {
		ctx = context.Background()
	}
	if timeout <= 0 {
		timeout = 5 * time.Second
	}

	testedAt := time.Now().Format(time.RFC3339)
	results := make([]TestResult, len(nodes))
	for i, node := range nodes {
		results[i] = TestResult{
			NodeName: node.Name,
			TestedAt: testedAt,
		}
	}
	if len(nodes) == 0 {
		return results
	}

	tcpIndexes := make([]int, 0, len(nodes))
	coreIndexes := make([]int, 0, len(nodes))
	coreNodes := make([]proxysubscription.ProxyNode, 0, len(nodes))
	for i, node := range nodes {
		switch {
		case node.Server == "" || node.Port <= 0:
			results[i].Error = "proxy node has invalid server or port"
			emitTestResult(onResult, results[i])
		case tcpHandshakeProtocol(node.Protocol):
			tcpIndexes = append(tcpIndexes, i)
		default:
			coreIndexes = append(coreIndexes, i)
			coreNodes = append(coreNodes, node)
		}
	}

	if len(tcpIndexes) > 0 {
		testTCPNodes(ctx, nodes, tcpIndexes, results, timeout, onResult)
	}
	if len(coreNodes) > 0 {
		for i, result := range testNodesWithCore(ctx, coreNodes, opts, timeout, onResult) {
			results[coreIndexes[i]] = result
		}
	}
	return results
}

// testTCPNodes 并发跑 TCP 握手测试，结果写回 results 对应下标。
func testTCPNodes(ctx context.Context, nodes []proxysubscription.ProxyNode, indexes []int, results []TestResult, timeout time.Duration, onResult func(TestResult)) {
	jobs := make(chan int)
	var mu sync.Mutex
	var wg sync.WaitGroup
	for i := 0; i < min(defaultTCPTestConcurrency, len(indexes)); i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for index := range jobs {
				node := nodes[index]
				result := TestResult{
					NodeName: node.Name,
					TestedAt: time.Now().Format(time.RFC3339),
				}
				if latency, err := testTCPHandshake(ctx, node, timeout); err != nil {
					result.Error = err.Error()
				} else {
					result.OK = true
					result.LatencyMS = latency
				}
				mu.Lock()
				results[index] = result
				emitTestResult(onResult, result)
				mu.Unlock()
			}
		}()
	}
	for _, index := range indexes {
		jobs <- index
	}
	close(jobs)
	wg.Wait()
}

func testNodesWithCore(ctx context.Context, nodes []proxysubscription.ProxyNode, opts Options, timeout time.Duration, onResult func(TestResult)) []TestResult {
	testedAt := time.Now().Format(time.RFC3339)
	results := make([]TestResult, len(nodes))
	for i, node := range nodes {
		results[i] = TestResult{
			NodeName: node.Name,
			TestedAt: testedAt,
		}
	}
	if len(nodes) == 0 {
		return results
	}

	opts = normalizeOptions(opts)
	if opts.CorePath == "" {
		opts.CorePath = FindCorePath("")
	}
	if opts.CorePath == "" {
		return finishPendingTestResults(results, errors.New("sing-box executable not found"), onResult)
	}
	if err := os.MkdirAll(opts.WorkDir, 0o755); err != nil {
		return finishPendingTestResults(results, fmt.Errorf("create proxy work dir: %v", err), onResult)
	}

	outbounds := make([]batchTestOutbound, 0, len(nodes))
	for i, node := range nodes {
		if node.Server == "" || node.Port <= 0 {
			results[i].Error = "proxy node has invalid server or port"
			emitTestResult(onResult, results[i])
			continue
		}
		outbound, err := buildOutbound(node)
		if err != nil {
			results[i].Error = err.Error()
			emitTestResult(onResult, results[i])
			continue
		}
		tag := fmt.Sprintf("test-%d", i)
		outbound["tag"] = tag
		outbounds = append(outbounds, batchTestOutbound{
			index:    i,
			tag:      tag,
			node:     node,
			outbound: outbound,
		})
	}
	if len(outbounds) == 0 {
		return results
	}

	workerCount := min(defaultBatchTestConcurrency, len(outbounds))
	batchTimeout := 5*time.Second + timeout*time.Duration((len(outbounds)+workerCount-1)/workerCount)
	testCtx, cancel := context.WithTimeout(ctx, batchTimeout)
	defer cancel()

	httpAddr, err := freeLoopbackAddr()
	if err != nil {
		return finishPendingTestResults(results, err, onResult)
	}
	socksAddr, err := freeLoopbackAddr()
	if err != nil {
		return finishPendingTestResults(results, err, onResult)
	}
	clashAPIAddr, err := freeLoopbackAddr()
	if err != nil {
		return finishPendingTestResults(results, err, onResult)
	}
	testDir, err := os.MkdirTemp(opts.WorkDir, "batch-test-*")
	if err != nil {
		return finishPendingTestResults(results, fmt.Errorf("create test work dir: %v", err), onResult)
	}
	defer func() { _ = os.RemoveAll(testDir) }()

	cfg, err := buildBatchTestConfig(outbounds, Options{
		CorePath:     opts.CorePath,
		WorkDir:      testDir,
		SOCKSAddr:    socksAddr,
		HTTPAddr:     httpAddr,
		ClashAPIAddr: clashAPIAddr,
	})
	if err != nil {
		return finishPendingTestResults(results, err, onResult)
	}
	configPath := filepath.Join(testDir, "sing-box-batch-test.json")
	if err := os.WriteFile(configPath, cfg, 0o600); err != nil {
		return finishPendingTestResults(results, fmt.Errorf("write test config: %v", err), onResult)
	}
	// 这份配置由 buildBatchTestConfig 自己生成，不再额外跑一次 sing-box check：
	// 配置有问题时 core 会启动失败，错误信息同样会通过 coreOutput 带出来。

	runCtx, stopCore := context.WithCancel(testCtx)
	defer stopCore()
	var coreOutput bytes.Buffer
	cmd := exec.CommandContext(runCtx, opts.CorePath, "run", "-c", configPath)
	cmd.Dir = testDir
	cmd.Stdout = &coreOutput
	cmd.Stderr = &coreOutput
	hideCommandWindow(cmd)
	if err := cmd.Start(); err != nil {
		return finishPendingTestResults(results, fmt.Errorf("start sing-box: %v", err), onResult)
	}
	done := make(chan error, 1)
	readyDone := make(chan struct{})
	go func() {
		done <- cmd.Wait()
		close(readyDone)
	}()
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

	if err := waitForLocalTCPWithDone(testCtx, clashAPIAddr, readyDone, "sing-box batch test inbound"); err != nil {
		output := strings.TrimSpace(coreOutput.String())
		if output != "" {
			err = fmt.Errorf("%v: %s", err, output)
		}
		return finishPendingTestResults(results, err, onResult)
	}

	jobs := make(chan batchTestOutbound)
	attempts := make(chan batchTestAttempt, len(outbounds))
	var wg sync.WaitGroup
	for i := 0; i < workerCount; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for outbound := range jobs {
				result := TestResult{
					NodeName: outbound.node.Name,
					TestedAt: time.Now().Format(time.RFC3339),
				}
				latency, err := testCoreDelayForTag(testCtx, clashAPIAddr, outbound.tag, timeout, fastTestURLs)
				if err != nil {
					result.Error = err.Error()
				} else {
					result.OK = true
					result.LatencyMS = latency
				}
				attempts <- batchTestAttempt{index: outbound.index, result: result}
			}
		}()
	}
	go func() {
		for _, outbound := range outbounds {
			jobs <- outbound
		}
		close(jobs)
		wg.Wait()
		close(attempts)
	}()

	for attempt := range attempts {
		results[attempt.index] = attempt.result
		emitTestResult(onResult, attempt.result)
	}
	return finishPendingTestResults(results, testCtx.Err(), onResult)
}

func finishPendingTestResults(results []TestResult, err error, onResult func(TestResult)) []TestResult {
	if err == nil {
		return results
	}
	for i := range results {
		if results[i].OK || results[i].Error != "" {
			continue
		}
		results[i].Error = err.Error()
		emitTestResult(onResult, results[i])
	}
	return results
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
	return testCoreDelayForTag(ctx, clashAPIAddr, "selected", timeout, testURLs)
}

func testCoreDelayForTag(ctx context.Context, clashAPIAddr, proxyTag string, timeout time.Duration, urls []string) (int64, error) {
	if len(urls) == 0 {
		return 0, errors.New("no delay test URL available")
	}
	if len(urls) == 1 {
		return raceCoreDelayURLs(ctx, clashAPIAddr, proxyTag, timeout, urls)
	}
	// 先只打首选 URL，节点正常时一次请求就够；失败了再用剩下的预算并发补测其余 URL。
	// 批量测速时穿过代理的请求数因此从 len(urls) 降到 1，节点多的时候差别很明显。
	primaryTimeout := timeout / 2
	delay, primaryErr := raceCoreDelayURLs(ctx, clashAPIAddr, proxyTag, primaryTimeout, urls[:1])
	if primaryErr == nil {
		return delay, nil
	}
	if ctx.Err() != nil {
		return 0, ctx.Err()
	}
	delay, err := raceCoreDelayURLs(ctx, clashAPIAddr, proxyTag, timeout-primaryTimeout, urls[1:])
	if err != nil {
		return 0, fmt.Errorf("%v; %v", primaryErr, err)
	}
	return delay, nil
}

func raceCoreDelayURLs(ctx context.Context, clashAPIAddr, proxyTag string, timeout time.Duration, urls []string) (int64, error) {
	testCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	client := &http.Client{Timeout: timeout}
	results := make(chan delayAttemptResult, len(urls))
	for _, testURL := range urls {
		go func(testURL string) {
			delay, err := testCoreDelayURL(testCtx, client, clashAPIAddr, proxyTag, timeout, testURL)
			if err != nil {
				results <- delayAttemptResult{attempt: fmt.Sprintf("%s: %v", testURL, err)}
				return
			}
			results <- delayAttemptResult{delay: delay}
		}(testURL)
	}

	var attempts []string
	for range urls {
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

func testCoreDelayURL(ctx context.Context, client *http.Client, clashAPIAddr string, proxyTag string, timeout time.Duration, testURL string) (int64, error) {
	endpoint := url.URL{
		Scheme: "http",
		Host:   clashAPIAddr,
		Path:   "/proxies/" + proxyTag + "/delay",
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
	applyDNSConfig(cfg, route, opts)
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

func buildBatchTestConfig(nodes []batchTestOutbound, opts Options) ([]byte, error) {
	opts = normalizeOptions(opts)
	if len(nodes) == 0 {
		return nil, errors.New("no proxy nodes to test")
	}
	outbounds := make([]map[string]any, 0, len(nodes)+2)
	for _, node := range nodes {
		outbounds = append(outbounds, node.outbound)
	}
	outbounds = append(outbounds,
		map[string]any{"type": "direct", "tag": "direct"},
		map[string]any{"type": "block", "tag": "block"},
	)
	route := map[string]any{
		"final": nodes[0].tag,
	}
	cfg := map[string]any{
		"log": map[string]any{
			"level": "warn",
		},
		"inbounds": []map[string]any{
			buildInbound("socks-in", "socks", opts.SOCKSAddr),
			buildInbound("http-in", "http", opts.HTTPAddr),
		},
		"outbounds": outbounds,
		"route":     route,
		"experimental": map[string]any{
			"clash_api": map[string]any{
				"external_controller": opts.ClashAPIAddr,
				"secret":              "",
			},
		},
	}
	applyDNSConfig(cfg, route, opts)
	return json.MarshalIndent(cfg, "", "  ")
}

// applyDNSConfig 给配置补上 dns 段和 default_domain_resolver。缺了这两个，
// Android 上节点域名一个都解析不出来，测延迟和连接会全部失败。
func applyDNSConfig(cfg map[string]any, route map[string]any, opts Options) {
	server := firstDNSServer(opts.DNSServers)
	if server == "" {
		return
	}
	cfg["dns"] = map[string]any{
		"servers": []map[string]any{
			{"type": "udp", "tag": dnsDirectTag, "server": server},
		},
		"final":    dnsDirectTag,
		"strategy": "prefer_ipv4",
	}
	route["default_domain_resolver"] = map[string]any{"server": dnsDirectTag}
}

func firstDNSServer(servers []string) string {
	for _, server := range servers {
		if addr := strings.TrimSpace(server); addr != "" {
			return addr
		}
	}
	return ""
}

// normalizeDNSServers 只保留能直接喂给 sing-box 的 IP：链路本地和带 zone 的地址（fe80::1%wlan0）
// 用不了，先剔掉；再把 IPv4 排到前面，IPv6-only 的环境才会用到 v6。
// Android 上拿不到系统 DNS 时用公共 DNS 兜底；其他平台（Windows 等）的 local DNS
// 本身可用，不注入 dns 段以保持原有行为。
func normalizeDNSServers(servers []string) []string {
	v4 := make([]string, 0, len(servers))
	v6 := make([]string, 0, len(servers))
	for _, server := range servers {
		addr := strings.TrimSpace(server)
		if addr == "" {
			continue
		}
		ip, err := netip.ParseAddr(addr)
		if err != nil || !ip.IsValid() || ip.IsLoopback() || ip.IsUnspecified() {
			continue
		}
		if ip.Zone() != "" || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() {
			continue
		}
		if ip.Is4() || ip.Is4In6() {
			v4 = append(v4, ip.Unmap().String())
			continue
		}
		v6 = append(v6, ip.String())
	}
	normalized := append(v4, v6...)
	if len(normalized) == 0 && runtime.GOOS == "android" {
		return append(normalized, fallbackDNSServers...)
	}
	return normalized
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
		if encoding := packetEncodingFor(node.PacketEncoding); encoding != "" {
			out["packet_encoding"] = encoding
		}
	case proxysubscription.ProtocolVLESS:
		out["uuid"] = node.UUID
		if node.Flow != "" {
			out["flow"] = node.Flow
		}
		if encoding := packetEncodingFor(node.PacketEncoding); encoding != "" {
			out["packet_encoding"] = encoding
		}
	case proxysubscription.ProtocolTrojan, proxysubscription.ProtocolHysteria2, proxysubscription.ProtocolAnyTLS:
		out["password"] = node.Password
	case proxysubscription.ProtocolHysteria:
		// hysteria v1 uses auth_str and requires both bandwidth hints
		out["auth_str"] = node.Password
		out["up_mbps"] = defaultHysteriaMbps
		out["down_mbps"] = defaultHysteriaMbps
	case proxysubscription.ProtocolTUIC:
		out["uuid"] = node.UUID
		out["password"] = node.Password
	}
	if node.TLS != nil && node.TLS.Enabled {
		out["tls"] = buildTLS(node.TLS)
	} else if alwaysTLSProtocol(node.Protocol) {
		// hysteria/tuic/anytls 链接经常不带 security/sni，不补 tls 段 sing-box 会报 "TLS required"
		out["tls"] = buildTLS(&proxysubscription.TLSOptions{Enabled: true})
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
	fingerprint := tls.Fingerprint
	if tls.PublicKey != "" {
		out["reality"] = map[string]any{
			"enabled":    true,
			"public_key": tls.PublicKey,
			"short_id":   tls.ShortID,
		}
		// sing-box 的 REALITY 客户端必须配 uTLS（否则报 "uTLS is required by reality client"），
		// 而很多机场的 vless+reality 链接不带 fp 参数，这里给个默认值。
		fingerprint = firstNonEmpty(fingerprint, defaultUTLSFingerprint)
	}
	if fingerprint != "" {
		out["utls"] = map[string]any{
			"enabled":     true,
			"fingerprint": fingerprint,
		}
	}
	return out
}

func alwaysTLSProtocol(protocol string) bool {
	switch protocol {
	case proxysubscription.ProtocolHysteria, proxysubscription.ProtocolHysteria2,
		proxysubscription.ProtocolTUIC, proxysubscription.ProtocolAnyTLS:
		return true
	default:
		return false
	}
}

func buildTransport(transport proxysubscription.TransportOptions) map[string]any {
	transportType := normalizedTransportType(transport.Type)
	out := map[string]any{"type": transportType}
	switch transportType {
	case "grpc":
		// gRPC only accepts service_name, path/headers are rejected by sing-box
		if service := strings.TrimPrefix(strings.TrimSpace(transport.Path), "/"); service != "" {
			out["service_name"] = service
		}
		return out
	case "quic":
		return out
	}
	if transport.Path != "" {
		out["path"] = transport.Path
	}
	if transport.Host != "" {
		if transportType == "http" {
			out["host"] = []string{transport.Host}
		} else {
			out["headers"] = map[string]string{"Host": transport.Host}
		}
	} else if len(transport.Headers) > 0 {
		out["headers"] = transport.Headers
	}
	return out
}

// packetEncodingFor 决定 vmess/vless 的 UDP 封装方式。机场链接基本不带这个参数，
// 而服务端多数是 Xray，默认上 xudp 游戏/语音这类 UDP 流量才能转发。
// 节点显式写了 none 的情况下不写该字段，交给内核用自己的默认行为。
func packetEncodingFor(nodeValue string) string {
	encoding := proxysubscription.NormalizePacketEncoding(nodeValue)
	switch encoding {
	case proxysubscription.PacketEncodingDisabled:
		return ""
	case "":
		return defaultPacketEncoding
	default:
		return encoding
	}
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
	opts.DNSServers = normalizeDNSServers(opts.DNSServers)
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
