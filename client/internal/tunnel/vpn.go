package tunnel

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/quic-go/quic-go"
	"golang.org/x/crypto/ssh"

	"github.com/example/safelink/pkg/config"
	"github.com/example/safelink/pkg/protocol"
	"github.com/example/safelink/pkg/transport"
)

// VPN implements a Layer 3 VPN tunnel that creates a TUN interface and pipes
// raw IP packets over a QUIC connection to a remote safelink VPN server.
type VPN struct {
	cfg   config.TunnelCfg
	log   *slog.Logger
	stats *Stats

	cancel  context.CancelFunc
	done    chan struct{}
	state   atomic.Value // string
	lastErr atomic.Value // string
	runCnt  atomic.Int64
	startAt atomic.Value // time.Time

	OnStateChange      func(state string, lastErr error)
	routeCleanupNeeded bool
	routeActive        atomic.Bool
}

const (
	vpnStateConnecting   = "connecting"
	vpnStateRunning      = "running"
	vpnStateReconnecting = "reconnecting"
	vpnStateStopped      = "stopped"
)

func NewVPN(cfg config.TunnelCfg, log *slog.Logger, stats *Stats) *VPN {
	v := &VPN{cfg: cfg, log: log, stats: stats}
	v.state.Store(vpnStateStopped)
	return v
}

func (v *VPN) Name() string { return v.cfg.Name }

// VPNSnapshot is the JSON-friendly state snapshot for a VPN tunnel.
type VPNSnapshot struct {
	State     string `json:"state"`
	LastError string `json:"last_error,omitempty"`
	UptimeSec int64  `json:"uptime_seconds"`
	RunCount  int64  `json:"run_count"`
}

func (v *VPN) Snapshot() VPNSnapshot {
	s := VPNSnapshot{
		State:    v.state.Load().(string),
		RunCount: v.runCnt.Load(),
	}
	if e := v.lastErr.Load(); e != nil {
		s.LastError = e.(string)
	}
	if t, ok := v.startAt.Load().(time.Time); ok && !t.IsZero() {
		s.UptimeSec = int64(time.Since(t).Seconds())
	}
	return s
}

// Serve satisfies the sshclient.Tunnel interface.
func (v *VPN) Serve(ctx context.Context, c *ssh.Client) error {
	_ = c
	return v.Run(ctx)
}

// Run is the main loop: connect → TUN → forward → reconnect on failure.
func (v *VPN) Run(ctx context.Context) error {
	defer v.setState(vpnStateStopped, nil)
	defer func() {
		if v.routeCleanupNeeded {
			gw, _ := ParseGateway(v.cfg.Tun.Subnet)
			DelRoutes(v.cfg.Tun.Subnet, RouteConfig{Gateway: gw, All: true})
			serverIP := extractHost(v.cfg.Forward)
			if serverIP != "" {
				delServerExcludeRoute(serverIP)
			}
			v.routeCleanupNeeded = false
			v.routeActive.Store(false)
		}
	}()

	backoff := 1 * time.Second
	maxBackoff := 60 * time.Second

	for ctx.Err() == nil {
		if err := v.runOnce(ctx); err != nil {
			v.log.Warn("vpn session ended", "err", err, "retry_in", roundDur(backoff))
			v.setState(vpnStateReconnecting, err)
			select {
			case <-time.After(applyJitterVPN(backoff)):
			case <-ctx.Done():
				return ctx.Err()
			}
			backoff *= 2
			if backoff > maxBackoff {
				backoff = maxBackoff
			}
		} else {
			backoff = 1 * time.Second
		}
	}
	return ctx.Err()
}

