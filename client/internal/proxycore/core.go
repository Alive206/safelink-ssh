// Package proxycore builds sing-box configs and manages the sing-box process.
package proxycore

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/example/safelink/pkg/proxysubscription"
)

const (
	StateStopped = "stopped"
	StateRunning = "running"
	StateError   = "error"
)

type Options struct {
	CorePath  string
	WorkDir   string
	SOCKSAddr string
	HTTPAddr  string
}

type Status struct {
	State         string `json:"state"`
	NodeName      string `json:"node_name,omitempty"`
	SOCKSAddr     string `json:"socks_addr"`
	HTTPAddr      string `json:"http_addr"`
	CorePath      string `json:"core_path,omitempty"`
	CoreAvailable bool   `json:"core_available"`
	LastError     string `json:"last_error,omitempty"`
	StartedAt     string `json:"started_at,omitempty"`
}

type TestResult struct {
	NodeName  string `json:"node_name"`
	OK        bool   `json:"ok"`
	LatencyMS int64  `json:"latency_ms,omitempty"`
	Error     string `json:"error,omitempty"`
	TestedAt  string `json:"tested_at"`
}

type Runner struct {
	mu        sync.Mutex
	opts      Options
	cmd       *exec.Cmd
	cancel    context.CancelFunc
	done      chan struct{}
	state     string
	nodeName  string
	lastError string
	startedAt time.Time
}

func New(opts Options) *Runner {
	opts = normalizeOptions(opts)
	return &Runner{opts: opts, state: StateStopped}
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

	runCtx, cancel := context.WithCancel(ctx)
	cmd := exec.CommandContext(runCtx, opts.CorePath, "run", "-c", configPath)
	cmd.Dir = opts.WorkDir
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
	done := r.done
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
		}
		close(done)
	}()
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
	defer r.mu.Unlock()
	status := Status{
		State:         r.state,
		NodeName:      r.nodeName,
		SOCKSAddr:     r.opts.SOCKSAddr,
		HTTPAddr:      r.opts.HTTPAddr,
		CorePath:      r.opts.CorePath,
		CoreAvailable: r.opts.CorePath != "",
		LastError:     r.lastError,
	}
	if !r.startedAt.IsZero() {
		status.StartedAt = r.startedAt.Format(time.RFC3339)
	}
	return status
}

func TestNode(ctx context.Context, node proxysubscription.ProxyNode, timeout time.Duration) TestResult {
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
	testCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	start := time.Now()
	dialer := net.Dialer{Timeout: timeout}
	conn, err := dialer.DialContext(testCtx, "tcp", net.JoinHostPort(node.Server, fmt.Sprintf("%d", node.Port)))
	if err != nil {
		result.Error = err.Error()
		return result
	}
	_ = conn.Close()
	result.OK = true
	result.LatencyMS = time.Since(start).Milliseconds()
	return result
}

func BuildConfig(node proxysubscription.ProxyNode, opts Options) ([]byte, error) {
	opts = normalizeOptions(opts)
	outbound, err := buildOutbound(node)
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
	}
	return json.MarshalIndent(cfg, "", "  ")
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
	if node.Transport.Type != "" {
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
	return out
}

func buildTransport(transport proxysubscription.TransportOptions) map[string]any {
	out := map[string]any{"type": transport.Type}
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

func FindCorePath(baseDir string) string {
	candidates := []string{
		filepath.Join(baseDir, "bin", "sing-box.exe"),
		filepath.Join(baseDir, "sing-box.exe"),
		"sing-box.exe",
		"sing-box",
	}
	for _, candidate := range candidates {
		if path, err := exec.LookPath(candidate); err == nil {
			return path
		}
		if _, err := os.Stat(candidate); err == nil {
			return candidate
		}
	}
	return ""
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
	return opts
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
