// Package store persists client-side data: tunnels and subscriptions.
package store

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/example/safelink/pkg/config"
)

// SubscriptionSource represents a remote subscription URL.
type SubscriptionSource struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	URL         string `json:"url"`
	Format      string `json:"format"`
	AutoRefresh bool   `json:"auto_refresh"`
	IntervalMin int    `json:"interval_min"`
	LastRefresh string `json:"last_refresh,omitempty"`
	LastError   string `json:"last_error,omitempty"`
	TunnelCount int    `json:"tunnel_count"`
}

// Store persists tunnels and subscriptions to JSON files.
type Store struct {
	mu          sync.Mutex
	dataDir     string
	tunnelsPath string
	subsPath    string
}

// New returns a Store rooted at the given data directory.
func New(dataDir string) *Store {
	return &Store{
		dataDir:     dataDir,
		tunnelsPath: filepath.Join(dataDir, "tunnels.json"),
		subsPath:    filepath.Join(dataDir, "subscriptions.json"),
	}
}

// LoadTunnels reads tunnels.json. Returns empty slice if not found.
func (s *Store) LoadTunnels() ([]config.TunnelCfg, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	data, err := os.ReadFile(s.tunnelsPath)
	if errors.Is(err, os.ErrNotExist) {
		return []config.TunnelCfg{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", s.tunnelsPath, err)
	}
	var tunnels []config.TunnelCfg
	if err := json.Unmarshal(data, &tunnels); err != nil {
		return nil, fmt.Errorf("parse %s: %w", s.tunnelsPath, err)
	}
	return tunnels, nil
}

// SaveTunnels writes the tunnel list atomically.
func (s *Store) SaveTunnels(tunnels []config.TunnelCfg) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if tunnels == nil {
		tunnels = []config.TunnelCfg{}
	}
	data, err := json.MarshalIndent(tunnels, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal tunnels: %w", err)
	}
	return s.writeFileAtomic(s.tunnelsPath, data)
}

// LoadSubscriptions reads subscriptions.json.
func (s *Store) LoadSubscriptions() ([]SubscriptionSource, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	data, err := os.ReadFile(s.subsPath)
	if errors.Is(err, os.ErrNotExist) {
		return []SubscriptionSource{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", s.subsPath, err)
	}
	var sources []SubscriptionSource
	if err := json.Unmarshal(data, &sources); err != nil {
		return nil, fmt.Errorf("parse %s: %w", s.subsPath, err)
	}
	return sources, nil
}

// SaveSubscriptions writes the subscription list atomically.
func (s *Store) SaveSubscriptions(sources []SubscriptionSource) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if sources == nil {
		sources = []SubscriptionSource{}
	}
	data, err := json.MarshalIndent(sources, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal subscriptions: %w", err)
	}
	return s.writeFileAtomic(s.subsPath, data)
}

// AddSubscription appends a new subscription and saves.
func (s *Store) AddSubscription(src SubscriptionSource) error {
	sources, err := s.LoadSubscriptions()
	if err != nil {
		return err
	}
	if src.ID == "" {
		src.ID = randomID()
	}
	sources = append(sources, src)
	return s.SaveSubscriptions(sources)
}

// DeleteSubscription removes a subscription by ID and saves.
func (s *Store) DeleteSubscription(id string) error {
	sources, err := s.LoadSubscriptions()
	if err != nil {
		return err
	}
	found := false
	result := make([]SubscriptionSource, 0, len(sources))
	for _, src := range sources {
		if src.ID == id {
			found = true
			continue
		}
		result = append(result, src)
	}
	if !found {
		return fmt.Errorf("subscription %q not found", id)
	}
	return s.SaveSubscriptions(result)
}

func (s *Store) writeFileAtomic(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("mkdir %s: %w", dir, err)
	}
	tmp, err := os.CreateTemp(dir, ".tmp-*")
	if err != nil {
		return fmt.Errorf("create tmp: %w", err)
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }()

	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write tmp: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("fsync tmp: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close tmp: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("rename %s: %w", path, err)
	}
	return nil
}

func randomID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
