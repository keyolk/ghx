package tui

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/keyolk/ghx/internal/pr"
)

// The Comments tab is the thread-oriented view of the same data the diff tab
// overlays: useful for sweeping every open thread without scrolling the patch.

// commentsView owns the thread list cursor and expansion state.
type commentsView struct {
	threads []pr.ReviewThread
	cursor  int
	offset  int

	// expanded thread ids show every comment rather than just the first
	expanded map[string]bool

	// hideResolved keeps finished conversations out of the way by default
	hideResolved bool
}

func newCommentsView() *commentsView {
	return &commentsView{expanded: map[string]bool{}, hideResolved: true}
}

func (c *commentsView) setThreads(threads []pr.ReviewThread) {
	c.threads = threads
	if c.cursor >= len(c.visible()) {
		c.cursor = max(len(c.visible())-1, 0)
	}
}

// visible applies the resolved filter.
//
// A thread whose resolution could not be determined stays visible: hiding it
// would be asserting it is resolved, and the whole point of the unknown state
// is that nothing here knows that.
func (c *commentsView) visible() []pr.ReviewThread {
	if !c.hideResolved {
		return c.threads
	}
	out := make([]pr.ReviewThread, 0, len(c.threads))
	for _, t := range c.threads {
		if !threadIsResolved(t) {
			out = append(out, t)
		}
	}
	return out
}

// threadIsResolved reports resolution only when it was actually learned. REST
// carries no resolution bit, so a thread recovered that way answers false here
// and is tagged "resolution unknown" instead of being shown as outstanding.
func threadIsResolved(t pr.ReviewThread) bool {
	return t.ResolutionKnown && t.IsResolved
}

// threadState names which of the three states a thread is in. Resolved and
// unresolved are not opposites here: a thread recovered over REST is in neither,
// and calling it open would be inventing an answer nothing has.
type threadState int

const (
	threadOpen threadState = iota
	threadDone
	threadUnknown
)

func stateOf(t pr.ReviewThread) threadState {
	if !t.ResolutionKnown {
		return threadUnknown
	}
	if t.IsResolved {
		return threadDone
	}
	return threadOpen
}

// threadStateGlyph is the leading state cell, styled unless the caller is about
// to paint a selection band over the row.
//
// It leads rather than trailing as a tag for two reasons. It lines up in a
// column, where a tag after a variable-length path and author does not — and it
// is a character, so it survives the cursor. A selected row is themed as one
// band (see prlist_render.go on why cell-by-cell backgrounds stripe), which
// overwrites colour and strikethrough alike; the row under the cursor was the
// one row with no state signal left, and it is the row being looked at.
func threadStateGlyph(t pr.ReviewThread, plain bool) string {
	glyph, style := iconThreadOpen, threadOpenGlyph
	switch stateOf(t) {
	case threadDone:
		glyph, style = iconThreadResolved, threadResolvedGlyph
	case threadUnknown:
		glyph, style = iconThreadUnknown, threadUnknownGlyph
	}
	if plain {
		return glyph
	}
	return style.Render(glyph)
}

// countThreadStates tallies the three states for the tab title, so the count of
// what is still outstanding is visible without turning the filter off.
func countThreadStates(threads []pr.ReviewThread) (open, done, unknown int) {
	for _, t := range threads {
		switch stateOf(t) {
		case threadDone:
			done++
		case threadUnknown:
			unknown++
		default:
			open++
		}
	}
	return open, done, unknown
}

func (c *commentsView) moveCursor(delta int) {
	n := len(c.visible())
	if n == 0 {
		return
	}
	c.cursor = clamp(c.cursor+delta, 0, n-1)
}

func (c *commentsView) selected() (pr.ReviewThread, bool) {
	v := c.visible()
	if c.cursor < 0 || c.cursor >= len(v) {
		return pr.ReviewThread{}, false
	}
	return v[c.cursor], true
}

