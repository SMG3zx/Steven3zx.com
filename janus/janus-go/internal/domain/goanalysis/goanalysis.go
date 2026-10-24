// Package goanalysis reads the Go files of a repository snapshot and reports which
// main packages can be built for GOOS=wasip1 GOARCH=wasm, and why the others
// cannot. It works on files already in memory: build constraints are evaluated by
// go/build over an in-memory file system, so the answer matches what the toolchain
// would decide, and nothing is compiled or run.
package goanalysis

import (
	"bytes"
	"go/build"
	"go/parser"
	"go/token"
	"io"
	"io/fs"
	"os"
	"path"
	"sort"
	"strconv"
	"strings"
	"time"
)

// File is one file of a repository snapshot.
type File struct {
	Path    string
	Content []byte
}

// Issue codes. A blocker stops the build; a warning builds but is likely to fail or
// misbehave when the module runs.
const (
	CodeExcluded      = "excluded-by-constraints"
	CodeCgo           = "cgo"
	CodeUnsupported   = "unsupported-import"
	CodeRawSockets    = "raw-sockets"
	CodeLocalDep      = "local-package-unavailable"
	CodeNotVendored   = "dependencies-not-vendored"
	CodeNeedsTerminal = "needs-terminal"
	CodeProcesses     = "starts-processes"
	CodeNetwork       = "network-listener"
)

// Issue is one reason a package may not build or run as WASM.
type Issue struct {
	Code    string   `json:"code"`
	Blocker bool     `json:"blocker"`
	Message string   `json:"message"`
	Files   []string `json:"files,omitempty"`
}

// Package is one directory of Go files in the snapshot.
type Package struct {
	// Dir is the directory relative to the repository root, "." for the root.
	Dir string `json:"dir"`
	// Name is the package clause of the files that build for wasip1, or "" when none do.
	Name string `json:"name,omitempty"`
	// Main is true for a package main that has files for wasip1.
	Main bool `json:"main"`
	// Entry is the file to offer as the Entrypoint when the package is buildable.
	Entry string `json:"entry,omitempty"`
	// Buildable is true for a main package with no blocker, including through the
	// packages of the same module that it imports.
	Buildable bool    `json:"buildable"`
	Issues    []Issue `json:"issues,omitempty"`
}

// Report is the analysis of a snapshot.
type Report struct {
	// Module is the module path from go.mod, or "" when the snapshot has none.
	Module string `json:"module,omitempty"`
	// Packages lists every package main, buildable or not, ordered by directory.
	Packages []Package `json:"packages"`
	// Issues apply to the whole module, such as dependencies that are not vendored.
	Issues []Issue `json:"issues,omitempty"`
}

