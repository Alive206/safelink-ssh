// Package web implements the embedded HTTP control panel.  It exposes a JSON
// API under /api/* and serves the prebuilt React UI from an embedded
// filesystem at /.
package web

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/example/sshtunneld/internal/config"
	"github.com/example/sshtunneld/internal/logging"
	"github.com/example/sshtunneld/internal/manager"
)

// Server is the long-lived HTTP server for the control panel.
type Server struct {
	httpSrv *http.Server
	auth    *authenticator
	log     *slog.Logger
	handler *handler
}

// New wires the manager + auth + log broadcaster into an *http.Server.
// The server is not started until Run is called.
func New(cfg config.WebCfg, role string, mgr *manager.Manager, bcast *logging.Broadcaster, log *slog.Logger) *Server {
	auth := newAuthenticator(cfg.Auth)
	h := newHandler(mgr, bcast, auth, log, role)

	mux := http.NewServeMux()
	h.routes(mux)

	srv := &http.Server{
		Addr:              cfg.Addr,
		Handler:           withLogging(log, mux),
		ReadHeaderTimeout: 10 * time.Second,
	}
	return &Server{httpSrv: srv, auth: auth, log: log.With("component", "web"), handler: h}
}

// SetShutdownFunc sets the function called when POST /api/shutdown is invoked.
func (s *Server) SetShutdownFunc(fn func()) {
	s.handler.shutdownFn = fn
}

// Run blocks serving HTTP until ctx is cancelled, then performs a graceful
// shutdown bounded by 5 seconds.
func (s *Server) Run(ctx context.Context) error {
	if s.httpSrv.Addr == "" {
		s.log.Info("web control panel disabled (web.addr is empty)")
		<-ctx.Done()
		return nil
	}
	s.log.Info("web control panel listening", "addr", s.httpSrv.Addr)

	// Periodic session GC.
	gcCtx, gcCancel := context.WithCancel(ctx)
	defer gcCancel()
	go func() {
		t := time.NewTicker(5 * time.Minute)
		defer t.Stop()
		for {
			select {
			case <-gcCtx.Done():
				return
			case <-t.C:
				s.auth.pruneSessions()
			}
		}
	}()

	// Subscription auto-refresh loop (only for client/standalone).
	if s.handler.role != config.RoleServer {
		go s.handler.subscriptionRefreshLoop(ctx)
	}

	errCh := make(chan error, 1)
	go func() {
		errCh <- s.httpSrv.ListenAndServe()
	}()
	select {
	case err := <-errCh:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			return fmt.Errorf("web listen: %w", err)
		}
		return nil
	case <-ctx.Done():
		shutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = s.httpSrv.Shutdown(shutCtx)
		return nil
	}
}
