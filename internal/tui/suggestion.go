package tui

import (
	"context"
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/keyolk/ghx/internal/gh"
	"github.com/keyolk/ghx/internal/pr"
)

// Applying a review suggestion from the diff. GitHub's web UI has a button for
// this and its API has no equivalent, so ghx does what the button does: read
// the file at the PR's head, replace the anchored lines, and commit the result
// to the head branch.
//
// It is the only action in the diff view that writes to someone's branch, so it
// goes through a confirmation that shows the exact replacement first — a
// suggestion anchored to a line that has since moved would otherwise be applied
// somewhere the reviewer never looked.

// suggestionPrompt is the pending confirmation for one suggestion.
type suggestionPrompt struct {
	thread     pr.ReviewThread
	suggestion pr.Suggestion
	// original is the lines being replaced, for the before/after in the prompt.
	original []string
	busy     bool
}

// suggestionUnderCursor returns the thread whose comment carries a suggestion,
// when the cursor is on one.
//
// Only the opening comment is considered. A reply containing a fenced
// suggestion is a counter-proposal in a conversation, and the anchor recorded
// on the thread belongs to the original comment — applying a reply's block at
// that anchor would write the wrong lines.
//
// An orphan is refused for the same reason, more urgently: its anchor is not in
// this diff at all, because the branch moved after the comment was written. The
// line number it carries still resolves against the current file, so applying it
// would write the suggestion at coordinates nobody has looked at.
func (v *diffView) suggestionUnderCursor() (pr.ReviewThread, pr.Suggestion, bool) {
	id, ok := v.threadUnderCursor()
	if !ok {
		return pr.ReviewThread{}, pr.Suggestion{}, false
	}
	if r := v.rows[v.cursor]; r.commentIdx != 0 || r.orphan {
		return pr.ReviewThread{}, pr.Suggestion{}, false
	}
	for _, t := range v.threads {
		if t.ID != id || len(t.Comments) == 0 {
			continue
		}
		found := pr.ParseSuggestions(t.Comments[0].Body)
		if len(found) == 0 {
			return pr.ReviewThread{}, pr.Suggestion{}, false
		}
		// Only the first block. GitHub's button applies every block in a comment
		// as one commit, but each needs its own anchor to be placed correctly and
		// a comment carries exactly one — so a multi-block comment is left to the
		// web UI rather than guessed at.
		return t, found[0], true
	}
	return pr.ReviewThread{}, pr.Suggestion{}, false
}

// startApplySuggestion opens the confirmation for the suggestion under the
// cursor, gathering the lines it would replace so the prompt can show them.
func (d *prDetailModel) startApplySuggestion() (*suggestionPrompt, error) {
	thread, suggestion, ok := d.diff.suggestionUnderCursor()
	if !ok {
		return nil, fmt.Errorf("put the cursor on a comment that contains a suggestion")
	}
	if thread.IsResolved {
		return nil, fmt.Errorf("that thread is resolved — unresolve it first if the suggestion still applies")
	}
	if thread.IsOutdated {
		// The branch moved after the comment was written, so GitHub dropped the
		// thread's `line` and only `originalLine` is left. That number still
		// resolves against the current file — which is exactly the danger: the
		// commit would land at coordinates that no longer mean what the reviewer
		// read. Applying a suggestion makes its own thread outdated, so this is
		// also what stops a second A from writing the same block twice.
		return nil, fmt.Errorf(
			"that suggestion is outdated — the branch moved since it was written; press R and re-read the diff")
	}
	if thread.DiffSide == "LEFT" {
		// A suggestion on a deleted line has nothing in the new file to replace.
		return nil, fmt.Errorf("that suggestion is anchored to a deleted line and cannot be applied")
	}
	return &suggestionPrompt{
		thread:     thread,
		suggestion: suggestion,
		original:   d.diff.linesForThread(thread),
	}, nil
}

