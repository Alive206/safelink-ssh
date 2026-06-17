// Package manager owns the runtime lifecycle of every configured tunnel.
// Each tunnel runs inside its own goroutine driven by a sshclient.Supervisor;
// the manager exposes an HTTP-friendly CRUD + start/stop/restart surface to
// the web control panel.
package manager

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/example/sshtunneld/internal/config"
	"github.com/example/sshtunneld/internal/sshclient"
	"github.com/example/sshtunneld/internal/store"
	"github.com/example/sshtunneld/internal/tunnel"
)

// ErrNotFound is returned when a named tunnel is not present.
var ErrNotFound = errors.New("tunnel not found")

// ErrExists is returned by Add when the name is already taken.
var ErrExists = errors.New("tunnel already exists")

// Status is the JSON-friendly snapshot exposed by the HTTP layer.
type Status struct {
	Config    config.TunnelCfg `json:"config"`
	State     string           `json:"state"`
	LastError string           `json:"last_error,omitempty"`
	StartedAt *time.Time       `json:"started_at,omitempty"`
	UptimeSec int64            `json:"uptime_seconds"`
	RunCount  int64            `json:"run_count"`
	Stats     tunnel.Snapshot  `json:"stats"`
}

// managedTunnel is the manager's per-tunnel runtime container.
type managedTunnel struct {
	cfg      config.TunnelCfg
	stats    *tunnel.Stats
	cancel   context.CancelFunc // nil when stopped
	done     chan struct{}      // closed when the supervisor goroutine exits
	state    atomic.Value       // sshclient.State
	lastErr  atomic.Value       // string
	runCount atomic.Int64
	startAt  atomic.Value // time.Time when the supervisor entered Running
}

func (m *managedTunnel) loadState() sshclient.State {
	if v := m.state.Load(); v != nil {
		return v.(sshclient.State)
	}
	return sshclient.StateStopped
}

func (m *managedTunnel) loadErr() string {
	if v := m.lastErr.Load(); v != nil {
		return v.(string)
	}
	return ""
}

func (m *managedTunnel) loadStart() (time.Time, bool) {
	if v := m.startAt.Load(); v != nil {
		t := v.(time.Time)
		return t, !t.IsZero()
	}
	return time.Time{}, false
}

// Manager coordinates a fleet of managedTunnel instances.
type Manager struct {
	mu              sync.RWMutex
	tunnels         map[string]*managedTunnel
	defaults        config.ConnDefaults
	knownHosts      string
	insecureHostKey bool
	log             *slog.Logger
	store           *store.Store
	rootCtx         context.Context
}

// KeysDir exposes the on-disk directory holding uploaded SSH private keys,
// passing through to the underlying store.
func (m *Manager) KeysDir() string { return m.store.KeysDir() }

// IsKeyInUse reports whether any tunnel currently references the given
// absolute key path.  Used by the keys API to refuse deletion of an
// in-use key.
func (m *Manager) IsKeyInUse(absPath string) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	for _, mt := range m.tunnels {
		if mt.cfg.SSH.IdentityFile == absPath {
			return true
		}
	}
	return false
}

// New constructs a Manager seeded from cfg.Tunnels.  Use Start to actually
// launch every supervisor.
func New(cfg *config.Config, log *slog.Logger, st *store.Store) *Manager {
	m := &Manager{
		tunnels:         make(map[string]*managedTunnel),
		defaults:        cfg.Defaults,
		knownHosts:      cfg.KnownHosts,
		insecureHostKey: cfg.InsecureHostKey,
		log:             log,
		store:           st,
	}
	for _, tc := range cfg.Tunnels {
		mt := &managedTunnel{cfg: tc, stats: tunnel.NewStats()}
		mt.state.Store(sshclient.StateStopped)
		m.tunnels[tc.Name] = mt
	}
	return m
}

// Start begins running every tunnel currently in the manager.  ctx becomes
// the parent context for all supervisors; cancelling it stops everyone.
func (m *Manager) Start(ctx context.Context) {
	m.rootCtx = ctx
	m.mu.RLock()
	names := make([]string, 0, len(m.tunnels))
	for n := range m.tunnels {
		names = append(names, n)
	}
	m.mu.RUnlock()

	for _, name := range names {
		if err := m.startTunnel(name); err != nil {
			m.log.Warn("auto-start tunnel failed", "tunnel", name, "err", err)
		}
	}
}

