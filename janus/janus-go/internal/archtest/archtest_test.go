// Package archtest enforces the dependency rules of section 9 of
// docs/architecture.md. It reads the import graph with `go list` and fails
// when a package under internal/ or cmd/ imports something its layer forbids.
//
// Rules "9.1" to "9.6" are the rules for domain, contract, platform, services,
// cmd/janus and the Agent.
package archtest

import (
	"bytes"
	"encoding/json"
	"os/exec"
	"strings"
	"testing"
)

const modulePath = "janus"

// purePackages is the allow-list of third-party packages that domain may
// import. An entry ending in "/..." allows that package and everything below
// it; any other entry allows exactly that package. Adding a line here is a
// visible review decision.
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
	layerComposition
	layerContract
	layerPlatform
	layerService
	layerAgent
)

// Only non-test imports are checked: `go list` reports test imports
// separately, so a test fixture or a fake imported from a _test.go file is
// allowed.

// check applies the dependency rules to pkgs, whose import paths are below root
// (the module path, or a fixture root under testdata).
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
	case within(rel, "internal/contract"):
		return layerContract, ""
	case within(rel, "internal/platform"):
		return layerPlatform, ""
	case within(rel, "internal/agent"):
		return layerAgent, ""
	case strings.HasPrefix(rel, "internal/services/"):
		name, _, _ := strings.Cut(strings.TrimPrefix(rel, "internal/services/"), "/")
		return layerService, name
	case within(rel, "cmd/janus"), within(rel, "cmd/janus-agent"):
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
	from, fromName := c.layerOf(pkg)
	to, toName := c.layerOf(imp)
	_, local := c.rel(imp)
	bad := func(rule, reason string) []violation {
		return []violation{{pkg, imp, rule, reason}}
	}

	switch {
	case to == layerService && from != layerComposition:
		if from == layerService && fromName == toName {
			return nil
		}
		if from == layerService {
			return bad("9.4", "a service never imports another service; use its contract")
		}
		return bad("9.5", "only cmd/janus imports services")
	case to == layerAgent && from != layerComposition && from != layerAgent:
		return bad("9.6", "only cmd and the Agent's own packages import the Agent")
	case to == layerComposition:
		return bad("9.5", "nothing imports cmd")
	case from == layerDomain:
		if local && to != layerDomain {
			return bad("9.1", "domain imports nothing else from this repository")
		}
		if !local && !isStdlib(imp) && !isPure(imp) {
			return bad("9.1", "domain imports only the standard library and allow-listed pure libraries")
		}
	case from == layerContract:
		if local && to != layerDomain && to != layerContract {
			return bad("9.2", "contract imports only domain")
		}
		if !local && !isStdlib(imp) {
			return bad("9.2", "contract imports only the standard library")
		}
	case from == layerPlatform:
		if to == layerContract || to == layerAgent {
			return bad("9.3", "platform imports no service, contract or Agent package")
		}
	case from == layerService:
		// Rule 9.4: a service imports contract, domain and platform from this
		// repository. The other layers were refused above.
	case from == layerAgent:
		if to == layerComposition {
			return bad("9.6", "the Agent never imports cmd")
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
		{"domain", []string{"package " + fixtures + "domain/internal/domain imports " + fixtures + "domain/internal/platform/natsx: rule 9.1"}},
		{"services", []string{
			"package " + fixtures + "services/internal/contract/projects imports " + fixtures + "services/internal/platform/natsx: rule 9.2",
			"package " + fixtures + "services/internal/platform/natsx imports " + fixtures + "services/internal/contract/shared: rule 9.3",
			"package " + fixtures + "services/internal/services/builds imports " + fixtures + "services/internal/services/projects: rule 9.4",
			"package " + fixtures + "services/internal/services/projects imports " + fixtures + "services/internal/agent/link: rule 9.6",
			"package " + fixtures + "services/cmd/other imports " + fixtures + "services/internal/services/projects: rule 9.5",
		}},
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
