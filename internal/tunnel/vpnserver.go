package tunnel

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"strings"
	"sync"

	"github.com/quic-go/quic-go"

	"github.com/example/sshtunneld/internal/transport"
)

type VPNServer struct {
	ListenAddr string
	Subnet     string
	Username   string
	Password   string
	TLSOpts    transport.TLSOpts
	Padding    bool // enable frame padding (default should be true)
	Log        *slog.Logger
}

func (s *VPNServer) Run(ctx context.Context) error {
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

func (s *VPNServer) handleClient(ctx context.Context, conn *quic.Conn) {
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

	// Read auth line: HTTP-style POST request.
	var authBuf []byte
	tmp := make([]byte, 1)
	for {
		n, rErr := ctl.Read(tmp)
		if n > 0 {
			authBuf = append(authBuf, tmp[0])
			// Detect end of HTTP headers (\r\n\r\n).
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

	// Parse HTTP-style auth: extract Authorization header.
	authLine := string(authBuf)
	log.Info("auth received", "bytes", len(authBuf))
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
		// Fallback: return a realistic HTTP 403 response to defeat active probing.
		fallbackResp := "HTTP/1.1 403 Forbidden\r\n" +
			"Server: cloudflare\r\n" +
			"Content-Type: text/html; charset=UTF-8\r\n" +
			"Content-Length: 155\r\n" +
			"\r\n" +
			"<html><head><title>403 Forbidden</title></head><body><center><h1>403 Forbidden</h1></center><hr><center>cloudflare</center></body></html>\r\n"
		_, _ = ctl.Write([]byte(fallbackResp))
		log.Warn("auth failed, sent fallback")
		ctl.Close()
		conn.CloseWithError(2, "auth failed")
		return
	}

	if _, err := ctl.Write([]byte("HTTP/1.1 200 OK\r\nContent-Length: 0\r\n\r\n")); err != nil {
		log.Warn("auth write", "err", err)
		ctl.Close()
		conn.CloseWithError(2, "auth failed")
		return
	}
	ctl.Close()
	log.Info("authenticated")

	// Create TUN (use "tun0" to match iptables FORWARD rules set by deploy).
	iface, err := CreateTUNNamed("tun0")
	if err != nil {
		log.Warn("create TUN", "err", err)
		conn.CloseWithError(3, "tun error")
		return
	}
	defer iface.Close()
	// Compute server IP: use .1 address from the subnet.
	serverIP := serverIPFromSubnet(s.Subnet)
	if err := configureTUNDev(iface, serverIP); err != nil {
		log.Warn("configure TUN", "err", err)
	}

	data, err := conn.AcceptStream(ctx)
	if err != nil {
		log.Warn("accept data", "err", err)
		return
	}
	defer data.Close()

	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); serverPipeQUICtoTUN(data, iface, s.Padding) }()
	go func() { defer wg.Done(); serverPipeTUNtoQUIC(iface, data, s.Padding) }()
	wg.Wait()
	_ = conn.CloseWithError(0, "done")
	log.Info("disconnected")
}

func serverPipeTUNtoQUIC(tun TUNDevice, data *quic.Stream, padding bool) {
	buf := make([]byte, 65536)
	for {
		n, err := tun.Read(buf)
		if err != nil { return }
		h := make([]byte, 4)
		binary.BigEndian.PutUint32(h, uint32(n))
		if _, err := data.Write(h); err != nil { return }
		if _, err := data.Write(buf[:n]); err != nil { return }
		if padding {
			padLen := transport.PaddingLen(n)
			if padLen > 0 {
				pad := make([]byte, padLen)
				if _, err := data.Write(pad); err != nil { return }
			}
		}
	}
}

func serverPipeQUICtoTUN(data *quic.Stream, tun TUNDevice, padding bool) {
	buf := make([]byte, 65536)
	h := make([]byte, 4)
	for {
		if _, err := io.ReadFull(data, h); err != nil { return }
		l := binary.BigEndian.Uint32(h)
		if l > 65535 { return }
		readLen := int(l)
		if padding {
			readLen += transport.PaddingLen(int(l))
		}
		if _, err := io.ReadFull(data, buf[:readLen]); err != nil { return }
		if _, err := tun.Write(buf[:l]); err != nil { return }
	}
}

// serverIPFromSubnet takes a subnet CIDR (e.g. "10.0.8.0/24") and returns
// the first usable host IP with prefix (e.g. "10.0.8.1/24") suitable for
// the server's TUN interface.
func serverIPFromSubnet(subnet string) string {
	ip, ipnet, err := net.ParseCIDR(subnet)
	if err != nil {
		return subnet // fallback: return as-is
	}
	// If the IP is the network address, use .1 instead.
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
