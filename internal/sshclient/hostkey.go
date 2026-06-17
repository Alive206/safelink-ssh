package sshclient

import (
	"fmt"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"
)

// newHostKeyCallback returns a strict known_hosts callback.  The file must
// already exist and contain the server's fingerprint; we deliberately do not
// expose an "insecure ignore" option.  Operators must seed known_hosts with
// `ssh-keyscan` before first use.
func newHostKeyCallback(knownHostsPath string) (ssh.HostKeyCallback, error) {
	cb, err := knownhosts.New(knownHostsPath)
	if err != nil {
		return nil, fmt.Errorf("load known_hosts %q: %w", knownHostsPath, err)
	}
	return cb, nil
}

// insecureHostKeyCallback returns a callback that accepts any host key.
// This is intentionally unsafe and should only be used in trusted networks
// or development environments where known_hosts management is impractical.
func insecureHostKeyCallback() ssh.HostKeyCallback {
	return ssh.InsecureIgnoreHostKey()
}
