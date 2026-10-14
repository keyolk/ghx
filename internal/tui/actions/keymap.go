package actions

import (
	"github.com/keyolk/tuikit/keys"

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

		keys.Binding{Action: "enable", Keys: []string{"e"}, Ctx: ctxWorkflows, Help: "enable", Footer: 4},
		keys.Binding{Action: "disable", Keys: []string{"d"}, Ctx: ctxWorkflows, Help: "disable", Footer: 5},

		keys.Binding{Action: "back", Keys: []string{"esc"}, Ctx: ctxLogs, Help: "back", Footer: 1},
		keys.Binding{Action: "down", Keys: []string{"j", "down"}, Ctx: ctxLogs, Help: "scroll down", Footer: 2, FooterGroup: "scroll"},
		keys.Binding{Action: "up", Keys: []string{"k", "up"}, Ctx: ctxLogs, Help: "scroll up", Footer: 2, FooterGroup: "scroll"},
		keys.Binding{Action: "quit", Keys: []string{"q", "ctrl+c"}, Ctx: ctxLogs, Help: "quit", Pinned: true, Footer: 99},
	)
	return keys.New(bs...)
}

var keymap = defaultKeymap()

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