// StopAll cancels every running supervisor and waits for them to exit.
func (m *Manager) StopAll() {
	m.mu.RLock()
	mts := make([]*managedTunnel, 0, len(m.tunnels))
	for _, mt := range m.tunnels {
		mts = append(mts, mt)
	}
	m.mu.RUnlock()
	for _, mt := range mts {
		m.stopManaged(mt)
	}
}

// List returns a snapshot of every tunnel's status, sorted by name.
func (m *Manager) List() []Status {
	m.mu.RLock()
	out := make([]Status, 0, len(m.tunnels))
	for _, mt := range m.tunnels {
		out = append(out, m.statusOf(mt))
	}
	m.mu.RUnlock()
	return out
}

// Get returns one tunnel's status.
func (m *Manager) Get(name string) (Status, error) {
	m.mu.RLock()
	mt, ok := m.tunnels[name]
	m.mu.RUnlock()
	if !ok {
		return Status{}, ErrNotFound
	}
	return m.statusOf(mt), nil
}

// Stats returns a per-tunnel snapshot keyed by tunnel name.
func (m *Manager) Stats() map[string]tunnel.Snapshot {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make(map[string]tunnel.Snapshot, len(m.tunnels))
	for n, mt := range m.tunnels {
		out[n] = mt.stats.Snapshot()
	}
	return out
}

// StatsOf returns a single tunnel's stats snapshot.
func (m *Manager) StatsOf(name string) (tunnel.Snapshot, error) {
	m.mu.RLock()
	mt, ok := m.tunnels[name]
	m.mu.RUnlock()
	if !ok {
		return tunnel.Snapshot{}, ErrNotFound
	}
	return mt.stats.Snapshot(), nil
}

// Add validates and inserts a new tunnel, persists tunnels.json, then starts
// it.  Returns ErrExists if name is already taken.
func (m *Manager) Add(tc config.TunnelCfg) error {
	if err := config.ValidateTunnel(tc); err != nil {
		return err
	}
	tc.SSH.IdentityFile, _ = config.ExpandHome(tc.SSH.IdentityFile)

	m.mu.Lock()
	if _, exists := m.tunnels[tc.Name]; exists {
		m.mu.Unlock()
		return ErrExists
	}
	mt := &managedTunnel{cfg: tc, stats: tunnel.NewStats()}
	mt.state.Store(sshclient.StateStopped)
	m.tunnels[tc.Name] = mt
	m.mu.Unlock()

	if err := m.persist(); err != nil {
		// Roll back the in-memory insert so disk and memory stay consistent.
		m.mu.Lock()
		delete(m.tunnels, tc.Name)
		m.mu.Unlock()
		return fmt.Errorf("persist tunnels: %w", err)
	}
	return m.startTunnel(tc.Name)
}

// Update replaces a tunnel's config in-place.  The supervisor is restarted to
// pick up the new fields.
func (m *Manager) Update(name string, tc config.TunnelCfg) error {
	if tc.Name != "" && tc.Name != name {
		return fmt.Errorf("rename via update is not supported (got %q, want %q)", tc.Name, name)
	}
	tc.Name = name
	if err := config.ValidateTunnel(tc); err != nil {
		return err
	}
	tc.SSH.IdentityFile, _ = config.ExpandHome(tc.SSH.IdentityFile)

	m.mu.Lock()
	mt, ok := m.tunnels[name]
	if !ok {
		m.mu.Unlock()
		return ErrNotFound
	}
	old := mt.cfg
	mt.cfg = tc
	m.mu.Unlock()

	if err := m.persist(); err != nil {
		// Roll back on persistence failure.
		m.mu.Lock()
		mt.cfg = old
		m.mu.Unlock()
		return fmt.Errorf("persist tunnels: %w", err)
	}
	return m.Restart(name)
}

