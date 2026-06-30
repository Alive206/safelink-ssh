// SafeLink Client - Windows desktop VPN/SSH tunnel application built with Wails.
package main

import (
	"embed"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	"github.com/wailsapp/wails/v2/pkg/options/windows"
	wailsruntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

//go:embed all:frontend/dist
var assets embed.FS

//go:embed build/windows/icon.ico
var trayIcon []byte

func main() {
	app := NewApp()
	stopSignals := watchProcessSignals(app)
	defer stopSignals()
	defer app.closeProxyForExit()

	minimizeToTray := app.launch.SSHConnectionID == ""
	if minimizeToTray {
		app.registerTray()
	}

	err := wails.Run(&options.App{
		Title:             "SafeLink",
		Width:             960,
		Height:            640,
		MinWidth:          800,
		MinHeight:         500,
		HideWindowOnClose: minimizeToTray,
		AssetServer: &assetserver.Options{
			Assets: assets,
		},
		BackgroundColour: &options.RGBA{R: 238, G: 248, B: 251, A: 1},
		OnStartup:        app.startup,
		OnShutdown:       app.shutdown,
		Bind: []interface{}{
			app,
		},
		Windows: &windows.Options{
			WebviewIsTransparent: false,
			WindowIsTranslucent:  false,
			DisableWindowIcon:    true,
		},
	})

	if err != nil {
		println("Error:", err.Error())
	}
}

func watchProcessSignals(app *App) func() {
	signals := make(chan os.Signal, 1)
	done := make(chan struct{})
	signal.Notify(signals, os.Interrupt, syscall.SIGTERM)

	go func() {
		select {
		case <-signals:
			app.closeProxyForExit()
			if app.ctx != nil {
				wailsruntime.Quit(app.ctx)
				time.AfterFunc(5*time.Second, func() {
					os.Exit(0)
				})
				return
			}
			os.Exit(0)
		case <-done:
			return
		}
	}()

	return func() {
		signal.Stop(signals)
		close(done)
	}
}
