package tui

import (
	"fmt"
	"slices"
	"strings"

	"github.com/keyolk/tuikit/keys"
)

// Top-level composition: the title line, the footer, overlay stacking, and the
// merge confirmation. Whichever overlay owns the keyboard also owns the hints.

// --- render ---

func (a *App) View() string {
	if a.width == 0 || a.height == 0 {
		return "Starting ghx…"
	}
	// Below this the panes have no room to say anything useful.
	if a.width < 30 || a.height < 8 {
		return fmt.Sprintf("Terminal too small (%dx%d).\nghx needs at least 30x8.",
			a.width, a.height)
	}

	// Everything below is sized to rows, never to a.height: the title line and
	// the footer are always drawn too.
	rows := a.contentRows()

	var content string
	switch a.state {
	case viewPRList:
		content = a.list.view(a.width, a.height)
	case viewPRDetail:
		if a.detail == nil {
			content = renderSpinner(a.spinnerFrame, "Loading…")
		} else {
			content = a.detail.view(a.width, rows)
		}
	}
	// Normalize before compositing: overlayBody pads its base out to the height
	// it is given, so a body of the wrong length becomes an overflowing frame
	// the moment any modal opens.
	content = fitRows(content, rows)

	// Overlays are composited last, innermost first.
	if a.helpOpen {
		content = overlayBody(content, a.renderHelpOverlay(a.width, rows), a.width, rows)
	}
	if a.palette.active {
		content = overlayBody(content, a.palette.render(a.width, rows), a.width, rows)
	}
	if a.search.active {
		content = overlayBody(content, a.search.render(a.width, rows, a.modalHints(stackSearch, a.width-4)), a.width, rows)
	}
	if a.mergePrompt != nil {
		content = overlayBody(content, a.renderMergePrompt(a.width, rows), a.width, rows)
	}
	if a.labels != nil {
		content = overlayBody(content, a.renderLabelPicker(a.width, rows), a.width, rows)
	}
	if a.statusFilter != nil {
		content = overlayBody(content, a.renderStatusFilter(a.width, rows), a.width, rows)
	}
	if a.repos != nil {
		content = overlayBody(content, a.renderRepoPicker(a.width, rows), a.width, rows)
	}
	// The confirmation sits above everything else it can coexist with: it is the
	// last thing between a keypress and an action that cannot be taken back.
	if a.confirm != nil {
		content = overlayBody(content, a.renderConfirm(a.width, rows), a.width, rows)
	}
	if a.suggestion != nil {
		content = overlayBody(content, a.renderSuggestionPrompt(a.width, rows), a.width, rows)
	}
	if a.composer.active {
		content = overlayBody(content, a.composer.render(a.width, rows, a.modalHints(stackComposer, a.width-4)), a.width, rows)
	}
	if a.mergeFailure != nil {
		content = overlayBody(content, a.renderMergeFailure(a.width, rows), a.width, rows)
	}

	return joinVertical(a.titleLine(), content, a.helpLine())
}

// contentRows is how many rows the body may occupy: the terminal minus the
// title line and the footer, both of which View always draws.
//
// Getting this wrong is invisible in the obvious direction and ugly in the
// other: bubbletea's renderer keeps the LAST height lines of an overflowing
// frame, so the rows that get dropped are the title and the tab strip. That is
// the "the tab index strip is sometimes missing in the PR detail view" report —
// it appeared exactly when the active tab's body was tall enough to fill the
// screen, and disappeared again on a short diff.
func (a *App) contentRows() int { return max(a.height-2, 1) }

// fitRows pads or clips s to exactly h rows. Padding matters as much as
// clipping: overlayBody anchors a modal to the bottom of the height it is
// given, so a short body would otherwise leave the modal floating mid-screen.
func fitRows(s string, h int) string {
	if h <= 0 {
		return ""
	}
	lines := strings.Split(s, "\n")
	for len(lines) < h {
		lines = append(lines, "")
	}
	return strings.Join(lines[:h], "\n")
}

func (a *App) titleLine() string {
	title := titleStyle.Render(" ghx ")
	var crumb string
	switch a.state {
	case viewPRList:
		if a.list != nil {
			crumb = a.list.title()
		}
	case viewPRDetail:
		if a.detail != nil {
			crumb = a.detail.title()
		}
	}
	line := title + " " + dimStyle.Render(crumb)
	// A backed-off poll is right-aligned so it never pushes the crumb around,
	// and only appears when it has something to say. Slowing the refresh
	// silently would read as ghx being broken, which is exactly the wrong
	// conclusion — it is the account's budget that ran low, not ghx.
	if note := a.freshnessNote(); note != "" {
		gap := a.width - lipglossWidth(line) - lipglossWidth(note)
		if gap >= 2 {
			line += strings.Repeat(" ", gap) + note
		}
	}
	out, _ := truncateExact(line, a.width)
	return out
}

