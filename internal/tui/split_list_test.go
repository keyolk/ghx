package tui

import (
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
	"github.com/keyolk/tuikit/keys"
	"github.com/keyolk/tuikit/split"
)

// Every position below is read from the rendered View: the divider is the
// column every body row draws a divider glyph in, and rows are found by their
// text. Nothing is recomputed with the layout arithmetic under test.

// cellsOf splits a stripped screen line into one string per terminal cell.
func cellsOf(line string) []string {
	var out []string
	for _, r := range line {
		w := ansi.StringWidth(string(r))
		out = append(out, string(r))
		for i := 1; i < w; i++ {
			out = append(out, "")
		}
	}
	return out
}

// dividerX is the column of the list|preview divider in the current frame, or
// -1 when no column holds a divider glyph on every body row.
func dividerX(a *App) int {
	lines := strings.Split(a.View(), "\n")
	// Body rows: below the title and tab strip, above the footer.
	rows := lines[bodyTop : len(lines)-1]
	var cand []int
	for x, c := range cellsOf(ansi.Strip(rows[0])) {
		if c == "│" || c == "┃" {
			cand = append(cand, x)
		}
	}
	for _, x := range cand {
		all := true
		for _, row := range rows[:ContentHeight(a.height-1)] {
			cs := cellsOf(ansi.Strip(row))
			if x >= len(cs) || (cs[x] != "│" && cs[x] != "┃") {
				all = false
				break
			}
		}
		if all {
			return x
		}
	}
	return -1
}

func mustDivider(t *testing.T, a *App) int {
	t.Helper()
	x := dividerX(a)
	if x < 0 {
		t.Fatalf("no divider on screen:\n%s", ansi.Strip(a.View()))
	}
	return x
}

func splitApp(t *testing.T) *App {
	t.Helper()
	a := mouseApp(t)
	a.list.pane.Show = true
	a.View()
	return a
}

func splitKey(a *App, k string) { a.Update(keyMsg(k)) }

func TestDraggingTheDividerMovesTheBoundary(t *testing.T) {
	c := captureCopies(t)
	a := splitApp(t)
	x0 := mustDivider(t, a)
	y := bodyTop + 2
	press(a, x0, y)
	a.View()
	if !a.list.pane.Split.Dragging() {
		t.Fatal("a press on the divider starts a drag")
	}
	if !strings.Contains(screenLine(a, y), "┃") {
		t.Error("the divider is drawn heavy while dragged")
	}
	motion(a, x0+12, y)
	release(a, x0+12, y)
	if got := dividerX(a); got != x0+12 {
		t.Fatalf("divider at %d after dragging from %d to %d", got, x0, x0+12)
	}
	if len(*c) != 0 {
		t.Fatalf("a divider drag copied text: %q", *c)
	}

	// The cells the list gained are the list's for clicks and selection.
	_, rowY := at(t, a, "#3")
	click(a, x0+6, rowY)
	if a.list.pane.Focus {
		t.Fatal("a click left of the moved divider hit the preview")
	}
	if got := cursorNumber(t, a); got != 3 {
		t.Fatalf("a click in the widened list selected #%d, want #3", got)
	}
	for _, p := range a.listPanes() {
		if p.ID == paneList && p.Rect.X+p.Rect.W != x0+12 {
			t.Errorf("selection list pane ends at %d, divider is at %d", p.Rect.X+p.Rect.W, x0+12)
		}
		if p.ID == panePreview && p.Rect.X != x0+13 {
			t.Errorf("selection preview pane starts at %d, want %d", p.Rect.X, x0+13)
		}
	}
}

func TestDividerPressNeverSelectsButAPanePressStill(t *testing.T) {
	c := captureCopies(t)
	a := splitApp(t)
	x0 := mustDivider(t, a)
	_, y := at(t, a, "#2")
	press(a, x0, y)
	motion(a, x0, y+3)
	release(a, x0, y+3)
	if len(*c) != 0 || a.sel.Active() {
		t.Fatalf("a press on the divider started a text selection (copies %q)", *c)
	}

	x, y := at(t, a, "#2")
	if cmd := drag(a, x, y, x+5, y+1); cmd == nil || !strings.Contains(lastCopy(c), "#2") {
		t.Fatalf("a drag in the list no longer selects text: %q", lastCopy(c))
	}
}

