// Package crawl walks the configured roots and yields indexable files.
package crawl

import (
	"io/fs"
	"path/filepath"
	"strings"
)

// Func receives each candidate file. Returning an error stops the walk.
type Func func(path string, info fs.FileInfo) error

// Walk visits every regular file under roots, skipping ignored directory
// names, symlinks, and platform-specific placeholders (see Skip).
// Unreadable entries are skipped rather than aborting the walk.
func Walk(roots []string, ignore []string, fn Func) error {
	for _, root := range roots {
		err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil || d == nil {
				return nil
			}
			info, err := d.Info()
			if err != nil {
				return nil
			}
			if d.IsDir() {
				if path != root && (IgnoredName(d.Name(), ignore) || Skip(info)) {
					return fs.SkipDir
				}
				return nil
			}
			if d.Type()&fs.ModeSymlink != 0 || !info.Mode().IsRegular() || Skip(info) {
				return nil
			}
			return fn(path, info)
		})
		if err != nil {
			return err
		}
	}
	return nil
}

// IgnoredName reports whether a single path element matches an ignore entry.
func IgnoredName(name string, ignore []string) bool {
	for _, ig := range ignore {
		if strings.EqualFold(name, ig) {
			return true
		}
	}
	return false
}

// IgnoredPath reports whether any element of path is an ignored name.
func IgnoredPath(path string, ignore []string) bool {
	parts := strings.FieldsFunc(path, func(r rune) bool { return r == '/' || r == '\\' })
	for _, p := range parts {
		if IgnoredName(p, ignore) {
			return true
		}
	}
	return false
}
