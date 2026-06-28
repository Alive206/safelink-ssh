package tunnel

import (
	"context"
	"fmt"
	"log/slog"
	"net"

	"golang.org/x/crypto/ssh"

	"github.com/example/safelink/pkg/config"
)

// Remote implements -R: ask the SSH server to listen on a remote address;
// each connection accepted there is dialed locally and proxied.
type Remote struct {
	cfg   config.TunnelCfg
	log   *slog.Logger
	stats *Stats
}

func (r *Remote) Name() string { return r.cfg.Name }

func (r *Remote) Serve(ctx context.Context, c *ssh.Client) error {
	ln, err := c.Listen("tcp", r.cfg.Listen)
	if err != nil {
		return fmt.Errorf("remote listen %s: %w", r.cfg.Listen, err)
	}
	r.log.Info("remote-forward up", "remote_listen", r.cfg.Listen, "forward", r.cfg.Forward)
	closeOnContext(ctx.Done(), ln)
	defer ln.Close()

	for {
		in, err := ln.Accept()
		if err != nil {
			if ctx.Err() != nil || isClosedErr(err) {
				return nil
			}
			return fmt.Errorf("remote accept: %w", err)
		}
		go r.handle(in)
	}
}

func (r *Remote) handle(in net.Conn) {
	out, err := net.Dial("tcp", r.cfg.Forward)
	if err != nil {
		r.log.Warn("local dial forward failed", "forward", r.cfg.Forward, "err", err)
		_ = in.Close()
		return
	}
	bidiCopy(in, wrapConn(out, r.stats, false))
}
