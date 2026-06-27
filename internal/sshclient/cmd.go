package sshclient

import (
	"bytes"
	"fmt"
	"io"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/example/sshtunneld/internal/config"
)

// DialSSH establishes a lightweight SSH connection to a remote host.
// Unlike Supervisor which manages a long-lived reconnect loop, this is
// a one-shot connection intended for running deployment commands.
func DialSSH(cfg config.SSHCfg, timeout time.Duration) (*ssh.Client, error) {
	authMethods, err := BuildAuthMethods(cfg)
	if err != nil {
		return nil, err
	}

	sshCfg := &ssh.ClientConfig{
		User:            cfg.User,
		Auth:            authMethods,
		HostKeyCallback: insecureHostKeyCallback(),
		Timeout:         timeout,
	}

	addr := cfg.Addr
	client, err := ssh.Dial("tcp", addr, sshCfg)
	if err != nil {
		return nil, fmt.Errorf("ssh dial %s: %w", addr, err)
	}
	return client, nil
}

// RunCommand executes a command on the remote host via SSH and returns
// the combined stdout+stderr output.
func RunCommand(client *ssh.Client, cmd string) (string, error) {
	session, err := client.NewSession()
	if err != nil {
		return "", fmt.Errorf("new session: %w", err)
	}
	defer session.Close()

	var buf bytes.Buffer
	session.Stdout = &buf
	session.Stderr = &buf

	if err := session.Run(cmd); err != nil {
		return buf.String(), fmt.Errorf("run %q: %w", cmd, err)
	}
	return buf.String(), nil
}

// RunCommandWithStdin runs a command with stdin data and returns output.
func RunCommandWithStdin(client *ssh.Client, cmd string, stdin io.Reader) (string, error) {
	session, err := client.NewSession()
	if err != nil {
		return "", fmt.Errorf("new session: %w", err)
	}
	defer session.Close()

	session.Stdin = stdin

	var buf bytes.Buffer
	session.Stdout = &buf
	session.Stderr = &buf

	if err := session.Run(cmd); err != nil {
		return buf.String(), fmt.Errorf("run %q: %w", cmd, err)
	}
	return buf.String(), nil
}

// UploadBytes uploads a byte slice to a remote file via SSH pipe.
// Writes to a temp file first, then renames atomically to avoid "text file busy"
// when replacing a running binary.
func UploadBytes(client *ssh.Client, data []byte, remotePath string, perm string) error {
	tmpPath := remotePath + ".upload"
	cmd := fmt.Sprintf("cat > %s && chmod %s %s && mv -f %s %s", tmpPath, perm, tmpPath, tmpPath, remotePath)
	session, err := client.NewSession()
	if err != nil {
		return fmt.Errorf("new session: %w", err)
	}
	defer session.Close()

	session.Stdin = bytes.NewReader(data)

	var buf bytes.Buffer
	session.Stdout = &buf
	session.Stderr = &buf

	if err := session.Run(cmd); err != nil {
		return fmt.Errorf("upload %s: %w\n%s", remotePath, err, buf.String())
	}
	return nil
}

// FileExists checks if a file exists on the remote host.
func FileExists(client *ssh.Client, path string) (bool, error) {
	out, err := RunCommand(client, "test -f "+path+" && echo yes || echo no")
	if err != nil {
		return false, err
	}
	return bytes.Contains(bytes.TrimSpace([]byte(out)), []byte("yes")), nil
}
