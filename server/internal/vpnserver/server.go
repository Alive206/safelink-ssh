//go:build linux || darwin

// Package vpnserver implements the QUIC-based VPN server that handles
// client connections, authentication, and TUN packet forwarding.
package vpnserver

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

	"github.com/quic-go/quic-go"
	"github.com/songgao/water"

	"github.com/example/safelink/pkg/protocol"
	"github.com/example/safelink/pkg/transport"
)

// TUNDevice is a platform-independent interface for TUN virtual network devices.
type TUNDevice interface {
	Read(p []byte) (int, error)
	Write(p []byte) (int, error)
	Name() string
	Close() error
}

// VPNServer is the QUIC-based VPN gateway.
type VPNServer struct {
	ListenAddr string
	Subnet     string
	Username   string
	Password   string
	TLSOpts    transport.TLSOpts
	Padding    bool
	Log        *slog.Logger
	Runtime    *Runtime
}

// Run starts the VPN server and blocks until ctx is cancelled.
func (s *VPNServer) Run(ctx context.Context) error {
	if s.Runtime == nil {
		s.Runtime = NewRuntime(RuntimeConfig{
			ListenAddr: s.ListenAddr,
			Subnet:     s.Subnet,
			Padding:    s.Padding,
		})
	}
	ln, err := transport.ListenQUIC(s.ListenAddr, s.TLSOpts)
	if err != nil {
		return fmt.Errorf("vpn server listen: %w", err)
	}
	defer ln.Close()
	s.Log.Info("vpn server listening", "addr", s.ListenAddr, "subnet", s.Subnet)

	for ctx.Err() == nil {
		conn, err := ln.Accept(ctx)
		if err != nil {
			if errors.Is(err, context.Canceled) {
				return nil
			}
			s.Log.Warn("accept error", "err", err)
			continue
		}
		go s.handleClient(ctx, conn)
	}
	return nil
}

func (s *VPNServer) handleClient(ctx context.Context, conn quic.Connection) {
	defer func() {
		if r := recover(); r != nil {
			s.Log.Warn("panic", "recover", r)
			conn.CloseWithError(99, "internal error")
		}
	}()
	log := s.Log.With("remote", conn.RemoteAddr())

	ctl, err := conn.AcceptStream(ctx)
	if err != nil {
		log.Warn("accept stream", "err", err)
		conn.CloseWithError(1, "internal error")
		return
	}

	// Read HTTP-style auth request.
	var authBuf []byte
	tmp := make([]byte, 1)
	for {
		n, rErr := ctl.Read(tmp)
		if n > 0 {
			authBuf = append(authBuf, tmp[0])
			if len(authBuf) >= 4 && string(authBuf[len(authBuf)-4:]) == "\r\n\r\n" {
				break
			}
		}
		if rErr != nil {
			log.Warn("auth read error", "err", rErr)
			ctl.Close()
			conn.CloseWithError(2, "auth failed")
			return
		}
	}

	// Parse Authorization header.
	authLine := string(authBuf)
	ok := false
	for _, line := range strings.Split(authLine, "\r\n") {
		if strings.HasPrefix(line, "Authorization: Basic ") {
			creds := strings.TrimPrefix(line, "Authorization: Basic ")
			parts := strings.SplitN(creds, ":", 2)
			ok = len(parts) == 2 && parts[0] == s.Username && parts[1] == s.Password
			break
		}
	}

	if !ok {
		fallbackResp := "HTTP/1.1 403 Forbidden\r\nServer: cloudflare\r\nContent-Type: text/html; charset=UTF-8\r\nContent-Length: 155\r\n\r\n<html><head><title>403 Forbidden</title></head><body><center><h1>403 Forbidden</h1></center><hr><center>cloudflare</center></body></html>\r\n"
		_, _ = ctl.Write([]byte(fallbackResp))
		log.Warn("auth failed, sent fallback")
		ctl.Close()
		conn.CloseWithError(2, "auth failed")
		return
	}

	if _, err := ctl.Write([]byte("HTTP/1.1 200 OK\r\nContent-Length: 0\r\n\r\n")); err != nil {
		ctl.Close()
		conn.CloseWithError(2, "auth failed")
		return
	}
	ctl.Close()
	log.Info("authenticated")
	clientID := ""
	if s.Runtime != nil {
		clientID = s.Runtime.RegisterClient(conn.RemoteAddr())
		s.Runtime.SetClientUser(clientID, s.Username)
		defer s.Runtime.UnregisterClient(clientID)
	}

	// Create TUN device.
	iface, err := createTUN("tun0")
	if err != nil {
		log.Warn("create TUN", "err", err)
		if s.Runtime != nil {
			s.Runtime.SetClientError(clientID, err)
		}
		conn.CloseWithError(3, "tun error")
		return
	}
	defer iface.Close()
	if s.Runtime != nil {
		s.Runtime.SetClientTUN(clientID, iface.Name())
	}

	serverIP := serverIPFromSubnet(s.Subnet)
	if err := configureTUN(iface, serverIP); err != nil {
		log.Warn("configure TUN", "err", err)
		if s.Runtime != nil {
			s.Runtime.SetClientError(clientID, err)
		}
	}

	data, err := conn.AcceptStream(ctx)
	if err != nil {
		log.Warn("accept data", "err", err)
		return
	}
	defer data.Close()

	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); s.pipeQUICtoTUN(clientID, data, iface) }()
	go func() { defer wg.Done(); s.pipeTUNtoQUIC(clientID, iface, data) }()
	wg.Wait()
	_ = conn.CloseWithError(0, "done")
	log.Info("disconnected")
}