// Analyze evaluates the Go files of a snapshot for GOOS=wasip1 GOARCH=wasm.
func Analyze(files []File) Report {
	fsys := newMemFS(files)
	report := Report{Packages: []Package{}}
	var goMod []byte
	vendored := false
	for _, file := range files {
		name := clean(file.Path)
		switch name {
		case "go.mod":
			goMod = file.Content
		case "vendor/modules.txt":
			vendored = true
		}
	}
	report.Module = modulePath(goMod)

	context := fsys.context()
	type directory struct {
		pkg      *build.Package
		err      error
		external []string
	}
	dirs := make(map[string]*directory)
	for _, dir := range fsys.goDirs() {
		if dir == "vendor" || strings.HasPrefix(dir, "vendor/") || hiddenOrTestdata(dir) {
			continue
		}
		pkg, err := context.ImportDir("/"+dir, 0)
		dirs[dir] = &directory{pkg: pkg, err: err}
	}

	order := make([]string, 0, len(dirs))
	for dir := range dirs {
		order = append(order, dir)
	}
	sort.Strings(order)

	// A directory is unavailable when none of its files build for wasip1.
	unavailable := func(dir string) bool {
		entry, ok := dirs[dir]
		if !ok {
			return false
		}
		return len(entry.pkg.GoFiles) == 0
	}
	local := func(importPath string) (string, bool) {
		if report.Module == "" {
			return "", false
		}
		if importPath == report.Module {
			return ".", true
		}
		if rest, ok := strings.CutPrefix(importPath, report.Module+"/"); ok {
			return rest, true
		}
		return "", false
	}

	externalNeeded := false
	for _, dir := range order {
		entry := dirs[dir]
		pkg := Package{Dir: dir, Name: entry.pkg.Name}
		var issues []Issue
		if len(entry.pkg.GoFiles) == 0 {
			if !mainIn(fsys, dir, entry.pkg.IgnoredGoFiles) {
				continue
			}
			pkg.Name = "main"
			issues = append(issues, excludedIssue(fsys, dir, entry.pkg.IgnoredGoFiles))
		} else if entry.pkg.Name != "main" {
			continue
		} else {
			pkg.Main = true
			pkg.Entry = entryFile(fsys, dir, entry.pkg.GoFiles)
		}
		issues = append(issues, cgoIssues(fsys, dir, entry.pkg.IgnoredGoFiles)...)
		if pkg.Main {
			seen := map[string]bool{dir: true}
			var walk func(string, *build.Package)
			walk = func(from string, current *build.Package) {
				issues = append(issues, importIssues(fsys, from, current)...)
				for _, imported := range current.Imports {
					if next, ok := local(imported); ok {
						if seen[next] {
							continue
						}
						seen[next] = true
						if unavailable(next) {
							issues = append(issues, Issue{
								Code: CodeLocalDep, Blocker: true,
								Message: "imports " + imported + ", which has no Go files for GOOS=wasip1 GOARCH=wasm",
							})
							continue
						}
						if dep, ok := dirs[next]; ok {
							walk(next, dep.pkg)
						}
						continue
					}
					if strings.Contains(strings.SplitN(imported, "/", 2)[0], ".") {
						externalNeeded = true
					}
				}
			}
			walk(dir, entry.pkg)
		}
		pkg.Issues = dedupe(issues)
		pkg.Buildable = pkg.Main && !blocked(pkg.Issues)
		report.Packages = append(report.Packages, pkg)
	}

	if externalNeeded && !vendored && len(goMod) > 0 {
		report.Issues = append(report.Issues, Issue{
			Code: CodeNotVendored, Blocker: true,
			Message: "the module imports packages from other modules but the repository has no vendor directory, and builds run without network access; run `go mod vendor` and commit it",
		})
		for i := range report.Packages {
			if report.Packages[i].Main {
				report.Packages[i].Buildable = false
			}
		}
	}
	return report
}

func blocked(issues []Issue) bool {
	for _, issue := range issues {
		if issue.Blocker {
			return true
		}
	}
	return false
}

func dedupe(issues []Issue) []Issue {
	seen := make(map[string]bool, len(issues))
	out := make([]Issue, 0, len(issues))
	for _, issue := range issues {
		key := issue.Code + "\x00" + issue.Message
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, issue)
	}
	return out
}

// excludedIssue explains why every file of a package main was left out, naming the
// constraints the files carry.
func excludedIssue(fsys *memFS, dir string, ignored []string) Issue {
	constraints := make(map[string]bool)
	for _, name := range ignored {
		if line := constraintLine(fsys.content(path.Join(dir, name))); line != "" {
			constraints[line] = true
		} else if suffix := filenameConstraint(name); suffix != "" {
			constraints[suffix] = true
		}
	}
	listed := make([]string, 0, len(constraints))
	for line := range constraints {
		listed = append(listed, line)
	}
	sort.Strings(listed)
	message := "every Go file is excluded for GOOS=wasip1 GOARCH=wasm"
	if len(listed) > 0 {
		message += " (" + strings.Join(listed, ", ") + ")"
	}
	files := make([]string, 0, len(ignored))
	for _, name := range ignored {
		files = append(files, path.Join(dir, name))
	}
	return Issue{Code: CodeExcluded, Blocker: true, Message: message, Files: files}
}

// constraintLine returns the //go:build line from the header of a file, or "".
func constraintLine(content []byte) string {
	for line := range strings.SplitSeq(string(content), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "//go:build ") {
			return line
		}
		if strings.HasPrefix(line, "package ") {
			return ""
		}
	}
	return ""
}

var knownOS = []string{"aix", "android", "darwin", "dragonfly", "freebsd", "hurd", "illumos", "ios", "js", "linux", "nacl", "netbsd", "openbsd", "plan9", "solaris", "wasip1", "windows", "zos"}

