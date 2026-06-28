package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ValidateTunnel performs structural validation of a single tunnel config.
func ValidateTunnel(t TunnelCfg) error {
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
			return errors.New("vpn mode requires server address")
		}
		if t.SSH.User == "" {
			return errors.New("vpn mode requires auth username")
		}
		if t.SSH.Password == "" {
			return errors.New("vpn mode requires auth password or token")
		}
		return nil
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

// ExpandHome resolves a leading ~ to the current user's home directory.
func ExpandHome(p string) (string, error) {
	if p == "" || p[0] != '~' {
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
