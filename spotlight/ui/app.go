package main

import (
	"context"
	"errors"
	"log"
	"os"
	"sync"

	"github.com/wailsapp/wails/v2/pkg/runtime"

	"spotlight-ui/internal/client"
	"spotlight-ui/internal/hotkey"
	"spotlight-ui/internal/uiconfig"
)

const windowTitle = "Spotlight"

// App holds the launcher window state. Its exported methods are callable from
// the frontend as window.go.main.App.<Name>.
type App struct {
	cfg uiconfig.Config
	api *client.Client

	ctx     context.Context
	mu      sync.Mutex
	visible bool
	notice  string // startup problem to show in the footer
	stop    func()
}

func NewApp(cfg uiconfig.Config) *App {
	return &App{cfg: cfg, api: client.New(cfg.DaemonURL)}
}

func (a *App) startup(ctx context.Context) {
	a.ctx = ctx
	combo, err := hotkey.Parse(a.cfg.Hotkey)
	if err != nil {
		log.Printf("hotkey disabled: %v", err)
		a.Notice("Invalid hotkey: " + err.Error())
		a.Show()
		return
	}
	stop, err := hotkey.Listen(combo, a.Toggle)
	if err != nil {
		log.Printf("hotkey disabled: %v", err)
		a.Notice(err.Error())
		a.Show() // without a hotkey the user needs some way to reach the window
		return
	}
	a.stop = stop
	log.Printf("listening for %s", a.cfg.Hotkey)
}

func (a *App) shutdown(context.Context) {
	if a.stop != nil {
		a.stop()
	}
}

// Notice records a message for the launcher footer. The frontend reads it
// from Config, so it survives the page loading after startup.
func (a *App) Notice(msg string) { a.notice = msg }

// Toggle shows the launcher, or hides it if it is already up.
func (a *App) Toggle() {
	a.mu.Lock()
	v := a.visible
	a.mu.Unlock()
	if v {
		a.Hide()
	} else {
		a.Show()
	}
}

func (a *App) Show() {
	if a.ctx == nil {
		return
	}
	a.mu.Lock()
	a.visible = true
	a.mu.Unlock()
	runtime.WindowCenter(a.ctx)
	runtime.WindowShow(a.ctx)
	focusWindow(windowTitle)
	runtime.EventsEmit(a.ctx, "shown")
}

func (a *App) Hide() {
	if a.ctx == nil {
		return
	}
	a.mu.Lock()
	a.visible = false
	a.mu.Unlock()
	runtime.WindowHide(a.ctx)
}

// Config tells the frontend how to behave.
func (a *App) Config() map[string]any {
	return map[string]any{"hotkey": a.cfg.Hotkey, "hideOnBlur": a.cfg.HideOnBlur, "notice": a.notice}
}

func (a *App) Search(query string) ([]client.Result, error) {
	if query == "" {
		return nil, nil
	}
	return a.api.Search(a.ctx, query, a.cfg.MaxResults)
}

func (a *App) Status() (client.Status, error) { return a.api.Status(a.ctx) }

// Open launches the file in its default app and hides the launcher.
func (a *App) Open(path string) error {
	if err := checkFile(path); err != nil {
		return err
	}
	a.Hide()
	return openPath(path)
}

// Reveal shows the file in the file manager and hides the launcher.
func (a *App) Reveal(path string) error {
	if err := checkFile(path); err != nil {
		return err
	}
	a.Hide()
	return revealPath(path)
}

// checkFile makes sure the path came from the index and still exists, rather
// than trusting the frontend to hand over an arbitrary command target.
func checkFile(path string) error {
	fi, err := os.Stat(path)
	if err != nil {
		return err
	}
	if !fi.Mode().IsRegular() {
		return errors.New("not a regular file")
	}
	return nil
}
