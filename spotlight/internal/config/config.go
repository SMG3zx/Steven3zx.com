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

	// Indexing pauses while system-wide load is above these percentages, and
	// GPU work is paced so this process stays at or under MaxGPUPercent.
	// 0 disables a limit.
	MaxCPUPercent float64 `json:"max_cpu_percent"`
	MaxMemPercent float64 `json:"max_mem_percent"`
	MaxGPUPercent float64 `json:"max_gpu_percent"`
	EmbedBatch    int     `json:"embed_batch"` // chunks per Ollama request
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
		MaxCPUPercent: 80,
		MaxMemPercent: 80,
		MaxGPUPercent: 80,
		EmbedBatch:    8,
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
	for _, l := range []struct {
		name string
		v    float64
	}{
		{"max_cpu_percent", c.MaxCPUPercent},
		{"max_mem_percent", c.MaxMemPercent},
		{"max_gpu_percent", c.MaxGPUPercent},
	} {
		if l.v < 0 || l.v > 100 {
			return c, fmt.Errorf("config: %s must be between 0 and 100 (0 disables the limit), got %v", l.name, l.v)
		}
	}
	if c.EmbedBatch < 1 {
		c.EmbedBatch = 1
	}
	return c, nil
}
