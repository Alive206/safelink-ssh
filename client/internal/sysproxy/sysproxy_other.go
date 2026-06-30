//go:build !windows

package sysproxy

import "runtime"

func SetSystemProxy(bool, string, string) error {
	return unsupported()
}

func SetAutoStart(bool) error {
	return unsupported()
}

func unsupported() error {
	return &UnsupportedError{OS: runtime.GOOS}
}

type UnsupportedError struct {
	OS string
}

func (e *UnsupportedError) Error() string {
	return "system proxy settings are not supported on " + e.OS
}
