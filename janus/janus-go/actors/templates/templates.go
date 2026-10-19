package templates

import (
	"fmt"
	"slices"
	"strings"
	"time"
)

// Default resource limits for a WASM Run. Every template starts from these.
const (
	DefaultFuel uint64 = 100_000_000
	// ScriptFuel is the fuel limit for JavaScript and TypeScript templates. They
	// run inside a QuickJS interpreter, which spends about 770 fuel per simple
	// loop iteration, so the default would stop a modest script early.
	ScriptFuel         uint64 = 2_000_000_000
	DefaultMemoryBytes        = 128 << 20
	DefaultTimeout            = 10 * time.Second
)

// Resources are the limits a WASM Run of a template's Artifact starts with.
type Resources struct {
	Fuel        uint64
	MemoryBytes int64
	Timeout     time.Duration
}

// Template is one premade deployment template.
type Template struct {
	// ID is the stable key, such as "go" or "js-bun".
	ID   string
	Name string
	// Language is the Language a Scan records, such as "Go". Runtime narrows
	// JavaScript/TypeScript to "Node" or "Bun" and is empty for other Languages.
	Language string
	Runtime  string
	// Manifest is the file at the repository root that a build requires.
	Manifest string
	// Targets are the WASM Targets this template builds, the preferred one first.
	// It is empty when Buildable is false.
	Targets []string
	// BuildCommand describes the build the template runs, for display.
	BuildCommand string
	// Entrypoint describes how an Entrypoint candidate is found.
	Entrypoint string
	// Buildable reports whether a WASM builder exists for the template, which is
	// true exactly when it has Targets. An unbuildable template is still
	// detected and recorded.
	Buildable bool
	Resources Resources
}

var all = []Template{
	{
		ID: "go", Name: "Go", Language: "Go", Manifest: "go.mod",
		Targets:      []string{"wasip1-wasm"},
		BuildCommand: "GOOS=wasip1 GOARCH=wasm go build -trimpath -o main.wasm .",
		Entrypoint:   "a Go file with package main in a module",
	},
	{
		ID: "rust", Name: "Rust", Language: "Rust", Manifest: "Cargo.toml",
		Targets:      []string{"wasm32-wasip1", "wasm32-unknown-unknown"},
		BuildCommand: "cargo build --locked --offline --release --target <target>",
		Entrypoint:   "main.rs in a Cargo package",
	},
	{
		ID: "js-node", Name: "JavaScript/TypeScript on Node", Language: "JavaScript/TypeScript", Runtime: "Node", Manifest: "package.json",
		Targets:      []string{"wasm32-wasip1"},
		BuildCommand: "bun build <entry> --target=browser | javy build",
		Entrypoint:   "the Entrypoint the Account confirmed, else package.json main, else index.ts or index.js",
	},
	{
		ID: "js-bun", Name: "JavaScript/TypeScript on Bun", Language: "JavaScript/TypeScript", Runtime: "Bun", Manifest: "package.json",
		Targets:      []string{"wasm32-wasip1"},
		BuildCommand: "bun build <entry> --target=browser | javy build",
		Entrypoint:   "the Entrypoint the Account confirmed, else package.json main, else index.ts or index.js",
	},
}

func init() {
	for i := range all {
		all[i].Buildable = len(all[i].Targets) > 0
		fuel := DefaultFuel
		if all[i].Language == "JavaScript/TypeScript" {
			fuel = ScriptFuel
		}
		all[i].Resources = Resources{Fuel: fuel, MemoryBytes: DefaultMemoryBytes, Timeout: DefaultTimeout}
	}
}

// All returns every template, in the order they ship.
func All() []Template { return slices.Clone(all) }

// Find returns the template for a Snapshot's Language and runtime. For
// JavaScript/TypeScript the runtime picks Node or Bun. A runtime that does not
// apply to the Language is ignored. ok is false when no template covers it.
func Find(language, runtime string) (Template, bool) {
	for _, t := range all {
		if !strings.EqualFold(t.Language, language) {
			continue
		}
		if t.Runtime == "" || strings.EqualFold(t.Runtime, runtime) {
			return t, true
		}
	}
	return Template{}, false
}

// Buildable returns a buildable template for a Language, whatever its runtime.
// ok is false when the Language has no WASM builder.
func Buildable(language string) (Template, bool) {
	for _, t := range all {
		if t.Buildable && strings.EqualFold(t.Language, language) {
			return t, true
		}
	}
	return Template{}, false
}

// CheckTarget reports why target cannot be built by t, or nil when it can.
func (t Template) CheckTarget(target string) error {
	if slices.Contains(t.Targets, target) {
		return nil
	}
	if !t.Buildable {
		return fmt.Errorf("WASM builds are not available for %s projects yet", t.Language)
	}
	if len(t.Targets) == 1 {
		return fmt.Errorf("%s WASM builds require target %s", t.Language, t.Targets[0])
	}
	return fmt.Errorf("unsupported %s WASM target", t.Language)
}

// DefaultTarget returns the target a build uses when none is chosen, or "" when
// the template is not buildable.
func (t Template) DefaultTarget() string {
	if len(t.Targets) == 0 {
		return ""
	}
	return t.Targets[0]
}

// KnownTarget reports whether any template builds target.
func KnownTarget(target string) bool {
	for _, t := range all {
		if slices.Contains(t.Targets, target) {
			return true
		}
	}
	return false
}
