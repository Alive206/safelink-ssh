package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// applyDefaultsAndExpand fills zero-valued duration fields and expands ~ in
// any path-like field.
func (c *Config) applyDefaultsAndExpand() error {
	// Role default and validation.
	if c.Role == "" {
		c.Role = RoleStandalone
	}
	switch c.Role {
	case RoleServer, RoleClient, RoleStandalone:
		// valid
	default:
		return fmt.Errorf("invalid role %q: must be server, client, or standalone", c.Role)
	}

	if c.LogLevel == "" {
		c.LogLevel = "info"
	}
	d := &c.Defaults
	if d.KeepAliveInterval == 0 {
		d.KeepAliveInterval = 30 * time.Second
	}
	if d.KeepAliveMaxFails == 0 {
		d.KeepAliveMaxFails = 3
	}
	if d.DialTimeout == 0 {
		d.DialTimeout = 10 * time.Second
	}
	if d.ReconnectInitial == 0 {
		d.ReconnectInitial = 1 * time.Second
	}
	if d.ReconnectMax == 0 {
		d.ReconnectMax = 60 * time.Second
	}

	if c.KnownHosts == "" {
		if c.InsecureHostKey {
			// known_hosts not needed when host key verification is disabled.
		} else {
			home, err := os.UserHomeDir()
			if err != nil {
				return fmt.Errorf("resolve home dir: %w", err)
			}
			c.KnownHosts = filepath.Join(home, ".ssh", "known_hosts")
		}
	} else {
		expanded, err := expandHome(c.KnownHosts)
		if err != nil {
			return err
		}
		c.KnownHosts = expanded
	}

	for i := range c.Tunnels {
		if c.Tunnels[i].SSH.IdentityFile != "" {
			expanded, err := expandHome(c.Tunnels[i].SSH.IdentityFile)
			if err != nil {
				return err
			}
			c.Tunnels[i].SSH.IdentityFile = expanded
		}
		if c.Tunnels[i].Tun.TLSCert != "" {
			expanded, err := expandHome(c.Tunnels[i].Tun.TLSCert)
			if err != nil {
				return err
			}
			c.Tunnels[i].Tun.TLSCert = expanded
		}
		if c.Tunnels[i].Tun.TLSKey != "" {
			expanded, err := expandHome(c.Tunnels[i].Tun.TLSKey)
			if err != nil {
				return err
			}
			c.Tunnels[i].Tun.TLSKey = expanded
		}
	}
	return nil
}

// expandHome resolves a leading ~ to the current user's home directory.
// It is a no-op for absolute or relative paths that do not begin with ~.
func expandHome(p string) (string, error) {
	if p == "" || (p[0] != '~') {
		return p, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home dir for %q: %w", p, err)
	}
	if p == "~" {
		return home, nil
	}
	if strings.HasPrefix(p, "~/") || strings.HasPrefix(p, `~\`) {
		return filepath.Join(home, p[2:]), nil
	}
	return p, nil
}

// ExpandHome is the exported version used by the store layer when normalising
// paths supplied by the web UI.
func ExpandHome(p string) (string, error) { return expandHome(p) }

// validate checks that every tunnel has the fields required for its mode and
// at least one auth method configured.  An empty tunnel list is allowed: the
// store layer may load tunnels from a separate JSON file.
func (c *Config) validate() error {
	seen := make(map[string]bool, len(c.Tunnels))
	for i, t := range c.Tunnels {
		if t.Name == "" {
			return fmt.Errorf("tunnel[%d]: name is required", i)
		}
		if seen[t.Name] {
			return fmt.Errorf("tunnel[%d] (%q): duplicate tunnel name", i, t.Name)
		}
		seen[t.Name] = true
		if err := validateTunnel(t); err != nil {
			return fmt.Errorf("tunnel[%d] (%q): %w", i, t.Name, err)
		}
	}
	return nil
}

// validateTunnel performs structural validation of a single tunnel.  It is
// shared between YAML load and the HTTP handlers that mutate tunnels.json.
func validateTunnel(t TunnelCfg) error {
	if t.Name == "" {
		return errors.New("name is required")
	}
	switch t.Mode {
	case ModeLocal, ModeRemote:
		if t.Listen == "" || t.Forward == "" {
			return fmt.Errorf("%s mode requires listen and forward", t.Mode)
		}
	case ModeDynamic:
		if t.Listen == "" {
			return errors.New("dynamic mode requires listen")
		}
	case ModeVPN:
		if t.Forward == "" {
			return errors.New("vpn mode requires forward (remote server address)")
		}
		if t.SSH.User == "" {
			return errors.New("vpn mode requires ssh.user (auth username)")
		}
		if t.SSH.Password == "" {
			return errors.New("vpn mode requires ssh.password (auth secret)")
		}
		return nil // VPN doesn't require SSH addr/identity
	default:
		return fmt.Errorf("unknown mode %q (want local|remote|dynamic|vpn)", t.Mode)
	}
	if t.SSH.Addr == "" || t.SSH.User == "" {
		return errors.New("ssh.addr and ssh.user are required")
	}
	if t.SSH.IdentityFile == "" && t.SSH.Password == "" {
		return errors.New("ssh.identity_file or ssh.password must be set")
	}
	return nil
}
