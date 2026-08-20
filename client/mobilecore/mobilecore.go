// Package mobilecore exposes SafeLink's shared Go logic through a small
// gomobile-friendly JSON bridge.
package mobilecore

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	"github.com/example/safelink/client/internal/proxycore"
	"github.com/example/safelink/client/internal/sshclient"
	"github.com/example/safelink/client/internal/sshsession"
	"github.com/example/safelink/client/internal/tunnel"
	"github.com/example/safelink/pkg/config"
	"github.com/example/safelink/pkg/proxysubscription"
	"github.com/example/safelink/pkg/subscription"
)

const Version = "0.1.0"

// Core is the long-lived object bound into Android through gomobile.
type Core struct {
	dataDir   string
	ssh       *sshsession.Manager
	sshMu     sync.Mutex
	sshEvents map[string][]map[string]any
	tunnelMu  sync.Mutex
	tunnels   map[string]*mobileTunnel
	vpnMu     sync.Mutex
	vpn       *mobileVPN
	logger    *slog.Logger
}

type mobileTunnel struct {
	cfg       config.TunnelCfg
	cancel    context.CancelFunc
	state     string
	lastError string
	startedAt time.Time
	stats     *tunnel.Stats
}

type mobileVPN struct {
	name      string
	kind      string
	cancel    context.CancelFunc
	tunnel    mobileVPNCloser
	fd        int
	state     string
	lastError string
	startedAt time.Time
	// 收发字节由转发协程累加、由 vpnStatus 读取，必须原子操作。
	bytesIn  atomic.Uint64
	bytesOut atomic.Uint64

	// 速率靠相邻两次 vpnStatus 采样的字节差算出，只在持有 vpnMu 时访问。
	sampledAt    time.Time
	sampledIn    uint64
	sampledOut   uint64
	downSpeedBps uint64
	upSpeedBps   uint64
}

// sampleTraffic 返回累计字节数与最近一次采样区间的速率（字节/秒）。
// 调用方必须持有 c.vpnMu：采样点会被就地推进。
func (v *mobileVPN) sampleTraffic(now time.Time) (bytesIn, bytesOut, downloadBps, uploadBps uint64) {
	bytesIn = v.bytesIn.Load()
	bytesOut = v.bytesOut.Load()
	if v.sampledAt.IsZero() {
		v.sampledAt = now
		v.sampledIn = bytesIn
		v.sampledOut = bytesOut
		return bytesIn, bytesOut, 0, 0
	}
	// 采样间隔太短时差值噪声会把速率放大几个数量级，保留上一轮结果等下次轮询。
	elapsed := now.Sub(v.sampledAt).Seconds()
	if elapsed < 0.2 {
		return bytesIn, bytesOut, v.downSpeedBps, v.upSpeedBps
	}
	v.downSpeedBps = uint64(float64(diffBytes(bytesIn, v.sampledIn)) / elapsed)
	v.upSpeedBps = uint64(float64(diffBytes(bytesOut, v.sampledOut)) / elapsed)
	v.sampledAt = now
	v.sampledIn = bytesIn
	v.sampledOut = bytesOut
	return bytesIn, bytesOut, v.downSpeedBps, v.upSpeedBps
}

// diffBytes 防御计数器被重置（重连换 mobileVPN 实例之外的意外回退）导致的下溢。
func diffBytes(current, previous uint64) uint64 {
	if current < previous {
		return 0
	}
	return current - previous
}

type mobileVPNCloser interface {
	Disconnect()
	IsConnected() bool
}

