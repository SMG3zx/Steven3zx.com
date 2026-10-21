// Package manifest holds the rules for the manifest of a commit: the list of its
// files with their hashes. A Staging Snapshot keeps the manifest of the commit it
// was started from, so an interrupted import knows which files it still has to
// store and whether what it already stored belongs to the same commit. Decoding
// and storage are adapter concerns; comparing manifests is decided here.
package manifest

import "encoding/json"

// Entry describes one file of a commit.
type Entry struct {
	Path string `json:"path"`
	Mode string `json:"mode"`
	Hash string `json:"hash"`
	Size int64  `json:"size"`
}

// Parse decodes the manifest of a Staging Snapshot. An empty manifest means that
// the scan never finished, so ok is false.
func Parse(raw string) (entries []Entry, ok bool) {
	if raw == "" {
		return nil, false
	}
	if err := json.Unmarshal([]byte(raw), &entries); err != nil {
		return nil, false
	}
	return entries, true
}

// StoredMatch reports whether every stored file, given as path to hash, is listed
// in entries with the same hash. A Staging Snapshot that fails this check is
// corrupt or from a different commit, so it must not be reused.
func StoredMatch(stored map[string]string, entries []Entry) bool {
	hashes := hashesOf(entries)
	for path, sha := range stored {
		hash, ok := hashes[path]
		if !ok || hash != sha {
			return false
		}
	}
	return true
}

// Same reports whether a and b list the same paths with the same hashes. Modes
// and sizes are not compared.
func Same(a, b []Entry) bool {
	if len(a) != len(b) {
		return false
	}
	hashes := hashesOf(b)
	for _, entry := range a {
		hash, ok := hashes[entry.Path]
		if !ok || hash != entry.Hash {
			return false
		}
	}
	return true
}

func hashesOf(entries []Entry) map[string]string {
	hashes := make(map[string]string, len(entries))
	for _, entry := range entries {
		hashes[entry.Path] = entry.Hash
	}
	return hashes
}
