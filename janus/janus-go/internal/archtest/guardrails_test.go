package archtest

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// The guardrails below are ratchets. Each one counts how many times a pattern
// appears in each file, and fails when a file has more than its allowance.
// An allowance is the count the code had when the rule was written. It is
// debt, not permission: when a site is fixed the count drops, and the test
// fails until the allowance is lowered to match. The map goes to zero over
// time, and a new site never gets one.

var (
	// sqlClock matches a SQL clock that bypasses the clock a service is given.
	sqlClock = regexp.MustCompile(`(?i)CURRENT_TIMESTAMP|strftime\(\s*'now'`)
	// errorText matches an error compared with another error's message text.
	errorText = regexp.MustCompile(`\.Error\(\)\s*==|==\s*[\w.]+\.Error\(\)`)
	// exportedPort matches an exported interface, which a service must not declare.
	exportedPort = regexp.MustCompile(`(?m)^type [A-Z]\w* interface`)
)

// clockAllowance: sites that read time from SQL, each file's count. Services
// should pass the clock in as a value (CODING_STANDARDS section 6).
var clockAllowance = map[string]int{
	"../services/builds/store.go":      3,
	"../services/fleet/store.go":       3,
	"../services/identity/github.go":   1,
	"../services/identity/users.go":    2,
	"../services/projects/imports.go":  2,
	"../services/projects/owners.go":   1,
	"../services/projects/pipeline.go": 2,
	"../services/projects/store.go":    3,
}

// errorTextAllowance: comparisons of an error's text, each file's count.
// Callers should match typed contract errors with errors.Is.
var errorTextAllowance = map[string]int{
	"../adapters/fleetnats/fleetnats.go": 4,
	"../adapters/httpapi/lifecycle.go":   2,
	"../adapters/httpapi/projects.go":    0,
}

// portAllowance: exported interfaces a service still declares, each file's
// count. A service declares its interfaces in the package that uses them, and
// unexported (CODING_STANDARDS section 5).
var portAllowance = map[string]int{
	"../services/fleet/headscale/client.go": 1,
	"../services/fleet/service.go":          1,
}

// TestServicesReadTheClockFromTheirConfig fails when a service reads the time
// from SQL rather than from the clock it was given.
func TestServicesReadTheClockFromTheirConfig(t *testing.T) {
	checkRatchet(t, []string{"../services"}, sqlClock, clockAllowance,
		"reads the clock from SQL; pass the clock into the service instead")
}

// TestErrorsAreNotMatchedByText fails when code compares an error's message
// with another error's text. Match the typed error instead.
func TestErrorsAreNotMatchedByText(t *testing.T) {
	checkRatchet(t, []string{"..", "../../cmd"}, errorText, errorTextAllowance,
		"matches an error by its text; match the typed contract error with errors.Is")
}

// TestServicesDeclareNoExportedPorts fails when a service exports an interface.
// Interfaces belong to the package that uses them (architecture.md section 1).
func TestServicesDeclareNoExportedPorts(t *testing.T) {
	checkRatchet(t, []string{"../services"}, exportedPort, portAllowance,
		"declares an exported interface inside a service; declare it in the package that uses it, unexported")
}

// checkRatchet counts pattern matches in each non-test Go file under roots and
// compares them with allowance.
func checkRatchet(t *testing.T, roots []string, pattern *regexp.Regexp, allowance map[string]int, reason string) {
	t.Helper()
	counts := map[string]int{}
	for _, root := range roots {
		err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if entry.IsDir() {
				if entry.Name() == "testdata" {
					return filepath.SkipDir
				}
				return nil
			}
			if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			source, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			if n := len(pattern.FindAllIndex(source, -1)); n > 0 {
				counts[filepath.ToSlash(filepath.Clean(path))] = n
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	var problems []string
	for file, n := range counts {
		if allowed := allowance[file]; n > allowed {
			problems = append(problems, fmt.Sprintf("%s has %d matches, allowed %d: %s", file, n, allowed, reason))
		}
	}
	for file, allowed := range allowance {
		if n := counts[file]; n < allowed {
			problems = append(problems, fmt.Sprintf("%s has %d matches, allowance is %d: lower the allowance to %d", file, n, allowed, n))
		}
	}
	sort.Strings(problems)
	for _, problem := range problems {
		t.Error(problem)
	}
}
