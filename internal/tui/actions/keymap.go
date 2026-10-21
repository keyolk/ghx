package actions

import (
	"errors"
	"path/filepath"
	"strings"

	"github.com/keyolk/tuikit/keys"
	"github.com/keyolk/tuikit/link"

	"github.com/keyolk/ghx/internal/tui"
)

// keymap.go is the actions panel's binding table: it drives handleKey and the
// footer, so the two cannot disagree. The list keys come from tui so `down`,
// `search` and `quit` match the PR app and the admin panel.

const (
	ctxList      keys.Ctx = "list"
	ctxRuns      keys.Ctx = "runs"
	ctxWorkflows keys.Ctx = "workflows"
	ctxLogs      keys.Ctx = "logs"
	ctxSearch    keys.Ctx = "search"
)

var (
	stackRuns      = []keys.Ctx{ctxRuns, ctxList}
	stackWorkflows = []keys.Ctx{ctxWorkflows, ctxList}
	stackLogs      = []keys.Ctx{ctxLogs}
	stackSearch    = []keys.Ctx{ctxSearch}
	allStacks      = [][]keys.Ctx{stackRuns, stackWorkflows, stackLogs, stackSearch}
)

func defaultKeymap() keys.Keymap {
	bs := tui.ListBindings(ctxList)
	bs = append(bs, tui.SearchBindings(ctxSearch)...)
	bs = append(bs,
		keys.Binding{Action: "clear", Keys: []string{"esc"}, Ctx: ctxList, Help: "clear the search"},
		keys.Binding{Action: "tab", Keys: []string{"1", "2"}, KeyLabel: "1-2", Ctx: ctxList, Help: "tab: 1 runs · 2 workflows"},

		keys.Binding{Action: "logs", Keys: []string{"enter"}, Ctx: ctxRuns, Help: "logs", Footer: 4},
		keys.Binding{Action: "rerun", Keys: []string{"r"}, Ctx: ctxRuns, Help: "rerun", Footer: 5},
		keys.Binding{Action: "rerunfailed", Keys: []string{"R"}, Ctx: ctxRuns, Help: "rerun failed", Footer: 6},
		keys.Binding{Action: "cancel", Keys: []string{"c"}, Ctx: ctxRuns, Help: "cancel", Footer: 7},
		keys.Binding{Action: "failed", Keys: []string{"f"}, Ctx: ctxRuns, Help: "failed only",
			Labels: map[string]string{"on": "all runs"}, Short: "failed", Footer: 8},
		// o opens the selected run's page (E11). Workflows have no URL in the
		// data gh returns, so the binding is runs-only.
		runsLink(),

		keys.Binding{Action: "enable", Keys: []string{"e"}, Ctx: ctxWorkflows, Help: "enable", Footer: 4},
		keys.Binding{Action: "disable", Keys: []string{"d"}, Ctx: ctxWorkflows, Help: "disable", Footer: 5},

		keys.Binding{Action: "back", Keys: []string{"esc"}, Ctx: ctxLogs, Help: "back", Footer: 1},
		keys.Binding{Action: "down", Keys: []string{"j", "down"}, Ctx: ctxLogs, Help: "scroll down", Footer: 2, FooterGroup: "scroll"},
		keys.Binding{Action: "up", Keys: []string{"k", "up"}, Ctx: ctxLogs, Help: "scroll up", Footer: 2, FooterGroup: "scroll"},
		keys.Binding{Action: "quit", Keys: []string{"q", "ctrl+c"}, Ctx: ctxLogs, Help: "quit", Pinned: true, Footer: 99},
	)
	return keys.New(bs...)
}

func runsLink() keys.Binding {
	b := link.Binding(ctxRuns)
	b.Short, b.Footer = "open", 9
	return b
}

// defaultKeys is the built-in table; an App without a loaded one uses it.
var defaultKeys = defaultKeymap()

// LoadKeymap is the built-in table with the user's overrides from
// keys-actions.yaml beside the PR app's keys.yaml (keys.ConfigPath("ghx",
// "actions")). A file of its own rather than a share of keys.yaml: the panels
// reuse action names (cancel, remove, logs) with meanings of their own, and
// keys.yaml rejects names the PR app does not bind. A bad file is reported and
// the defaults are kept, as for the PR app.
func LoadKeymap() (keys.Keymap, error) {
	path := keys.ConfigPath("ghx", "actions")
	km, err := defaultKeys.LoadOverrides(path, allStacks...)
	if err != nil {
		// The error leads with the full path, which can fill the one-line
		// toast before the part that says what is wrong; the file name is
		// enough to find it.
		return km, errors.New(strings.ReplaceAll(err.Error(), path, filepath.Base(path)))
	}
	return km, nil
}

// keymap is the table the App dispatches and draws with.
func (a *App) keymap() keys.Keymap { return a.km.Or(defaultKeys) }

// UseKeymap installs a loaded table; err, from LoadKeymap, is shown once the
// panel is up.
func (a *App) UseKeymap(km keys.Keymap, err error) *App {
	a.km, a.keymapErr = km, err
	return a
}

func (a *App) stack() []keys.Ctx {
	switch {
	case a.searching:
		return stackSearch
	case a.logView != "":
		return stackLogs
	case a.active == tabWorkflows:
		return stackWorkflows
	}
	return stackRuns
}

func (a *App) keyOpts() keys.Opts {
	return keys.Opts{State: func(action string) string {
		if action == "failed" && a.failedOnly {
			return "on"
		}
		return ""
	}}
}
