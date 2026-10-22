package archtest

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// tour is the part of a CodeTour file this test reads.
type tour struct {
	Title string `json:"title"`
	Steps []struct {
		File        string `json:"file"`
		Line        int    `json:"line"`
		Title       string `json:"title"`
		Description string `json:"description"`
	} `json:"steps"`
}

// anchor is the first backticked term in a step's description. The file a step
// points at must still contain it.
var anchor = regexp.MustCompile("`([^`]+)`")

// lineEntry is the "line" field of one step in a tour file.
var lineEntry = regexp.MustCompile(`"line": (\d+)`)

// ownTours are the tours this repo generates. They live in janus/.tours, and
// their file paths are relative to the janus folder that holds janus-go.
var ownTours = []string{
	"request-flow.tour", "deployment-flow.tour", "build-pipeline.tour", "ports.tour",
	"sign-in.tour", "authorization.tour", "project-import.tour", "job-resume.tour",
	"agent-enrollment.tour", "agent-runtime.tour", "usage-recording.tour", "startup-shutdown.tour",
}

// move is a step's "line" field that must hold line instead of what it holds.
type move struct {
	start, end, line int
}

// TestTourStepsPointAtTheirSymbols keeps the tours in janus/.tours honest. A step
// fails when its file is gone or no longer contains the symbol its description
// names. A step whose symbol has moved within its file is reported, and with
// UPDATE_TOURS set its line is rewritten to where the symbol is now:
//
//	UPDATE_TOURS=1 go test ./internal/archtest -run TestTourStepsPointAtTheirSymbols
//
// A step with no file is an introduction and is not checked.
func TestTourStepsPointAtTheirSymbols(t *testing.T) {
	tourDir := filepath.Join("..", "..", "..", ".tours")
	root := filepath.Join("..", "..", "..")
	update := os.Getenv("UPDATE_TOURS") != ""
	for _, name := range ownTours {
		file := filepath.Join(tourDir, name)
		data, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		var parsed tour
		if err := json.Unmarshal(data, &parsed); err != nil {
			t.Fatalf("%s is not valid JSON: %v", file, err)
		}
		if len(parsed.Steps) == 0 {
			t.Errorf("%s has no steps", file)
		}
		// Each step with a file has one "line" field, in step order.
		entries := lineEntry.FindAllSubmatchIndex(data, -1)
		var moves []move
		entry := 0
		for i, step := range parsed.Steps {
			if step.File == "" {
				continue
			}
			where := name + " step " + strconv.Itoa(i+1)
			if entry >= len(entries) {
				t.Fatalf("%s: no line field for the step", where)
			}
			span := entries[entry]
			entry++
			source, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(step.File)))
			if err != nil {
				t.Errorf("%s: %s does not exist", where, step.File)
				continue
			}
			match := anchor.FindStringSubmatch(step.Description)
			if match == nil {
				t.Errorf("%s: description names no symbol in backticks", where)
				continue
			}
			lines := strings.Split(string(source), "\n")
			found := symbolLine(lines, step.Line, match[1])
			switch {
			case found == 0:
				t.Errorf("%s: %s no longer contains %q", where, step.File, match[1])
			case found == step.Line:
			case !update:
				t.Errorf("%s: %q is at line %d of %s, not %d (run with UPDATE_TOURS=1)", where, match[1], found, step.File, step.Line)
			default:
				moves = append(moves, move{start: span[0], end: span[1], line: found})
			}
		}
		if len(moves) > 0 {
			if err := os.WriteFile(file, rewriteLines(data, moves), 0o644); err != nil {
				t.Fatal(err)
			}
			t.Logf("%s: moved %d step(s)", name, len(moves))
		}
	}
}

// symbolLine returns the 1-based line of the file a step should point at: the
// recorded line when it still contains symbol, otherwise the first line that does.
// It returns zero when the file no longer contains symbol at all.
func symbolLine(lines []string, recorded int, symbol string) int {
	if recorded >= 1 && recorded <= len(lines) && strings.Contains(lines[recorded-1], symbol) {
		return recorded
	}
	for i, line := range lines {
		if strings.Contains(line, symbol) {
			return i + 1
		}
	}
	return 0
}

// rewriteLines returns data with each move's "line" field set to its new line. The
// moves are in file order, and only the number changes.
func rewriteLines(data []byte, moves []move) []byte {
	var out bytes.Buffer
	prev := 0
	for _, m := range moves {
		out.Write(data[prev:m.start])
		fmt.Fprintf(&out, `"line": %d`, m.line)
		prev = m.end
	}
	out.Write(data[prev:])
	return out.Bytes()
}
