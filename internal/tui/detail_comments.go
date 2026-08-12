package tui

import (
	"fmt"
	"regexp"
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
func (c *commentsView) visible() []pr.ReviewThread {
	if !c.hideResolved {
		return c.threads
	}
	out := make([]pr.ReviewThread, 0, len(c.threads))
	for _, t := range c.threads {
		if !t.IsResolved {
			out = append(out, t)
		}
	}
	return out
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
func (c *commentsView) toggleExpand() {
	t, ok := c.selected()
	if !ok {
		return
	}
	c.expanded[t.ID] = !c.expanded[t.ID]
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
	if !found || t.ID == "" {
		return pr.ReviewThread{}, false, false
	}
	for i := range c.threads {
		if c.threads[i].ID == t.ID {
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
		if c.expanded[t.ID] {
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
	if t.IsResolved {
		tags = append(tags, "resolved")
	}
	tag := ""
	if len(tags) > 0 {
		tag = " [" + strings.Join(tags, "] [") + "]"
	}

	head := fmt.Sprintf("%s %s  %s%s", fold, loc, author, tag)

	// Build the plain line first so the selected form can be themed as a whole:
	// a background wrapped around the styled header would stop at its reset code
	// and leave the row looking half-selected.
	plain := head
	if rest := width - lipglossWidth(head) - 3; rest > 20 && preview != "" {
		p, _ := truncateExact(preview, rest)
		plain += "  " + p
	}
	if selected {
		plain, _ = truncateExact(plain, width)
		if pad := width - lipglossWidth(plain); pad > 0 {
			plain += strings.Repeat(" ", pad)
		}
		return selectedRowStyle.Render(plain)
	}

	style := threadStyle
	if t.IsResolved {
		style = threadResolved
	}
	line := style.Render(head)
	// Show a preview only when the header leaves room for it to be readable.
	if rest := width - lipglossWidth(head) - 3; rest > 20 && preview != "" {
		p, _ := truncateExact(preview, rest)
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
	resolved := "show resolved"
	if !c.hideResolved {
		resolved = "hide resolved"
	}
	return fmtHints(
		"j/k", "thread",
		"enter", "expand",
		"c", "reply",
		"X", "resolve/unresolve",
		"d", "jump to diff",
		"t", resolved,
		"esc", "back",
	)
}
