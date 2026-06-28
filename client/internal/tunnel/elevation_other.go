//go:build !windows

package tunnel

import "errors"

// IsRunningAsAdmin always returns true on non-Windows platforms.
func IsRunningAsAdmin() bool {
	return true
}

// RequestAdminRestart is only supported on Windows.
func RequestAdminRestart() error {
	return errors.New("admin restart only supported on Windows")
}

// IsTUNAccessDenied always returns false on non-Windows platforms.
func IsTUNAccessDenied(err error) bool {
	return false
}
