package admin

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
	// `ㅎ` is the physical `g`, which jumps to the top of the pane.
	a := testApp(t, 120, 40)
	a.pane.Cursor = 5
	a.handleKey(keyMsg("ㅎ"))
	if a.pane.Cursor != 0 {
		t.Fatalf("cursor = %d after ㅎ (physical g); want 0", a.pane.Cursor)
	}
}

func TestHangulQuitsLikeLatinQ(t *testing.T) {
	// `ㅂ` sits on the physical `q` key.
	a := testApp(t, 120, 40)
	if !isQuit(a.handleKey(keyMsg("ㅂ"))) {
		t.Fatal("ㅂ (physical q) did not quit")
	}
}

// The search box and the login prompt are text entry, so a jamo there is the
// intended input.
func TestTextInputsKeepHangulVerbatim(t *testing.T) {
	t.Run("search box", func(t *testing.T) {
		a := testApp(t, 120, 40)
		a.handleKey(keyMsg("/"))
		if !a.searching {
			t.Fatal("/ did not open the search prompt")
		}
		a.handleKey(keyMsg("ㅂ"))
		if a.query != "ㅂ" {
			t.Fatalf("query = %q, want the jamo verbatim", a.query)
		}
	})
	t.Run("login prompt", func(t *testing.T) {
		a := testApp(t, 120, 40)
		a.prompt = prompt{kind: promptLogin}
		a.handleKey(keyMsg("ㅂ"))
		if a.prompt.value != "ㅂ" {
			t.Fatalf("login value = %q, want the jamo verbatim", a.prompt.value)
		}
	})
}

// …but the permission prompt is a shortcut menu (tab/j/k/l/h), not text entry,
// so it must normalize even though it is the same prompt struct.
func TestPermissionPromptNormalizesHangul(t *testing.T) {
	a := testApp(t, 120, 40)
	a.prompt = prompt{
		kind:    promptPermission,
		choices: permissionChoices(),
		target:  "someone",
	}
	before := a.prompt.choice
	// `ㅓ` is the physical `j`, which advances the choice.
	a.handleKey(keyMsg("ㅓ"))
	if a.prompt.choice == before {
		t.Fatalf("choice stayed at %d; ㅓ (physical j) should have advanced it", before)
	}
}

// The confirmation, the write prompt, and the search box all take keys before
// the global switch, so ctrl+c has to be handled above them.
func TestCtrlCQuitsFromEveryState(t *testing.T) {
	for _, tc := range []struct {
		name  string
		setup func(*App)
	}{
		{"list", func(*App) {}},
		{"searching", func(a *App) { a.handleKey(keyMsg("/")) }},
		{"login prompt", func(a *App) { a.prompt = prompt{kind: promptLogin} }},
		{"permission prompt", func(a *App) {
			a.prompt = prompt{kind: promptPermission, choices: permissionChoices()}
		}},
		{"confirmation", func(a *App) { a.confirm = &confirmAction{} }},
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
