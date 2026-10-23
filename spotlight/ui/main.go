// Command spotlight-ui is the launcher window for the spotlight index: press
// the hotkey, type, hit Enter. It talks to the indexd daemon over localhost.
package main

import (
	"embed"
	"flag"
	"io/fs"
	"log"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	"github.com/wailsapp/wails/v2/pkg/options/windows"

	"spotlight-ui/internal/uiconfig"
)

//go:embed all:frontend
var frontend embed.FS

func main() {
	cfgPath := flag.String("config", "", "path to ui.json (default: next to the executable, then the user config dir)")
	flag.Parse()

	cfg, err := uiconfig.Load(*cfgPath)
	if err != nil {
		log.Fatal(err)
	}
	assets, err := fs.Sub(frontend, "frontend")
	if err != nil {
		log.Fatal(err)
	}
	app := NewApp(cfg)

	err = wails.Run(&options.App{
		Title:             windowTitle,
		Width:             720,
		Height:            470,
		DisableResize:     true,
		Frameless:         true,
		AlwaysOnTop:       true,
		StartHidden:       true,
		HideWindowOnClose: true,
		BackgroundColour:  &options.RGBA{R: 24, G: 24, B: 27, A: 255},
		AssetServer:       &assetserver.Options{Assets: assets},
		OnStartup:         app.startup,
		OnShutdown:        app.shutdown,
		Bind:              []interface{}{app},
		// Launching the program again (e.g. from a desktop shortcut on Linux)
		// toggles the window instead of starting a second copy.
		SingleInstanceLock: &options.SingleInstanceLock{
			UniqueId:               "com.steven3zx.spotlight-ui",
			OnSecondInstanceLaunch: func(options.SecondInstanceData) { app.Toggle() },
		},
		Windows: &windows.Options{
			DisableWindowIcon: true,
		},
	})
	if err != nil {
		log.Fatal(err)
	}
}
