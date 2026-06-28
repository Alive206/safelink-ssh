package sshclient

import (
	"errors"
	"fmt"
	"os"

	"golang.org/x/crypto/ssh"

	"github.com/example/safelink/pkg/config"
)

// BuildAuthMethods produces the ordered list of ssh.AuthMethod.
func BuildAuthMethods(c config.SSHCfg) ([]ssh.AuthMethod, error) {
	var methods []ssh.AuthMethod

	if c.IdentityFile != "" {
		signer, err := loadSigner(c.IdentityFile, c.Passphrase)
		if err != nil {
			return nil, fmt.Errorf("load identity %q: %w", c.IdentityFile, err)
		}
		methods = append(methods, ssh.PublicKeys(signer))
	}
	if c.Password != "" {
		methods = append(methods, ssh.Password(c.Password))
	}
	if len(methods) == 0 {
		return nil, errors.New("no auth method: identity_file or password required")
	}
	return methods, nil
}

func loadSigner(path, passphrase string) (ssh.Signer, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read key file: %w", err)
	}
	if passphrase == "" {
		return ssh.ParsePrivateKey(raw)
	}
	return ssh.ParsePrivateKeyWithPassphrase(raw, []byte(passphrase))
}
