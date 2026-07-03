//go:build android

package mobilecore

import (
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"os"
	"sync"
	"time"

	tun2socks "github.com/Jigsaw-Code/outline-go-tun2socks/android"
	"github.com/quic-go/quic-go"
	"golang.org/x/sys/unix"

	"github.com/example/safelink/pkg/config"
	"github.com/example/safelink/pkg/protocol"
	"github.com/example/safelink/pkg/transport"
)

func (c *Core) startVPNFromFD(data map[string]any) string {
	fd := asIntDefault(data, "fd", -1)
	if fd < 0 {
		return failCode("bad_request", "fd is required")
	}
	var cfg config.TunnelCfg
	if err := decodeField(data, "tunnel", &cfg); err != nil {
		_ = closeFD(fd)
		return fail("bad_tunnel", err)
	}
	if cfg.Mode != config.ModeVPN {
		_ = closeFD(fd)
		return failCode("bad_tunnel", "only vpn mode can use startVPNFromFD")
	}
	if err := config.ValidateTunnel(cfg); err != nil {
		_ = closeFD(fd)
		return fail("validate_tunnel", err)
	}

	c.vpnMu.Lock()
	if c.vpn != nil {
		stopMobileVPN(c.vpn)
	}
	ctx, cancel := context.WithCancel(context.Background())
	vpn := &mobileVPN{
		name:      cfg.Name,
		kind:      "safelink-vpn",
		cancel:    cancel,
		fd:        fd,
		state:     "connecting",
		startedAt: time.Now(),
	}
	c.vpn = vpn
	c.vpnMu.Unlock()

	tun := os.NewFile(uintptr(fd), "android-vpn-tun")
	go c.runVPN(ctx, vpn, tun, cfg)
	return ok(map[string]any{"state": vpn.state, "name": cfg.Name})
}

func (c *Core) startTun2SocksFromFD(data map[string]any) string {
	fd := asIntDefault(data, "fd", -1)
	if fd < 0 {
		return failCode("bad_request", "fd is required")
	}
	name := asStringDefault(data, "name", "Proxy VPN")
	host := asStringDefault(data, "socks_host", "127.0.0.1")
	port := asIntDefault(data, "socks_port", 10808)
	if port <= 0 || port > 65535 {
		_ = closeFD(fd)
		return failCode("bad_request", "socks_port must be between 1 and 65535")
	}
	if err := unix.SetNonblock(fd, false); err != nil {
		_ = closeFD(fd)
		return fail("tun_fd_blocking", err)
	}

	c.vpnMu.Lock()
	if c.vpn != nil {
		stopMobileVPN(c.vpn)
	}
	c.vpnMu.Unlock()

	outlineTunnel, err := tun2socks.ConnectSocksTunnel(fd, host, port, asBoolDefault(data, "udp", true))
	if err != nil {
		_ = closeFD(fd)
		return fail("start_tun2socks", err)
	}

	c.vpnMu.Lock()
	vpn := &mobileVPN{
		name:      name,
		kind:      "proxy-vpn",
		tunnel:    outlineTunnel,
		fd:        fd,
		state:     "running",
		startedAt: time.Now(),
	}
	c.vpn = vpn
	c.vpnMu.Unlock()
	return ok(map[string]any{"state": vpn.state, "name": name, "socks_host": host, "socks_port": port})
}

func (c *Core) stopVPN(data map[string]any) string {
	c.vpnMu.Lock()
	vpn := c.vpn
	if vpn != nil {
		stopMobileVPN(vpn)
	}
	c.vpnMu.Unlock()
	return ok(map[string]any{"state": "stopped"})
}

func (c *Core) vpnStatus(data map[string]any) string {
	c.vpnMu.Lock()
	defer c.vpnMu.Unlock()
	if c.vpn == nil {
		return ok(map[string]any{"state": "stopped"})
	}
	return ok(map[string]any{
		"name":        c.vpn.name,
		"kind":        c.vpn.kind,
		"state":       c.vpn.state,
		"last_error":  c.vpn.lastError,
		"started_at":  c.vpn.startedAt.Format(time.RFC3339),
		"bytes_in":    c.vpn.bytesIn,
		"bytes_out":   c.vpn.bytesOut,
		"uptime_secs": int64(time.Since(c.vpn.startedAt).Seconds()),
	})
}

func (c *Core) runVPN(ctx context.Context, vpn *mobileVPN, tun *os.File, cfg config.TunnelCfg) {
	defer tun.Close()
	defer func() {
		c.vpnMu.Lock()
		if c.vpn == vpn {
			vpn.state = "stopped"
			vpn.cancel = nil
		}
		c.vpnMu.Unlock()
	}()

	backoff := time.Second
	for ctx.Err() == nil {
		if err := c.runVPNSession(ctx, vpn, tun, cfg); err != nil {
			c.setVPNState(vpn, "reconnecting", err)
			select {
			case <-time.After(backoff):
			case <-ctx.Done():
				return
			}
			backoff *= 2
			if backoff > 60*time.Second {
				backoff = 60 * time.Second
			}
			continue
		}
		backoff = time.Second
	}
}

