package archtest

import (
	"encoding/json"
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

// anchor is the first backticked term in a step's description. The line the
// step points at must still contain it.
var anchor = regexp.MustCompile("`([^`]+)`")

// TestTourStepsPointAtTheirSymbols keeps the tours in .tours/ honest. A step
// fails when its file is gone, its line is past the end of the file, or the
// line no longer contains the symbol its description names.
func TestTourStepsPointAtTheirSymbols(t *testing.T) {
	root := filepath.Join("..", "..")
	tours, err := filepath.Glob(filepath.Join(root, ".tours", "*.tour"))
	if err != nil {
		t.Fatal(err)
	}
	if len(tours) == 0 {
		t.Fatal("no tours found in .tours/")
	}
	for _, file := range tours {
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
		for i, step := range parsed.Steps {
			where := filepath.Base(file) + " step " + strconv.Itoa(i+1)
			source, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(step.File)))
			if err != nil {
				t.Errorf("%s: %s does not exist", where, step.File)
				continue
			}
			lines := strings.Split(string(source), "\n")
			if step.Line < 1 || step.Line > len(lines) {
				t.Errorf("%s: line %d is outside %s (%d lines)", where, step.Line, step.File, len(lines))
				continue
			}
			match := anchor.FindStringSubmatch(step.Description)
			if match == nil {
				t.Errorf("%s: description names no symbol in backticks", where)
				continue
			}
			if !strings.Contains(lines[step.Line-1], match[1]) {
				t.Errorf("%s: %s line %d no longer contains %q", where, step.File, step.Line, match[1])
			}
		}
	}
}
