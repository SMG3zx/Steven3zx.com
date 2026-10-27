package main

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func send(m tea.Model, msg tea.Msg) tea.Model {
	updated, _ := m.Update(msg)
	return updated
}

func typed(s string) tea.Msg {
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
}

func key(k tea.KeyType) tea.Msg {
	return tea.KeyMsg{Type: k}
}

// settle delivers animation frames until the current step has finished typing out.
func settle(t *testing.T, m tea.Model) tea.Model {
	t.Helper()
	for i := 0; i < 1000; i++ {
		got := m.(model)
		if got.revealed >= got.currentLen() {
			return m
		}
		m = send(m, frameMsg{})
	}
	t.Fatal("reveal never finished")
	return m
}

func TestTUIRunsTypedExpression(t *testing.T) {
	var m tea.Model = newModel()
	m = send(m, typed("x+1"))
	m = send(m, key(tea.KeyEnter))

	got := m.(model)
	if got.mode != modeSteps {
		t.Fatalf("mode = %v, want modeSteps", got.mode)
	}
	if got.report.Result != "x + 1" {
		t.Errorf("Result = %q, want %q", got.report.Result, "x + 1")
	}
}

func TestTUIRevealAnimatesOverFrames(t *testing.T) {
	var m tea.Model = newModel()
	m = send(m, typed("(x+1)(x+2)"))
	m = send(m, key(tea.KeyEnter))

	if got := m.(model); got.revealed != 0 || got.currentLen() == 0 {
		t.Fatalf("new step should start hidden: revealed=%d len=%d", got.revealed, got.currentLen())
	}

	frames := 0
	last := -1
	for {
		updated, cmd := m.Update(frameMsg{})
		m = updated
		frames++
		got := m.(model)
		if got.revealed <= last {
			t.Fatalf("reveal did not advance: %d after %d", got.revealed, last)
		}
		last = got.revealed
		if cmd == nil {
			break
		}
		if frames > 1000 {
			t.Fatal("reveal never stopped")
		}
	}
	if got := m.(model); got.revealed != got.currentLen() {
		t.Errorf("revealed = %d, want all %d runes", got.revealed, got.currentLen())
	}
	if frames < 2 {
		t.Errorf("reveal finished in %d frame; expected it to take several", frames)
	}
}

func TestTUIKeyFinishesRevealWithoutAdvancing(t *testing.T) {
	var m tea.Model = newModel()
	m = send(m, typed("(x+1)(x+2)"))
	m = send(m, key(tea.KeyEnter))

	m = send(m, key(tea.KeyRight))
	got := m.(model)
	if got.step != 0 {
		t.Errorf("right during reveal moved to step %d, want 0", got.step)
	}
	if got.revealed != got.currentLen() {
		t.Errorf("right during reveal left revealed=%d, want %d", got.revealed, got.currentLen())
	}

	m = send(m, key(tea.KeyRight))
	if got := m.(model).step; got != 1 {
		t.Errorf("second right gave step %d, want 1", got)
	}
}

func TestTUIStepsThroughRun(t *testing.T) {
	var m tea.Model = newModel()
	m = send(m, typed("(x+1)(x+2)"))
	m = send(m, key(tea.KeyEnter))

	m = settle(t, m)
	m = send(m, key(tea.KeyLeft))
	if got := m.(model).step; got != 0 {
		t.Errorf("left at the first step gave %d, want 0", got)
	}

	last := len(m.(model).report.Steps) - 1
	for i := 0; i < last+5; i++ {
		m = settle(t, m)
		m = send(m, key(tea.KeyRight))
	}
	if got := m.(model).step; got != last {
		t.Errorf("step = %d after running past the end, want %d", got, last)
	}
	m = send(m, key(tea.KeyEsc))
	if got := m.(model).mode; got != modeInput {
		t.Errorf("esc from steps gave mode %v, want modeInput", got)
	}
}

func TestTUITranscriptKeepsEarlierSteps(t *testing.T) {
	var m tea.Model = newModel()
	m = send(m, typed("(x+1)(x+2)"))
	m = send(m, key(tea.KeyEnter))
	for i := 0; i < 2; i++ {
		m = settle(t, m)
		m = send(m, key(tea.KeyRight))
	}
	m = settle(t, m)

	view := m.View()
	for _, want := range []string{"Read the input", "ASCII form", "Split into tokens"} {
		if !strings.Contains(view, want) {
			t.Errorf("view is missing earlier or current step %q:\n%s", want, view)
		}
	}
}

func TestTUIViewFitsWindowHeight(t *testing.T) {
	var m tea.Model = newModel()
	m = send(m, tea.WindowSizeMsg{Width: 80, Height: 14})
	m = send(m, typed("(x+4)(x^2-4x+16)"))
	m = send(m, key(tea.KeyEnter))
	for i := 0; i < 30; i++ {
		m = settle(t, m)
		m = send(m, key(tea.KeyRight))
	}
	m = settle(t, m)

	view := m.View()
	if lines := strings.Count(strings.TrimRight(view, "\n"), "\n") + 1; lines > 14 {
		t.Errorf("view is %d lines, want at most 14:\n%s", lines, view)
	}
	if !strings.Contains(view, m.(model).report.Steps[30].Title) {
		t.Errorf("current step is not in the view:\n%s", view)
	}
}

func TestTUIBankEntryIsCheckedAgainstWorksheet(t *testing.T) {
	var m tea.Model = newModel()
	m = send(m, key(tea.KeyCtrlB))
	if got := m.(model).mode; got != modeBank {
		t.Fatalf("ctrl+b gave mode %v, want modeBank", got)
	}
	m = send(m, key(tea.KeyEnter))

	got := m.(model)
	if !got.checked || !got.checkOK {
		t.Errorf("first bank entry was not checked and matched: checked=%v ok=%v", got.checked, got.checkOK)
	}
	m = settle(t, m)
	if view := m.View(); !strings.Contains(view, "matches the worksheet answer") {
		t.Errorf("step view does not show the worksheet check:\n%s", view)
	}
}
