// Package uiconfig loads the launcher's small JSON config.
package uiconfig

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

type Config struct {
	// Hotkey is the global shortcut that shows and hides the launcher, e.g.
	// "win+space", "alt+space" or "ctrl+shift+f". Windows only; elsewhere bind
	// a desktop shortcut to the executable (a second launch toggles the window).
	Hotkey     string `json:"hotkey"`
	DaemonURL  string `json:"daemon_url"`  // where indexd listens
	MaxResults int    `json:"max_results"` // 1-100
	HideOnBlur bool   `json:"hide_on_blur"`
}

func Defaults() Config {
	return Config{
		Hotkey:     "win+space",
		DaemonURL:  "http://127.0.0.1:7788",
		MaxResults: 12,
		HideOnBlur: true,
	}
}

// Load reads path over the defaults. A missing file is not an error. An empty
// path searches next to the executable, then the user config directory.
func Load(path string) (Config, error) {
	c := Defaults()
	if path == "" {
		path = find()
		if path == "" {
			return c, nil
		}
	}
	b, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) && path == find() {
		return c, nil
	}
	if err != nil {
		return c, fmt.Errorf("read ui config: %w", err)
	}
	if err := json.Unmarshal(b, &c); err != nil {
		return c, fmt.Errorf("parse ui config %s: %w", path, err)
	}
	if c.MaxResults < 1 || c.MaxResults > 100 {
		return c, fmt.Errorf("ui config: max_results must be 1-100, got %d", c.MaxResults)
	}
	if c.DaemonURL == "" || c.Hotkey == "" {
		return c, errors.New("ui config: hotkey and daemon_url must not be empty")
	}
	return c, nil
}

func find() string {
	var cands []string
	if exe, err := os.Executable(); err == nil {
		cands = append(cands, filepath.Join(filepath.Dir(exe), "ui.json"))
	}
	if dir, err := os.UserConfigDir(); err == nil {
		cands = append(cands, filepath.Join(dir, "spotlight", "ui.json"))
	}
	for _, p := range cands {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return ""
}