// filenameConstraint reports an implicit constraint from a _GOOS file name.
func filenameConstraint(name string) string {
	base := strings.TrimSuffix(strings.TrimSuffix(name, ".go"), "_test")
	for _, goos := range knownOS {
		if strings.HasSuffix(base, "_"+goos) {
			return "file name _" + goos
		}
	}
	return ""
}

func cgoIssues(fsys *memFS, dir string, ignored []string) []Issue {
	var files []string
	for _, name := range ignored {
		if importsOf(fsys.content(path.Join(dir, name)))["C"] {
			files = append(files, path.Join(dir, name))
		}
	}
	if len(files) == 0 {
		return nil
	}
	return []Issue{{Code: CodeCgo, Blocker: true, Message: "uses cgo, which the WASM toolchain does not support", Files: files}}
}

// unsupported maps import paths to what is wrong with them on wasip1.
var unsupported = map[string]struct {
	code, message string
	blocker       bool
}{
	"golang.org/x/sys/unix":              {CodeUnsupported, "golang.org/x/sys/unix has no wasip1 support", true},
	"golang.org/x/sys/windows":           {CodeUnsupported, "golang.org/x/sys/windows is Windows only", true},
	"github.com/google/gopacket":         {CodeUnsupported, "gopacket needs libpcap through cgo", true},
	"github.com/google/gopacket/pcap":    {CodeUnsupported, "gopacket/pcap needs libpcap through cgo", true},
	"github.com/mdlayher/packet":         {CodeRawSockets, "raw packet sockets (AF_PACKET) do not exist in any WASI version", true},
	"github.com/mdlayher/raw":            {CodeRawSockets, "raw sockets do not exist in any WASI version", true},
	"github.com/charmbracelet/bubbletea": {CodeNeedsTerminal, "a terminal UI needs a TTY, which a WASM module run by a host does not have", false},
	"github.com/gdamore/tcell/v2":        {CodeNeedsTerminal, "a terminal UI needs a TTY, which a WASM module run by a host does not have", false},
	"os/exec":                            {CodeProcesses, "starting processes is not possible in WASI 0.1 or 0.2", false},
	"net/http/httptest":                  {CodeNetwork, "WASI 0.1 has no listening sockets (0.2 does, but Go builds only target 0.1)", false},
}

// importIssues checks the imports and the source of a package for things WASI lacks.
func importIssues(fsys *memFS, dir string, pkg *build.Package) []Issue {
	var issues []Issue
	for _, imported := range pkg.Imports {
		if known, ok := unsupported[imported]; ok {
			issues = append(issues, Issue{Code: known.code, Blocker: known.blocker, Message: known.message})
		}
	}
	for _, name := range pkg.GoFiles {
		source := string(fsys.content(path.Join(dir, name)))
		for _, needle := range []string{"AF_PACKET", "syscall.Socket(", "unix.Socket("} {
			if strings.Contains(source, needle) {
				issues = append(issues, Issue{
					Code: CodeRawSockets, Blocker: true, Files: []string{path.Join(dir, name)},
					Message: "opens raw sockets (" + strings.TrimSuffix(needle, "(") + "), which no WASI version provides",
				})
				break
			}
		}
		if strings.Contains(source, "net.Listen(") {
			issues = append(issues, Issue{
				Code: CodeNetwork, Files: []string{path.Join(dir, name)},
				Message: "listens on a network address, which WASI 0.1 cannot do (0.2 can, but Go builds only target 0.1)",
			})
		}
	}
	return issues
}

func importsOf(content []byte) map[string]bool {
	imports := make(map[string]bool)
	file, err := parser.ParseFile(token.NewFileSet(), "", content, parser.ImportsOnly)
	if err != nil || file == nil {
		return imports
	}
	for _, spec := range file.Imports {
		if value, err := strconv.Unquote(spec.Path.Value); err == nil {
			imports[value] = true
		}
	}
	return imports
}

// mainIn reports whether any of the named files declares package main.
func mainIn(fsys *memFS, dir string, names []string) bool {
	for _, name := range names {
		file, err := parser.ParseFile(token.NewFileSet(), "", fsys.content(path.Join(dir, name)), parser.PackageClauseOnly)
		if err == nil && file.Name.Name == "main" {
			return true
		}
	}
	return false
}