func TestDoubleClickOnTheDividerResets(t *testing.T) {
	a := splitApp(t)
	x0 := mustDivider(t, a)
	splitKey(a, ">")
	splitKey(a, ">")
	moved := mustDivider(t, a)
	if moved <= x0 {
		t.Fatal("setup: > should move the divider")
	}
	click(a, moved, bodyTop+1)
	release(a, moved, bodyTop+1)
	click(a, moved, bodyTop+1)
	if got := mustDivider(t, a); got != x0 {
		t.Fatalf("double click left the divider at %d, want the default %d", got, x0)
	}
}

func TestSplitKeysMoveTheBoundary(t *testing.T) {
	a := splitApp(t)
	x0 := mustDivider(t, a)
	splitKey(a, ">")
	grown := mustDivider(t, a)
	if grown <= x0 {
		t.Fatalf("> left the divider at %d (was %d)", grown, x0)
	}
	splitKey(a, "<")
	splitKey(a, "<")
	if got := mustDivider(t, a); got >= grown || got >= x0 {
		t.Fatalf("< < left the divider at %d (default %d, grown %d)", got, x0, grown)
	}
	splitKey(a, "=")
	if got := mustDivider(t, a); got != x0 {
		t.Fatalf("= left the divider at %d, want %d", got, x0)
	}
	// [ and ] were the resize keys before; they still are.
	splitKey(a, "]")
	if got := mustDivider(t, a); got != grown {
		t.Fatalf("] left the divider at %d, want %d like >", got, grown)
	}
	splitKey(a, "[")
	if got := mustDivider(t, a); got != x0 {
		t.Fatalf("[ left the divider at %d, want %d", got, x0)
	}
}

func TestSplitKeysAreInFooterAndHelp(t *testing.T) {
	a := splitApp(t)
	a.width = 220
	a.list.resize(220, 30)
	lines := strings.Split(ansi.Strip(a.View()), "\n")
	if footer := lines[len(lines)-1]; !strings.Contains(footer, "resize") || !strings.Contains(footer, "<") {
		t.Errorf("footer has no resize hint: %q", footer)
	}
	// Tall enough for every help section to be drawn without scrolling.
	a.height = 200
	a.list.resize(220, 200)
	splitKey(a, "?")
	help := ansi.Strip(a.View())
	for _, want := range []string{"Layout", "grow the list pane", "shrink the list pane", "reset the list|preview split"} {
		if !strings.Contains(help, want) {
			t.Errorf("help is missing %q", want)
		}
	}
}

func TestSplitKeysDoNotConflict(t *testing.T) {
	km := DefaultKeymap()
	for _, c := range km.Conflicts(AllStacks()...) {
		t.Error(c)
	}
	// No other action in the list's stack answers these keys.
	for _, k := range []string{"<", ">", "=", "[", "]"} {
		b, ok := km.Resolve(stackList, keyMsg(k), keys.Opts{})
		if !ok || !strings.HasPrefix(b.Action, "split.") {
			t.Errorf("%q resolves to %q in the list", k, b.Action)
		}
	}
}

// The dead-zone class: presses past the limit must not bank, so the first
// press back moves the divider at once.
func TestGrowPastTheLimitThenOneShrinkMovesAtOnce(t *testing.T) {
	// At 60 columns the 30-column list minimum binds before the 25% bound
	// does: a ratio clamped only to the bounds would bank presses there.
	for _, w := range []int{120, 60} {
		a := splitApp(t)
		a.width = w
		a.list.resize(w, 30)
		limitsMoveAtOnce(t, a)
	}
}

func limitsMoveAtOnce(t *testing.T, a *App) {
	t.Helper()
	for range 30 {
		splitKey(a, ">")
	}
	top := mustDivider(t, a)
	splitKey(a, "<")
	if got := mustDivider(t, a); got >= top {
		t.Fatalf("one < after the limit left the divider at %d (limit %d)", got, top)
	}
	for range 30 {
		splitKey(a, "<")
	}
	bottom := mustDivider(t, a)
	if bottom < 30 {
		t.Fatalf("the list shrank to %d columns, under its 30-column minimum", bottom)
	}
	splitKey(a, ">")
	if got := mustDivider(t, a); got <= bottom {
		t.Fatalf("one > after the lower limit left the divider at %d (limit %d)", got, bottom)
	}
}

