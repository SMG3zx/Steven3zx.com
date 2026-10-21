package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// settings is what enroll records for the Agent to run with. It holds no
// secret: the pre-auth key is used once and is not kept; the node's own key
// lives in the tailnet directory.
type settings struct {
	AgentID    string `json:"agent_id"`
	Name       string `json:"name"`
	Server     string `json:"server"`
	ControlURL string `json:"control_url"`
	Tag        string `json:"tag"`
	Folder     string `json:"folder"`
}

func settingsPath(state string) string   { return filepath.Join(state, "agent.json") }
func tailnetDir(state string) string     { return filepath.Join(state, "tailnet") }
func deploymentsDir(state string) string { return filepath.Join(state, "local") }

func loadSettings(state string) (settings, error) {
	data, err := os.ReadFile(settingsPath(state))
	if errors.Is(err, os.ErrNotExist) {
		return settings{}, fmt.Errorf("this Agent is not enrolled (no %s); run janus-agent enroll first", settingsPath(state))
	}
	if err != nil {
		return settings{}, err
	}
	var s settings
	if err = json.Unmarshal(data, &s); err != nil {
		return settings{}, fmt.Errorf("read %s: %w", settingsPath(state), err)
	}
	return s, nil
}

func saveSettings(state string, s settings) error {
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	if err = os.MkdirAll(state, 0o700); err != nil {
		return err
	}
	return os.WriteFile(settingsPath(state), append(data, '\n'), 0o600)
}

// hostFolder checks that folder is an existing directory and returns its
// absolute path, which is what modules are confined to.
func hostFolder(folder string) (string, error) {
	if folder == "" {
		return "", errors.New("choose the host folder Deployments may use with -folder")
	}
	abs, err := filepath.Abs(folder)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(abs)
	if err != nil {
		return "", fmt.Errorf("the host folder: %w", err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("the host folder %s is not a directory", abs)
	}
	return abs, nil
}