// entryFile picks the file to offer as the Entrypoint: the one that declares func main.
func entryFile(fsys *memFS, dir string, names []string) string {
	sorted := append([]string(nil), names...)
	sort.Strings(sorted)
	for _, name := range sorted {
		if bytes.Contains(fsys.content(path.Join(dir, name)), []byte("func main()")) {
			return path.Join(dir, name)
		}
	}
	if len(sorted) > 0 {
		return path.Join(dir, sorted[0])
	}
	return ""
}

func modulePath(goMod []byte) string {
	for line := range strings.SplitSeq(string(goMod), "\n") {
		line = strings.TrimSpace(line)
		if rest, ok := strings.CutPrefix(line, "module "); ok {
			return strings.Trim(strings.TrimSpace(rest), `"`)
		}
	}
	return ""
}

func hiddenOrTestdata(dir string) bool {
	for part := range strings.SplitSeq(dir, "/") {
		if part == "testdata" || (part != "." && strings.HasPrefix(part, ".")) || strings.HasPrefix(part, "_") {
			return true
		}
	}
	return false
}

func clean(name string) string { return path.Clean(strings.TrimPrefix(name, "./")) }

// memFS serves the snapshot to go/build.
type memFS struct {
	files map[string][]byte
	dirs  map[string][]string
}

func newMemFS(files []File) *memFS {
	fsys := &memFS{files: make(map[string][]byte), dirs: make(map[string][]string)}
	for _, file := range files {
		name := clean(file.Path)
		fsys.files[name] = file.Content
		dir := path.Dir(name)
		fsys.dirs[dir] = append(fsys.dirs[dir], path.Base(name))
		// Register the parents so IsDir answers for them.
		for parent := dir; parent != "." && parent != "/"; parent = path.Dir(parent) {
			if _, ok := fsys.dirs[parent]; !ok {
				fsys.dirs[parent] = nil
			}
		}
	}
	return fsys
}

func (m *memFS) content(name string) []byte { return m.files[clean(name)] }

// goDirs lists the directories that hold at least one .go file, sorted.
func (m *memFS) goDirs() []string {
	var out []string
	for dir, names := range m.dirs {
		for _, name := range names {
			if strings.HasSuffix(name, ".go") {
				out = append(out, dir)
				break
			}
		}
	}
	sort.Strings(out)
	return out
}

func (m *memFS) context() *build.Context {
	context := build.Default
	context.GOOS, context.GOARCH = "wasip1", "wasm"
	context.CgoEnabled = false
	context.UseAllFiles = false
	context.BuildTags = nil
	context.ReleaseTags = build.Default.ReleaseTags
	rel := func(p string) string { return clean(strings.TrimPrefix(p, "/")) }
	context.IsDir = func(p string) bool { _, ok := m.dirs[rel(p)]; return ok }
	context.ReadDir = func(p string) ([]fs.FileInfo, error) {
		names, ok := m.dirs[rel(p)]
		if !ok {
			return nil, os.ErrNotExist
		}
		infos := make([]fs.FileInfo, 0, len(names))
		for _, name := range names {
			infos = append(infos, memInfo{name: name, size: int64(len(m.files[path.Join(rel(p), name)]))})
		}
		return infos, nil
	}
	context.OpenFile = func(p string) (io.ReadCloser, error) {
		data, ok := m.files[rel(p)]
		if !ok {
			return nil, os.ErrNotExist
		}
		return io.NopCloser(bytes.NewReader(data)), nil
	}
	context.JoinPath = path.Join
	context.IsAbsPath = func(p string) bool { return strings.HasPrefix(p, "/") }
	context.SplitPathList = func(list string) []string { return strings.Split(list, ":") }
	context.HasSubdir = func(root, dir string) (string, bool) {
		if rest, ok := strings.CutPrefix(dir, root+"/"); ok {
			return rest, true
		}
		return "", false
	}
	return &context
}

type memInfo struct {
	name string
	size int64
}

func (i memInfo) Name() string       { return i.name }
func (i memInfo) Size() int64        { return i.size }
func (i memInfo) Mode() fs.FileMode  { return 0o644 }
func (i memInfo) ModTime() time.Time { return time.Time{} }
func (i memInfo) IsDir() bool        { return false }
func (i memInfo) Sys() any           { return nil }
