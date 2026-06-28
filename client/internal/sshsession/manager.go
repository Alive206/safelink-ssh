// Package sshsession manages interactive SSH PTY sessions for the desktop UI.
package sshsession

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"sync"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/example/safelink/client/internal/sshclient"
	"github.com/example/safelink/pkg/config"
)

type Config struct {
	Addr         string `json:"addr"`
	User         string `json:"user"`
	IdentityFile string `json:"identity_file"`
	Passphrase   string `json:"passphrase"`
	Password     string `json:"password"`
	Rows         int    `json:"rows"`
	Cols         int    `json:"cols"`
}

type OutputEvent struct {
	SessionID string `json:"session_id"`
	Data      string `json:"data"`
}

type ClosedEvent struct {
	SessionID string `json:"session_id"`
	Message   string `json:"message,omitempty"`
}

type ErrorEvent struct {
	SessionID string `json:"session_id"`
	Message   string `json:"message"`
}

type PTY interface {
	io.WriteCloser
	WindowChange(rows, cols int) error
	Wait() error
}

type Opener func(context.Context, Config) (PTY, io.Reader, error)

type Options struct {
	Open     Opener
	OnOutput func(OutputEvent)
	OnClosed func(ClosedEvent)
	OnError  func(ErrorEvent)
}

type Manager struct {
	mu       sync.RWMutex
	sessions map[string]*sessionRef
	open     Opener
	onOutput func(OutputEvent)
	onClosed func(ClosedEvent)
	onError  func(ErrorEvent)
}

type sessionRef struct {
	pty PTY
}

func NewManager(opts Options) *Manager {
	open := opts.Open
	if open == nil {
		open = OpenSSH
	}
	return &Manager{
		sessions: make(map[string]*sessionRef),
		open:     open,
		onOutput: opts.OnOutput,
		onClosed: opts.OnClosed,
		onError:  opts.OnError,
	}
}

func (m *Manager) Create(ctx context.Context, cfg Config) (string, error) {
	if cfg.Addr == "" {
		return "", errors.New("ssh addr is required")
	}
	if cfg.User == "" {
		return "", errors.New("ssh user is required")
	}
	pty, output, err := m.open(ctx, cfg)
	if err != nil {
		return "", err
	}

	id := randomID()
	m.mu.Lock()
	m.sessions[id] = &sessionRef{pty: pty}
	m.mu.Unlock()

	go m.copyOutput(id, output)
	go m.wait(id, pty)
	return id, nil
}

func (m *Manager) Write(id, data string) error {
	ref, err := m.get(id)
	if err != nil {
		return err
	}
	_, err = io.WriteString(ref.pty, data)
	return err
}

func (m *Manager) Resize(id string, rows, cols int) error {
	ref, err := m.get(id)
	if err != nil {
		return err
	}
	if rows <= 0 || cols <= 0 {
		return errors.New("terminal rows and cols must be positive")
	}
	return ref.pty.WindowChange(rows, cols)
}

func (m *Manager) Close(id string) error {
	ref, err := m.get(id)
	if err != nil {
		return err
	}
	return ref.pty.Close()
}

func (m *Manager) CloseAll() {
	m.mu.RLock()
	refs := make([]*sessionRef, 0, len(m.sessions))
	for _, ref := range m.sessions {
		refs = append(refs, ref)
	}
	m.mu.RUnlock()
	for _, ref := range refs {
		_ = ref.pty.Close()
	}
}

func (m *Manager) Active(id string) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	_, ok := m.sessions[id]
	return ok
}

func (m *Manager) get(id string) (*sessionRef, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	ref, ok := m.sessions[id]
	if !ok {
		return nil, fmt.Errorf("ssh session %q not found", id)
	}
	return ref, nil
}

