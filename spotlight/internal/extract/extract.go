// Package extract turns file bytes into plain text. Only text-like formats are
// supported for now; PDF, DOCX and OCR handlers plug in here later.
package extract

import (
	"bytes"
	"errors"
	"path/filepath"
	"strings"
)

// ErrUnsupported means the file type or content can't be turned into text.
var ErrUnsupported = errors.New("unsupported file type")

var textExts = map[string]bool{
	".txt": true, ".md": true, ".markdown": true, ".rst": true, ".org": true,
	".go": true, ".ts": true, ".tsx": true, ".js": true, ".jsx": true,
	".py": true, ".rs": true, ".java": true, ".c": true, ".h": true,
	".cpp": true, ".hpp": true, ".cs": true, ".zig": true, ".sh": true,
	".ps1": true, ".sql": true, ".json": true, ".yaml": true, ".yml": true,
	".toml": true, ".ini": true, ".conf": true, ".xml": true, ".html": true,
	".css": true, ".csv": true, ".log": true,
}

// Text returns the plain text of data if path has a text extension and the
// content isn't binary.
func Text(path string, data []byte) (string, error) {
	if !textExts[strings.ToLower(filepath.Ext(path))] {
		return "", ErrUnsupported
	}
	if bytes.IndexByte(data, 0) >= 0 {
		return "", ErrUnsupported
	}
	return strings.ToValidUTF8(string(data), "�"), nil
}