// toggleExpand shows or hides the replies of the selected thread.
// threadIdentity is the key a thread is tracked by in the UI: which row the
// cursor is on, which threads are expanded, which one an action applies to.
//
// It cannot be t.ID. A thread recovered over REST has none — REST has no thread
// object, only a comment list grouped by in_reply_to_id — so every such thread
// carries the empty string, and every lookup written as `t.ID == id` matches
// the first one. Pressing A on the third thread would then apply the first
// thread's suggestion, at the first thread's line. The root comment's numeric
// id is what REST does give, and it is unique per thread by construction.
func threadIdentity(t pr.ReviewThread) string {
	if t.ID != "" {
		return t.ID
	}
	if len(t.Comments) > 0 && t.Comments[0].DatabaseID != 0 {
		return "rest:" + strconv.FormatInt(t.Comments[0].DatabaseID, 10)
	}
	// Nothing identifies this thread. Returning "" would collide with every
	// other such thread, so anchor-derived text is the last resort — two threads
	// on the same line are rare, and it is still better than all of them.
	return "anchor:" + threadKey(t.Path, orDefault(t.DiffSide, "RIGHT"), threadLine(t))
}

func (c *commentsView) toggleExpand() {
	t, ok := c.selected()
	if !ok {
		return
	}
	c.expanded[threadIdentity(t)] = !c.expanded[threadIdentity(t)]
}

// toggleResolvedFilter flips whether resolved threads are hidden, for the `t`
// key. Distinct from toggleThreadResolved, which acts on a single thread.
func (c *commentsView) toggleResolvedFilter() {
	c.hideResolved = !c.hideResolved
	c.cursor = 0
	c.offset = 0
}

// toggleThreadResolved flips the selected thread's IsResolved locally so the
// view updates instantly, and reports the thread plus the new state so the
// caller can persist it. Optimistic: the server round-trip follows, and a
// reload will reconcile on failure.
func (c *commentsView) toggleThreadResolved() (thread pr.ReviewThread, resolve bool, ok bool) {
	t, found := c.selected()
	// An empty ID means the thread did not come from GraphQL, so there is no node
	// to resolve. Flipping the marker locally would show a resolution that was
	// never persisted.
	if !found || t.ID == "" || !t.ResolutionKnown {
		return pr.ReviewThread{}, false, false
	}
	id := threadIdentity(t)
	for i := range c.threads {
		if threadIdentity(c.threads[i]) == id {
			c.threads[i].IsResolved = !c.threads[i].IsResolved
			break
		}
	}
	return t, !t.IsResolved, true
}

// render draws the thread list, expanding the selected thread's replies.
func (c *commentsView) render(width, height int) string {
	v := c.visible()
	if len(v) == 0 {
		if len(c.threads) > 0 {
			return dimStyle.Render(fmt.Sprintf(
				"All %d threads are resolved — press t to show them.", len(c.threads)))
		}

		return dimStyle.Render("No review threads.")
	}

	// Build display lines first, tracking which line each thread starts on so
	// the cursor can be kept visible even when threads span several lines.
	var lines []string
	starts := make([]int, len(v))
	for i, t := range v {
		starts[i] = len(lines)
		lines = append(lines, c.threadHeader(t, i == c.cursor, width))
		if c.expanded[threadIdentity(t)] {
			for _, cm := range t.Comments {
				lines = append(lines, c.commentLines(cm, width)...)
			}
		}
	}

	c.clampOffsetLines(starts, len(lines), height)
	end := min(c.offset+height, len(lines))
	return strings.Join(lines[c.offset:end], "\n")
}

func (c *commentsView) clampOffsetLines(starts []int, total, height int) {
	if height <= 0 || c.cursor >= len(starts) {
		return
	}
	target := starts[c.cursor]
	if target < c.offset {
		c.offset = target
	}
	if target >= c.offset+height {
		c.offset = target - height + 1
	}
	c.offset = clamp(c.offset, 0, max(total-height, 0))
}

