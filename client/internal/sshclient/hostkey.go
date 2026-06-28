package sshclient

import (
	"fmt"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"
)

func newHostKeyCallback(knownHostsPath string) (ssh.HostKeyCallback, error) {
	cb, err := knownhosts.New(knownHostsPath)
	if err != nil {
		return nil, fmt.Errorf("load known_hosts %q: %w", knownHostsPath, err)
	}
	return cb, nil
}
