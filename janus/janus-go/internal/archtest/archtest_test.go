// Package archtest enforces the dependency rules of section 4 of
// docs/architecture.md. It reads the import graph with `go list` and fails
// when a package under internal/ or cmd/ imports something its layer forbids.
package archtest

import (
	"bytes"
	"encoding/json"
	"os/exec"
	"strings"
	"testing"
)

const modulePath = "janus"

// purePackages is the allow-list of third-party packages that domain and app
// may import. An entry ending in "/..." allows that package and everything
// below it; any other entry allows exactly that package. Adding a line here
// is a visible review decision.
var purePackages = []string{
	"github.com/google/uuid",
	"golang.org/x/crypto/argon2",
	"github.com/golang-jwt/jwt/v5",
	"github.com/open-policy-agent/opa/...",
	"github.com/klauspost/compress/zstd",
	// The OpenTelemetry API, not the SDK or the exporters.
	"go.opentelemetry.io/otel",
	"go.opentelemetry.io/otel/attribute",
	"go.opentelemetry.io/otel/codes",
	"go.opentelemetry.io/otel/metric",
	"go.opentelemetry.io/otel/trace",
}

// appForbiddenStdlib are standard-library packages (and their subpackages)
// that app must not import: they are adapter technology.
var appForbiddenStdlib = []string{"net/http", "database/sql", "os/exec"}

type listedPackage struct {
	ImportPath string
	Imports    []string
	Error      *struct{ Err string }
}

type violation struct {
	pkg, imp, rule, reason string
}

func (v violation) String() string {
	return "package " + v.pkg + " imports " + v.imp + ": rule " + v.rule + ": " + v.reason
}

type layer int

const (
	layerOther layer = iota
	layerDomain
	layerApp
	layerAdapter
	layerComposition
)

// Only non-test imports are checked: `go list` reports test imports
// separately, so a *test adapter imported from a _test.go file is allowed.

// check applies the four dependency rules to pkgs, whose import paths are
// below root (the module path, or a fixture root under testdata).
func check(root string, pkgs []listedPackage) []violation {
	c := checker{root: root}
	var out []violation
	for _, p := range pkgs {
		for _, imp := range p.Imports {
			out = append(out, c.checkImport(p.ImportPath, imp)...)
		}
	}
	return out
}

type checker struct{ root string }

// rel returns the path of an in-repository package relative to the root.
func (c checker) rel(path string) (string, bool) {
	if path == c.root {
		return "", true
	}
	return strings.CutPrefix(path, c.root+"/")
}

func within(rel, dir string) bool {
	return rel == dir || strings.HasPrefix(rel, dir+"/")
}

func (c checker) layerOf(path string) (layer, string) {
	rel, ok := c.rel(path)
	if !ok {
		return layerOther, ""
	}
	switch {
	case within(rel, "internal/domain"):
		return layerDomain, ""
	case within(rel, "internal/app"):
		return layerApp, ""
	case strings.HasPrefix(rel, "internal/adapters/"):
		name, _, _ := strings.Cut(strings.TrimPrefix(rel, "internal/adapters/"), "/")
		return layerAdapter, name
	case within(rel, "cmd/janus"):
		return layerComposition, ""
	}
	return layerOther, ""
}

func isStdlib(path string) bool {
	first, _, _ := strings.Cut(path, "/")
	return !strings.Contains(first, ".")
}

func isPure(path string) bool {
	for _, allowed := range purePackages {
		if dir, ok := strings.CutSuffix(allowed, "/..."); ok {
			if within(path, dir) {
				return true
			}
		} else if path == allowed {
			return true
		}
	}
	return false
}

