package goanalysis

import (
	"strings"
	"testing"
)

func snapshot(entries ...string) []File {
	files := make([]File, 0, len(entries)/2)
	for i := 0; i+1 < len(entries); i += 2 {
		files = append(files, File{Path: entries[i], Content: []byte(entries[i+1])})
	}
	return files
}

func find(t *testing.T, report Report, dir string) Package {
	t.Helper()
	for _, pkg := range report.Packages {
		if pkg.Dir == dir {
			return pkg
		}
	}
	t.Fatalf("no package in %q; report: %+v", dir, report)
	return Package{}
}

func hasIssue(issues []Issue, code string) bool {
	for _, issue := range issues {
		if issue.Code == code {
			return true
		}
	}
	return false
}

func TestAPlainMainPackageIsBuildable(t *testing.T) {
	report := Analyze(snapshot(
		"go.mod", "module example.com/app\n\ngo 1.26\n",
		"main.go", "package main\n\nfunc main() {}\n",
	))
	pkg := find(t, report, ".")
	if !pkg.Buildable || !pkg.Main || pkg.Entry != "main.go" {
		t.Fatalf("package = %+v, want a buildable main with entry main.go", pkg)
	}
}

func TestALinuxOnlyProgramIsReportedWithItsConstraint(t *testing.T) {
	report := Analyze(snapshot(
		"go.mod", "module example.com/l2p\n\ngo 1.26\n",
		"main.go", "//go:build linux\n\npackage main\n\nfunc main() {}\n",
		"flow.go", "//go:build linux\n\npackage main\n",
	))
	pkg := find(t, report, ".")
	if pkg.Buildable || pkg.Main {
		t.Fatalf("package = %+v, want it blocked", pkg)
	}
	if !hasIssue(pkg.Issues, CodeExcluded) || !strings.Contains(pkg.Issues[0].Message, "//go:build linux") {
		t.Fatalf("issues = %+v, want the linux constraint named", pkg.Issues)
	}
}

func TestAFileNameSuffixExcludesAFile(t *testing.T) {
	report := Analyze(snapshot(
		"go.mod", "module example.com/app\n",
		"main_linux.go", "package main\n\nfunc main() {}\n",
	))
	pkg := find(t, report, ".")
	if pkg.Buildable || !strings.Contains(pkg.Issues[0].Message, "_linux") {
		t.Fatalf("package = %+v, want the _linux suffix named", pkg)
	}
}

func TestACommandUnderCmdIsBuildableWhenTheRootIsNot(t *testing.T) {
	report := Analyze(snapshot(
		"go.mod", "module example.com/app\n",
		"main.go", "//go:build linux\n\npackage main\n",
		"cmd/decode/main.go", "package main\n\nfunc main() {}\n",
	))
	if pkg := find(t, report, "cmd/decode"); !pkg.Buildable || pkg.Entry != "cmd/decode/main.go" {
		t.Fatalf("cmd/decode = %+v, want buildable", pkg)
	}
	if pkg := find(t, report, "."); pkg.Buildable {
		t.Fatalf("root = %+v, want blocked", pkg)
	}
}

func TestAMainThatImportsAnUnavailableLocalPackageIsBlocked(t *testing.T) {
	report := Analyze(snapshot(
		"go.mod", "module example.com/app\n",
		"main.go", "package main\n\nimport \"example.com/app/capture\"\n\nfunc main() { capture.Run() }\n",
		"capture/capture.go", "//go:build linux\n\npackage capture\n\nfunc Run() {}\n",
	))
	pkg := find(t, report, ".")
	if pkg.Buildable || !hasIssue(pkg.Issues, CodeLocalDep) {
		t.Fatalf("package = %+v, want the unavailable local package reported", pkg)
	}
}

func TestExternalImportsNeedVendoring(t *testing.T) {
	files := snapshot(
		"go.mod", "module example.com/app\n\nrequire github.com/x/y v1.0.0\n",
		"main.go", "package main\n\nimport _ \"github.com/x/y\"\n\nfunc main() {}\n",
	)
	report := Analyze(files)
	if find(t, report, ".").Buildable || !hasIssue(report.Issues, CodeNotVendored) {
		t.Fatalf("report = %+v, want a vendoring blocker", report)
	}
	vendored := Analyze(append(files, File{Path: "vendor/modules.txt", Content: []byte("# github.com/x/y v1.0.0\n")}))
	if !find(t, vendored, ".").Buildable || len(vendored.Issues) != 0 {
		t.Fatalf("report = %+v, want buildable once vendored", vendored)
	}
}

func TestRawSocketsAndCgoAreBlockers(t *testing.T) {
	report := Analyze(snapshot(
		"go.mod", "module example.com/app\n",
		"main.go", "package main\n\nimport \"syscall\"\n\nfunc main() { syscall.Socket(syscall.AF_PACKET, 0, 0) }\n",
		"cgo/main.go", "package main\n\n// #include <stdio.h>\nimport \"C\"\n\nfunc main() {}\n",
	))
	if pkg := find(t, report, "."); pkg.Buildable || !hasIssue(pkg.Issues, CodeRawSockets) {
		t.Fatalf("root = %+v, want a raw socket blocker", pkg)
	}
	if pkg := find(t, report, "cgo"); pkg.Buildable || !hasIssue(pkg.Issues, CodeCgo) {
		t.Fatalf("cgo = %+v, want a cgo blocker", pkg)
	}
}

func TestWarningsDoNotBlock(t *testing.T) {
	report := Analyze(snapshot(
		"go.mod", "module example.com/app\n",
		"main.go", "package main\n\nimport \"os/exec\"\n\nfunc main() { _ = exec.Command }\n",
	))
	pkg := find(t, report, ".")
	if !pkg.Buildable || !hasIssue(pkg.Issues, CodeProcesses) {
		t.Fatalf("package = %+v, want buildable with a warning", pkg)
	}
}

func TestLibraryPackagesAndVendorAreNotListed(t *testing.T) {
	report := Analyze(snapshot(
		"go.mod", "module example.com/app\n",
		"lib/lib.go", "package lib\n",
		"vendor/x/main.go", "package main\n\nfunc main() {}\n",
		"testdata/main.go", "package main\n\nfunc main() {}\n",
	))
	if len(report.Packages) != 0 {
		t.Fatalf("packages = %+v, want none", report.Packages)
	}
}
