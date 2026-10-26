package vm

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"time"
)

// record is what gets persisted for each VM. Runtime state is never persisted.
type record struct {
	ID        string    `json:"id"`
	CreatedAt time.Time `json:"createdAt"`
	Spec      Spec      `json:"spec"`
}

// loadRecords reads the VM list from path. A missing file means no VMs yet.
func loadRecords(path string) ([]record, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	var recs []record
	if err := json.Unmarshal(data, &recs); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	return recs, nil
}

// saveRecords writes the VM list to path atomically via a temp file and rename.
func saveRecords(path string, recs []record) error {
	data, err := json.MarshalIndent(recs, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
