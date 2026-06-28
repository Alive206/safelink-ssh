package sshclient

import (
	"context"
	"fmt"
	"time"

	"golang.org/x/crypto/ssh"
)

// runKeepAlive periodically sends a global keepalive request to the server.
func runKeepAlive(ctx context.Context, c *ssh.Client, interval time.Duration, maxFails int) error {
	if interval <= 0 {
		<-ctx.Done()
		return ctx.Err()
	}
	t := time.NewTicker(interval)
	defer t.Stop()

	fails := 0
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-t.C:
			_, _, err := c.SendRequest("keepalive@openssh.com", true, nil)
			if err != nil {
				fails++
				if fails >= maxFails {
					return fmt.Errorf("keepalive lost after %d attempts: %w", fails, err)
				}
				continue
			}
			fails = 0
		}
	}
}
