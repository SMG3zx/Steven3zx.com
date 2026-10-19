package builder

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// packageMain returns the main field of package.json in workspace, or "".
func packageMain(workspace string) string {
	data, err := os.ReadFile(filepath.Join(workspace, "package.json"))
	if err != nil {
		return ""
	}
	var manifest struct {
		Main string `json:"main"`
	}
	if json.Unmarshal(data, &manifest) != nil {
		return ""
	}
	return manifest.Main
}
