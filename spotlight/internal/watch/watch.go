// Package watch reports file changes under the configured roots using fsnotify.
package watch

import (
	"context"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/fsnotify/fsnotify"

	"spotlight/internal/crawl"
)

const debounce = 500 * time.Millisecond

// Run watches roots recursively and calls onChange for each path that was
// created, written, renamed or removed. Events are debounced per path. onChange
// runs on the event loop, so it should return quickly or hand work off.
//
// fsnotify is not recursive on Windows, so every directory is added by hand,
// including directories created while running.
func Run(ctx context.Context, roots, ignore []string, onChange func(ctx context.Context, path string), log *slog.Logger) error {
	w, err := fsnotify.NewWatcher()
	if err != nil {
		return err
	}
	defer w.Close()

	for _, root := range roots {
		if _, err := addTree(w, root, ignore); err != nil {
			log.Warn("watch root", "root", root, "err", err)
		}
	}

	pending := make(map[string]time.Time)
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil

		case err, ok := <-w.Errors:
			if !ok {
				return nil
			}
			log.Warn("watch error", "err", err)

		case ev, ok := <-w.Events:
			if !ok {
				return nil
			}
			if ev.Op == fsnotify.Chmod || crawl.IgnoredPath(ev.Name, ignore) {
				continue
			}
			if ev.Has(fsnotify.Create) {
				if fi, err := os.Stat(ev.Name); err == nil && fi.IsDir() {
					files, err := addTree(w, ev.Name, ignore)
					if err != nil {
						log.Warn("watch new dir", "path", ev.Name, "err", err)
					}
					now := time.Now()
					for _, f := range files {
						pending[f] = now
					}
					continue
				}
			}
			pending[ev.Name] = time.Now()

		case now := <-ticker.C:
			for p, t := range pending {
				if now.Sub(t) < debounce {
					continue
				}
				delete(pending, p)
				onChange(ctx, p)
			}
		}
	}
}

// addTree adds a watch on root and every non-ignored subdirectory. It returns
// the regular files it found so the caller can index them.
func addTree(w *fsnotify.Watcher, root string, ignore []string) ([]string, error) {
	var files []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d == nil {
			return nil
		}
		if d.IsDir() {
			if path != root && crawl.IgnoredName(d.Name(), ignore) {
				return fs.SkipDir
			}
			_ = w.Add(path) // best effort; a failed watch just misses live updates
			return nil
		}
		if d.Type().IsRegular() {
			files = append(files, path)
		}
		return nil
	})
	return files, err
}
