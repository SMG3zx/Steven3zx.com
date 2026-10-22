package crawl

import (
	"regexp"
	"strings"
)

// gitignore is one parsed .gitignore file. Patterns follow git's rules: the
// last matching pattern wins, "!" re-includes, a trailing "/" matches only
// directories, and a pattern containing a slash is anchored to the file's own
// directory. Supported glob syntax is *, ?, [...] and **.
type gitignore struct {
	rules []gitRule
}

type gitRule struct {
	re      *regexp.Regexp
	negate  bool
	dirOnly bool
}

func parseGitignore(data []byte, foldCase bool) *gitignore {
	g := &gitignore{}
	for _, line := range strings.Split(string(data), "\n") {
		if r, ok := parseRule(strings.TrimSuffix(line, "\r"), foldCase); ok {
			g.rules = append(g.rules, r)
		}
	}
	return g
}

func parseRule(line string, foldCase bool) (gitRule, bool) {
	// Trailing spaces are ignored unless escaped with a backslash.
	for strings.HasSuffix(line, " ") && !strings.HasSuffix(line, `\ `) {
		line = strings.TrimSuffix(line, " ")
	}
	if line == "" || strings.HasPrefix(line, "#") {
		return gitRule{}, false
	}

	var r gitRule
	if strings.HasPrefix(line, "!") {
		r.negate = true
		line = line[1:]
	} else if len(line) > 1 && line[0] == '\\' && (line[1] == '!' || line[1] == '#' || line[1] == ' ') {
		line = line[1:]
	}
	if strings.HasSuffix(line, "/") {
		r.dirOnly = true
		line = strings.TrimRight(line, "/")
	}
	if line == "" {
		return gitRule{}, false
	}

	// A slash anywhere (leading or in the middle) anchors the pattern to the
	// directory holding the .gitignore. Otherwise it matches at any depth.
	anchored := strings.Contains(line, "/")
	line = strings.TrimPrefix(line, "/")

	prefix := "^"
	if !anchored {
		prefix = "^(?:.*/)?"
	}
	if foldCase {
		prefix = "(?i)" + prefix
	}
	re, err := regexp.Compile(prefix + globToRegex(line) + "$")
	if err != nil {
		return gitRule{}, false
	}
	r.re = re
	return r, true
}

// globToRegex translates a gitignore glob to a regular expression body.
func globToRegex(p string) string {
	rs := []rune(p)
	var b strings.Builder
	for i := 0; i < len(rs); {
		switch c := rs[i]; c {
		case '*':
			if i+1 < len(rs) && rs[i+1] == '*' {
				j := i + 2
				atStart := i == 0 || rs[i-1] == '/'
				switch {
				case atStart && j < len(rs) && rs[j] == '/': // "**/": zero or more directories
					b.WriteString(`(?:.*/)?`)
					i = j + 1
				case atStart && j == len(rs): // trailing "/**": everything inside
					b.WriteString(`.*`)
					i = j
				default: // "**" elsewhere behaves like "*"
					b.WriteString(`[^/]*`)
					i = j
				}
				continue
			}
			b.WriteString(`[^/]*`)
			i++
		case '?':
			b.WriteString(`[^/]`)
			i++
		case '[':
			j := i + 1
			if j < len(rs) && (rs[j] == '!' || rs[j] == '^') {
				j++
			}
			if j < len(rs) && rs[j] == ']' {
				j++
			}
			for j < len(rs) && rs[j] != ']' {
				j++
			}
			if j >= len(rs) { // no closing bracket: literal "["
				b.WriteString(`\[`)
				i++
				continue
			}
			class := string(rs[i+1 : j])
			if strings.HasPrefix(class, "!") {
				class = "^" + class[1:]
			}
			b.WriteString("[" + class + "]")
			i = j + 1
		case '\\':
			if i+1 < len(rs) {
				b.WriteString(regexp.QuoteMeta(string(rs[i+1])))
				i += 2
			} else {
				b.WriteString(`\\`)
				i++
			}
		default:
			b.WriteString(regexp.QuoteMeta(string(c)))
			i++
		}
	}
	return b.String()
}

// match evaluates rel, a slash-separated path relative to the directory that
// holds the .gitignore. matched reports whether any rule applied; ignored is
// the verdict of the last rule that did.
func (g *gitignore) match(rel string, isDir bool) (ignored, matched bool) {
	for _, r := range g.rules {
		if r.dirOnly && !isDir {
			continue
		}
		if r.re.MatchString(rel) {
			ignored, matched = !r.negate, true
		}
	}
	return ignored, matched
}
