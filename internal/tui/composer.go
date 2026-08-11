package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/textarea"
	tea "github.com/charmbracelet/bubbletea"
)

// The composer is an in-TUI textarea rather than a shell-out to $EDITOR: the
// point of commenting on a diff line is seeing that line while you write, and
// suspending the alt-screen throws that context away. $EDITOR stays available
// on `e` for long-form bodies.

// composerTarget describes what a pending comment will attach to.
type composerTarget struct {
	path      string
	line      int
	side      string // "LEFT" | "RIGHT"
	startLine int    // 0 = single-line
	replyTo   string // parent comment id; non-empty = reply into a thread
	issue     bool   // true = PR-level comment, no line anchor
	// review, when set ("request-changes"), submits the body as that review
	// action instead of posting a standalone comment.
	review string
	// prNumber and repo let the composer act from the list view, where there is
	// no open detail model to read them from. Zero/empty means "use the detail
	// view's PR", which is the case for every inline comment.
	prNumber       int
	repo           string
	credentialRepo string
}

// composer holds the modal's state.
type composer struct {
	ta     textarea.Model
	target composerTarget
	active bool
	busy   bool // a post is in flight
	width  int
	height int
}

func newComposer() *composer {
	ta := textarea.New()
	ta.Placeholder = "Write a comment…"
	ta.ShowLineNumbers = false
	ta.CharLimit = 0 // GitHub allows long bodies; don't invent a cap
	return &composer{ta: ta}
}

// open activates the composer for a target and focuses the textarea.
func (c *composer) open(t composerTarget, width, height int) tea.Cmd {
	c.target = t
	c.active = true
	c.busy = false
	c.ta.Reset()
	c.resize(width, height)
	return c.ta.Focus()
}

func (c *composer) close() {
	c.active = false
	c.busy = false
	c.ta.Blur()
	c.ta.Reset()
}

func (c *composer) resize(width, height int) {
	c.width, c.height = width, height
	boxH := composerHeight(height)
	c.ta.SetWidth(max(width-4, 20))
	c.ta.SetHeight(max(boxH-4, 1))
}

// composerHeight gives the modal about a third of the screen, bounded so it
// stays usable on a short terminal and never swallows the whole view.
func composerHeight(total int) int {
	h := total / 3
	return clamp(h, 6, 14)
}

func (c *composer) body() string {
	return strings.TrimSpace(c.ta.Value())
}

// update handles composer keys. Returns (cmd, handled); handled=false means the
// key wasn't consumed and the caller may treat it normally.
func (c *composer) update(msg tea.KeyMsg) (tea.Cmd, bool) {
	if !c.active {
		return nil, false
	}
	if c.busy {
		// Swallow input while posting so a second Enter can't double-submit.
		return nil, true
	}
	switch msg.String() {
	case "esc":
		c.close()
		return nil, true
	case "ctrl+s":
		// Explicit submit that works regardless of newline handling.
		return c.submit(), true
	case "enter":
		// Enter submits a single-line body; once the body has newlines the
		// user is clearly composing prose, so Enter keeps inserting them and
		// ctrl+s submits.
		if !strings.Contains(c.ta.Value(), "\n") {
			return c.submit(), true
		}
	case "ctrl+e":
		return c.openEditor(), true
	}
	var cmd tea.Cmd
	c.ta, cmd = c.ta.Update(msg)
	return cmd, true
}

// submit validates and emits a post request; app.go performs the gh call.
func (c *composer) submit() tea.Cmd {
	body := c.body()
	if body == "" {
		return func() tea.Msg { return errMsg{err: fmt.Errorf("comment body is empty")} }
	}
	c.busy = true
	t := c.target
	return func() tea.Msg {
		return postCommentMsg{target: t, body: body}
	}
}

// openEditor hands the current draft to $EDITOR and reads it back.
func (c *composer) openEditor() tea.Cmd {
	return func() tea.Msg { return openEditorMsg{draft: c.ta.Value()} }
}

// setBody replaces the draft, used after returning from $EDITOR.
func (c *composer) setBody(s string) {
	c.ta.SetValue(s)
}

// render draws the composer as a bottom overlay describing its target.
func (c *composer) render(width, height int) string {
	if !c.active {
		return ""
	}
	boxH := composerHeight(height)
	title := c.titleText()
	hint := fmtHints("enter", "post", "^s", "post", "^e", "$EDITOR", "esc", "cancel")
	if c.busy {
		hint = dimStyle.Render("posting…")
	}
	body := c.ta.View() + "\n" + hint
	return decoratedPane(title, body, width, boxH, true)
}

func (c *composer) titleText() string {
	t := c.target
	switch {
	// A review action must not read as an ordinary comment: submitting
	// "request changes" is a different act with a different effect on the PR.
	case t.review == "request-changes":
		return "Request changes"
	case t.review != "":
		return "Review: " + t.review
	case t.issue:
		return "PR comment"
	case t.replyTo != "":
		return fmt.Sprintf("Reply · %s:%d", t.path, t.line)
	case t.startLine > 0 && t.startLine != t.line:
		lo, hi := t.startLine, t.line
		if lo > hi {
			lo, hi = hi, lo
		}
		return fmt.Sprintf("%s:%d-%d (%s)", t.path, lo, hi, t.side)
	default:
		return fmt.Sprintf("%s:%d (%s)", t.path, t.line, t.side)
	}
}
