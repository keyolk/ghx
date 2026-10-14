package tui

import (
	"fmt"
	"slices"

	"github.com/keyolk/tuikit/keys"
)

// detail_footer.go derives the detail view's footer from the binding table.
// Each tab narrows what it offers with keys.Opts.Visible — the keys still run,
// they are in `?` and the palette — and picks two-meaning labels with State.

// footerBudget is the width a footer that carries extra text (the comments
// legend) holds itself to: past 100 columns a common terminal cuts the end of
// the line, which is where that text sits.
const footerBudget = 100

// offering returns a Visible func that shows only the named actions, plus quit
// and help, which every footer keeps.
func offering(actions ...string) func(string) bool {
	return func(a string) bool {
		return a == "quit" || a == "help" || slices.Contains(actions, a)
	}
}

// stack is the detail view's context stack for the active tab.
func (d *prDetailModel) stack() []keys.Ctx {
	switch d.activeTab {
	case tabFiles:
		return stackFiles
	case tabDiff:
		return stackDiff
	case tabComments:
		return stackComments
	case tabCommits:
		return stackCommits
	case tabChecks:
		if d.checks.showingLogs() {
			return stackLogs
		}
		return stackChecks
	}
	return stackOverview
}

func (d *prDetailModel) keyOpts() keys.Opts {
	switch d.activeTab {
	case tabFiles:
		return keys.Opts{Visible: offering("down", "up", "openfile", "back")}
	case tabDiff:
		return d.diff.keyOpts()
	case tabComments:
		return d.comments.keyOpts()
	case tabChecks:
		return d.checks.keyOpts()
	}
	// Overview and Commits show the PR actions: nothing else in those tabs
	// hints that the PR can be approved or merged from here.
	return keys.Opts{}
}

// helpLine is the active tab's footer.
func (d *prDetailModel) helpLine(width int) string {
	switch d.activeTab {
	case tabDiff:
		return d.diff.helpLine(width)
	case tabComments:
		return d.comments.helpLine(width)
	case tabChecks:
		return d.checks.helpLine(width)
	}
	return d.km.Or(defaultKeymap).Footer(d.stack(), width, d.keyOpts(), footerStyle())
}

// --- diff ---

func (v *diffView) keyOpts() keys.Opts {
	return keys.Opts{Enabled: v.enabled, Visible: v.offered, State: v.keyState}
}

// enabled lets h/l fall through to tab cycling when the diff has nothing for
// them: a file that is already folded (or unfolded), or a unified line with no
// column to switch to.
func (v *diffView) enabled(action string) bool {
	if action != "left" && action != "right" {
		return true
	}
	if path, ok := v.foldPathForCursor(); ok {
		return v.folded[path] != (action == "left")
	}
	if !v.sideBySide {
		return false
	}
	side := sideRight
	if action == "left" {
		side = sideLeft
	}
	_, ok := v.sideTarget(side)
	return ok
}

// offered keeps the diff footer to the keys that mean something on the row
// under the cursor: on a thread the keys reply, on a file header h/l fold, in
// a visual range only extending and commenting matter.
func (v *diffView) offered(action string) bool {
	show := offering
	switch {
	case v.visual:
		return show("down", "up", "comment", "visual", "back")(action)
	case v.onThread():
		if action == "apply" {
			// A applies only where there is something to apply — a hint for a
			// key that would refuse is worse than none.
			_, _, ok := v.suggestionUnderCursor()
			return ok
		}
		return show("down", "up", "thread", "comment", "layout", "back")(action)
	case v.onHeader():
		return show("down", "up", "nexthunk", "prevhunk", "prevfile", "nextfile", "left", "right", "layout", "back")(action)
	case v.sideBySide:
		return show("down", "up", "nexthunk", "prevhunk", "prevfile", "nextfile", "left", "right", "comment", "visual", "layout", "back")(action)
	}
	// Unified: no columns to switch between, so h/l are not offered.
	return show("down", "up", "nexthunk", "prevhunk", "prevfile", "nextfile", "comment", "visual", "layout", "thread", "back")(action)
}

func (v *diffView) onThread() bool { _, ok := v.threadUnderCursor(); return ok }
func (v *diffView) onHeader() bool { _, ok := v.foldPathForCursor(); return ok }

// keyState picks the label of the keys whose meaning follows the cursor.
func (v *diffView) keyState(action string) string {
	switch action {
	case "down", "up":
		if v.visual {
			return "visual"
		}
	case "left", "right":
		if v.onHeader() {
			return "header"
		}
	case "layout":
		if v.sideBySide {
			return "sbs"
		}
		return "unified"
	case "visual":
		if v.visual {
			return "on"
		}
	case "comment":
		if v.onThread() {
			return "thread"
		}
	case "thread":
		if v.threadHasReplies() {
			return "replies"
		}
		if v.onThread() {
			return "reply"
		}
	}
	return ""
}

// helpLine is the diff footer. In visual mode it also states the range that
// would actually be commented on: the highlight can span a hunk boundary while
// the range cannot, and the operator needs to see which it is before pressing c.
func (v *diffView) helpLine(width int) string {
	prefix := v.sidePrefix()
	if v.visual {
		_, _, line, startLine, ok := v.commentTarget()
		switch {
		case !ok:
			prefix = dimStyle.Render("(not a commentable line) ")
		case startLine == 0:
			// The selection is not a usable range — say so rather than letting
			// the highlight imply otherwise.
			prefix += diffHunkStyle.Render(fmt.Sprintf("line %d only", line)) + " "
		default:
			lo, hi := min(startLine, line), max(startLine, line)
			prefix += diffHunkStyle.Render(fmt.Sprintf("lines %d-%d", lo, hi)) + " "
		}
	}
	rest := max(width-lipglossWidth(prefix), 1)
	return prefix + v.km.Or(defaultKeymap).Footer(stackDiff, rest, v.keyOpts(), footerStyle())
}

// --- comments ---

func (c *commentsView) keyOpts() keys.Opts {
	return keys.Opts{
		Visible: offering("down", "up", "reply", "resolve", "jumpdiff", "resolvedfilter", "back"),
		State: func(action string) string {
			if action != "resolvedfilter" {
				return ""
			}
			if c.hideResolved {
				return "hiding"
			}
			return "showing"
		},
	}
}

// helpLine is the comments footer plus a legend for the thread glyphs whenever
// more than one state is on screen. The legend is not guessable from any key,
// so the hints give way to it: they are rendered into the width the legend
// leaves, and drop their lowest-priority entries first.
func (c *commentsView) helpLine(width int) string {
	km := c.km.Or(defaultKeymap)
	legend := c.glyphLegend()
	if legend == "" {
		return km.Footer(stackComments, width, c.keyOpts(), footerStyle())
	}
	styled := dimStyle.Render(legend)
	budget := min(width, footerBudget)
	hints := km.Footer(stackComments, max(budget-lipglossWidth(styled)-2, 1), c.keyOpts(), footerStyle())
	return fitLegend(hints, styled, budget)
}

// --- checks ---

func (c *checksView) keyOpts() keys.Opts {
	if c.showingLogs() {
		return keys.Opts{Visible: offering("down", "up", "closelogs")}
	}
	return keys.Opts{Visible: offering("down", "up", "logs", "browser", "back")}
}

func (c *checksView) helpLine(width int) string {
	stack := stackChecks
	if c.showingLogs() {
		stack = stackLogs
	}
	return c.km.Or(defaultKeymap).Footer(stack, width, c.keyOpts(), footerStyle())
}
