//go:build windows

package core

import (
	"fmt"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

const inetSettings = `Software\Microsoft\Windows\CurrentVersion\Internet Settings`

var (
	wininet            = windows.NewLazySystemDLL("wininet.dll")
	procInternetSetOpt = wininet.NewProc("InternetSetOptionW")
)

func refreshWinInet() {
	// INTERNET_OPTION_SETTINGS_CHANGED = 39, REFRESH = 37
	procInternetSetOpt.Call(0, 39, 0, 0)
	procInternetSetOpt.Call(0, 37, 0, 0)
}

// SetSystemProxy points the Windows per-user proxy at the
// local mixed inbound.
func SetSystemProxy(port int) error {
	key, err := registry.OpenKey(registry.CURRENT_USER, inetSettings, registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer key.Close()
	if err := key.SetStringValue("ProxyServer", fmt.Sprintf("127.0.0.1:%d", port)); err != nil {
		return err
	}
	if err := key.SetDWordValue("ProxyEnable", 1); err != nil {
		return err
	}
	refreshWinInet()
	return nil
}

// ClearSystemProxy disables the per-user proxy.
func ClearSystemProxy() error {
	key, err := registry.OpenKey(registry.CURRENT_USER, inetSettings, registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer key.Close()
	if err := key.SetDWordValue("ProxyEnable", 0); err != nil {
		return err
	}
	refreshWinInet()
	return nil
}
