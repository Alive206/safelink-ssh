// Package main provides Wails bindings (Go methods exposed to the frontend).
package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/example/safelink/client/internal/manager"
	"github.com/example/safelink/client/internal/store"
	"github.com/example/safelink/client/internal/tunnel"
	"github.com/example/safelink/pkg/config"
)

// App struct holds the application state exposed to the Wails frontend.
type App struct {
	ctx     context.Context
	cancel  context.CancelFunc
	manager *manager.Manager
	store   *store.Store
	log     *slog.Logger
}

// NewApp creates a new App application struct.
func NewApp() *App {
	return &App{
		log: slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo})),
	}
}

// startup is called when the app starts. The context is saved so we can call
// the runtime methods.
func (a *App) startup(ctx context.Context) {
	rootCtx, cancel := context.WithCancel(ctx)
	a.ctx = rootCtx
	a.cancel = cancel

	// Determine data directory.
	dataDir := defaultDataDir()
	_ = os.MkdirAll(dataDir, 0o755)

	a.store = store.New(dataDir)
	tunnels, _ := a.store.LoadTunnels()
	defaults := config.ConnDefaults{
		KeepAliveInterval: 30_000_000_000, // 30s
		KeepAliveMaxFails: 3,
		DialTimeout:       10_000_000_000, // 10s
		ReconnectInitial:  1_000_000_000,  // 1s
		ReconnectMax:      60_000_000_000, // 60s
	}
	a.manager = manager.New(tunnels, defaults, a.log, a.store)
	a.manager.Start(rootCtx)
}

// shutdown is called when the app is closing.
func (a *App) shutdown(_ context.Context) {
	if a.manager != nil {
		a.manager.StopAll()
	}
	if a.cancel != nil {
		a.cancel()
	}
}

// --- Tunnel Management (exposed to frontend) ---

// ListTunnels returns all tunnel statuses.
func (a *App) ListTunnels() []manager.Status {
	return a.manager.List()
}

// AddTunnel adds and starts a new tunnel.
func (a *App) AddTunnel(cfg config.TunnelCfg) error {
	return a.manager.Add(cfg)
}

// UpdateTunnel updates an existing tunnel config.
func (a *App) UpdateTunnel(name string, cfg config.TunnelCfg) error {
	return a.manager.Update(name, cfg)
}

// DeleteTunnel removes a tunnel.
func (a *App) DeleteTunnel(name string) error {
	return a.manager.Delete(name)
}

// StartTunnel starts a stopped tunnel.
func (a *App) StartTunnel(name string) error {
	return a.manager.StartTunnel(name)
}

// StopTunnel stops a running tunnel.
func (a *App) StopTunnel(name string) error {
	return a.manager.Stop(name)
}

// RestartTunnel restarts a tunnel.
func (a *App) RestartTunnel(name string) error {
	return a.manager.Restart(name)
}

// ToggleRoute enables or disables the global route for a VPN tunnel.
func (a *App) ToggleRoute(name string, enable bool) error {
	return a.manager.SetVPNRoute(name, enable)
}

// GetStats returns per-tunnel traffic statistics.
func (a *App) GetStats() map[string]tunnel.Snapshot {
	return a.manager.Stats()
}

// --- Subscription Management ---

// ImportSubscription adds a subscription URL and fetches its tunnels.
func (a *App) ImportSubscription(name, url string) error {
	return a.store.AddSubscription(store.SubscriptionSource{
		Name:   name,
		URL:    url,
		Format: "auto",
	})
}

// ListSubscriptions returns all subscription sources.
func (a *App) ListSubscriptions() ([]store.SubscriptionSource, error) {
	return a.store.LoadSubscriptions()
}

// DeleteSubscription removes a subscription by ID.
func (a *App) DeleteSubscription(id string) error {
	return a.store.DeleteSubscription(id)
}

// --- Driver Status ---

// CheckDriver returns the TUN driver status.
func (a *App) CheckDriver() (*tunnel.DriverStatus, error) {
	return tunnel.CheckDriver()
}

// InstallDriver installs the WinTUN driver.
func (a *App) InstallDriver() error {
	return tunnel.InstallDriver()
}

// --- Helpers ---

func defaultDataDir() string {
	appData := os.Getenv("APPDATA")
	if appData == "" {
		home, _ := os.UserHomeDir()
		appData = filepath.Join(home, "AppData", "Roaming")
	}
	return filepath.Join(appData, "SafeLink")
}

// GetVersion returns the application version.
func (a *App) GetVersion() string {
	return "1.0.0"
}

// GetDataDir returns the data directory path.
func (a *App) GetDataDir() string {
	return defaultDataDir()
}

// BulkImportTunnels imports a batch of tunnel configs (from subscription).
func (a *App) BulkImportTunnels(tunnels []config.TunnelCfg, prefix string) (int, int, []string) {
	return a.manager.BulkMerge(tunnels, prefix)
}

// Greet is a simple test binding.
func (a *App) Greet(name string) string {
	return fmt.Sprintf("Hello %s, SafeLink is running!", name)
}
