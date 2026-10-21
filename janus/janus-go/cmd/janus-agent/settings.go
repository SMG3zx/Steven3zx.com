package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	contractfleet "janus/internal/contract/fleet"
)

// settings is what enroll records for the Agent to run with. It holds no
// secret: the pre-auth key is used once and is not kept; the node's own key
// lives in the tailnet directory, and the NATS credentials in their own files.
type settings struct {
	AgentID    string `json:"agent_id"`
	OwnerID    string `json:"owner_id"`
	Name       string `json:"name"`
	Server     string `json:"server"`
	ControlURL string `json:"control_url"`
	Tag        string `json:"tag"`
	Folder     string `json:"folder"`
	// Trust is the public half of the hub's NATS trust, which the Agent's own
	// server verifies its leaf against.
	Trust *contractfleet.AgentTrust `json:"trust,omitempty"`
}

// The files of the NATS credentials of an Agent: its leaf connection to the hub,
// and the credentials its own server's clients use. They are secrets.
const (
	leafCreds  = "leaf.creds"
	localCreds = "local.creds"
)

func settingsPath(state string) string    { return filepath.Join(state, "agent.json") }
func tailnetDir(state string) string      { return filepath.Join(state, "tailnet") }
func deploymentsDir(state string) string  { return filepath.Join(state, "local") }
func credsPath(state, name string) string { return filepath.Join(state, name) }

// saveCreds stores a credentials file of the Agent's state directory, readable by
// its owner only.
func saveCreds(state, name string, body []byte) error {
	if err := os.MkdirAll(state, 0o700); err != nil {
		return err
	}
	return os.WriteFile(credsPath(state, name), body, 0o600)
}

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
