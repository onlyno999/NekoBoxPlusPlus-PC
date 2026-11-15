//go:build !windows

package core

// SetSystemProxy is a no-op outside Windows (development
// builds); the shipped build is Windows-only.
func SetSystemProxy(port int) error { return nil }

// ClearSystemProxy is a no-op outside Windows.
func ClearSystemProxy() error { return nil }
