// Package config loads the indexer's JSON configuration.
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

type Config struct {
	Roots         []string `json:"roots"`
	IgnoreDirs    []string `json:"ignore_dirs"`
	MaxFileBytes  int64    `json:"max_file_bytes"`
	DBPath        string   `json:"db_path"`
	ListenAddr    string   `json:"listen_addr"`
	OllamaURL     string   `json:"ollama_url"`
	EmbedModel    string   `json:"embed_model"`
	ChunkSize     int      `json:"chunk_size"`
	ChunkOverlap  int      `json:"chunk_overlap"`
	Watch         bool     `json:"watch"`
	RescanMinutes int      `json:"rescan_minutes"`
}

// Load reads path and fills unset fields with defaults.
func Load(path string) (Config, error) {
	c := Config{
		IgnoreDirs: []string{
			".git", "node_modules", "target", "dist", "build", ".venv",
			"__pycache__", "$RECYCLE.BIN", "System Volume Information",
		},
		MaxFileBytes:  2 << 20,
		ListenAddr:    "127.0.0.1:7788",
		OllamaURL:     "http://127.0.0.1:11434",
		EmbedModel:    "nomic-embed-text",
		ChunkSize:     1200,
		ChunkOverlap:  200,
		Watch:         true,
		RescanMinutes: 30,
	}

	b, err := os.ReadFile(path)
	if err != nil {
		return c, fmt.Errorf("read config: %w", err)
	}
	if err := json.Unmarshal(b, &c); err != nil {
		return c, fmt.Errorf("parse config %s: %w", path, err)
	}

	if c.DBPath == "" {
		dir, err := os.UserCacheDir()
		if err != nil {
			return c, err
		}
		c.DBPath = filepath.Join(dir, "spotlight", "index.db")
	}
	if len(c.Roots) == 0 {
		return c, errors.New("config: no roots configured")
	}
	return c, nil
}