// NewCore creates a mobile core using the Android app's private data directory.
func NewCore(dataDir string) *Core {
	c := &Core{
		dataDir:   dataDir,
		sshEvents: make(map[string][]map[string]any),
		tunnels:   make(map[string]*mobileTunnel),
		logger:    slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	c.ssh = sshsession.NewManager(sshsession.Options{
		OnOutput: func(event sshsession.OutputEvent) {
			c.appendSSHEvent(event.SessionID, map[string]any{
				"type":        "output",
				"session_id":  event.SessionID,
				"data":        event.Data,
				"data_base64": event.DataBase64,
			})
		},
		OnClosed: func(event sshsession.ClosedEvent) {
			c.appendSSHEvent(event.SessionID, map[string]any{
				"type":       "closed",
				"session_id": event.SessionID,
				"message":    event.Message,
			})
		},
		OnError: func(event sshsession.ErrorEvent) {
			c.appendSSHEvent(event.SessionID, map[string]any{
				"type":       "error",
				"session_id": event.SessionID,
				"message":    event.Message,
			})
		},
	})
	return c
}

// Call dispatches a JSON request and always returns a JSON response.
func (c *Core) Call(method, payload string) string {
	var data map[string]any
	if payload != "" {
		if err := json.Unmarshal([]byte(payload), &data); err != nil {
			return fail("bad_json", err)
		}
	}
	switch method {
	case "version":
		return ok(map[string]any{"version": Version})
	case "parseSubscription":
		return c.parseSubscription(data)
	case "parseProxySubscription":
		return c.parseProxySubscription(data)
	case "parseVpnSubscription":
		return c.parseVPNSubscription(data)
	case "buildProxyConfig":
		return c.buildProxyConfig(data)
	case "defaultProxyRules":
		return ok(map[string]any{"rules": config.DefaultProxyRules()})
	case "refreshRuleSets":
		return c.refreshRuleSets(data)
	case "testProxyNode":
		return c.testProxyNode(data)
	case "testProxyNodes":
		return c.testProxyNodes(data)
	case "speedTestProxyNode":
		return c.speedTestProxyNode(data)
	case "validateTunnel":
		return c.validateTunnel(data)
	case "createSSHSession":
		return c.createSSHSession(data)
	case "sendSSHInput":
		return c.sendSSHInput(data)
	case "resizeSSHSession":
		return c.resizeSSHSession(data)
	case "closeSSHSession":
		return c.closeSSHSession(data)
	case "drainSSHEvents":
		return c.drainSSHEvents(data)
	case "startTunnel":
		return c.startTunnel(data)
	case "stopTunnel":
		return c.stopTunnel(data)
	case "listTunnelStates":
		return c.listTunnelStates(data)
	case "startVPNFromFD":
		return c.startVPNFromFD(data)
	case "startTun2SocksFromFD":
		return c.startTun2SocksFromFD(data)
	case "stopVPN":
		return c.stopVPN(data)
	case "vpnStatus":
		return c.vpnStatus(data)
	default:
		return failCode("not_found", fmt.Sprintf("unknown method %q", method))
	}
}

func (c *Core) parseSubscription(data map[string]any) string {
	body := []byte(asString(data, "body"))
	format := asStringDefault(data, "format", subscription.FormatAuto)
	tunnels, detected, err := subscription.Parse(body, format)
	if err == nil {
		return ok(map[string]any{
			"kind":    "vpn",
			"format":  detected,
			"tunnels": tunnels,
		})
	}
	nodes, proxyDetected, proxyErr := proxysubscription.Parse(body, proxysubscription.FormatAuto)
	if proxyErr == nil {
		return ok(map[string]any{
			"kind":   "proxy",
			"format": proxyDetected,
			"nodes":  nodes,
		})
	}
	return failCode("parse_subscription", fmt.Sprintf("%v; %v", err, proxyErr))
}

func (c *Core) parseProxySubscription(data map[string]any) string {
	nodes, detected, err := proxysubscription.Parse([]byte(asString(data, "body")), asStringDefault(data, "format", proxysubscription.FormatAuto))
	if err != nil {
		return fail("parse_proxy_subscription", err)
	}
	return ok(map[string]any{"format": detected, "nodes": nodes})
}

func (c *Core) parseVPNSubscription(data map[string]any) string {
	tunnels, detected, err := subscription.Parse([]byte(asString(data, "body")), asStringDefault(data, "format", subscription.FormatAuto))
	if err != nil {
		return fail("parse_vpn_subscription", err)
	}
	return ok(map[string]any{"format": detected, "tunnels": tunnels})
}

func (c *Core) buildProxyConfig(data map[string]any) string {
	var node proxysubscription.ProxyNode
	if err := decodeField(data, "node", &node); err != nil {
		return fail("bad_node", err)
	}
	var rules []config.ProxyRule
	if raw, ok := data["rules"]; ok {
		if err := decodeValue(raw, &rules); err != nil {
			return fail("bad_rules", err)
		}
	}
	cfg, err := proxycore.BuildConfig(node, proxycore.Options{
		Mode: asStringDefault(data, "mode", proxycore.ProxyModeRule),
		// WorkDir 决定 cache_file 落在哪，必须是内核实际运行的那个目录，
		// 不给就会回落到 os.TempDir()，而 Android 上那是 /data/local/tmp，应用写不进去。
		WorkDir:       c.proxyRuntimeDir(data),
		RuleSetDir:    c.ruleSetDir(data),
		SOCKSAddr:     asStringDefault(data, "socks_addr", "127.0.0.1:10808"),
		HTTPAddr:      asStringDefault(data, "http_addr", "127.0.0.1:10809"),
		ClashAPIAddr:  asString(data, "clash_api_addr"),
		RuleModeRules: rules,
		DNSServers:    asStringSlice(data, "dns_servers"),
	})
	if err != nil {
		return fail("build_proxy_config", err)
	}
	return ok(map[string]any{"config": string(cfg)})
}

// refreshRuleSets 把内置规则集更新到最新。Android 的内核由 Kotlin 侧启动，
// 不走 proxycore.Runner，所以桌面端那套“起来之后顺手更新”在这里不会发生，
// 得由 Flutter 在连上之后显式调一次。下载失败不影响任何事，内置的那份一直可用。
func (c *Core) refreshRuleSets(data map[string]any) string {
	dir := c.ruleSetDir(data)
	go proxycore.RefreshRuleSets(context.Background(), dir)
	return ok(map[string]any{"dir": dir, "started": true})
}

// proxyRuntimeDir 是 sing-box 实际运行的目录，cache_file 要落在这里。
// 必须和 Kotlin 侧 SingBoxRuntime 启动内核用的目录一致，否则 rule_set 的 .srs
// 缓存换个目录就得重新下载。Kotlin 会注入 proxy_work_dir，这里只是兜底。
func (c *Core) proxyRuntimeDir(data map[string]any) string {
	if dir := asString(data, "proxy_work_dir"); dir != "" {
		return dir
	}
	return filepath.Join(c.dataDir, "sing-box")
}

// ruleSetDir 是内置规则集的落地目录，必须是应用私有目录：Android 上 os.TempDir()
// 是 /data/local/tmp，应用没有写权限，规则集铺不出去会让 BuildConfig 整个失败。
// 连接和测速共用同一份，省掉每个临时测速目录各铺一遍 250KB。
func (c *Core) ruleSetDir(data map[string]any) string {
	if dir := asString(data, "rule_set_dir"); dir != "" {
		return dir
	}
	return filepath.Join(c.dataDir, "rule-sets")
}

func (c *Core) testProxyNode(data map[string]any) string {
	var node proxysubscription.ProxyNode
	if err := decodeField(data, "node", &node); err != nil {
		return fail("bad_node", err)
	}
	result := proxycore.TestNode(context.Background(), node, c.proxyOptions(data), timeoutFromPayload(data, 8*time.Second))
	return ok(map[string]any{"result": result})
}

func (c *Core) testProxyNodes(data map[string]any) string {
	var nodes []proxysubscription.ProxyNode
	if err := decodeField(data, "nodes", &nodes); err != nil {
		return fail("bad_nodes", err)
	}
	results := proxycore.TestNodes(context.Background(), nodes, c.proxyOptions(data), timeoutFromPayload(data, 5*time.Second), nil)
	return ok(map[string]any{"results": results})
}

func (c *Core) speedTestProxyNode(data map[string]any) string {
	var node proxysubscription.ProxyNode
	if err := decodeField(data, "node", &node); err != nil {
		return fail("bad_node", err)
	}
	result := proxycore.SpeedTestNode(context.Background(), node, c.proxyOptions(data), timeoutFromPayload(data, 15*time.Second))
	return ok(map[string]any{"result": result})
}

func (c *Core) proxyOptions(data map[string]any) proxycore.Options {
	var rules []config.ProxyRule
	if raw, ok := data["rules"]; ok {
		_ = decodeValue(raw, &rules)
	}
	workDir := asString(data, "work_dir")
	if workDir == "" {
		workDir = filepath.Join(c.dataDir, "proxy-tests")
	}
	return proxycore.Options{
		CorePath: asString(data, "core_path"),
		WorkDir:  workDir,
		// 规则集跟连接路径共用，不随每次测速的临时目录走
		RuleSetDir:    c.ruleSetDir(data),
		Mode:          asStringDefault(data, "mode", proxycore.ProxyModeRule),
		SOCKSAddr:     asStringDefault(data, "socks_addr", "127.0.0.1:10808"),
		HTTPAddr:      asStringDefault(data, "http_addr", "127.0.0.1:10809"),
		ClashAPIAddr:  asString(data, "clash_api_addr"),
		RuleModeRules: rules,
		DNSServers:    asStringSlice(data, "dns_servers"),
	}
}

func asStringSlice(data map[string]any, key string) []string {
	if data == nil {
		return nil
	}
	raw, ok := data[key].([]any)
	if !ok {
		return nil
	}
	values := make([]string, 0, len(raw))
	for _, item := range raw {
		if value, ok := item.(string); ok && value != "" {
			values = append(values, value)
		}
	}
	return values
}

func timeoutFromPayload(data map[string]any, fallback time.Duration) time.Duration {
	ms := asIntDefault(data, "timeout_ms", 0)
	if ms <= 0 {
		return fallback
	}
	return time.Duration(ms) * time.Millisecond
}

func (c *Core) validateTunnel(data map[string]any) string {
	var tunnel config.TunnelCfg
	if err := decodeField(data, "tunnel", &tunnel); err != nil {
		return fail("bad_tunnel", err)
	}
	if err := config.ValidateTunnel(tunnel); err != nil {
		return fail("validate_tunnel", err)
	}
	return ok(map[string]any{"valid": true})
}

func (c *Core) createSSHSession(data map[string]any) string {
	var cfg sshsession.Config
	if err := decodeField(data, "config", &cfg); err != nil {
		return fail("bad_ssh_config", err)
	}
	id, err := c.ssh.Create(context.Background(), cfg)
	if err != nil {
		return fail("create_ssh_session", err)
	}
	return ok(map[string]any{"session_id": id})
}

func (c *Core) sendSSHInput(data map[string]any) string {
	sessionID := asString(data, "session_id")
	if sessionID == "" {
		return failCode("bad_request", "session_id is required")
	}
	if err := c.ssh.Write(sessionID, asString(data, "data")); err != nil {
		return fail("send_ssh_input", err)
	}
	return ok(map[string]any{"sent": true})
}

func (c *Core) resizeSSHSession(data map[string]any) string {
	sessionID := asString(data, "session_id")
	if sessionID == "" {
		return failCode("bad_request", "session_id is required")
	}
	rows := asIntDefault(data, "rows", 24)
	cols := asIntDefault(data, "cols", 80)
	if err := c.ssh.Resize(sessionID, rows, cols); err != nil {
		return fail("resize_ssh_session", err)
	}
	return ok(map[string]any{"resized": true})
}

func (c *Core) closeSSHSession(data map[string]any) string {
	sessionID := asString(data, "session_id")
	if sessionID == "" {
		return failCode("bad_request", "session_id is required")
	}
	if err := c.ssh.Close(sessionID); err != nil {
		return fail("close_ssh_session", err)
	}
	return ok(map[string]any{"closed": true})
}

func (c *Core) drainSSHEvents(data map[string]any) string {
	sessionID := asString(data, "session_id")
	c.sshMu.Lock()
	defer c.sshMu.Unlock()
	if sessionID != "" {
		events := c.sshEvents[sessionID]
		delete(c.sshEvents, sessionID)
		return ok(map[string]any{"events": events})
	}
	var events []map[string]any
	for id, items := range c.sshEvents {
		events = append(events, items...)
		delete(c.sshEvents, id)
	}
	return ok(map[string]any{"events": events})
}

func (c *Core) appendSSHEvent(sessionID string, event map[string]any) {
	c.sshMu.Lock()
	defer c.sshMu.Unlock()
	c.sshEvents[sessionID] = append(c.sshEvents[sessionID], event)
}

func (c *Core) startTunnel(data map[string]any) string {
	var cfg config.TunnelCfg
	if err := decodeField(data, "tunnel", &cfg); err != nil {
		return fail("bad_tunnel", err)
	}
	if cfg.Mode == config.ModeVPN {
		return failCode("unsupported_tunnel", "vpn mode is started through Android VpnService")
	}
	if err := config.ValidateTunnel(cfg); err != nil {
		return fail("validate_tunnel", err)
	}
	c.tunnelMu.Lock()
	if existing, exists := c.tunnels[cfg.Name]; exists && existing.cancel != nil {
		c.tunnelMu.Unlock()
		return ok(map[string]any{"state": existing.state, "name": cfg.Name})
	}
	stats := tunnel.NewStats()
	impl, err := tunnel.Build(cfg, c.logger, stats)
	if err != nil {
		c.tunnelMu.Unlock()
		return fail("build_tunnel", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	runtime := &mobileTunnel{
		cfg:       cfg,
		cancel:    cancel,
		state:     string(sshclient.StateConnecting),
		startedAt: time.Now(),
		stats:     stats,
	}
	c.tunnels[cfg.Name] = runtime
	c.tunnelMu.Unlock()

	supervisor := sshclient.NewSupervisor(cfg, config.ConnDefaults{}, "", true, impl, c.logger)
	supervisor.OnStateChange = func(state sshclient.State, lastErr error) {
		c.tunnelMu.Lock()
		defer c.tunnelMu.Unlock()
		runtime.state = string(state)
		if lastErr != nil {
			runtime.lastError = lastErr.Error()
		} else if state == sshclient.StateRunning {
			runtime.lastError = ""
		}
	}
	go func() {
		supervisor.Run(ctx)
		c.tunnelMu.Lock()
		defer c.tunnelMu.Unlock()
		runtime.cancel = nil
		runtime.state = string(sshclient.StateStopped)
	}()
	return ok(map[string]any{"state": runtime.state, "name": cfg.Name})
}

func (c *Core) stopTunnel(data map[string]any) string {
	name := asString(data, "name")
	if name == "" {
		return failCode("bad_request", "name is required")
	}
	c.tunnelMu.Lock()
	runtime, exists := c.tunnels[name]
	if !exists {
		c.tunnelMu.Unlock()
		return failCode("not_found", fmt.Sprintf("tunnel %q not found", name))
	}
	cancel := runtime.cancel
	runtime.cancel = nil
	runtime.state = string(sshclient.StateStopped)
	c.tunnelMu.Unlock()
	if cancel != nil {
		cancel()
	}
	return ok(map[string]any{"state": string(sshclient.StateStopped), "name": name})
}

func (c *Core) listTunnelStates(data map[string]any) string {
	c.tunnelMu.Lock()
	defer c.tunnelMu.Unlock()
	items := make([]map[string]any, 0, len(c.tunnels))
	for name, runtime := range c.tunnels {
		snapshot := runtime.stats.Snapshot()
		items = append(items, map[string]any{
			"name":       name,
			"state":      runtime.state,
			"last_error": runtime.lastError,
			"started_at": runtime.startedAt.Format(time.RFC3339),
			"bytes_in":   snapshot.BytesIn,
			"bytes_out":  snapshot.BytesOut,
		})
	}
	return ok(map[string]any{"tunnels": items})
}

func ok(data any) string {
	out, err := json.Marshal(map[string]any{"ok": true, "data": data})
	if err != nil {
		return fail("marshal", err)
	}
	return string(out)
}

func fail(code string, err error) string {
	return failCode(code, err.Error())
}

func failCode(code, message string) string {
	out, _ := json.Marshal(map[string]any{
		"ok":      false,
		"code":    code,
		"message": message,
	})
	return string(out)
}

func asString(data map[string]any, key string) string {
	if data == nil {
		return ""
	}
	if value, ok := data[key].(string); ok {
		return value
	}
	return ""
}

func asStringDefault(data map[string]any, key, fallback string) string {
	if value := asString(data, key); value != "" {
		return value
	}
	return fallback
}

func asIntDefault(data map[string]any, key string, fallback int) int {
	if data == nil {
		return fallback
	}
	switch value := data[key].(type) {
	case int:
		return value
	case float64:
		return int(value)
	case json.Number:
		i, err := value.Int64()
		if err == nil {
			return int(i)
		}
	}
	return fallback
}

func asBoolDefault(data map[string]any, key string, fallback bool) bool {
	if data == nil {
		return fallback
	}
	switch value := data[key].(type) {
	case bool:
		return value
	case string:
		switch value {
		case "true", "1", "yes", "on":
			return true
		case "false", "0", "no", "off":
			return false
		}
	}
	return fallback
}

func decodeField(data map[string]any, key string, dst any) error {
	if data == nil {
		return fmt.Errorf("%s is required", key)
	}
	raw, ok := data[key]
	if !ok {
		return fmt.Errorf("%s is required", key)
	}
	return decodeValue(raw, dst)
}

func decodeValue(raw, dst any) error {
	encoded, err := json.Marshal(raw)
	if err != nil {
		return err
	}
	return json.Unmarshal(encoded, dst)
}
