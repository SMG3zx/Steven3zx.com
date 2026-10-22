// Package crawl decides which files are worth indexing and walks the
// configured roots to find them. All of the rules (built-in ignores, secret
// file names, .gitignore files) live in Matcher so that the full scan, the file
// watcher and single-file re-indexing agree on what is eligible.
package crawl

import "io/fs"

// Func receives each candidate file. Returning an error stops the walk.
type Func func(path string, info fs.FileInfo) error
