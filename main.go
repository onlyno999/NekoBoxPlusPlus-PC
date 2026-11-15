package main

import (
	"embed"
	"fmt"
	"os"
	"path/filepath"
	"runtime/debug"
	"time"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	"github.com/wailsapp/wails/v2/pkg/options/windows"

	"nekoboxpp-pc/internal/core"
)

//go:embed all:frontend/dist
var assets embed.FS

// bootlog appends one line to startup.log next to the exe.
// A portable app that crashes before its window appears
// leaves no other trace, so every startup step is logged
// and any panic is captured with its stack.
func bootlog(format string, args ...any) {
	exe, err := os.Executable()
	if err != nil {
		return
	}
	f, err := os.OpenFile(filepath.Join(filepath.Dir(exe), "startup.log"),
		os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	fmt.Fprintf(f, "%s %s\n", time.Now().Format("15:04:05.000"),
		fmt.Sprintf(format, args...))
}

func main() {
	defer func() {
		if r := recover(); r != nil {
			bootlog("PANIC: %v\n%s", r, debug.Stack())
		}
	}()
	bootlog("=== main start ===")
	core.EnsureWintun()
	bootlog("wintun ensured")
	app := NewApp()
	bootlog("app created")
	err := wails.Run(&options.App{
		Title:     "NekoBox++",
		Width:     1280,
		Height:    820,
		MinWidth:  980,
		MinHeight: 640,
		Frameless: true,
		AssetServer: &assetserver.Options{
			Assets: assets,
		},
		OnStartup:  app.startup,
		OnShutdown: app.shutdown,
		Bind: []any{
			app,
		},
		Windows: &windows.Options{
			WebviewIsTransparent: false,
			WindowIsTranslucent:  false,
		},
	})
	bootlog("wails.Run returned: %v", err)
}
