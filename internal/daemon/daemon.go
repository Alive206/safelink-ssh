// Package daemon ties config loading, supervisor wiring and signal handling
// together into a single Run entry point used by main.
package daemon

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/example/sshtunneld/internal/bootstrap"
	"github.com/example/sshtunneld/internal/logging"
	"github.com/example/sshtunneld/internal/manager"
	"github.com/example/sshtunneld/internal/store"
	"github.com/example/sshtunneld/internal/web"
)

// Options controls the optional first-run behaviours for Run.
type Options struct {
	// AutoInit creates a default YAML config (with a freshly-generated admin
	// user) when cfgPath does not exist yet.  Recommended for end-user "just
	// double-click" launches.
	AutoInit bool

	// OpenBrowser, when true, points the OS default browser at the web UI a
	// short moment after start-up.  No-op when web.addr is empty.
	OpenBrowser bool
}

// Run loads the config at cfgPath, starts a supervisor goroutine for every
// configured tunnel, brings up the web control panel and blocks until the
// process receives SIGINT or SIGTERM (Ctrl+C on Windows).
func Run(cfgPath string) error { return RunWithOptions(cfgPath, Options{}) }

// RunWithOptions is Run plus the first-run conveniences enabled by Options.
//
// Wiring order (intentional):
//
//  1. bootstrap.EnsureConfig — write a default YAML on first launch (opt-in)
//  2. store.LoadAll          — merge YAML + tunnels.json
//  3. manager.New/Start      — own every tunnel's lifecycle
//  4. web.New/Run            — expose HTTP API & UI (+ optional open browser)
func RunWithOptions(cfgPath string, opts Options) error {
	if opts.AutoInit {
		created, pw, err := bootstrap.EnsureConfig(cfgPath)
		if err != nil {
			return fmt.Errorf("bootstrap: %w", err)
		}
		if created {
			fmt.Fprintln(os.Stderr, strings.Repeat("=", 60))
			fmt.Fprintln(os.Stderr, " Welcome to sshtunneld — first-run setup complete.")
			fmt.Fprintln(os.Stderr, strings.Repeat("-", 60))
			fmt.Fprintln(os.Stderr, "  URL:      http://127.0.0.1:8080")
			fmt.Fprintln(os.Stderr, "  Username: admin")
			fmt.Fprintln(os.Stderr, "  Password:", pw)
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, " Saved to: configs/admin_credentials.txt")
			fmt.Fprintln(os.Stderr, " Rotate any time with:  sshtunneld passwd admin")
			fmt.Fprintln(os.Stderr, strings.Repeat("=", 60))
		}
	}

	st := store.New(cfgPath)
	cfg, err := st.LoadAll()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	log, bcast := logging.NewWithBroadcast(cfg.LogLevel, nil)

	log.Info("sshtunneld starting",
		"tunnels", len(cfg.Tunnels),
		"known_hosts", cfg.KnownHosts,
		"web_addr", cfg.Web.Addr,
	)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	mgr := manager.New(cfg, log, st)
	mgr.Start(ctx)

	srv := web.New(cfg.Web, cfg.Role, mgr, bcast, log)
	srv.SetShutdownFunc(stop) // allow POST /api/shutdown to trigger graceful exit

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		if err := srv.Run(ctx); err != nil {
			log.Error("web server stopped", "err", err)
		}
	}()

	if opts.OpenBrowser && cfg.Web.Addr != "" {
		go func() {
			// Wait briefly so the listener is actually accepting connections
			// before we fire up the browser.
			select {
			case <-time.After(800 * time.Millisecond):
			case <-ctx.Done():
				return
			}
			url := webURL(cfg.Web.Addr)
			if err := openBrowser(url); err != nil {
				log.Warn("open browser failed", "url", url, "err", err)
			}
		}()
	}

	<-ctx.Done()
	log.Info("shutdown signal received, stopping tunnels")
	mgr.StopAll()
	wg.Wait()
	log.Info("sshtunneld stopped")
	return nil
}

// webURL turns a listen address like "0.0.0.0:8080" into a browser-friendly
// URL pointing at the loopback interface on the same port.
func webURL(addr string) string {
	host, port := splitHostPort(addr)
	switch host {
	case "", "0.0.0.0", "[::]", "::":
		host = "127.0.0.1"
	}
	if port == "" {
		return "http://" + host
	}
	return "http://" + host + ":" + port
}

func splitHostPort(addr string) (string, string) {
	// Avoid pulling in net just for this — handle "host:port" and ":port".
	idx := strings.LastIndex(addr, ":")
	if idx < 0 {
		return addr, ""
	}
	return addr[:idx], addr[idx+1:]
}

// openBrowser launches the OS default browser at url.  Best-effort: errors
// are surfaced to the caller for logging but never fatal.
func openBrowser(url string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		cmd = exec.Command("cmd", "/c", "start", "", url)
	case "darwin":
		cmd = exec.Command("open", url)
	default:
		cmd = exec.Command("xdg-open", url)
	}
	return cmd.Start()
}