// Delete stops and removes a tunnel, then persists the updated list.
func (m *Manager) Delete(name string) error {
	m.mu.Lock()
	mt, ok := m.tunnels[name]
	if !ok {
		m.mu.Unlock()
		return ErrNotFound
	}
	delete(m.tunnels, name)
	m.mu.Unlock()

	m.stopManaged(mt)
	return m.persist()
}

// StartTunnel launches a previously stopped tunnel.
func (m *Manager) StartTunnel(name string) error { return m.startTunnel(name) }

// Stop cancels a running tunnel without removing it.
func (m *Manager) Stop(name string) error {
	m.mu.RLock()
	mt, ok := m.tunnels[name]
	m.mu.RUnlock()
	if !ok {
		return ErrNotFound
	}
	m.stopManaged(mt)
	return nil
}

// Restart is Stop + Start.
func (m *Manager) Restart(name string) error {
	if err := m.Stop(name); err != nil {
		return err
	}
	return m.startTunnel(name)
}

// ----- internals ---------------------------------------------------------

func (m *Manager) statusOf(mt *managedTunnel) Status {
	st := Status{
		Config:   mt.cfg,
		State:    string(mt.loadState()),
		RunCount: mt.runCount.Load(),
		Stats:    mt.stats.Snapshot(),
	}
	if e := mt.loadErr(); e != "" {
		st.LastError = e
	}
	if t, ok := mt.loadStart(); ok {
		ts := t
		st.StartedAt = &ts
		st.UptimeSec = int64(time.Since(t).Seconds())
	}
	// Mask secrets in the JSON view; actual storage keeps the originals.
	if st.Config.SSH.Password != "" {
		st.Config.SSH.Password = "***"
	}
	if st.Config.SSH.Passphrase != "" {
		st.Config.SSH.Passphrase = "***"
	}
	return st
}

func (m *Manager) startTunnel(name string) error {
	if m.rootCtx == nil {
		return errors.New("manager not started yet")
	}
	m.mu.Lock()
	mt, ok := m.tunnels[name]
	if !ok {
		m.mu.Unlock()
		return ErrNotFound
	}
	if mt.cancel != nil {
		m.mu.Unlock()
		return nil // already running
	}
	t, err := tunnel.Build(mt.cfg, m.log, mt.stats)
	if err != nil {
		m.mu.Unlock()
		return fmt.Errorf("build tunnel: %w", err)
	}
	sup := sshclient.NewSupervisor(mt.cfg, m.defaults, m.knownHosts, m.insecureHostKey, t, m.log)
	sup.OnStateChange = func(state sshclient.State, lastErr error) {
		mt.state.Store(state)
		if lastErr != nil {
			mt.lastErr.Store(lastErr.Error())
		} else if state == sshclient.StateRunning {
			mt.lastErr.Store("")
		}
		switch state {
		case sshclient.StateRunning:
			mt.startAt.Store(time.Now())
			mt.runCount.Add(1)
		case sshclient.StateStopped:
			mt.startAt.Store(time.Time{})
		}
	}
	ctx, cancel := context.WithCancel(m.rootCtx)
	mt.cancel = cancel
	mt.done = make(chan struct{})
	m.mu.Unlock()

	go func() {
		defer close(mt.done)
		sup.Run(ctx)
		// Clean up so a future Start can succeed.
		m.mu.Lock()
		if mt.cancel != nil {
			// Only clear if nobody else replaced it (Stop sets cancel=nil).
			mt.cancel = nil
		}
		m.mu.Unlock()
	}()
	return nil
}

func (m *Manager) stopManaged(mt *managedTunnel) {
	m.mu.Lock()
	cancel := mt.cancel
	done := mt.done
	mt.cancel = nil
	m.mu.Unlock()
	if cancel == nil {
		return
	}
	cancel()
	if done != nil {
		<-done
	}
}

func (m *Manager) persist() error {
	if m.store == nil {
		return nil
	}
	m.mu.RLock()
	out := make([]config.TunnelCfg, 0, len(m.tunnels))
	for _, mt := range m.tunnels {
		out = append(out, mt.cfg)
	}
	m.mu.RUnlock()
	return m.store.SaveTunnels(out)
}
