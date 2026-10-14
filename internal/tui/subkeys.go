package tui

import (
	"github.com/keyolk/tuikit/keys"
)

// subkeys.go is what the subcommand TUIs (ghx admin, ghx actions) share with
// each other and, by action name, with the PR app: the list movement, search
// and quit keys. Each subcommand builds its own table from these plus its own
// actions, so `down`, `search` and `quit` mean the same thing in all three.

// ListBindings are the shared list keys of a subcommand panel, bound in ctx.
// The paging keys are the subcommands' own (^d/^u, half a page), not the PR
// list's.
func ListBindings(ctx keys.Ctx) []keys.Binding {
	return []keys.Binding{
		{Action: "quit", Keys: []string{"q", "ctrl+c"}, Ctx: ctx, Group: grpOther, Help: "quit", Pinned: true, Footer: 99},
		{Action: "down", Keys: []string{"j", "down"}, Ctx: ctx, Group: grpNav, Help: "move down", Footer: 2, FooterGroup: "move"},
		{Action: "up", Keys: []string{"k", "up"}, Ctx: ctx, Group: grpNav, Help: "move up", Footer: 2, FooterGroup: "move"},
		{Action: "pagedown", Keys: []string{"ctrl+d", "pgdown"}, Ctx: ctx, Group: grpNav, Help: "half a page down"},
		{Action: "pageup", Keys: []string{"ctrl+u", "pgup"}, Ctx: ctx, Group: grpNav, Help: "half a page up"},
		// home/end as in the PR app: G alone is unreachable under a Korean
		// input source.
		{Action: "top", Keys: []string{"g", "home"}, Ctx: ctx, Group: grpNav, Help: "jump to top"},
		{Action: "bottom", Keys: []string{"G", "end"}, Ctx: ctx, Group: grpNav, Help: "jump to bottom"},
		{Action: "search", Keys: []string{"/"}, Ctx: ctx, Group: grpFilters, Help: "search", Footer: 3},
	}
}

// SearchBindings are the keys of the one-line search prompt the subcommands
// show; everything else it receives is typed.
func SearchBindings(ctx keys.Ctx) []keys.Binding {
	return []keys.Binding{
		{Action: "apply", Keys: []string{"enter"}, Ctx: ctx, Group: grpFilters, Help: "apply", Footer: 1},
		{Action: "clear", Keys: []string{"esc"}, Ctx: ctx, Group: grpFilters, Help: "clear", Footer: 2},
		{Action: "erase", Keys: []string{"backspace"}, Ctx: ctx, Group: grpFilters, Help: "erase a character"},
	}
}

// FooterStyle is the footer the PR app draws, for the subcommands.
func FooterStyle() keys.FooterStyle { return footerStyle() }