func (c checker) checkImport(pkg, imp string) []violation {
	from, fromAdapter := c.layerOf(pkg)
	to, toAdapter := c.layerOf(imp)
	_, local := c.rel(imp)
	bad := func(rule, reason string) []violation {
		return []violation{{pkg, imp, rule, reason}}
	}

	switch from {
	case layerDomain:
		if local && to != layerDomain {
			return bad("1", "domain imports nothing else from this repository")
		}
		if !local && !isStdlib(imp) && !isPure(imp) {
			return bad("1", "domain imports only the standard library and allow-listed pure libraries")
		}
	case layerApp:
		if local && to != layerDomain && to != layerApp {
			return bad("2", "app imports only domain")
		}
		if !local && !isStdlib(imp) && !isPure(imp) {
			return bad("2", "app imports only allow-listed pure libraries")
		}
		if isStdlib(imp) {
			for _, f := range appForbiddenStdlib {
				if within(imp, f) {
					return bad("2", "app must not import "+f)
				}
			}
		}
	case layerAdapter:
		switch {
		case to == layerAdapter && toAdapter != fromAdapter:
			return bad("3", "an adapter never imports another adapter")
		case to == layerComposition:
			return bad("3", "an adapter never imports cmd")
		}
	default:
		if to == layerAdapter && from != layerComposition {
			return bad("4", "only cmd/janus imports concrete adapters")
		}
	}
	return nil
}

// goList lists the packages matching patterns, running in the module root.
func goList(t *testing.T, patterns ...string) []listedPackage {
	t.Helper()
	args := append([]string{"list", "-e", "-json"}, patterns...)
	cmd := exec.Command("go", args...)
	cmd.Dir = "../.."
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("go %s: %v\n%s", strings.Join(args, " "), err, stderr.String())
	}
	var pkgs []listedPackage
	dec := json.NewDecoder(&stdout)
	for dec.More() {
		var p listedPackage
		if err := dec.Decode(&p); err != nil {
			t.Fatalf("decode go list output: %v", err)
		}
		if p.Error != nil {
			t.Fatalf("go list could not load %s: %s", p.ImportPath, p.Error.Err)
		}
		pkgs = append(pkgs, p)
	}
	return pkgs
}

func TestRepositoryFollowsDependencyRules(t *testing.T) {
	var pkgs []listedPackage
	for _, p := range goList(t, "./...") {
		if strings.HasPrefix(p.ImportPath, modulePath+"/internal/") || strings.HasPrefix(p.ImportPath, modulePath+"/cmd/") {
			pkgs = append(pkgs, p)
		}
	}
	for _, v := range check(modulePath, pkgs) {
		t.Error(v)
	}
}

func TestFixtures(t *testing.T) {
	const fixtures = modulePath + "/internal/archtest/testdata/"
	tests := []struct {
		name string
		want []string // each must appear in exactly one violation message
	}{
		{"clean", nil},
		{"rule1", []string{"package " + fixtures + "rule1/internal/domain imports " + fixtures + "rule1/internal/adapters/sqlstore: rule 1"}},
		{"rule2", []string{
			"package " + fixtures + "rule2/internal/app imports net/http: rule 2",
			"package " + fixtures + "rule2/internal/app imports go.uber.org/zap: rule 2",
			"package " + fixtures + "rule2/internal/app imports database/sql: rule 2",
			"package " + fixtures + "rule2/internal/app imports os/exec: rule 2",
			"package " + fixtures + "rule2/internal/app imports github.com/bytecodealliance/wasmtime-go/v49: rule 2",
		}},
		{"rule3", []string{
			"package " + fixtures + "rule3/internal/adapters/httpapi imports " + fixtures + "rule3/internal/adapters/sqlstore: rule 3",
			"package " + fixtures + "rule3/internal/adapters/httpapi imports " + fixtures + "rule3/internal/adapters/storetest: rule 3",
		}},
		{"rule4", []string{"package " + fixtures + "rule4/cmd/other imports " + fixtures + "rule4/internal/adapters/sqlstore: rule 4"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := fixtures + tt.name
			got := check(root, goList(t, "./internal/archtest/testdata/"+tt.name+"/..."))
			if len(got) != len(tt.want) {
				t.Fatalf("got %d violations, want %d:\n%v", len(got), len(tt.want), got)
			}
			for _, want := range tt.want {
				n := 0
				for _, v := range got {
					if strings.HasPrefix(v.String(), want) {
						n++
					}
				}
				if n != 1 {
					t.Errorf("want one violation starting %q, got %v", want, got)
				}
			}
		})
	}
}