func stopMobileVPN(vpn *mobileVPN) {
	if vpn.cancel != nil {
		vpn.cancel()
		vpn.cancel = nil
	}
	if vpn.tunnel != nil {
		vpn.tunnel.Disconnect()
		vpn.tunnel = nil
	}
	if vpn.fd >= 0 {
		_ = closeFD(vpn.fd)
		vpn.fd = -1
	}
	vpn.state = "stopped"
}

func (c *Core) runVPNSession(ctx context.Context, vpn *mobileVPN, tun *os.File, cfg config.TunnelCfg) error {
	serverAddr := cfg.Forward
	if serverAddr == "" {
		serverAddr = cfg.SSH.Addr
	}
	c.setVPNState(vpn, "connecting", nil)
	conn, err := transport.DialQUIC(ctx, serverAddr, transport.TLSOpts{
		SNI:       cfg.Tun.SNI,
		PinSHA256: cfg.Tun.PinSHA256,
	})
	if err != nil {
		return err
	}
	defer conn.CloseWithError(0, "done")

	if err := authenticateVPN(ctx, conn, cfg); err != nil {
		return err
	}
	stream, err := conn.OpenStreamSync(ctx)
	if err != nil {
		return fmt.Errorf("open data stream: %w", err)
	}
	defer stream.Close()

	c.setVPNState(vpn, "running", nil)
	done := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		pipeAndroidTUNToQUIC(ctx, vpn, tun, stream, cfg)
	}()
	go func() {
		defer wg.Done()
		pipeQUICToAndroidTUN(ctx, vpn, stream, tun, cfg)
	}()
	go func() {
		wg.Wait()
		close(done)
	}()
	select {
	case <-done:
		return io.EOF
	case <-ctx.Done():
		stream.CancelRead(0)
		stream.CancelWrite(0)
		return ctx.Err()
	}
}

func authenticateVPN(ctx context.Context, conn quic.Connection, cfg config.TunnelCfg) error {
	ctl, err := conn.OpenStreamSync(ctx)
	if err != nil {
		return fmt.Errorf("open auth stream: %w", err)
	}
	defer ctl.Close()
	authPayload := fmt.Sprintf("%s:%s", cfg.SSH.User, cfg.SSH.Password)
	req := fmt.Sprintf("POST /connect HTTP/1.1\r\nHost: gateway.icloud.com\r\nAuthorization: Basic %s\r\nContent-Length: 0\r\n\r\n", authPayload)
	if _, err := ctl.Write([]byte(req)); err != nil {
		return fmt.Errorf("auth send: %w", err)
	}
	resp := make([]byte, 128)
	n, err := ctl.Read(resp)
	if err != nil && n == 0 {
		return fmt.Errorf("auth response: %w", err)
	}
	if n < 12 || string(resp[:12]) != "HTTP/1.1 200" {
		return fmt.Errorf("auth rejected: %s", string(resp[:n]))
	}
	return nil
}

func pipeAndroidTUNToQUIC(ctx context.Context, vpn *mobileVPN, tun *os.File, stream quic.Stream, cfg config.TunnelCfg) {
	padding := paddingEnabled(cfg)
	buf := make([]byte, 65536)
	header := make([]byte, protocol.HeaderSize)
	for ctx.Err() == nil {
		n, err := tun.Read(buf)
		if err != nil {
			return
		}
		binary.BigEndian.PutUint32(header, uint32(n))
		if _, err := stream.Write(header); err != nil {
			return
		}
		if _, err := stream.Write(buf[:n]); err != nil {
			return
		}
		if padding {
			if padLen := protocol.PaddingLen(n); padLen > 0 {
				if _, err := stream.Write(make([]byte, padLen)); err != nil {
					return
				}
			}
		}
		vpn.bytesOut += uint64(n)
	}
}

func pipeQUICToAndroidTUN(ctx context.Context, vpn *mobileVPN, stream quic.Stream, tun *os.File, cfg config.TunnelCfg) {
	padding := paddingEnabled(cfg)
	buf := make([]byte, 65536)
	header := make([]byte, protocol.HeaderSize)
	for ctx.Err() == nil {
		if _, err := io.ReadFull(stream, header); err != nil {
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
		if _, err := io.ReadFull(stream, buf[:readLen]); err != nil {
			return
		}
		if _, err := tun.Write(buf[:pktLen]); err != nil {
			return
		}
		vpn.bytesIn += uint64(pktLen)
	}
}

func paddingEnabled(cfg config.TunnelCfg) bool {
	if cfg.Tun.Padding == nil {
		return true
	}
	return *cfg.Tun.Padding
}

func (c *Core) setVPNState(vpn *mobileVPN, state string, err error) {
	c.vpnMu.Lock()
	defer c.vpnMu.Unlock()
	vpn.state = state
	if err != nil {
		vpn.lastError = err.Error()
	} else if state == "running" {
		vpn.lastError = ""
	}
}

func closeFD(fd int) error {
	return os.NewFile(uintptr(fd), "android-vpn-tun").Close()
}
