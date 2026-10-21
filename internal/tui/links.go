package tui

import (
	tea "github.com/charmbracelet/bubbletea"
	"github.com/keyolk/tuikit/keys"
	"github.com/keyolk/tuikit/link"
)

// Links (E11). A label that stands for a URL (a PR title, a check name, a
// PR-level comment) is an OSC 8 hyperlink, so cmd/ctrl+click opens it even
// while ghx captures the mouse. `o` opens the selected item's URL:
//
//   - list, and the Overview, Files and Commits tabs: the PR, through the
//     existing "browser" action (ctxPR), which also opens every marked row
//     and honours the configured browser. It predates link.Binding and keeps
//     its name so keys.yaml overrides of "browser" still apply.
//   - Checks: the selected check's details page (link.open in ctxChecks).
//   - Comments: the selected PR-level comment's page; a review thread carries
//     no URL in ghx's data, so `o` says so (link.open in ctxComments).
//
// The two inner bindings take `o` from ctxPR's "browser" on their tabs: the
// selected row's own page is what the key means there. They declare that in
// Shadows rather than doing it silently.
//
// The Diff tab keeps `o` as fold: a reviewer's hand is on it there, and the
// PR is one tab away.

// linkBinding is link.Binding for ctx, placed in ghx's help and footer.
func linkBinding(ctx keys.Ctx, footer int) keys.Binding {
	b := link.Binding(ctx)
	b.Group = grpPR
	b.Short = "open"
	b.Footer = footer
	b.Shadows = []string{"browser"}
	return b
}

// openLinkCmd opens url, or says that the selected item has none. A URL that
// is not http(s) is refused by link.Open, which reports why.
func openLinkCmd(url, what string) tea.Cmd {
	if url == "" {
		return func() tea.Msg { return toastMsg{text: "no link for this " + what} }
	}
	return link.Open(url)
}

// selectedLink is the URL of the row under the cursor on a tab whose rows
// carry one, and what to call the row when it has none.
func (d *prDetailModel) selectedLink() (url, what string) {
	switch d.activeTab {
	case tabChecks:
		c := d.checks
		if c.cursor >= 0 && c.cursor < len(c.checks) {
			return c.checks[c.cursor].Link, "check"
		}
		return "", "check"
	case tabComments:
		if cv, ok := d.comments.selectedConversation(); ok {
			return cv.URL, "comment"
		}
		return "", "review thread"
	}
	return "", "row"
}
