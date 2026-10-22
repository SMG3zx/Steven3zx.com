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
	adapterAssert = regexp.MustCompile(`_ agent\.(\w+) =`)
	minimumPorts  = 2
)

// TestEveryPortHasAnAdapterThatSaysSo reads the ports the Agent declares in
// internal/domain/agent/ports.go and fails when no implementation asserts it. Go
// satisfies interfaces implicitly, so the assertion is what makes the link
// visible to a reader: `var _ agent.Runner = Wasmtime{}` names the adapter.
func TestEveryPortHasAnAdapterThatSaysSo(t *testing.T) {
	var names []string
	for _, file := range []string{filepath.Join("..", "domain", "agent", "ports.go")} {
		ports, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		for _, match := range portDecl.FindAllStringSubmatch(string(ports), -1) {
			names = append(names, match[1])
		}
	}
	if len(names) < minimumPorts {
		t.Fatalf("found %d ports in ports.go, expected at least %d; the pattern may have stopped matching", len(names), minimumPorts)
	}

	asserted := map[string]bool{}
	err := filepath.WalkDir(filepath.Join("..", "agent"), func(path string, entry fs.DirEntry, err error) error {
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
			t.Errorf("port agent.%s has no implementation with `var _ agent.%s = (*Adapter)(nil)`", name, name)
		}
	}
}
