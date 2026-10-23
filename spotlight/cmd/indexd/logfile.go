package main

import (
	"os"
	"path/filepath"
)

const maxLogBytes = 10 << 20

// openLog opens path for appending, creating its directory. If the file is
// already over maxLogBytes it is moved to path+".1" first, replacing the
// previous one, so logs stay bounded across restarts.
func openLog(path string) (*os.File, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	if fi, err := os.Stat(path); err == nil && fi.Size() > maxLogBytes {
		_ = os.Remove(path + ".1")
		if err := os.Rename(path, path+".1"); err != nil {
			return nil, err
		}
	}
	return os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
}
