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

func TestStageTextIsTheResultOfTheStep(t *testing.T) {
	cases := []struct {
		step Step
		want string
	}{
		{Step{"Compute", "Add", "(x) + (1) = x + 1"}, "x + 1"},
		{Step{"Compute", "Raise to a power", "(x + 1)^2 = x^2 + 2x + 1"}, "x^2 + 2x + 1"},
		{Step{"Input", "ASCII form", "(x+1)^(2)"}, "(x+1)^(2)"},
		{Step{"Parse", "Pratt parser", "read x as an operand"}, "read x as an operand"},
	}
	for _, c := range cases {
		if got := stageText(c.step); got != c.want {
			t.Errorf("stageText(%q) = %q, want %q", c.step.Detail, got, c.want)
		}
	}
}

func TestTUIShowsOnlyTheCurrentStage(t *testing.T) {
	var m tea.Model = newModel()
	m = send(m, typed("(x+1)(x+2)"))
	m = send(m, key(tea.KeyEnter))
	for i := 0; i < 2; i++ {
		m = settle(t, m)
		m = send(m, key(tea.KeyRight))
	}
	m = settle(t, m)

	view := m.View()
	if !strings.Contains(view, "Split into tokens") {
		t.Errorf("view does not show the current step:\n%s", view)
	}
	if strings.Contains(view, "Read the input") {
		t.Errorf("view still shows an earlier step, which should be gone:\n%s", view)
	}
}

// parseStep finds the first parse step that takes op, and returns its index.
func parseStep(t *testing.T, rep Report, op string) int {
	t.Helper()
	for i := range rep.Steps {
		if ev, ok := rep.Scenes[i]; ok && ev.Kind == "take" && ev.Op == op {
			return i
		}
	}
	t.Fatalf("no parse step taking %q", op)
	return -1
}

func TestParseSceneShowsNestedCallsAndComparison(t *testing.T) {
	rep := Solve("1 + 2 * 3", OpSimplify)
	ev := rep.Scenes[parseStep(t, rep, "*")]

	scene := renderParseScene(ev, 70)
	if n := strings.Count(scene, "parse_expression()"); n != ev.Depth+1 {
		t.Errorf("scene has %d parse_expression frames, want %d:\n%s", n, ev.Depth+1, scene)
	}
	if want := "3 ≥ 2"; !strings.Contains(scene, want) {
		t.Errorf("scene is missing the comparison %q:\n%s", want, scene)
	}
	for _, label := range []string{"lhs", "op"} {
		if !strings.Contains(scene, label) {
			t.Errorf("scene is missing the %q label:\n%s", label, scene)
		}
	}
}

func TestTUIParseStepShowsTheScene(t *testing.T) {
	var m tea.Model = newModel()
	m = send(m, typed("1 + 2 * 3"))
	m = send(m, key(tea.KeyEnter))
	idx := parseStep(t, m.(model).report, "*")
	for i := 0; i < idx; i++ {
		m = settle(t, m)
		m = send(m, key(tea.KeyRight))
	}
	m = settle(t, m)

	if view := m.View(); !strings.Contains(view, "parse_expression()") {
		t.Errorf("parse step does not show the nested call frames:\n%s", view)
	}
}

