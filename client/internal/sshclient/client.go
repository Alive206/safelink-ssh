// Package sshclient establishes and supervises a long-lived SSH connection
// used to drive one tunnel.
package sshclient

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math/rand"
	"net"
	"sync"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/example/safelink/pkg/config"
)

// Tunnel is the minimal contract a tunnel implementation must satisfy.
type Tunnel interface {
	Name() string
	Serve(ctx context.Context, c *ssh.Client) error
}

// State enumerates the high-level states a Supervisor reports.
type State string

const (
	StateConnecting   State = "connecting"
	StateRunning      State = "running"
	StateReconnecting State = "reconnecting"
	StateStopped      State = "stopped"
)

// Supervisor wraps a single tunnel together with the SSH connection.
type Supervisor struct {
	tunnelCfg       config.TunnelCfg
	defaults        config.ConnDefaults
	knownHosts      string
	insecureHostKey bool
	tunnel          Tunnel
	log             *slog.Logger

	OnStateChange func(state State, lastErr error)
}

// NewSupervisor wires up a Supervisor.
func NewSupervisor(tc config.TunnelCfg, defaults config.ConnDefaults, knownHosts string, insecureHostKey bool, tunnel Tunnel, log *slog.Logger) *Supervisor {
	return &Supervisor{
		tunnelCfg:       tc,
		defaults:        defaults,
		knownHosts:      knownHosts,
		insecureHostKey: insecureHostKey,
		tunnel:          tunnel,
		log:             log.With("tunnel", tc.Name),
	}
}

func (s *Supervisor) emit(state State, err error) {
	if s.OnStateChange != nil {
		s.OnStateChange(state, err)
	}
}

// Run drives the dial → serve → reconnect loop until ctx is cancelled.
func (s *Supervisor) Run(ctx context.Context) {
	defer s.emit(StateStopped, nil)
	backoff := s.defaults.ReconnectInitial
	for ctx.Err() == nil {
		s.emit(StateConnecting, nil)
		client, err := s.dial(ctx)
		if err != nil {
			s.log.Warn("ssh dial failed", "err", err, "retry_in", roundDur(applyJitter(backoff)))
			s.emit(StateReconnecting, err)
			s.sleepBackoff(ctx, &backoff)
			continue
		}

		s.log.Info("ssh connected", "addr", s.tunnelCfg.SSH.Addr)
		s.emit(StateRunning, nil)
		started := time.Now()
		s.serve(ctx, client)
		_ = client.Close()
		s.log.Info("ssh disconnected", "uptime", time.Since(started).Round(time.Second))

		if time.Since(started) > 60*time.Second {
			backoff = s.defaults.ReconnectInitial
		}
		if ctx.Err() != nil {
			return
		}
		s.emit(StateReconnecting, nil)
		s.sleepBackoff(ctx, &backoff)
	}
}

func (s *Supervisor) serve(ctx context.Context, client *ssh.Client) {
	subCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	var wg sync.WaitGroup
	wg.Add(2)

	go func() {
		defer wg.Done()
		err := runKeepAlive(subCtx, client, s.defaults.KeepAliveInterval, s.defaults.KeepAliveMaxFails)
		if err != nil && !errors.Is(err, context.Canceled) {
			s.log.Warn("keepalive ended", "err", err)
		}
		cancel()
	}()

	go func() {
		defer wg.Done()
		err := s.tunnel.Serve(subCtx, client)
		if err != nil && !errors.Is(err, context.Canceled) {
			s.log.Warn("tunnel ended", "err", err)
		}
		cancel()
	}()

	wg.Wait()
}

func (s *Supervisor) dial(ctx context.Context) (*ssh.Client, error) {
	authMethods, err := BuildAuthMethods(s.tunnelCfg.SSH)
	if err != nil {
		return nil, err
	}

	var hostKeyCB ssh.HostKeyCallback
	if s.insecureHostKey {
		hostKeyCB = ssh.InsecureIgnoreHostKey()
	} else {
		hostKeyCB, err = newHostKeyCallback(s.knownHosts)
		if err != nil {
			return nil, fmt.Errorf("host key callback: %w", err)
		}
	}

	cfg := &ssh.ClientConfig{
		User:            s.tunnelCfg.SSH.User,
		Auth:            authMethods,
		HostKeyCallback: hostKeyCB,
		Timeout:         s.defaults.DialTimeout,
	}

	d := net.Dialer{Timeout: s.defaults.DialTimeout}
	tcpConn, err := d.DialContext(ctx, "tcp", s.tunnelCfg.SSH.Addr)
	if err != nil {
		return nil, fmt.Errorf("tcp dial: %w", err)
	}
	sshConn, chans, reqs, err := ssh.NewClientConn(tcpConn, s.tunnelCfg.SSH.Addr, cfg)
	if err != nil {
		_ = tcpConn.Close()
		return nil, fmt.Errorf("ssh handshake: %w", err)
	}
	return ssh.NewClient(sshConn, chans, reqs), nil
}

func (s *Supervisor) sleepBackoff(ctx context.Context, d *time.Duration) {
	wait := applyJitter(*d)
	select {
	case <-time.After(wait):
	case <-ctx.Done():
		return
	}
	*d *= 2
	if *d > s.defaults.ReconnectMax {
		*d = s.defaults.ReconnectMax
	}
}

func applyJitter(d time.Duration) time.Duration {
	factor := 0.8 + rand.Float64()*0.4
	return time.Duration(float64(d) * factor)
}

func roundDur(d time.Duration) time.Duration { return d.Round(100 * time.Millisecond) }