func (c *commentsView) threadHeader(t pr.ReviewThread, selected bool, width int) string {
	fold := iconFoldClosed
	if c.expanded[t.ID] {
		fold = iconFoldOpen
	}
	loc := fmt.Sprintf("%s:%d", t.Path, threadLine(t))
	if lo, hi, ok := threadRange(t); ok {
		loc = fmt.Sprintf("%s:%d-%d", t.Path, lo, hi)
	}
	author, preview := "", ""
	if len(t.Comments) > 0 {
		author = t.Comments[0].Author.Login
		preview = commentPreview(t.Comments[0].Body)
	}
	var tags []string
	if n := len(t.Comments); n > 1 {
		tags = append(tags, fmt.Sprintf("%d replies", n-1))
	}
	// The state is no longer a tag — it is the leading glyph, which lines up and
	// outlives the cursor. A tag here would say the same thing twice, in the spot
	// where it was least readable.
	tag := ""
	if len(tags) > 0 {
		tag = " [" + strings.Join(tags, "] [") + "]"
	}

	// The glyph is outside the styled head so the selection band cannot swallow
	// it: the band is applied to the row as a whole, and anything styled inside
	// it loses its own colour at the first reset.
	head := fmt.Sprintf("%s %s  %s%s", fold, loc, author, tag)

	// Build the plain line first so the selected form can be themed as a whole:
	// a background wrapped around the styled header would stop at its reset code
	// and leave the row looking half-selected.
	body := head
	avail := width - lipglossWidth(head) - 5 // 2 for the glyph cell, 3 for spacing
	if avail > 20 && preview != "" {
		p, _ := truncateExact(preview, avail)
		body += "  " + p
	}
	if selected {
		// The band covers the row minus the glyph cell, which stays outside it and
		// keeps its colour — under the cursor is exactly where the old rendering
		// lost the state, and it is the row the reviewer is reading.
		bandWidth := max(width-2, 1)
		body, _ = truncateExact(body, bandWidth)
		if pad := bandWidth - lipglossWidth(body); pad > 0 {
			body += strings.Repeat(" ", pad)
		}
		return threadStateGlyph(t, false) + " " + selectedRowStyle.Render(body)
	}

	style := threadStyle
	if threadIsResolved(t) {
		style = threadResolved
	}
	line := threadStateGlyph(t, false) + " " + style.Render(head)
	// Show a preview only when the header leaves room for it to be readable.
	if avail > 20 && preview != "" {
		p, _ := truncateExact(preview, avail)
		line += dimStyle.Render("  " + p)
	}
	line, _ = truncateExact(line, width)
	return line
}

// commentLines renders one comment's author and wrapped body, indented.
func (c *commentsView) commentLines(cm pr.ThreadComment, width int) []string {
	out := []string{"    " + prAuthorStyle.Render(cm.Author.Login) +
		dimStyle.Render("  "+cm.CreatedAt.Format("2006-01-02 15:04"))}
	avail := max(width-6, 20)
	for _, para := range strings.Split(cm.Body, "\n") {
		if strings.TrimSpace(para) == "" {
			out = append(out, "")
			continue
		}
		for _, seg := range wrapText(para, avail) {
			out = append(out, "      "+seg)
		}
	}
	return out
}

// wrapText breaks a paragraph at word boundaries to fit width cells.
func wrapText(s string, width int) []string {
	if width <= 0 {
		return []string{s}
	}
	words := strings.Fields(s)
	if len(words) == 0 {
		return []string{""}
	}
	var lines []string
	cur := ""
	for _, w := range words {
		cand := w
		if cur != "" {
			cand = cur + " " + w
		}
		if lipglossWidth(cand) > width && cur != "" {
			lines = append(lines, cur)
			cur = w
			continue
		}
		cur = cand
	}
	if cur != "" {
		lines = append(lines, cur)
	}
	return lines
}

// threadLine picks the line to display, falling back for outdated threads.
func threadLine(t pr.ReviewThread) int {
	if t.Line > 0 {
		return t.Line
	}
	return t.OriginalLine
}

