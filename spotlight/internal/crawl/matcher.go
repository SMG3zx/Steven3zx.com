package crawl

import (
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
)

const (
	gitignoreTTL      = 5 * time.Second // how long a parsed .gitignore is trusted before re-stat
	maxGitignoreBytes = 1 << 20
)

// Options configures a Matcher.
type Options struct {
	Roots []string

	// Added to the built-in lists (BuiltinIgnoreDirs and friends).
	IgnoreDirs  []string // directory names, matched at any depth
	IgnorePaths []string // slash-separated directory paths, matched anywhere
	IgnoreFiles []string // base-name globs

	// ExactFiles are absolute paths that are never indexed, such as the index
	// database and its -wal/-shm files when they live under a root.
	ExactFiles []string

	// RespectGitignore applies .gitignore files found from each root downward.
	RespectGitignore bool
	// ForceInclude holds gitignore-style patterns, relative to a root, that
	// override .gitignore. They don't override the built-in rules.
	ForceInclude []string
}

// Matcher answers "should this path be indexed?" and walks the roots.
// It is safe for concurrent use.
type Matcher struct {
	roots      []string
	dirNames   []string   // lower-case
	dirPaths   [][]string // lower-case segments
	fileGlobs  []string   // lower-case
	exact      map[string]struct{}
	respectGit bool
	force      *gitignore
	fold       bool // case-insensitive file system (Windows, macOS)

	mu    sync.Mutex
	cache map[string]*giEntry
}

type giEntry struct {
	g       *gitignore // nil when the directory has no .gitignore
	mod     time.Time
	size    int64
	checked time.Time
}

func NewMatcher(o Options) *Matcher {
	m := &Matcher{
		exact:      make(map[string]struct{}),
		respectGit: o.RespectGitignore,
		fold:       runtime.GOOS != "linux",
		cache:      make(map[string]*giEntry),
	}
	for _, r := range o.Roots {
		if abs, err := filepath.Abs(r); err == nil {
			r = abs
		}
		m.roots = append(m.roots, filepath.Clean(r))
	}
	for _, d := range concat(BuiltinIgnoreDirs, o.IgnoreDirs) {
		if d = strings.TrimSpace(d); d != "" {
			m.dirNames = append(m.dirNames, strings.ToLower(d))
		}
	}
	for _, p := range concat(BuiltinIgnorePaths, o.IgnorePaths) {
		if segs := splitSlash(p); len(segs) > 0 {
			for i := range segs {
				segs[i] = strings.ToLower(segs[i])
			}
			m.dirPaths = append(m.dirPaths, segs)
		}
	}
	for _, g := range concat(BuiltinIgnoreFiles, o.IgnoreFiles) {
		if g = strings.ToLower(strings.TrimSpace(g)); g != "" {
			m.fileGlobs = append(m.fileGlobs, g)
		}
	}
	for _, f := range o.ExactFiles {
		if abs, err := filepath.Abs(f); err == nil {
			f = abs
		}
		m.exact[m.norm(f)] = struct{}{}
	}
	if len(o.ForceInclude) > 0 {
		m.force = parseGitignore([]byte(strings.Join(o.ForceInclude, "\n")), m.fold)
	}
	return m
}

// Roots returns the cleaned absolute roots.
func (m *Matcher) Roots() []string { return append([]string(nil), m.roots...) }

// Ignored reports whether path must not be indexed (or, for a directory,
// descended into). It checks every directory above the entry, so it is correct
// for a single path handed over by the file watcher. Paths outside all roots
// are ignored; the roots themselves are not.
func (m *Matcher) Ignored(p string, isDir bool) bool {
	root, comps, ok := m.locate(p)
	if !ok {
		return true
	}
	for i := range comps {
		if m.leafIgnored(root, comps[:i+1], i < len(comps)-1 || isDir) {
			return true
		}
	}
	return false
}

// Walk visits every eligible regular file under the roots. Ignored
// directories are pruned without being read, and unreadable entries are skipped
// rather than aborting the walk.
func (m *Matcher) Walk(fn Func) error {
	for _, root := range m.roots {
		err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
			if err != nil || d == nil || p == root {
				return nil
			}
			info, err := d.Info()
			if err != nil {
				return nil
			}
			rel, err := filepath.Rel(root, p)
			if err != nil {
				return nil
			}
			comps := strings.Split(rel, string(filepath.Separator))
			if d.IsDir() {
				if Skip(info) || m.leafIgnored(root, comps, true) {
					return fs.SkipDir
				}
				return nil
			}
			if d.Type()&fs.ModeSymlink != 0 || !info.Mode().IsRegular() || Skip(info) {
				return nil
			}
			if m.leafIgnored(root, comps, false) {
				return nil
			}
			return fn(p, info)
		})
		if err != nil {
			return err
		}
	}
	return nil
}

