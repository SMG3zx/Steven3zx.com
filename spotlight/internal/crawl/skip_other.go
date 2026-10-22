//go:build !windows

package crawl

import "io/fs"

// Skip has no platform-specific placeholders to detect on this OS.
func Skip(info fs.FileInfo) bool { return false }
