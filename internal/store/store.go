// Package store persists configuration that the web UI is allowed to mutate.
// Static settings (log_level, defaults, web, known_hosts) live in the main
// YAML; the tunnel list lives in a sibling JSON file so it can be rewritten
// atomically without touching anything else.
package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/example/sshtunneld/internal/config"
)

// Store binds the immutable main YAML to the writable tunnels.json file.
type Store struct {
	mu          sync.Mutex // serialises writes to tunnelsPath
	mainPath    string
	tunnelsPath string
}

// New returns a Store rooted at mainPath; tunnels.json is co-located in the
// same directory.  The JSON file is created lazily on first save.
func New(mainPath string) *Store {
	dir := filepath.Dir(mainPath)
	return &Store{
		mainPath:    mainPath,
		tunnelsPath: filepath.Join(dir, "tunnels.json"),
	}
}

// MainPath returns the YAML path the store was initialised with.
func (s *Store) MainPath() string { return s.mainPath }

// TunnelsPath returns the JSON file path used for the writable tunnel list.
func (s *Store) TunnelsPath() string { return s.tunnelsPath }

// KeysDir returns the directory where uploaded SSH private keys are stored.
// Co-located with the YAML so backup is one folder.  Created on demand.
func (s *Store) KeysDir() string {
	return filepath.Join(filepath.Dir(s.mainPath), "keys")
}

// LoadAll reads the YAML, then overlays tunnels from tunnels.json when present.
//
// Resolution order for the tunnel list:
//  1. tunnels.json exists and is valid → it wins (and YAML's tunnels are ignored)
//  2. otherwise → fall back to whatever was in the YAML (may be empty)
func (s *Store) LoadAll() (*config.Config, error) {
	cfg, err := config.Load(s.mainPath)
	if err != nil {
		return nil, err
	}

	data, err := os.ReadFile(s.tunnelsPath)
	switch {
	case err == nil:
		var tunnels []config.TunnelCfg
		if err := json.Unmarshal(data, &tunnels); err != nil {
			return nil, fmt.Errorf("parse %s: %w", s.tunnelsPath, err)
		}
		// Re-expand ~ in identity_file just like YAML load does.
		for i := range tunnels {
			if tunnels[i].SSH.IdentityFile != "" {
				exp, err := config.ExpandHome(tunnels[i].SSH.IdentityFile)
				if err != nil {
					return nil, err
				}
				tunnels[i].SSH.IdentityFile = exp
			}
			if err := config.ValidateTunnel(tunnels[i]); err != nil {
				return nil, fmt.Errorf("tunnels.json[%d] (%q): %w", i, tunnels[i].Name, err)
			}
		}
		cfg.Tunnels = tunnels
	case errors.Is(err, os.ErrNotExist):
		// Use whatever (possibly empty) list came from YAML.
	default:
		return nil, fmt.Errorf("read %s: %w", s.tunnelsPath, err)
	}

	return cfg, nil
}

// SaveTunnels writes the given list to tunnels.json atomically (write to a
// sibling tmp file, fsync, then os.Rename).  Concurrent callers are
// serialised on s.mu.
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

	dir := filepath.Dir(s.tunnelsPath)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("mkdir %s: %w", dir, err)
	}

	tmp, err := os.CreateTemp(dir, ".tunnels-*.json")
	if err != nil {
		return fmt.Errorf("create tmp: %w", err)
	}
	tmpName := tmp.Name()
	defer func() {
		// Best-effort cleanup if rename never happened.
		_ = os.Remove(tmpName)
	}()

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
	if err := os.Rename(tmpName, s.tunnelsPath); err != nil {
		return fmt.Errorf("rename %s: %w", s.tunnelsPath, err)
	}
	return nil
}
