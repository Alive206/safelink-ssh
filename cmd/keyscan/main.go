// keyscan is a tiny helper used to seed known_hosts on Windows where the
// bundled OpenSSH ssh-keyscan is too old to negotiate post-quantum KEX with
// modern sshd.  Usage:
//
//	go run ./cmd/keyscan host[:port] [host2 ...]
//
// It connects with an InsecureIgnoreHostKey callback for the *sole* purpose
// of capturing whichever host key the server presents, prints it as a
// known_hosts line, and exits.  No data is exchanged besides the SSH
// handshake — no auth attempt is made.
package main

import (
	"fmt"
	"net"
	"os"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: keyscan host[:port] [host2 ...]")
		os.Exit(2)
	}
	exit := 0
	for _, h := range os.Args[1:] {
		if err := scan(h); err != nil {
			fmt.Fprintf(os.Stderr, "keyscan %s: %v\n", h, err)
			exit = 1
		}
	}
	os.Exit(exit)
}

func scan(target string) error {
	host, port, err := net.SplitHostPort(target)
	if err != nil {
		host = target
		port = "22"
	}
	addr := net.JoinHostPort(host, port)
	hostField := host
	if port != "22" {
		hostField = "[" + host + "]:" + port
	}

	// Try every common host-key type so the resulting known_hosts entries
	// cover whichever algorithm the daemon actually negotiates with sshd.
	algos := []string{
		ssh.KeyAlgoED25519,
		ssh.KeyAlgoECDSA256, ssh.KeyAlgoECDSA384, ssh.KeyAlgoECDSA521,
		ssh.KeyAlgoRSA,
	}
	seen := map[string]bool{}
	found := 0
	for _, algo := range algos {
		var captured ssh.PublicKey
		cfg := &ssh.ClientConfig{
			User:              "keyscan",
			Auth:              nil,
			HostKeyAlgorithms: []string{algo},
			HostKeyCallback: func(_ string, _ net.Addr, key ssh.PublicKey) error {
				captured = key
				return fmt.Errorf("captured")
			},
			Timeout: 10 * time.Second,
		}
		_, _ = ssh.Dial("tcp", addr, cfg)
		if captured == nil {
			continue
		}
		line := strings.TrimSpace(string(ssh.MarshalAuthorizedKey(captured)))
		if seen[line] {
			continue
		}
		seen[line] = true
		fmt.Printf("%s %s\n", hostField, line)
		found++
	}
	if found == 0 {
		return fmt.Errorf("no host key returned (network error?)")
	}
	return nil
}