// requestApplySuggestion asks app.go to open the confirmation, or explains why
// the cursor is not on something applicable.
func (d *prDetailModel) requestApplySuggestion() tea.Cmd {
	prompt, err := d.startApplySuggestion()
	if err != nil {
		return errCmd(err)
	}
	return func() tea.Msg { return openSuggestionMsg{prompt: prompt} }
}

// linesForThread returns the current text of the lines a thread is anchored to,
// read from the diff that is already loaded. Used only for display.
func (v *diffView) linesForThread(t pr.ReviewThread) []string {
	lo, hi, ok := threadRange(t)
	if !ok {
		lo, hi = threadLine(t), threadLine(t)
	}
	var out []string
	for _, r := range v.rows {
		if r.kind != rowDiffLine || r.path != t.Path || r.side == "LEFT" {
			continue
		}
		if r.anchorLine >= lo && r.anchorLine <= hi {
			out = append(out, r.line.Content)
		}
	}
	return out
}

// applySuggestion performs the write.
func (a *App) applySuggestion(p *suggestionPrompt) tea.Cmd {
	if a.detail == nil {
		return nil
	}
	t := p.thread
	lo, hi, ok := threadRange(t)
	if !ok {
		// Single-line: GitHub reports start_line as absent, and the apply path
		// takes 0 to mean "just this line".
		lo, hi = 0, threadLine(t)
	}
	target := gh.SuggestionTarget{
		Path:      t.Path,
		StartLine: lo,
		Line:      hi,
	}
	replacement, deletion := p.suggestion.Replacement, p.suggestion.Deletion
	n, client := a.detail.number, a.detail.client
	return func() tea.Msg {
		// A commit is several round trips (head ref, file read, commit) and the
		// file can be large; the ordinary 30s is not always enough.
		c, cancel := context.WithTimeout(context.Background(), 90*time.Second)
		defer cancel()
		if err := client.ApplySuggestion(c, n, target, replacement, deletion); err != nil {
			return suggestionAppliedMsg{err: err}
		}
		return suggestionAppliedMsg{path: t.Path, line: hi}
	}
}

func (a *App) handleSuggestionKey(msg tea.KeyMsg) tea.Cmd {
	p := a.suggestion
	if p.busy {
		// A commit is in flight. Ignoring keys here is what stops a second
		// return from pushing the same change twice.
		return nil
	}
	switch msg.String() {
	case "y", "Y":
		p.busy = true
		return a.applySuggestion(p)
	case "n", "N", "esc", "q":
		a.suggestion = nil
		return nil
	}
	return nil
}

// renderSuggestionPrompt shows what would be written before it is written.
func (a *App) renderSuggestionPrompt(width, height int) string {
	p := a.suggestion
	inner := min(width-8, 88)
	var b strings.Builder

	where := fmt.Sprintf("%s:%d", p.thread.Path, threadLine(p.thread))
	if lo, hi, ok := threadRange(p.thread); ok {
		where = fmt.Sprintf("%s:%d-%d", p.thread.Path, lo, hi)
	}
	if p.busy {
		b.WriteString("Committing the suggestion to " + where + "…\n")
		return decoratedPane("apply suggestion", b.String(), min(width-4, 92), 5, true)
	}

	b.WriteString("Apply this suggestion to " + where + "?\n")
	b.WriteString(checkFailStyle.Render(
		"This commits to the PR's branch and cannot be undone from ghx.") + "\n\n")

	for _, line := range p.original {
		b.WriteString(diffDelStyle.Render(fitCell("-"+line, inner)) + "\n")
	}
	if p.suggestion.Deletion {
		b.WriteString(dimStyle.Render("(the lines above are removed)") + "\n")
	} else {
		for _, line := range strings.Split(p.suggestion.Replacement, "\n") {
			b.WriteString(diffAddStyle.Render(fitCell("+"+line, inner)) + "\n")
		}
	}
	b.WriteString("\n" + fmtHints("y", "apply", "n", "cancel"))

	rows := len(p.original) + strings.Count(p.suggestion.Replacement, "\n") + 9
	return decoratedPane("apply suggestion", b.String(),
		min(width-4, 92), min(rows, max(height-2, 8)), true)
}