func TestSplitRatioSurvivesARestart(t *testing.T) {
	a := splitApp(t)
	splitKey(a, ">")
	splitKey(a, ">")
	want := mustDivider(t, a)
	runQuit(t, a)

	s, err := LoadUIState()
	if err != nil {
		t.Fatal(err)
	}
	b := splitApp(t)
	b.RestoreUIState(s)
	if got := mustDivider(t, b); got != want {
		t.Fatalf("restored divider at %d, want %d", got, want)
	}
}

func writeUIJSON(t *testing.T, body string) {
	t.Helper()
	dir := filepath.Join(os.Getenv("XDG_STATE_HOME"), prefsApp)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "ui.json"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestGarbageSplitRatioFallsBackToTheDefault(t *testing.T) {
	ref := splitApp(t)
	def := mustDivider(t, ref)
	for _, body := range []string{`{"split":{"ratio":40}}`, `{"split":{"ratio":-0.3}}`, `{"split":{"ratio":1}}`} {
		a := splitApp(t)
		writeUIJSON(t, body)
		s, err := LoadUIState()
		if err != nil {
			t.Fatal(err)
		}
		a.RestoreUIState(s)
		if got := mustDivider(t, a); got != def {
			t.Errorf("%s: divider at %d, want the default %d", body, got, def)
		}
	}
}

func TestOldPercentIsMigratedOnce(t *testing.T) {
	a := splitApp(t)
	writeUIJSON(t, `{"split_ratio": 60}`)
	s, err := LoadUIState()
	if err != nil {
		t.Fatal(err)
	}
	a.RestoreUIState(s)
	if r := a.list.pane.Split.Ratio(); math.Abs(r-0.6) > 1e-9 {
		t.Fatalf("migrated ratio %v, want 0.6", r)
	}
	// The first message rewrites the file in the new form.
	if _, cmd := a.Update(tea.WindowSizeMsg{Width: 120, Height: 30}); cmd == nil {
		t.Fatal("a migrated state was not scheduled for saving")
	}
	runQuit(t, a)
	s, err = LoadUIState()
	if err != nil {
		t.Fatal(err)
	}
	if s.SplitRatio != 0 || math.Abs(s.Split.Ratio-0.6) > 1e-9 {
		t.Fatalf("saved %+v, want split 0.6 and no split_ratio", s)
	}
	// A legacy percent out of the old range is clamped as before.
	b := splitApp(t)
	b.RestoreUIState(UIState{SplitRatio: 90})
	if r := b.list.pane.Split.Ratio(); r != 0.75 {
		t.Fatalf("legacy 90 restored as %v, want 0.75", r)
	}
}

func TestNarrowTerminalsKeepTheCollapseRuleAndFit(t *testing.T) {
	for _, tc := range []struct {
		w, h  int
		split bool
	}{{60, 30, true}, {80, 24, true}, {39, 30, false}} {
		a := splitApp(t)
		a.width, a.height = tc.w, tc.h
		a.list.resize(tc.w, tc.h)
		for range 10 {
			splitKey(a, ">")
		}
		for i, line := range strings.Split(a.View(), "\n") {
			if w := ansi.StringWidth(line); w > tc.w {
				t.Errorf("%dx%d: line %d is %d cells wide", tc.w, tc.h, i, w)
			}
		}
		if got := dividerX(a) >= 0; got != tc.split {
			t.Errorf("%dx%d: divider drawn = %v, want %v", tc.w, tc.h, got, tc.split)
		}
		if tc.split && tc.w-mustDivider(t, a)-1 < 1 {
			t.Errorf("%dx%d: the preview lost its last column", tc.w, tc.h)
		}
	}
}

func TestSplitBindingsComeFromTheKit(t *testing.T) {
	var actions []string
	for _, b := range splitBindings() {
		actions = append(actions, b.Action)
	}
	want := []string{split.ActionGrow, split.ActionShrink, split.ActionReset}
	if strings.Join(actions, ",") != strings.Join(want, ",") {
		t.Fatalf("split actions %v, want %v", actions, want)
	}
}
