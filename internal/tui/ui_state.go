package tui

import (
	"slices"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/keyolk/tuikit/prefs"
)

// ui_state.go keeps the layout a reviewer set up across restarts: the source
// tab, the filter, the split ratio, the detail tab. It is written to
// $XDG_STATE_HOME/ghx/ui.json through tuikit's prefs.Saver — debounced, off
// the UI loop, atomic, and flushed on quit — never from inside Update.

const prefsApp = "ghx"

// UIState is what survives a restart. Fields are names, not indices: the source
// tabs are led by whatever repositories detection found this time, so "tab 3"
// would point at a different queue on the next launch.
type UIState struct {
	Source     string   `json:"source,omitempty"`
	Query      string   `json:"query,omitempty"`
	Status     []string `json:"status,omitempty"`
	SplitRatio int      `json:"split_ratio,omitempty"`
	DetailTab  string   `json:"detail_tab,omitempty"`
}

// LoadUIState reads the saved UI state. A missing file is the zero state; a
// corrupt one is reported and ignored — losing a layout must not stop ghx.
func LoadUIState() (UIState, error) { return prefs.Load(prefsApp, UIState{}) }

// currentUIState reads the state off the models.
func (a *App) currentUIState() UIState {
	s := a.lastUI // keeps the detail tab while no PR is open
	s.SplitRatio = a.cfg.DiffSplitRatio
	if a.list != nil {
		s.Source = ""
		if a.list.curTab < len(a.list.sources) {
			s.Source = a.list.sources[a.list.curTab].Name
		}
		s.Query = a.list.query
		s.Status = nil
		for _, st := range prStatusOptions {
			if a.list.statusFilters[st] {
				s.Status = append(s.Status, string(st))
			}
		}
	}
	if a.detail != nil {
		s.DetailTab = detailTabNames[a.detail.activeTab]
	}
	return s
}

// saveUIState starts a debounced write when the state moved since the last
// one. Called once per message from Update, so no mutation site has to
// remember to save.
func (a *App) saveUIState() tea.Cmd {
	if a.quitting {
		return nil
	}
	s := a.currentUIState()
	if uiStateEqual(s, a.lastUI) {
		return nil
	}
	a.lastUI = s
	var cmd tea.Cmd
	a.saver, cmd = a.saver.Save(s)
	return cmd
}

func uiStateEqual(x, y UIState) bool {
	return x.Source == y.Source && x.Query == y.Query && slices.Equal(x.Status, y.Status) &&
		x.SplitRatio == y.SplitRatio && x.DetailTab == y.DetailTab
}

// RestoreUIState applies a saved state before the program starts.
//
// The source tab is restored only when detection found nothing: starting ghx
// inside a checkout opens on that repository's PRs on purpose, and a tab saved
// from yesterday's session must not override where the user is now.
func (a *App) RestoreUIState(s UIState) {
	if s.SplitRatio > 0 {
		a.cfg.DiffSplitRatio = clamp(s.SplitRatio, 25, 75)
	}
	if a.list != nil {
		if len(a.list.detectedRepos) == 0 && s.Source != "" {
			for i, src := range a.list.sources {
				if src.Name == s.Source {
					a.list.curTab = i
					break
				}
			}
		}
		a.list.query = s.Query
		clear(a.list.statusFilters)
		for _, st := range s.Status {
			if slices.Contains(prStatusOptions, prStatus(st)) {
				a.list.statusFilters[prStatus(st)] = true
			}
		}
		a.list.syncListItems()
	}
	// The restored state is the baseline: writing it straight back would be a
	// write that changes nothing.
	a.lastUI = a.currentUIState()
	a.lastUI.DetailTab = s.DetailTab
}

// preferredDetailTab is the tab a newly opened PR starts on.
func (a *App) preferredDetailTab() (detailTabKind, bool) {
	for tab, name := range detailTabNames {
		if name == a.lastUI.DetailTab && name != "" {
			return tab, true
		}
	}
	return 0, false
}

// StartupWarning shows a problem found before the program started — a bad
// keys.yaml, an unreadable ui.json — as an error toast, which stays until a
// key: the user has to see why their keys or layout did not apply.
func (a *App) StartupWarning(err error) {
	a.setErrorToast(errorStyle.Render("warning: ") + err.Error())
}