func (v *VPN) runOnce(ctx context.Context) error {
	v.setState(vpnStateConnecting, nil)
	v.runCnt.Add(1)

	serverAddr := v.cfg.Forward
	if serverAddr == "" {
		serverAddr = v.cfg.SSH.Addr
	}
	if serverAddr == "" {
		return errors.New("vpn: forward or ssh.addr must specify the remote server address")
	}

	tlsOpts := transport.TLSOpts{
		SNI:       v.cfg.Tun.SNI,
		PinSHA256: v.cfg.Tun.PinSHA256,
	}
	qconn, err := transport.DialQUIC(ctx, serverAddr, tlsOpts)
	if err != nil {
		return fmt.Errorf("quic dial: %w", err)
	}
	defer qconn.CloseWithError(0, "session done")

	// Open a control stream for authentication.
	ctl, err := qconn.OpenStreamSync(ctx)
	if err != nil {
		return fmt.Errorf("open control stream: %w", err)
	}
	authPayload := fmt.Sprintf("%s:%s", v.cfg.SSH.User, v.cfg.SSH.Password)
	httpReq := fmt.Sprintf("POST /connect HTTP/1.1\r\nHost: gateway.icloud.com\r\nAuthorization: Basic %s\r\nContent-Length: 0\r\n\r\n", authPayload)
	if _, err := fmt.Fprint(ctl, httpReq); err != nil {
		ctl.Close()
		return fmt.Errorf("auth send: %w", err)
	}
	resp := make([]byte, 128)
	n, err := ctl.Read(resp)
	ctl.Close()
	if err != nil && n == 0 {
		return fmt.Errorf("auth response: %w", err)
	}
	if !strings.Contains(string(resp[:n]), "200") {
		return fmt.Errorf("auth rejected: %s", string(resp[:n]))
	}

	v.log.Info("vpn authenticated, creating TUN device")

	// Create TUN interface.
	iface, err := CreateTUN()
	if err != nil {
		return fmt.Errorf("create TUN device: %w", err)
	}
	defer iface.Close()

	if err := configureTUNDev(iface, v.cfg.Tun.Subnet); err != nil {
		v.log.Warn("configure TUN IP failed", "err", err)
	}

	// Auto route.
	if v.cfg.Tun.AutoRoute && v.cfg.Tun.Subnet != "" {
		gw, err := ParseGateway(v.cfg.Tun.Subnet)
		if err == nil {
			serverIP := extractHost(v.cfg.Forward)
			if serverIP != "" {
				if err := addServerExcludeRoute(serverIP); err != nil {
					v.log.Warn("add server exclusion route failed", "err", err)
				}
			}
			rc := RouteConfig{Gateway: gw, All: true}
			if err := AddRoutes(v.cfg.Tun.Subnet, rc); err != nil {
				v.log.Warn("route add failed", "err", err)
			} else {
				v.routeCleanupNeeded = true
				v.routeActive.Store(true)
				v.log.Info("routes added via TUN", "gateway", gw)
			}
		}
	}

	v.startAt.Store(time.Now())
	v.setState(vpnStateRunning, nil)
	v.stats.ConnActive.Add(1)
	v.stats.ConnTotal.Add(1)
	v.log.Info("vpn running", "tun_subnet", v.cfg.Tun.Subnet)

	// Open data stream.
	data, err := qconn.OpenStreamSync(ctx)
	if err != nil {
		v.stats.ConnActive.Add(-1)
		return fmt.Errorf("open data stream: %w", err)
	}
	defer data.Close()

	go func() {
		<-ctx.Done()
		data.CancelRead(0)
		data.CancelWrite(0)
		iface.Close()
	}()

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		v.pipeTUNtoQUIC(iface, data)
	}()
	go func() {
		defer wg.Done()
		v.pipeQUICtoTUN(data, iface)
	}()
	wg.Wait()
	v.stats.ConnActive.Add(-1)

	if v.routeCleanupNeeded {
		gw, _ := ParseGateway(v.cfg.Tun.Subnet)
		DelRoutes(v.cfg.Tun.Subnet, RouteConfig{Gateway: gw, All: true})
		serverIP := extractHost(v.cfg.Forward)
		if serverIP != "" {
			delServerExcludeRoute(serverIP)
		}
		v.routeCleanupNeeded = false
		v.routeActive.Store(false)
		v.log.Info("routes cleaned up")
	}
	return nil
}

