package tui

import (
	"fmt"
	"strings"
	"time"
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
		content = overlayBody(content, renderHelpOverlay(a.width, rows), a.width, rows)
	}
	if a.palette.active {
		content = overlayBody(content, a.palette.render(a.width, rows), a.width, rows)
	}
	if a.search.active {
		content = overlayBody(content, a.search.render(a.width, rows), a.width, rows)
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
	// The confirmation sits above everything else it can coexist with: it is the
	// last thing between a keypress and an action that cannot be taken back.
	if a.confirm != nil {
		content = overlayBody(content, a.renderConfirm(a.width, rows), a.width, rows)
	}
	if a.suggestion != nil {
		content = overlayBody(content, a.renderSuggestionPrompt(a.width, rows), a.width, rows)
	}
	if a.composer.active {
		content = overlayBody(content, a.composer.render(a.width, rows), a.width, rows)
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
	if note := a.pollStatusNote(); note != "" {
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

// pollStatusNote describes the poll cadence when it is not the configured one.
func (a *App) pollStatusNote() string {
	// A suspended poll has to say so. Coming back to a window whose rows are an
	// hour old, with nothing on screen admitting it, is worse than the polling
	// this avoids — the marker is what makes the rows readable as "as of when I
	// left" rather than "as of now".
	if a.unfocused {
		return dimStyle.Render("paused · unfocused ")
	}
	interval := a.pollInterval()
	if interval <= a.cfg.PollDuration() {
		return ""
	}
	b := a.client.GraphQLBudget()
	// The budget is the more urgent of the two reasons and the one the user can
	// act on (close some windows), so it wins the single slot.
	if b.Known && b.Fraction() < 0.5 {
		return dimStyle.Render(fmt.Sprintf("API %d%% · poll %s ",
			int(b.Fraction()*100), shortDuration(interval)))
	}
	return dimStyle.Render(fmt.Sprintf("idle · poll %s ", shortDuration(interval)))
}

// shortDuration renders a poll interval the way a person would say it.
func shortDuration(d time.Duration) string {
	switch {
	case d >= time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	case d >= time.Minute:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	default:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
}

func (a *App) helpLine() string {
	// An action in flight outranks even a toast: it is the only thing on screen
	// that is still happening, and in the detail view — where there is no row to
	// carry a marker — the footer is the sole place it can be said at all.
	if note := a.pendingNote(); note != "" {
		return truncateFooter(note, a.width)
	}
	// A recent toast outranks hints: it's the answer to what the user just did.
	if a.toast != "" && time.Since(a.toastAt) < 4*time.Second {
		return truncateFooter(a.toast, a.width)
	}
	if a.helpOpen {
		return truncateFooter(fmtHints("?", "close", "esc", "close", "q", "close"), a.width)
	}
	// Whichever overlay owns the keyboard also owns the hints; showing the
	// underlying view's keys while a modal is up advertises keys that do nothing.
	if a.composer.active {
		return truncateFooter(fmtHints("enter", "post", "^e", "$EDITOR", "esc", "cancel"), a.width)
	}
	if a.confirm != nil {
		return truncateFooter(fmtHints("y", "yes", "n", "no"), a.width)
	}
	if a.suggestion != nil {
		if a.suggestion.busy {
			return truncateFooter(dimStyle.Render("committing…"), a.width)
		}
		return truncateFooter(fmtHints("y", "apply", "n", "cancel"), a.width)
	}
	if a.labels != nil {
		return truncateFooter(
			fmtHints("sp", "toggle", "enter", "apply", "esc", "cancel"), a.width)
	}
	if a.statusFilter != nil {
		return truncateFooter(
			fmtHints("sp", "toggle", "enter", "apply", "c", "clear", "esc", "cancel"), a.width)
	}
	if a.mergePrompt != nil {
		return truncateFooter(fmtHints("y", "merge", "s/m/b", "strategy", "esc", "cancel"), a.width)
	}
	if a.palette.active {
		return truncateFooter(fmtHints("enter", "run", "esc", "cancel"), a.width)
	}
	if a.search.active {
		return truncateFooter(fmtHints("enter", "apply", "esc", "clear"), a.width)
	}
	var s string
	switch a.state {
	case viewPRList:
		if a.list != nil {
			s = a.list.helpLine()
		}
	case viewPRDetail:
		if a.detail != nil {
			s = a.detail.helpLine()
		}
	}
	return truncateFooter(s, a.width)
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
	b.WriteString("Strategy: " + strategyChoice(p.strategy) + "\n\n")
	b.WriteString(fmtHints("y", "merge", "s", "squash", "m", "merge commit", "b", "rebase", "esc", "cancel"))
	return decoratedPane("confirm merge", b.String(), min(width-4, 70), 9, true)
}

func strategyChoice(cur string) string {
	opts := []string{"squash", "merge", "rebase"}
	parts := make([]string, 0, len(opts))
	for _, o := range opts {
		if o == cur {
			parts = append(parts, tabActiveStyle.Render("["+o+"]"))
		} else {
			parts = append(parts, dimStyle.Render(" "+o+" "))
		}
	}
	return strings.Join(parts, " ")
}