func (s *VPNServer) pipeTUNtoQUIC(clientID string, tun TUNDevice, data quic.Stream) {
	buf := make([]byte, 65536)
	for {
		n, err := tun.Read(buf)
		if err != nil {
			return
		}
		if s.Runtime != nil {
			s.Runtime.AddClientTraffic(clientID, 0, uint64(n))
		}
		h := make([]byte, protocol.HeaderSize)
		binary.BigEndian.PutUint32(h, uint32(n))
		if _, err := data.Write(h); err != nil {
			return
		}
		if _, err := data.Write(buf[:n]); err != nil {
			return
		}
		if s.Padding {
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

func (s *VPNServer) pipeQUICtoTUN(clientID string, data quic.Stream, tun TUNDevice) {
	buf := make([]byte, 65536)
	h := make([]byte, protocol.HeaderSize)
	for {
		if _, err := io.ReadFull(data, h); err != nil {
			return
		}
		l := binary.BigEndian.Uint32(h)
		if l > protocol.MaxPacketLen {
			return
		}
		readLen := int(l)
		if s.Padding {
			readLen += protocol.PaddingLen(int(l))
		}
		if _, err := io.ReadFull(data, buf[:readLen]); err != nil {
			return
		}
		if s.Runtime != nil {
			s.Runtime.AddClientTraffic(clientID, uint64(l), 0)
		}
		if _, err := tun.Write(buf[:l]); err != nil {
			return
		}
	}
}

// createTUN creates a TUN device (Linux/macOS using water library).
func createTUN(name string) (TUNDevice, error) {
	cfg := water.Config{DeviceType: water.TUN}
	if name != "" {
		cfg.Name = name
	}
	iface, err := water.New(cfg)
	if err != nil {
		return nil, fmt.Errorf("create TUN %q: %w", name, err)
	}
	return &waterDevice{iface: iface}, nil
}

type waterDevice struct {
	iface *water.Interface
}

func (w *waterDevice) Name() string                { return w.iface.Name() }
func (w *waterDevice) Read(p []byte) (int, error)  { return w.iface.Read(p) }
func (w *waterDevice) Write(p []byte) (int, error) { return w.iface.Write(p) }
func (w *waterDevice) Close() error                { return w.iface.Close() }

// configureTUN sets the IP address on the TUN interface.
func configureTUN(iface TUNDevice, subnet string) error {
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
		gw := ipStr
		if out, e := exec.Command("ifconfig", name, ipStr, gw, "netmask", maskStr, "up").CombinedOutput(); e != nil {
			return fmt.Errorf("ifconfig: %w: %s", e, out)
		}
	default:
		return fmt.Errorf("configureTUN not supported on %s", runtime.GOOS)
	}
	return nil
}

// serverIPFromSubnet takes "10.0.8.0/24" and returns "10.0.8.1/24".
func serverIPFromSubnet(subnet string) string {
	ip, ipnet, err := net.ParseCIDR(subnet)
	if err != nil {
		return subnet
	}
	network := ipnet.IP.Mask(ipnet.Mask)
	if ip.Equal(network) {
		host := make(net.IP, len(network))
		copy(host, network)
		host[len(host)-1] |= 0x01
		ip = host
	}
	ones, _ := ipnet.Mask.Size()
	return fmt.Sprintf("%s/%d", ip.String(), ones)
}