// threadRange returns the thread's line span, low end first. GitHub normally
// reports startLine <= line, but an outdated thread can come back with the two
// remapped independently and inverted, which would render as "468-416".
func threadRange(t pr.ReviewThread) (lo, hi int, ok bool) {
	end := threadLine(t)
	start := t.StartLine
	if start <= 0 {
		start = t.OriginalStartLine
	}
	if start <= 0 || end <= 0 || start == end {
		return 0, 0, false
	}
	if start > end {
		start, end = end, start
	}
	return start, end, true
}

// commentPreview flattens a comment body into one scannable line. Review bots
// lead with markdown badges and HTML, which would otherwise fill the preview
// with `<sub>` and shields.io URLs instead of the actual finding.
func commentPreview(body string) string {
	s := body
	// Drop markdown images (badges) and collapse links to their text.
	s = markdownImageRe.ReplaceAllString(s, "")
	s = markdownLinkRe.ReplaceAllString(s, "$1")
	s = htmlTagRe.ReplaceAllString(s, "")
	// Strip emphasis and heading markers that add noise without structure here.
	s = strings.NewReplacer("**", "", "`", "", "#", "").Replace(s)
	s = strings.ReplaceAll(s, "\n", " ")
	return strings.TrimSpace(whitespaceRe.ReplaceAllString(s, " "))
}

var (
	markdownImageRe = regexp.MustCompile(`!\[[^\]]*\]\([^)]*\)`)
	markdownLinkRe  = regexp.MustCompile(`\[([^\]]*)\]\([^)]*\)`)
	htmlTagRe       = regexp.MustCompile(`</?[a-zA-Z][^>]*>`)
	whitespaceRe    = regexp.MustCompile(`\s+`)
)

// helpLine returns the comments-tab footer hints.
func (c *commentsView) helpLine() string {
	resolved := "show all"
	if !c.hideResolved {
		resolved = "hide done"
	}
	// enter:expand is dropped rather than the legend. Expanding with enter is the
	// convention every other list in ghx follows, and it is in the ? overlay; the
	// glyph column is neither conventional nor guessable, and it is on screen in
	// every row.
	// The hints are shortened to make room for the glyph legend. A footer past
	// 100 cells is truncated on a common terminal, and what it drops is whatever
	// sits at the end — which would be the legend, the one part that is not
	// guessable from the key it describes. "resolve/unresolve" and "jump to diff"
	// are each the obvious reading of their key in this tab; the glyphs are not.
	line := fmtHints(
		"j/k", "thread",
		"c", "reply",
		"X", "resolve",
		"d", "diff",
		"t", resolved,
		"esc", "back",
	)
	// Name the glyphs whenever more than one state is on screen. Keyed to what
	// is visible rather than to the filter: an unknown-resolution thread survives
	// the default filter, so `?` appears without `t` ever being pressed, and a
	// glyph nobody can decode is not a signal. With one state showing there is
	// nothing to tell apart and the legend is just noise in the footer.
	// 100 columns is the footer budget this codebase holds itself to; past it a
	// terminal cuts the end of the line, which is where the legend sits.
	return fitLegend(line, dimStyle.Render(c.glyphLegend()), 100)
}

// glyphLegend names the states currently on screen, or "" when there is only
// one and nothing to distinguish.
func (c *commentsView) glyphLegend() string {
	open, done, unknown := countThreadStates(c.visible())
	var parts []string
	if open > 0 {
		parts = append(parts, iconThreadOpen+" open")
	}
	if done > 0 {
		parts = append(parts, iconThreadResolved+" resolved")
	}
	if unknown > 0 {
		parts = append(parts, iconThreadUnknown+" unknown")
	}
	if len(parts) < 2 {
		return ""
	}
	return strings.Join(parts, " ")
}

// fitLegend drops the legend rather than let it push the footer past the width
// a common terminal shows. Truncation would cut the legend anyway — it sits at
// the end — but silently, and half a legend is worse than none: "✓ res" reads
// as a label rather than as a key. The glyphs are also in the ? overlay.
func fitLegend(hints, legend string, width int) string {
	if legend == "" {
		return hints
	}
	full := hints + "  " + legend
	if lipglossWidth(full) <= width {
		return full
	}
	return hints
}
