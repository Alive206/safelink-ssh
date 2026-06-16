package tunnel

import (
	"context"
	"fmt"
	"log/slog"
	"net"

	"golang.org/x/crypto/ssh"

	"github.com/example/sshtunneld/internal/config"
)

// Local implements -L: listen on a local address and dial each accepted
// connection's destination through the SSH server.
type Local struct {
	cfg   config.TunnelCfg
	log   *slog.Logger
	stats *Stats
}

func (l *Local) Name() string { return l.cfg.Name }

func (l *Local) Serve(ctx context.Context, c *ssh.Client) error {
	ln, err := net.Listen("tcp", l.cfg.Listen)
	if err != nil {
		return fmt.Errorf("local listen %s: %w", l.cfg.Listen, err)
	}
	l.log.Info("local-forward up", "listen", l.cfg.Listen, "forward", l.cfg.Forward)
	closeOnContext(ctx.Done(), ln)
	defer ln.Close()

	for {
		in, err := ln.Accept()
		if err != nil {
			if ctx.Err() != nil || isClosedErr(err) {
				return nil
			}
			return fmt.Errorf("accept: %w", err)
		}
		go l.handle(c, in)
	}
}

func (l *Local) handle(c *ssh.Client, in net.Conn) {
	out, err := c.Dial("tcp", l.cfg.Forward)
	if err != nil {
		l.log.Warn("dial forward failed", "forward", l.cfg.Forward, "err", err)
		_ = in.Close()
		return
	}
	// Attach the counter to the local-side conn (`in`) so BytesIn = traffic
	// from the local app, BytesOut = traffic returned to it.
	bidiCopy(wrapConn(in, l.stats, true), out)
}
