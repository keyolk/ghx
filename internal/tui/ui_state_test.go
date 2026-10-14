package tui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/keyolk/tuikit/prefs"

	"github.com/keyolk/ghx/internal/config"
	"github.com/keyolk/ghx/internal/gh"
)

// runQuit drives a quit to completion: the flush command writes ui.json and
// reports Saved{Final}, which Update turns into tea.Quit.
func runQuit(t *testing.T, a *App) {
	t.Helper()
	cmd := a.quit()
	if cmd == nil {
		t.Fatal("quit returned no command")
	}
	saved, ok := cmd().(prefs.Saved)
	if !ok || !saved.Final {
		t.Fatalf("quit did not flush the UI state first: %#v", saved)
	}
	if saved.Err != nil {
		t.Fatal(saved.Err)
	}
	if _, next := a.Update(saved); !isQuit(next) {
		t.Fatal("the final save did not quit")
	}
}

func twoSourceApp(t *testing.T) *App {
	t.Helper()
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	cfg := config.DefaultConfig()
	cfg.Sources = []config.SourceDef{{Name: "mine", Query: "author:@me"}, {Name: "reviews", Query: "review-requested:@me"}}
	a := NewApp(cfg, DefaultKeymap(), gh.NewClient(0))
	a.width, a.height = 120, 30
	a.list.fileCache = &prFileCache{dir: t.TempDir()}
	return a
}

// The tab, filter and split survive a restart, and nothing is written for a
// session that changed nothing.
func TestUIStateSurvivesARestart(t *testing.T) {
	a := twoSourceApp(t)
	if _, cmd := a.Update(tea.WindowSizeMsg{Width: 120, Height: 30}); cmd != nil {
		t.Error("an unchanged state scheduled a save")
	}
	a.Update(keyMsg("2"))
	a.Update(keyMsg("]"))
	a.list.applyQuery("flaky")
	a.Update(searchSubmitMsg{query: "flaky"})
	runQuit(t, a)

	s, err := LoadUIState()
	if err != nil {
		t.Fatal(err)
	}
	if s.Source != "reviews" || s.Query != "flaky" || s.SplitRatio != 45 {
		t.Fatalf("saved %+v, want reviews / flaky / 45", s)
	}

	b := twoSourceApp(t)
	b.RestoreUIState(s)
	if b.list.curTab != 1 || b.list.query != "flaky" || b.cfg.DiffSplitRatio != 45 {
		t.Errorf("restored tab %d query %q ratio %d", b.list.curTab, b.list.query, b.cfg.DiffSplitRatio)
	}
}

// Starting inside a checkout opens on that repository on purpose; yesterday's
// tab must not override it.
func TestDetectedRepoBeatsTheSavedTab(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	cfg := config.DefaultConfig()
	cfg.Sources = []config.SourceDef{{Name: "mine", Query: "author:@me"}}
	a := NewAppWithRepo(cfg, DefaultKeymap(), gh.NewClient(0), []string{"acme/one"})
	a.list.fileCache = &prFileCache{dir: t.TempDir()}
	before := a.list.curTab
	a.RestoreUIState(UIState{Source: "mine"})
	if a.list.curTab != before {
		t.Errorf("the saved tab replaced the detected repository's tab")
	}
}

// The detail tab a PR was last read in is where the next one opens.
func TestDetailTabIsRemembered(t *testing.T) {
	a := testApp(t, sampleRows)
	a.RestoreUIState(UIState{DetailTab: "Diff"})
	a.openSelected()
	if a.detail == nil || a.detail.activeTab != tabDiff {
		t.Fatalf("opened on %v, want the Diff tab", a.detail.activeTab)
	}
	a.handleKey(keyMsg("4"))
	if got := a.currentUIState().DetailTab; got != "Comments" {
		t.Errorf("DetailTab = %q after switching to Comments", got)
	}
}

// A second quit while the flush is in flight quits at once: a stuck disk must
// not trap the user.
func TestSecondQuitDoesNotWaitForTheWrite(t *testing.T) {
	a := testApp(t, sampleRows)
	a.quit()
	if cmd := a.quit(); cmd == nil {
		t.Fatal("second quit returned nothing")
	} else if _, ok := cmd().(tea.QuitMsg); !ok {
		t.Fatal("second quit did not quit immediately")
	}
}
