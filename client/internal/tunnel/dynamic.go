package tunnel

import (
	"context"
	"fmt"
	"io"
	"log"
	"log/slog"
	"net"

	socks5 "github.com/things-go/go-socks5"
	"golang.org/x/crypto/ssh"

	"github.com/example/safelink/pkg/config"
)

// Dynamic implements -D: a local SOCKS5 listener whose outbound dials are
// routed through the SSH client.
type Dynamic struct {
	cfg   config.TunnelCfg
	log   *slog.Logger
	stats *Stats
}

func (d *Dynamic) Name() string { return d.cfg.Name }

func (d *Dynamic) Serve(ctx context.Context, c *ssh.Client) error {
	server := socks5.NewServer(
		socks5.WithDial(func(ctx context.Context, network, addr string) (net.Conn, error) {
			if network != "tcp" && network != "tcp4" && network != "tcp6" {
				return nil, fmt.Errorf("socks5 dial: unsupported network %q", network)
			}
			conn, err := c.DialContext(ctx, "tcp", addr)
			if err != nil {
				return nil, err
			}
			return wrapConn(conn, d.stats, false), nil
		}),
		socks5.WithLogger(socks5.NewLogger(log.New(io.Discard, "", 0))),
	)

	ln, err := net.Listen("tcp", d.cfg.Listen)
	if err != nil {
		return fmt.Errorf("dynamic listen %s: %w", d.cfg.Listen, err)
	}
	d.log.Info("dynamic-forward (SOCKS5) up", "listen", d.cfg.Listen)
	closeOnContext(ctx.Done(), ln)
	defer ln.Close()

	if err := server.Serve(ln); err != nil {
		if ctx.Err() != nil || isClosedErr(err) {
			return nil
		}
		return fmt.Errorf("socks5 serve: %w", err)
	}
	return nil
}
