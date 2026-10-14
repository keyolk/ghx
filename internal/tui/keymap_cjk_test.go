package tui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/keyolk/tuikit/prefs"
)

// isQuit reports whether a returned command quits, by running it. Quitting
// first flushes the UI state (prefs.Saver.Flush) and quits on the final
// Saved, so that message counts as quitting too. tea.Quit is a plain func, so
// it cannot be compared directly.
func isQuit(cmd tea.Cmd) bool {
	if cmd == nil {
		return false
	}
	switch msg := cmd().(type) {
	case tea.QuitMsg:
		return true
	case prefs.Saved:
		return msg.Final
	}
	return false
}

// --- through the real dispatcher --------------------------------------------

// Under a Korean input source every shortcut arrives as a jamo. They have to
// map back, or the app is unusable until the input source is switched.
func TestHangulShortcutsFireLikeLatin(t *testing.T) {
	t.Run("help overlay", func(t *testing.T) {
		// `?` is punctuation and unaffected, so close the overlay with the
		// jamo instead: `ㅂ` is the physical `q`, one of the three keys the
		// help overlay accepts.
		a := testApp(t, sampleRows)
		a.helpOpen = true
		a.handleKey(keyMsg("ㅂ"))
		if a.helpOpen {
			t.Fatal("ㅂ (physical q) did not close the help overlay")
		}
	})
	t.Run("status filter", func(t *testing.T) {
		// `ㄹ` is the physical `f`, which opens the status filter.
		a := testApp(t, sampleRows)
		a.handleKey(keyMsg("ㄹ"))
		if a.statusFilter == nil {
			t.Fatal("ㄹ (physical f) did not open the status filter")
		}
	})
	t.Run("repo picker", func(t *testing.T) {
		// `ㄷ` is the physical `e`, which opens the repo picker.
		a := testApp(t, sampleRows)
		a.handleKey(keyMsg("ㄷ"))
		if a.repos == nil {
			t.Fatal("ㄷ (physical e) did not open the repo picker")
		}
	})
}

// A jamo typed into a text field is the intended input — a Korean review
// comment or search term must survive verbatim.
func TestTextInputsKeepHangulVerbatim(t *testing.T) {
	t.Run("search box", func(t *testing.T) {
		a := testApp(t, sampleRows)
		a.search.open("")
		a.handleKey(keyMsg("ㅂ"))
		if a.search.query != "ㅂ" {
			t.Fatalf("search query = %q, want the jamo verbatim", a.search.query)
		}
	})
	t.Run("palette", func(t *testing.T) {
		a := testApp(t, sampleRows)
		a.palette.open()
		a.handleKey(keyMsg("ㅂ"))
		if a.palette.input != "ㅂ" {
			t.Fatalf("palette input = %q, want the jamo verbatim", a.palette.input)
		}
	})
	t.Run("repo picker query", func(t *testing.T) {
		a := testApp(t, sampleRows)
		a.handleKey(keyMsg("e"))
		if a.repos == nil {
			t.Fatal("e did not open the repo picker")
		}
		a.handleKey(keyMsg("ㅁ"))
		if a.repos.query != "ㅁ" {
			t.Fatalf("repo query = %q, want the jamo verbatim", a.repos.query)
		}
	})
}

// --- ctrl+c ------------------------------------------------------------------

// The composer and every prompt take keys before the global switch, so ctrl+c
// has to be handled above them or there is no exit that does not depend on
// their own bindings.
func TestCtrlCQuitsFromEveryState(t *testing.T) {
	for _, tc := range []struct {
		name  string
		setup func(*App)
	}{
		{"pr list", func(*App) {}},
		{"help overlay", func(a *App) { a.helpOpen = true }},
		{"search box", func(a *App) { a.search.open("") }},
		{"palette", func(a *App) { a.palette.open() }},
		{"status filter", func(a *App) { a.handleKey(keyMsg("f")) }},
		{"repo picker", func(a *App) { a.handleKey(keyMsg("e")) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := testApp(t, sampleRows)
			tc.setup(a)
			if !isQuit(a.handleKey(keyMsg("ctrl+c"))) {
				t.Fatalf("ctrl+c did not quit from %s", tc.name)
			}
		})
	}
}

// esc must still back out rather than quit, so the two exits stay distinct.
func TestEscStillClosesThePaletteWithoutQuitting(t *testing.T) {
	a := testApp(t, sampleRows)
	a.palette.open()
	if isQuit(a.handleKey(keyMsg("esc"))) {
		t.Fatal("esc quit the program; it should only close the palette")
	}
	if a.palette.active {
		t.Fatal("esc did not close the palette")
	}
}