func (m *Manager) copyOutput(id string, output io.Reader) {
	if output == nil {
		return
	}
	buf := make([]byte, 32*1024)
	for {
		n, err := output.Read(buf)
		if n > 0 && m.onOutput != nil {
			m.onOutput(OutputEvent{SessionID: id, Data: string(buf[:n])})
		}
		if err != nil {
			if !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrClosedPipe) && m.onError != nil {
				m.onError(ErrorEvent{SessionID: id, Message: err.Error()})
			}
			return
		}
	}
}

func (m *Manager) wait(id string, pty PTY) {
	err := pty.Wait()
	m.mu.Lock()
	delete(m.sessions, id)
	m.mu.Unlock()
	message := ""
	if err != nil && !errors.Is(err, io.EOF) {
		message = err.Error()
		if m.onError != nil {
			m.onError(ErrorEvent{SessionID: id, Message: message})
		}
	}
	if m.onClosed != nil {
		m.onClosed(ClosedEvent{SessionID: id, Message: message})
	}
}

func OpenSSH(ctx context.Context, cfg Config) (PTY, io.Reader, error) {
	rows, cols := cfg.Rows, cfg.Cols
	if rows <= 0 {
		rows = 24
	}
	if cols <= 0 {
		cols = 80
	}
	authMethods, err := sshclient.BuildAuthMethods(config.SSHCfg{
		Addr:         cfg.Addr,
		User:         cfg.User,
		IdentityFile: cfg.IdentityFile,
		Passphrase:   cfg.Passphrase,
		Password:     cfg.Password,
	})
	if err != nil {
		return nil, nil, err
	}
	clientCfg := &ssh.ClientConfig{
		User:            cfg.User,
		Auth:            authMethods,
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
		Timeout:         15 * time.Second,
	}

	type dialResult struct {
		client *ssh.Client
		err    error
	}
	dialDone := make(chan dialResult, 1)
	go func() {
		client, err := ssh.Dial("tcp", cfg.Addr, clientCfg)
		dialDone <- dialResult{client: client, err: err}
	}()

	var sshClient *ssh.Client
	select {
	case result := <-dialDone:
		if result.err != nil {
			return nil, nil, fmt.Errorf("ssh dial: %w", result.err)
		}
		sshClient = result.client
	case <-ctx.Done():
		return nil, nil, ctx.Err()
	}

	sess, err := sshClient.NewSession()
	if err != nil {
		_ = sshClient.Close()
		return nil, nil, fmt.Errorf("new ssh session: %w", err)
	}
	stdin, err := sess.StdinPipe()
	if err != nil {
		_ = sess.Close()
		_ = sshClient.Close()
		return nil, nil, fmt.Errorf("stdin pipe: %w", err)
	}
	stdout, err := sess.StdoutPipe()
	if err != nil {
		_ = sess.Close()
		_ = sshClient.Close()
		return nil, nil, fmt.Errorf("stdout pipe: %w", err)
	}
	if err := sess.RequestPty("xterm-256color", rows, cols, ssh.TerminalModes{
		ssh.ECHO:          1,
		ssh.TTY_OP_ISPEED: 14400,
		ssh.TTY_OP_OSPEED: 14400,
	}); err != nil {
		_ = sess.Close()
		_ = sshClient.Close()
		return nil, nil, fmt.Errorf("request pty: %w", err)
	}
	if err := sess.Shell(); err != nil {
		_ = sess.Close()
		_ = sshClient.Close()
		return nil, nil, fmt.Errorf("start shell: %w", err)
	}
	return &sshPTY{client: sshClient, session: sess, stdin: stdin}, stdout, nil
}

type sshPTY struct {
	client  *ssh.Client
	session *ssh.Session
	stdin   io.WriteCloser
}

func (p *sshPTY) Write(data []byte) (int, error) { return p.stdin.Write(data) }

func (p *sshPTY) WindowChange(rows, cols int) error {
	return p.session.WindowChange(rows, cols)
}

func (p *sshPTY) Wait() error {
	err := p.session.Wait()
	_ = p.client.Close()
	return err
}

func (p *sshPTY) Close() error {
	_ = p.stdin.Close()
	_ = p.session.Close()
	return p.client.Close()
}

func randomID() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(b[:])
}
