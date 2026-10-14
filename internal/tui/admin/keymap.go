package admin

import (
	"github.com/keyolk/tuikit/keys"

	"github.com/keyolk/ghx/internal/tui"
)

// keymap.go is the admin panel's binding table: it drives handleKey, the
// footer and the prompt hints. The list keys come from tui so `down`, `search`
// and `quit` match the PR app and the actions panel.
//
// The write keys live in per-category contexts in front of the list, so they
// exist only where they do something — and Enabled turns them off while the
// pane is loading or showing an error, where there is no row to act on and,
// for a 403, no permission to act with.

const (
	ctxList       keys.Ctx = "list"
	ctxCollab     keys.Ctx = "collaborators"
	ctxTeams      keys.Ctx = "teams"
	ctxMembers    keys.Ctx = "members"
	ctxSearch     keys.Ctx = "search"
	ctxLogin      keys.Ctx = "login"
	ctxPermission keys.Ctx = "permission"
	ctxConfirm    keys.Ctx = "confirm"
)

var (
	stackList       = []keys.Ctx{ctxList}
	stackCollab     = []keys.Ctx{ctxCollab, ctxList}
	stackTeams      = []keys.Ctx{ctxTeams, ctxList}
	stackMembers    = []keys.Ctx{ctxMembers, ctxList}
	stackSearch     = []keys.Ctx{ctxSearch}
	stackLogin      = []keys.Ctx{ctxLogin}
	stackPermission = []keys.Ctx{ctxPermission}
	stackConfirm    = []keys.Ctx{ctxConfirm}
	allStacks       = [][]keys.Ctx{stackList, stackCollab, stackTeams, stackMembers, stackSearch, stackLogin, stackPermission, stackConfirm}
)

func defaultKeymap() keys.Keymap {
	bs := tui.ListBindings(ctxList)
	bs = append(bs, tui.SearchBindings(ctxSearch)...)
	bs = append(bs,
		keys.Binding{Action: "category", Keys: []string{"1", "2", "3", "4", "5", "6", "7"}, KeyLabel: "1-7", Ctx: ctxList, Help: "category", Footer: 1},
		// One narrowing layer at a time: the filter first, then the drill-down.
		keys.Binding{Action: "clear", Keys: []string{"esc"}, Ctx: ctxList, Help: "clear the search, then leave the team",
			Labels: map[string]string{"team": "back to teams"}, Footer: 20},

		keys.Binding{Action: "add", Keys: []string{"a"}, Ctx: ctxCollab, Help: "add", Footer: 4},
		keys.Binding{Action: "permission", Keys: []string{"p"}, Ctx: ctxCollab, Help: "permission", Footer: 5},
		keys.Binding{Action: "remove", Keys: []string{"d"}, Ctx: ctxCollab, Help: "remove", Footer: 6},

		keys.Binding{Action: "members", Keys: []string{"enter"}, Ctx: ctxTeams, Help: "members", Footer: 4},
		keys.Binding{Action: "permission", Keys: []string{"p"}, Ctx: ctxTeams, Help: "permission", Footer: 5},
		keys.Binding{Action: "remove", Keys: []string{"d"}, Ctx: ctxTeams, Help: "revoke", Footer: 6},

		keys.Binding{Action: "add", Keys: []string{"a"}, Ctx: ctxMembers, Help: "add member", Footer: 4},
		keys.Binding{Action: "remove", Keys: []string{"d"}, Ctx: ctxMembers, Help: "remove", Footer: 5},

		keys.Binding{Action: "next", Keys: []string{"enter"}, Ctx: ctxLogin, Help: "next", Footer: 1},
		keys.Binding{Action: "cancel", Keys: []string{"esc"}, Ctx: ctxLogin, Help: "cancel", Footer: 2},
		keys.Binding{Action: "erase", Keys: []string{"backspace"}, Ctx: ctxLogin, Help: "erase a character"},

		keys.Binding{Action: "next", Keys: []string{"tab", "j", "down", "right", "l"}, Ctx: ctxPermission, Help: "next", Footer: 1},
		keys.Binding{Action: "prev", Keys: []string{"shift+tab", "k", "up", "left", "h"}, Ctx: ctxPermission, Help: "previous"},
		keys.Binding{Action: "apply", Keys: []string{"enter"}, Ctx: ctxPermission, Help: "apply", Footer: 2},
		keys.Binding{Action: "cancel", Keys: []string{"esc"}, Ctx: ctxPermission, Help: "cancel", Footer: 3},

		// Anything but y is not consent (TestOnlyYConfirmsAWrite).
		keys.Binding{Action: "yes", Keys: []string{"y", "Y"}, Ctx: ctxConfirm, Help: "yes", Footer: 1},
		keys.Binding{Action: "no", Keys: []string{"n", "N", "esc"}, Ctx: ctxConfirm, Help: "no", Footer: 2},
	)
	return keys.New(bs...)
}

var keymap = defaultKeymap()

func (a *App) stack() []keys.Ctx {
	switch {
	case a.teamSlug != "":
		return stackMembers
	case a.active == catCollaborators:
		return stackCollab
	case a.active == catTeams:
		return stackTeams
	}
	return stackList
}

func (a *App) keyOpts() keys.Opts {
	return keys.Opts{
		Enabled: func(action string) bool {
			switch action {
			case "add", "permission", "remove", "members":
				return !a.loading && a.err == nil && !a.memberBusy
			}
			return true
		},
		Visible: func(action string) bool {
			switch action {
			case "permission", "remove", "members":
				// Row keys only where there is a row. An *empty* list still
				// offers `a`: a repository with no collaborators is exactly
				// where adding one is wanted.
				return a.itemCount() > 0
			case "clear":
				return a.teamSlug != ""
			}
			return true
		},
		State: func(action string) string {
			if action == "clear" && a.teamSlug != "" {
				return "team"
			}
			return ""
		},
	}
}

// hints renders a prompt's own keys for its bar.
func hints(stack []keys.Ctx, width int) string {
	return keymap.Footer(stack, width, keys.Opts{}, tui.FooterStyle())
}
