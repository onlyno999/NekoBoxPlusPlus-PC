//go:build windows

package core

import (
	_ "embed"
	"os"
	"path/filepath"
)

//go:embed wintun.dll
var wintunDLL []byte

// EnsureWintun writes the bundled wintun.dll next to the
// executable when it is missing or has a different size, so
// the single-file portable build can use TUN mode. Best
// effort: when the directory is read-only the TUN start
// reports its own error and system-proxy mode still works.
func EnsureWintun() {
	exe, err := os.Executable()
	if err != nil {
		return
	}
	dst := filepath.Join(filepath.Dir(exe), "wintun.dll")
	if cur, err := os.ReadFile(dst); err == nil && len(cur) == len(wintunDLL) {
		return
	}
	_ = os.WriteFile(dst, wintunDLL, 0o644)
}