func TestTUIViewFitsWindowHeight(t *testing.T) {
	var m tea.Model = newModel()
	m = send(m, tea.WindowSizeMsg{Width: 80, Height: 26})
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

func TestTUISolvesTypedEquation(t *testing.T) {
	var m tea.Model = newModel()
	m = send(m, typed("5x^4 = 135x"))
	if !m.(model).avail[OpSolve] {
		t.Fatalf("solve is not lit for an equation: avail=%v", m.(model).avail)
	}
	m = send(m, key(tea.KeyCtrlS))

	got := m.(model)
	if got.mode != modeSteps || got.op != OpSolve {
		t.Fatalf("ctrl+s gave mode %v op %v, want modeSteps and solve", got.mode, got.op)
	}
	if got.report.Result != "{0, 3}" {
		t.Errorf("Result = %q, want %q", got.report.Result, "{0, 3}")
	}
	last := len(got.report.Steps) - 1
	for i := 0; i < last; i++ {
		m = settle(t, m)
		m = send(m, key(tea.KeyRight))
	}
	m = settle(t, m)
	if view := m.View(); !strings.Contains(view, "result: {0, 3}") {
		t.Errorf("final step does not show the solution:\n%s", view)
	}
}

func TestTUIFooterLightsKeysAsInputIsAnalysed(t *testing.T) {
	var m tea.Model = newModel()
	if got := m.(model).avail; len(got) != 0 {
		t.Fatalf("empty input should light nothing, got %v", got)
	}

	m = send(m, typed("5x^4 = 135x"))
	avail := m.(model).avail
	if !avail[OpSolve] || avail[OpFactor] || avail[OpSimplify] {
		t.Errorf("equation: solve=%v factor=%v simplify=%v, want true, false, false",
			avail[OpSolve], avail[OpFactor], avail[OpSimplify])
	}

	for range "5x^4 = 135x" {
		m = send(m, key(tea.KeyBackspace))
	}
	m = send(m, typed("x^2 - 5x + 6"))
	avail = m.(model).avail
	if !avail[OpFactor] || !avail[OpSimplify] || avail[OpSolve] {
		t.Errorf("polynomial: factor=%v simplify=%v solve=%v, want true, true, false",
			avail[OpFactor], avail[OpSimplify], avail[OpSolve])
	}
	view := m.View()
	for _, want := range []string{"ctrl+f factor", "ctrl+s solve", "ctrl+o more operations"} {
		if !strings.Contains(view, want) {
			t.Errorf("footer is missing %q:\n%s", want, view)
		}
	}
}

func TestTUIPickRunsOperationWithoutKey(t *testing.T) {
	var m tea.Model = newModel()
	m = send(m, typed("2x + 4 > 10"))
	m = send(m, key(tea.KeyCtrlO))
	if got := m.(model).mode; got != modePick {
		t.Fatalf("ctrl+o gave mode %v, want modePick", got)
	}
	if view := m.View(); !strings.Contains(view, "inequality") {
		t.Errorf("picker does not list inequality:\n%s", view)
	}

	for i, op := range allOps {
		if op == OpInequality {
			for j := 0; j < i; j++ {
				m = send(m, key(tea.KeyDown))
			}
			break
		}
	}
	m = send(m, key(tea.KeyEnter))

	got := m.(model)
	if got.mode != modeSteps || got.op != OpInequality {
		t.Fatalf("enter gave mode %v op %v, want modeSteps and inequality", got.mode, got.op)
	}
	if got.report.Result != "(3, ∞)" {
		t.Errorf("Result = %q, want %q", got.report.Result, "(3, ∞)")
	}
}

func TestTUIPickEscReturnsToInput(t *testing.T) {
	var m tea.Model = newModel()
	m = send(m, typed("x+1"))
	m = send(m, key(tea.KeyCtrlO))
	m = send(m, key(tea.KeyEsc))
	if got := m.(model); got.mode != modeInput || got.input != "x+1" {
		t.Errorf("esc from pick gave mode %v input %q, want modeInput and x+1", got.mode, got.input)
	}
}

func TestEveryOperationHasAPickerEntry(t *testing.T) {
	if len(allOps) != int(OpLineForms)+1 {
		t.Fatalf("picker has %d operations, want %d", len(allOps), int(OpLineForms)+1)
	}
	for _, k := range inputKeys {
		if keyFor(k.op) != k.key {
			t.Errorf("keyFor(%v) = %q, want %q", k.op, keyFor(k.op), k.key)
		}
	}
}

func TestTUIAllLineFormsShowsEveryForm(t *testing.T) {
	var m tea.Model = newModel()
	m = send(m, typed("parallel to y = 3x through (2,-2)"))
	if !m.(model).avail[OpLineForms] {
		t.Fatalf("footer does not light ctrl+l for a line")
	}
	m = send(m, key(tea.KeyCtrlL))
	got := m.(model)
	if got.mode != modeSteps || got.op != OpLineForms {
		t.Fatalf("ctrl+l gave mode %v op %v, want modeSteps and all line forms", got.mode, got.op)
	}
	for len(got.report.Steps) > 0 && got.step < len(got.report.Steps)-1 {
		m = send(m, key(tea.KeyRight))
		got = m.(model)
	}
	for got.animating() {
		m = send(m, key(tea.KeyRight))
		got = m.(model)
	}
	view := m.View()
	for _, want := range []string{"Point-slope form:", "y + 2 = 3(x - 2)", "Slope-intercept form:", "y = 3x - 8", "Standard form:", "3x - y = 8"} {
		if !strings.Contains(view, want) {
			t.Errorf("result view is missing %q:\n%s", want, view)
		}
	}
}
