package actions

import (
	"fmt"
	"regexp"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/keyolk/ghx/internal/gh"
)

// The actions panel shared the PR app's frame-height bug and the admin panel's
// lack of a search: it wrote every row to the frame and let bubbletea clip it,
// which drops the top rows — the title and the tab strip.

func testApp(t *testing.T, w, h int) *App {
	t.Helper()
	a := NewApp(gh.NewClient(0), "acme/ops")
	a.width, a.height = w, h
	return a
}

func keyMsg(s string) tea.KeyMsg {
	switch s {
	case "enter":
		return tea.KeyMsg{Type: tea.KeyEnter}
	case "esc":
		return tea.KeyMsg{Type: tea.KeyEsc}
	case "ctrl+c":
		return tea.KeyMsg{Type: tea.KeyCtrlC}
	}
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
}

func typeKeys(a *App, s string) {
	for _, r := range s {
		a.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
}

var ansi = regexp.MustCompile(`\x1b\[[0-9;]*m`)

func plain(s string) string { return ansi.ReplaceAllString(s, "") }

func manyRuns(n int) []gh.Run {
	out := make([]gh.Run, 0, n)
	for i := 0; i < n; i++ {
		concl := "success"
		if i%3 == 0 {
			concl = "failure"
		}
		out = append(out, gh.Run{
			DatabaseID:   int64(1000 + i),
			WorkflowName: fmt.Sprintf("build-%03d", i),
			Status:       "completed",
			Conclusion:   concl,
			Event:        "push",
			HeadBranch:   "main",
		})
	}
	return out
}

func TestFrameFitsTerminalWithLongRunList(t *testing.T) {
	for _, size := range []struct{ w, h int }{{100, 20}, {160, 30}, {250, 62}, {80, 10}} {
		a := testApp(t, size.w, size.h)
		a.runs = manyRuns(150)

		frame := a.View()
		if got := len(strings.Split(frame, "\n")); got != size.h {
			t.Errorf("%dx%d: frame is %d rows, want exactly %d",
				size.w, size.h, got, size.h)
		}
		rows := strings.Split(frame, "\n")
		if !strings.Contains(plain(rows[0]), "ghx actions") {
			t.Errorf("%dx%d: first row is %q, want the title",
				size.w, size.h, plain(rows[0]))
		}
		if !strings.Contains(plain(rows[1]), "Runs") {
			t.Errorf("%dx%d: second row is %q, want the tab strip",
				size.w, size.h, plain(rows[1]))
		}
	}
}

// The log viewer takes over the whole screen and had the same defect: a log is
// thousands of lines, and each one wider than the pane costs an extra row.
func TestLogViewerFitsTerminal(t *testing.T) {
	a := testApp(t, 100, 24)
	lines := make([]string, 500)
	for i := range lines {
		lines[i] = strings.Repeat(fmt.Sprintf("line %d ", i), 40) // far wider than 100
	}
	a.logView = strings.Join(lines, "\n")

	frame := a.View()
	if got := len(strings.Split(frame, "\n")); got != 24 {
		t.Errorf("log viewer frame is %d rows, want 24", got)
	}
	for i, row := range strings.Split(frame, "\n") {
		if w := len(plain(row)); w > 100 {
			t.Errorf("row %d is %d cells wide, want at most 100", i, w)
			break
		}
	}
	if !strings.Contains(plain(strings.Split(frame, "\n")[0]), "run logs") {
		t.Error("the log viewer lost its header")
	}
}

func TestSearchFiltersRuns(t *testing.T) {
	a := testApp(t, 120, 20)
	a.runs = []gh.Run{
		{DatabaseID: 1, WorkflowName: "release", Conclusion: "success"},
		{DatabaseID: 2, WorkflowName: "unit-tests", Conclusion: "failure"},
	}

	a.handleKey(keyMsg("/"))
	typeKeys(a, "release")
	got := plain(a.View())
	if !strings.Contains(got, "release") {
		t.Errorf("the matching run is missing:\n%s", got)
	}
	if strings.Contains(got, "unit-tests") {
		t.Errorf("a non-matching run survived the filter:\n%s", got)
	}
	// r is "rerun" outside the prompt; inside it, it is a query character.
	if a.pane.Cursor != 0 {
		t.Errorf("typing moved the cursor to %d", a.pane.Cursor)
	}
}

// f narrows to failures and / then searches within them. The cursor indexes the
// result of both, so r and c act on the run the user is looking at.
func TestFailedFilterAndSearchCompose(t *testing.T) {
	a := testApp(t, 120, 20)
	a.runs = []gh.Run{
		{DatabaseID: 1, WorkflowName: "deploy", Conclusion: "success"},
		{DatabaseID: 2, WorkflowName: "deploy", Conclusion: "failure"},
		{DatabaseID: 3, WorkflowName: "lint", Conclusion: "failure"},
	}

	a.handleKey(keyMsg("f"))
	if got := len(a.visibleRuns()); got != 2 {
		t.Fatalf("f left %d runs, want the 2 failures", got)
	}
	a.handleKey(keyMsg("/"))
	typeKeys(a, "deploy")
	a.handleKey(keyMsg("enter"))

	runs := a.visibleRuns()
	if len(runs) != 1 || runs[0].DatabaseID != 2 {
		t.Fatalf("visible runs = %+v, want only the failed deploy (#2)", runs)
	}
	// Cursor 0 of the filtered list is run #2, not run #1 of the source.
	if got := runs[a.pane.Cursor].DatabaseID; got != 2 {
		t.Errorf("the cursor points at run #%d, want #2", got)
	}
}

func TestCursorStaysVisibleWhileScrollingRuns(t *testing.T) {
	a := testApp(t, 120, 20)
	a.runs = manyRuns(150)

	a.handleKey(keyMsg("G"))
	if !strings.Contains(plain(a.View()), "build-149") {
		t.Error("G left the last run off screen")
	}
	if got := len(strings.Split(a.View(), "\n")); got != 20 {
		t.Errorf("scrolling to the end made the frame %d rows", got)
	}
}
