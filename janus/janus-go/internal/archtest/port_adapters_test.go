package archtest

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

var (
	portDecl      = regexp.MustCompile(`(?m)^type (\w+) interface`)
	adapterAssert = regexp.MustCompile(`_ app\.(\w+) =`)
	minimumPorts  = 9
)

// TestEveryPortHasAnAdapterThatSaysSo reads the ports declared in
// internal/app/ports.go and fails when no adapter asserts it. Go satisfies
// interfaces implicitly, so the assertion is what makes the link visible to a
// reader: `var _ app.Store = (*sqlstore.Store)(nil)` names the adapter.
func TestEveryPortHasAnAdapterThatSaysSo(t *testing.T) {
	ports, err := os.ReadFile(filepath.Join("..", "app", "ports.go"))
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, match := range portDecl.FindAllStringSubmatch(string(ports), -1) {
		names = append(names, match[1])
	}
	if len(names) < minimumPorts {
		t.Fatalf("found %d ports in ports.go, expected at least %d; the pattern may have stopped matching", len(names), minimumPorts)
	}

	asserted := map[string]bool{}
	err = filepath.WalkDir(filepath.Join("..", "adapters"), func(path string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return err
		}
		source, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for _, match := range adapterAssert.FindAllStringSubmatch(string(source), -1) {
			asserted[match[1]] = true
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range names {
		if !asserted[name] {
			t.Errorf("port app.%s has no adapter with `var _ app.%s = (*Adapter)(nil)`", name, name)
		}
	}
}