// locate finds the root containing p and p's path components below it.
func (m *Matcher) locate(p string) (root string, comps []string, ok bool) {
	for _, r := range m.roots {
		rel, err := filepath.Rel(r, p)
		if err != nil {
			continue
		}
		if rel == "." {
			return r, nil, true
		}
		if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			continue
		}
		return r, strings.Split(rel, string(filepath.Separator)), true
	}
	return "", nil, false
}

// leafIgnored judges only the last component of comps, assuming the
// directories above it were already accepted.
func (m *Matcher) leafIgnored(root string, comps []string, isDir bool) bool {
	name := comps[len(comps)-1]
	if isDir {
		for _, d := range m.dirNames {
			if strings.EqualFold(name, d) {
				return true
			}
		}
		if m.pathFragmentIgnored(comps) {
			return true
		}
	} else {
		if len(m.exact) > 0 {
			full := filepath.Join(append([]string{root}, comps...)...)
			if _, ok := m.exact[m.norm(full)]; ok {
				return true
			}
		}
		if m.fileNameIgnored(strings.ToLower(name)) {
			return true
		}
	}
	return m.respectGit && m.gitIgnored(root, comps, isDir)
}

func (m *Matcher) pathFragmentIgnored(comps []string) bool {
	for _, frag := range m.dirPaths {
		if len(comps) < len(frag) {
			continue
		}
		tail := comps[len(comps)-len(frag):]
		match := true
		for i := range frag {
			if !strings.EqualFold(tail[i], frag[i]) {
				match = false
				break
			}
		}
		if match {
			return true
		}
	}
	return false
}

func (m *Matcher) fileNameIgnored(lower string) bool {
	for _, g := range m.fileGlobs {
		if ok, _ := path.Match(g, lower); ok {
			return true
		}
	}
	return isSecretName(lower)
}

// gitIgnored applies the .gitignore files from root down to the entry's parent
// directory. Deeper files take precedence over shallower ones, as in git.
func (m *Matcher) gitIgnored(root string, comps []string, isDir bool) bool {
	if m.force != nil {
		if ig, matched := m.force.match(strings.Join(comps, "/"), isDir); matched && ig {
			return false
		}
	}
	ignored := false
	dir := root
	for j := range comps {
		if g := m.loadGitignore(dir); g != nil {
			if ig, matched := g.match(strings.Join(comps[j:], "/"), isDir); matched {
				ignored = ig
			}
		}
		dir = filepath.Join(dir, comps[j])
	}
	return ignored
}

// loadGitignore returns the parsed .gitignore in dir, or nil. Results are
// cached and revalidated against the file's mtime and size every few seconds,
// so edits to a .gitignore are picked up by the long-running daemon.
func (m *Matcher) loadGitignore(dir string) *gitignore {
	m.mu.Lock()
	defer m.mu.Unlock()

	now := time.Now()
	e := m.cache[dir]
	if e != nil && now.Sub(e.checked) < gitignoreTTL {
		return e.g
	}
	fi, err := os.Stat(filepath.Join(dir, ".gitignore"))
	if err != nil || fi.IsDir() || fi.Size() > maxGitignoreBytes {
		m.cache[dir] = &giEntry{checked: now}
		return nil
	}
	if e != nil && e.g != nil && e.mod.Equal(fi.ModTime()) && e.size == fi.Size() {
		e.checked = now
		return e.g
	}
	data, err := os.ReadFile(filepath.Join(dir, ".gitignore"))
	if err != nil {
		m.cache[dir] = &giEntry{checked: now}
		return nil
	}
	g := parseGitignore(data, m.fold)
	m.cache[dir] = &giEntry{g: g, mod: fi.ModTime(), size: fi.Size(), checked: now}
	return g
}

func (m *Matcher) norm(p string) string {
	p = filepath.Clean(p)
	if m.fold {
		p = strings.ToLower(p)
	}
	return p
}

func concat(a, b []string) []string {
	return append(append(make([]string, 0, len(a)+len(b)), a...), b...)
}

func splitSlash(p string) []string {
	return strings.FieldsFunc(p, func(r rune) bool { return r == '/' || r == '\\' })
}
