package actions

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// isQuit reports whether a returned command is tea.Quit, by running it and
// checking for the QuitMsg. tea.Quit is a plain func, so it cannot be compared
// directly.
func isQuit(cmd tea.Cmd) bool {
	if cmd == nil {
		return false
	}
	_, ok := cmd().(tea.QuitMsg)
	return ok
}

// Under a Korean input source every shortcut arrives as a jamo. They have to
// map back to the Latin key at the same physical position, or the panel is
// unusable until the input source is switched.
func TestHangulShortcutsFireLikeLatin(t *testing.T) {
	// `ㄹ` is the physical `f`, which toggles the failed-only filter.
	a := testApp(t, 120, 40)
	if a.failedOnly {
		t.Fatal("fixture starts with the failed-only filter on")
	}
	a.handleKey(keyMsg("ㄹ"))
	if !a.failedOnly {
		t.Fatal("ㄹ (physical f) did not toggle the failed-only filter")
	}
}

func TestHangulQuitsLikeLatinQ(t *testing.T) {
	// `ㅂ` sits on the physical `q` key.
	a := testApp(t, 120, 40)
	if !isQuit(a.handleKey(keyMsg("ㅂ"))) {
		t.Fatal("ㅂ (physical q) did not quit")
	}
}

// A jamo typed into the search box is the query — a Korean workflow name would
// otherwise be unsearchable.
func TestSearchPromptKeepsHangulVerbatim(t *testing.T) {
	a := testApp(t, 120, 40)
	a.handleKey(keyMsg("/"))
	if !a.searching {
		t.Fatal("/ did not open the search prompt")
	}
	a.handleKey(keyMsg("ㅂ"))
	if a.query != "ㅂ" {
		t.Fatalf("query = %q, want the jamo verbatim", a.query)
	}
}

// The search prompt and the log viewer take keys before the global switch, so
// ctrl+c has to be handled above them.
func TestCtrlCQuitsFromEveryState(t *testing.T) {
	for _, tc := range []struct {
		name  string
		setup func(*App)
	}{
		{"run list", func(*App) {}},
		{"searching", func(a *App) { a.handleKey(keyMsg("/")) }},
		{"log viewer", func(a *App) { a.logView = "some log output" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := testApp(t, 120, 40)
			tc.setup(a)
			if !isQuit(a.handleKey(keyMsg("ctrl+c"))) {
				t.Fatalf("ctrl+c did not quit from %s", tc.name)
			}
		})
	}
}