// pendingNote describes the action in flight, or "" when none is.
//
// It counts the PRs rather than naming them: a bulk merge can span a dozen, and
// a footer that listed them would be truncated into saying nothing useful. The
// rows carry the which; this carries the what.
func (a *App) pendingNote() string {
	if a.pending == nil {
		return ""
	}
	frame := spinnerFrames[a.spinnerFrame%len(spinnerFrames)]
	verb := a.pending.kind.verb()
	// The count only appears when it is more than one. "merging 1 pull request"
	// says the same as "merging" and reads as a report rather than as progress.
	if n := len(a.pending.keys); n > 1 {
		return pendingMarkStyle.Render(fmt.Sprintf("%s %s — %d pull requests…", frame, verb, n))
	}
	return pendingMarkStyle.Render(fmt.Sprintf("%s %s…", frame, verb))
}

func (a *App) helpLine() string {
	// An action in flight outranks even a toast: it is the only thing on screen
	// that is still happening, and in the detail view — where there is no row to
	// carry a marker — the footer is the sole place it can be said at all.
	if note := a.pendingNote(); note != "" {
		return truncateFooter(note, a.width)
	}
	// The box owns the keyboard, so it owns the footer too: the toast it set
	// would only repeat the box's headline.
	if a.mergeFailure != nil {
		return a.failureHints(a.width)
	}
	// A recent toast outranks hints: it's the answer to what the user just did.
	if toast := a.toast.Text(); toast != "" {
		return truncateFooter(toast, a.width)
	}
	// Whichever overlay owns the keyboard also owns the hints; showing the
	// underlying view's keys while a modal is up advertises keys that do nothing.
	if a.suggestion != nil && a.suggestion.busy {
		return truncateFooter(dimStyle.Render("committing…"), a.width)
	}
	if stack := a.modalStack(); stack != nil {
		return a.modalHints(stack, a.width)
	}
	var s string
	switch a.state {
	case viewPRList:
		if a.list != nil {
			s = a.list.helpLine(a.width)
		}
	case viewPRDetail:
		if a.detail != nil {
			s = a.detail.helpLine(a.width)
		}
	}
	return truncateFooter(s, a.width)
}

// modalStack is the stack of the modal that owns the keyboard, in the order
// handleKey asks them, or nil when the view does.
func (a *App) modalStack() []keys.Ctx {
	switch {
	case a.composer.active:
		return stackComposer
	case a.confirm != nil:
		return stackConfirm
	case a.suggestion != nil:
		return stackSuggest
	case a.statusFilter != nil:
		return stackStatus
	case a.labels != nil:
		return stackLabels
	case a.repos != nil:
		return stackRepos
	case a.mergePrompt != nil:
		return stackMerge
	case a.search.active:
		return stackSearch
	case a.palette.active:
		return stackPalette
	case a.helpOpen:
		return stackHelp
	}
	return nil
}

// modalHints is a modal's hint line from the table, optionally narrowed to
// some of its actions.
func (a *App) modalHints(stack []keys.Ctx, width int, only ...string) string {
	var o keys.Opts
	if len(only) > 0 {
		o.Visible = func(action string) bool { return slices.Contains(only, action) }
	}
	return a.keymap().Footer(stack, width, o, footerStyle())
}

// renderMergePrompt draws the confirmation, stating plainly that it can't be undone.
func (a *App) renderMergePrompt(width, height int) string {
	p := a.mergePrompt
	num := 0
	base := ""
	if a.detail != nil {
		num = a.detail.number
		if a.detail.detail != nil {
			base = a.detail.detail.BaseRefName
		}
	}
	var b strings.Builder
	b.WriteString(fmt.Sprintf("Merge #%d into %s?\n", num, base))
	b.WriteString(checkFailStyle.Render("This cannot be undone.") + "\n\n")
	b.WriteString(strategyLabel + strategyChoice(p.strategy) + "\n\n")
	b.WriteString(a.modalHints(stackMerge, min(width-4, 70)-4))
	return decoratedPane("confirm merge", b.String(), min(width-4, 70), 9, true)
}

// mergeStrategies are the choices the merge prompt offers, in drawing order.
var mergeStrategies = []string{"squash", "merge", "rebase"}

// strategyParts is the choice line's pieces, one per strategy.
func strategyParts(cur string) []string {
	parts := make([]string, 0, len(mergeStrategies))
	for _, o := range mergeStrategies {
		if o == cur {
			parts = append(parts, tabActiveStyle.Render("["+o+"]"))
		} else {
			parts = append(parts, dimStyle.Render(" "+o+" "))
		}
	}
	return parts
}

// strategyLabel prefixes the choice line; the mouse hit test measures it.
const strategyLabel = "Strategy: "

func strategyChoice(cur string) string {
	return strings.Join(strategyParts(cur), " ")
}