func (v *VPN) pipeTUNtoQUIC(tun TUNDevice, data quic.Stream) {
	padding := v.paddingEnabled()
	buf := make([]byte, 65536)
	for {
		n, err := tun.Read(buf)
		if err != nil {
			return
		}
		v.stats.BytesOut.Add(uint64(n))
		header := make([]byte, 4)
		binary.BigEndian.PutUint32(header, uint32(n))
		if _, err := data.Write(header); err != nil {
			return
		}
		if _, err := data.Write(buf[:n]); err != nil {
			return
		}
		if padding {
			padLen := protocol.PaddingLen(n)
			if padLen > 0 {
				pad := make([]byte, padLen)
				if _, err := data.Write(pad); err != nil {
					return
				}
			}
		}
	}
}

func (v *VPN) pipeQUICtoTUN(data quic.Stream, tun TUNDevice) {
	padding := v.paddingEnabled()
	buf := make([]byte, 65536)
	header := make([]byte, 4)
	for {
		if _, err := io.ReadFull(data, header); err != nil {
			return
		}
		pktLen := binary.BigEndian.Uint32(header)
		if pktLen > 65535 {
			return
		}
		readLen := int(pktLen)
		if padding {
			readLen += protocol.PaddingLen(int(pktLen))
		}
		if _, err := io.ReadFull(data, buf[:readLen]); err != nil {
			return
		}
		v.stats.BytesIn.Add(uint64(pktLen))
		if _, err := tun.Write(buf[:pktLen]); err != nil {
			return
		}
	}
}

func (v *VPN) paddingEnabled() bool {
	if v.cfg.Tun.Padding == nil {
		return true
	}
	return *v.cfg.Tun.Padding
}

func (v *VPN) setState(s string, err error) {
	v.state.Store(s)
	if err != nil {
		v.lastErr.Store(err.Error())
	} else if s == vpnStateRunning {
		v.lastErr.Store("")
	}
	switch s {
	case vpnStateRunning:
		v.startAt.Store(time.Now())
		v.runCnt.Add(1)
	case vpnStateStopped:
		v.startAt.Store(time.Time{})
	}
	if v.OnStateChange != nil {
		v.OnStateChange(s, err)
	}
}

// RouteActive reports whether the global route is currently enabled.
func (v *VPN) RouteActive() bool { return v.routeActive.Load() }

// SetRoute dynamically enables or disables the global route through the VPN.
func (v *VPN) SetRoute(enable bool) error {
	if enable == v.routeActive.Load() {
		return nil
	}
	subnet := v.cfg.Tun.Subnet
	if subnet == "" {
		return fmt.Errorf("no subnet configured")
	}
	gw, err := ParseGateway(subnet)
	if err != nil {
		return err
	}
	if enable {
		serverIP := extractHost(v.cfg.Forward)
		if serverIP != "" {
			if err := addServerExcludeRoute(serverIP); err != nil {
				v.log.Warn("add server exclusion route failed", "err", err)
			}
		}
		rc := RouteConfig{Gateway: gw, All: true}
		if err := AddRoutes(subnet, rc); err != nil {
			return fmt.Errorf("add routes: %w", err)
		}
		v.routeActive.Store(true)
		v.routeCleanupNeeded = true
		v.log.Info("global route enabled", "gateway", gw)
	} else {
		DelRoutes(subnet, RouteConfig{Gateway: gw, All: true})
		serverIP := extractHost(v.cfg.Forward)
		if serverIP != "" {
			delServerExcludeRoute(serverIP)
		}
		v.routeActive.Store(false)
		v.routeCleanupNeeded = false
		v.log.Info("global route disabled")
	}
	return nil
}

func roundDur(d time.Duration) time.Duration { return d.Round(100 * time.Millisecond) }

