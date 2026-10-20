package templates

import (
	"strings"
	"testing"
)

func TestFourTemplatesShipInOrder(t *testing.T) {
	var ids []string
	for _, tpl := range All() {
		ids = append(ids, tpl.ID)
	}
	if got := strings.Join(ids, ","); got != "go,rust,js-node,js-bun" {
		t.Fatalf("templates = %s", got)
	}
}

func TestFindPicksTheTemplateForALanguageAndRuntime(t *testing.T) {
	tests := []struct{ language, runtime, want string }{
		{"Go", "", "go"},
		{"go", "", "go"},
		{"Rust", "", "rust"},
		{"JavaScript/TypeScript", "Node", "js-node"},
		{"JavaScript/TypeScript", "Bun", "js-bun"},
		{"javascript/typescript", "bun", "js-bun"},
		{"Go", "Node", "go"},
		{"JavaScript/TypeScript", "", ""},
		{"Python", "", ""},
		{"", "", ""},
	}
	for _, tc := range tests {
		got, ok := Find(tc.language, tc.runtime)
		if (tc.want == "") == ok || got.ID != tc.want {
			t.Errorf("Find(%q, %q) = %q, %v, want %q", tc.language, tc.runtime, got.ID, ok, tc.want)
		}
	}
}

func TestEveryTemplateIsBuildable(t *testing.T) {
	for _, tpl := range All() {
		if !tpl.Buildable || tpl.DefaultTarget() == "" || tpl.BuildCommand == "" {
			t.Errorf("%s Buildable = %v, DefaultTarget = %q, BuildCommand = %q", tpl.ID, tpl.Buildable, tpl.DefaultTarget(), tpl.BuildCommand)
		}
	}
	if tpl, ok := Buildable("JavaScript/TypeScript"); !ok || tpl.DefaultTarget() != "wasm32-wasip1" {
		t.Errorf("Buildable(JavaScript/TypeScript) = %q, %v", tpl.ID, ok)
	}
	if tpl, ok := Buildable("rust"); !ok || tpl.ID != "rust" {
		t.Errorf("Buildable(rust) = %v, %v", tpl.ID, ok)
	}
}

func TestCheckTarget(t *testing.T) {
	goTpl, _ := Find("Go", "")
	rustTpl, _ := Find("Rust", "")
	node, _ := Find("JavaScript/TypeScript", "Node")
	tests := []struct {
		tpl     Template
		target  string
		wantErr string
	}{
		{goTpl, "wasip1-wasm", ""},
		{goTpl, "wasm32-wasip1", "Go WASM builds require target wasip1-wasm"},
		{rustTpl, "wasm32-wasip1", ""},
		{rustTpl, "wasm32-unknown-unknown", ""},
		{rustTpl, "wasip1-wasm", "unsupported Rust WASM target"},
		{node, "wasm32-wasip1", ""},
		{node, "wasip1-wasm", "JavaScript/TypeScript WASM builds require target wasm32-wasip1"},
		{Template{Language: "Python"}, "wasm32-wasip1", "not available for Python projects yet"},
	}
	for _, tc := range tests {
		err := tc.tpl.CheckTarget(tc.target)
		switch {
		case tc.wantErr == "" && err != nil:
			t.Errorf("%s %s: %v", tc.tpl.ID, tc.target, err)
		case tc.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tc.wantErr)):
			t.Errorf("%s %s: err = %v, want %q", tc.tpl.ID, tc.target, err, tc.wantErr)
		}
	}
}

func TestEveryTemplateStartsFromTheDefaultLimits(t *testing.T) {
	for _, tpl := range All() {
		r := tpl.Resources
		wantFuel := DefaultFuel
		if tpl.Language == "JavaScript/TypeScript" {
			wantFuel = ScriptFuel
		}
		if r.Fuel != wantFuel || r.MemoryBytes != DefaultMemoryBytes || r.Timeout != DefaultTimeout {
			t.Errorf("%s resources = %+v", tpl.ID, r)
		}
		if tpl.Manifest == "" || tpl.Entrypoint == "" {
			t.Errorf("%s is missing its manifest or entrypoint rule", tpl.ID)
		}
	}
}

func TestKnownTarget(t *testing.T) {
	for _, target := range []string{"wasip1-wasm", "wasm32-wasip1", "wasm32-unknown-unknown"} {
		if !KnownTarget(target) {
			t.Errorf("%s should be known", target)
		}
	}
	if KnownTarget("--privileged") {
		t.Error("--privileged should not be a target")
	}
}
