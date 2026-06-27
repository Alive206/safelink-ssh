// Package tunnel implements the forwarding modes (-L / -R / -D / VPN) on
// top of an established *ssh.Client (or QUIC transport for VPN).
package tunnel

import (
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"sync"

	"github.com/example/sshtunneld/internal/config"
	"github.com/example/sshtunneld/internal/sshclient"
)

// Build returns a Tunnel implementation suitable for the given config.  The
// optional stats pointer (nil disables counting) is injected so the manager
// can attribute bytes/connections to a specific tunnel.
func Build(t config.TunnelCfg, log *slog.Logger, stats *Stats) (sshclient.Tunnel, error) {
	tlog := log.With("tunnel", t.Name)
	switch t.Mode {
	case config.ModeLocal:
		return &Local{cfg: t, log: tlog, stats: stats}, nil
	case config.ModeRemote:
		return &Remote{cfg: t, log: tlog, stats: stats}, nil
	case config.ModeDynamic:
		return &Dynamic{cfg: t, log: tlog, stats: stats}, nil
	case config.ModeVPN:
		return NewVPN(t, tlog, stats), nil
	default:
		return nil, fmt.Errorf("unknown tunnel mode %q", t.Mode)
	}
}

// halfCloser is implemented by net.TCPConn and ssh channel-backed conns; we
// use it to send EOF in one direction without tearing the whole conn down.
type halfCloser interface {
	CloseWrite() error
}

// bidiCopy proxies traffic both ways between a and b.  It returns when both
// directions are closed.  Either side may be a *countingConn — Read/Write
// bookkeeping happens transparently inside.
func bidiCopy(a, b net.Conn) {
	var wg sync.WaitGroup
	wg.Add(2)
	go copyAndCloseWrite(&wg, a, b)
	go copyAndCloseWrite(&wg, b, a)
	wg.Wait()
	_ = a.Close()
	_ = b.Close()
}

func copyAndCloseWrite(wg *sync.WaitGroup, dst, src net.Conn) {
	defer wg.Done()
	_, _ = io.Copy(dst, src)
	if hc, ok := dst.(halfCloser); ok {
		_ = hc.CloseWrite()
	}
}

// closeOnContext closes ln when ctx is cancelled.  It is the canonical way to
// unblock an Accept loop on shutdown.
func closeOnContext[L io.Closer](done <-chan struct{}, ln L) {
	go func() {
		<-done
		_ = ln.Close()
	}()
}

// isClosedErr reports whether err is the benign "listener closed" condition
// that we expect during shutdown.
func isClosedErr(err error) bool {
	return errors.Is(err, net.ErrClosed) || errors.Is(err, io.EOF)
}
