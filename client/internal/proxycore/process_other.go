//go:build !windows

package proxycore

import "os/exec"

func hideCommandWindow(cmd *exec.Cmd) {
}
