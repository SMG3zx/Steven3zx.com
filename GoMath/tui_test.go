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

func TestTUIStepsThroughRun(t *testing.T) {
	var m tea.Model = newModel()
	m = send(m, typed("(x+1)(x+2)"))
	m = send(m, key(tea.KeyEnter))

	m = send(m, key(tea.KeyRight))
	if got := m.(model).step; got != 1 {
		t.Errorf("after right, step = %d, want 1", got)
	}
	m = send(m, key(tea.KeyLeft))
	m = send(m, key(tea.KeyLeft))
	if got := m.(model).step; got != 0 {
		t.Errorf("step went below zero: %d", got)
	}
	last := len(m.(model).report.Steps) - 1
	for i := 0; i < last+5; i++ {
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
	if view := got.View(); !strings.Contains(view, "matches the worksheet answer") {
		t.Errorf("step view does not show the worksheet check:\n%s", view)
	}
}