func applyJitterVPN(d time.Duration) time.Duration {
	const min = 0.8
	const max = 1.2
	factor := min + (max-min)*(float64(time.Now().UnixNano()%1000)/1000)
	return time.Duration(float64(d) * factor)
}

func extractHost(addr string) string {
	if addr == "" {
		return ""
	}
	h, _, err := net.SplitHostPort(addr)
	if err != nil {
		return addr
	}
	return h
}

func addServerExcludeRoute(serverIP string) error {
	switch runtime.GOOS {
	case "windows":
		gw := findDefaultGateway()
		if gw == "" {
			return fmt.Errorf("cannot determine default gateway")
		}
		return execRoute("route", "add", serverIP, "mask", "255.255.255.255", gw)
	case "linux":
		return execRoute("ip", "route", "add", serverIP+"/32", "via", findDefaultGateway())
	case "darwin":
		gw := findDefaultGateway()
		return execRoute("route", "add", "-host", serverIP, gw)
	}
	return nil
}

func delServerExcludeRoute(serverIP string) {
	switch runtime.GOOS {
	case "windows":
		_ = execRoute("route", "delete", serverIP)
	case "linux":
		_ = execRoute("ip", "route", "del", serverIP+"/32")
	case "darwin":
		_ = execRoute("route", "delete", "-host", serverIP)
	}
}

func findDefaultGateway() string {
	switch runtime.GOOS {
	case "windows":
		out, err := exec.Command("powershell", "-Command",
			"(Get-NetRoute -DestinationPrefix '0.0.0.0/0' | Sort-Object RouteMetric | Select-Object -First 1).NextHop").Output()
		if err == nil {
			gw := strings.TrimSpace(string(out))
			if gw != "" {
				return gw
			}
		}
	case "linux":
		out, err := exec.Command("ip", "route", "show", "default").Output()
		if err == nil {
			parts := strings.Fields(string(out))
			for i, p := range parts {
				if p == "via" && i+1 < len(parts) {
					return parts[i+1]
				}
			}
		}
	case "darwin":
		out, err := exec.Command("route", "-n", "get", "default").Output()
		if err == nil {
			for _, line := range strings.Split(string(out), "\n") {
				if strings.Contains(line, "gateway:") {
					return strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), "gateway:"))
				}
			}
		}
	}
	return ""
}

func configureTUNDev(iface TUNDevice, subnet string) error {
	if subnet == "" {
		return nil
	}
	ip, ipnet, err := net.ParseCIDR(subnet)
	if err != nil {
		return fmt.Errorf("parse subnet %q: %w", subnet, err)
	}
	ipStr := ip.String()
	maskStr := fmt.Sprintf("%d.%d.%d.%d", ipnet.Mask[0], ipnet.Mask[1], ipnet.Mask[2], ipnet.Mask[3])
	name := iface.Name()

	switch runtime.GOOS {
	case "linux":
		if out, e := exec.Command("ip", "addr", "add", subnet, "dev", name).CombinedOutput(); e != nil {
			return fmt.Errorf("ip addr add: %w: %s", e, out)
		}
		if out, e := exec.Command("ip", "link", "set", name, "up").CombinedOutput(); e != nil {
			return fmt.Errorf("ip link set up: %w: %s", e, out)
		}
	case "darwin":
		gw, _ := ParseGateway(subnet)
		if gw == "" {
			gw = ipStr
		}
		if out, e := exec.Command("ifconfig", name, ipStr, gw, "netmask", maskStr, "up").CombinedOutput(); e != nil {
			return fmt.Errorf("ifconfig: %w: %s", e, out)
		}
	case "windows":
		if out, e := exec.Command("netsh", "interface", "ip", "set", "address", name, "static", ipStr, maskStr).CombinedOutput(); e != nil {
			return fmt.Errorf("netsh: %w: %s", e, out)
		}
	default:
		return fmt.Errorf("configureTUN not supported on %s", runtime.GOOS)
	}
	return nil
}
