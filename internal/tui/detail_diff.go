package tui

import (
	"fmt"
	"strings"

	"github.com/keyolk/ghx/internal/diff"
	"github.com/keyolk/ghx/internal/pr"
)

// The diff viewer is the primary review surface. It flattens all files' hunks
// into one cursorable row list, overlays existing review threads at their
// anchor lines, and reports the (path, side, line) under the cursor so an
// inline comment can be attached to exactly that spot.

// diffRowKind distinguishes what a rendered row represents.
type diffRowKind int

const (
	rowFileHeader diffRowKind = iota
	rowHunkHeader
	rowDiffLine
	rowThread
)

// diffRow is one rendered row in the flattened view.
type diffRow struct {
	kind     diffRowKind
	fileIdx  int
	path     string
	line     pr.DiffLine
	threadID string
	text     string // pre-rendered content for thread/header rows
	// hunkIdx identifies the hunk a row belongs to (-1 for file headers). A
	// multi-line comment may not span hunks: the lines in the gap between them
	// are absent from the diff, so GitHub has nothing to attach them to.
	hunkIdx int
	// commentable rows carry the anchor an inline comment would use
	side       string
	anchorLine int
	// commentIdx is which comment of the thread a rowThread shows: 0 for the
	// opening comment, 1.. for replies. Replies only appear when the thread is
	// expanded.
	commentIdx int
	// orphan marks a thread whose anchor is not in this diff (an outdated line,
	// or a file the diff does not contain). It is listed under the file header
	// instead of being dropped — a comment the reviewer cannot see is worse than
	// one shown out of place.
	orphan bool
}

// diffView holds the diff viewer's own state so detail_diff.go owns it rather
// than growing prDetailModel.
type diffView struct {
	files   []pr.DiffFile
	threads []pr.ReviewThread

	rows   []diffRow
	cursor int
	offset int

	// sideBySide switches the layout; sideOffset is its own scroll position
	// because one screen row can hold two diff rows, so the unified offset does
	// not translate.
	sideBySide bool
	sideOffset int

	// sideFocus is which column the cursor is in when a screen row holds two
	// rows (a modification pairs its deletion and addition). The cursor index
	// alone cannot express that: both halves would look selected, and a range
	// spanning them would mix LEFT and RIGHT lines, which GitHub rejects.
	sideFocus halfSide

	// folded file paths; folding shrinks the row set for large diffs
	folded map[string]bool

	// expanded thread ids show their replies inline. Collapsed by default: a
	// bot thread can run to a dozen comments and would bury the code.
	expanded map[string]bool

	// visual-mode range selection for multi-line comments
	visual      bool
	visualStart int
}

func newDiffView() *diffView {
	return &diffView{
		folded:   map[string]bool{},
		expanded: map[string]bool{},
	}
}

// setContent parses raw diff text and rebuilds the row list.
func (v *diffView) setContent(raw string, threads []pr.ReviewThread) error {
	files, err := diff.Parse(raw)
	if err != nil {
		return err
	}
	v.files = files
	v.threads = threads
	v.rebuild()
	return nil
}

// setThreads updates the overlay without re-parsing the diff.
func (v *diffView) setThreads(threads []pr.ReviewThread) {
	v.threads = threads
	v.rebuild()
}

// threadIndex groups threads by path/side/line for O(1) overlay lookup.
func (v *diffView) threadIndex() map[string][]pr.ReviewThread {
	idx := make(map[string][]pr.ReviewThread, len(v.threads))
	for _, t := range v.threads {
		// A thread whose line is nil/0 (outdated diff) falls back to its
		// original line so it still surfaces somewhere sensible.
		line := t.Line
		if line == 0 {
			line = t.OriginalLine
		}
		side := t.DiffSide
		if side == "" {
			side = "RIGHT"
		}
		key := threadKey(t.Path, side, line)
		idx[key] = append(idx[key], t)
	}
	return idx
}

func threadKey(path, side string, line int) string {
	return path + "\x00" + side + "\x00" + fmt.Sprint(line)
}

// rebuild flattens files → rows, inserting thread rows under their anchors.
//
// Every thread the PR has must end up somewhere: one anchored in the diff sits
// under its line, and one whose anchor is missing (an outdated line, or a file
// this diff does not touch) is listed under a file header. Silently dropping it
// would hide review feedback the author still needs to answer.
func (v *diffView) rebuild() {
	idx := v.threadIndex()
	rows := make([]diffRow, 0, 256)

	appendThread := func(t pr.ReviewThread, fi, hi int, side string, anchor int, orphan bool) {
		rows = append(rows, diffRow{
			kind:       rowThread,
			fileIdx:    fi,
			path:       t.Path,
			hunkIdx:    hi,
			threadID:   t.ID,
			text:       renderThreadSummary(t, v.expanded[t.ID]),
			side:       side,
			anchorLine: anchor,
			commentIdx: 0,
			orphan:     orphan,
		})
		// Replies are hidden until asked for: a bot thread can run to a dozen
		// comments and would bury the code it is about.
		if !v.expanded[t.ID] {
			return
		}
		for ci := 1; ci < len(t.Comments); ci++ {
			rows = append(rows, diffRow{
				kind:       rowThread,
				fileIdx:    fi,
				path:       t.Path,
				hunkIdx:    hi,
				threadID:   t.ID,
				text:       renderThreadReply(t.Comments[ci]),
				side:       side,
				anchorLine: anchor,
				commentIdx: ci,
				orphan:     orphan,
			})
		}
	}

	for fi, f := range v.files {
		rows = append(rows, diffRow{
			kind:    rowFileHeader,
			fileIdx: fi,
			path:    f.Path,
			hunkIdx: -1,
			text:    v.fileHeaderText(f),
		})
		if v.folded[f.Path] {
			continue
		}
		// Threads for this file whose anchor the diff does not contain go right
		// after the header, where they are still associated with the file.
		for _, t := range v.orphanThreadsFor(f) {
			appendThread(t, fi, -1, orDefault(t.DiffSide, "RIGHT"), threadLine(t), true)
		}
		for hi, h := range f.Hunks {
			rows = append(rows, diffRow{
				kind:    rowHunkHeader,
				fileIdx: fi,
				path:    f.Path,
				hunkIdx: hi,
				text: fmt.Sprintf("@@ -%d,%d +%d,%d @@",
					h.OldStart, h.OldCount, h.NewStart, h.NewCount),
			})
			for _, l := range h.Lines {
				side, anchor, ok := diff.CommentTarget(l)
				rows = append(rows, diffRow{
					kind:       rowDiffLine,
					fileIdx:    fi,
					path:       f.Path,
					hunkIdx:    hi,
					line:       l,
					side:       side,
					anchorLine: anchor,
				})
				if !ok {
					continue
				}
				for _, t := range idx[threadKey(f.Path, side, anchor)] {
					appendThread(t, fi, hi, side, anchor, false)
				}
			}
		}
	}

	// Threads on files this diff does not include (the change moved on, or the
	// PR was force-pushed) have no header to sit under. Group them at the end so
	// they are still reachable instead of vanishing.
	if missing := v.threadsForMissingFiles(); len(missing) > 0 {
		rows = append(rows, diffRow{
			kind:    rowFileHeader,
			fileIdx: -1,
			hunkIdx: -1,
			text: fmt.Sprintf("%s comments on files not in this diff (%d)",
				iconFoldOpen, len(missing)),
		})
		for _, t := range missing {
			appendThread(t, -1, -1, orDefault(t.DiffSide, "RIGHT"), threadLine(t), true)
		}
	}

	v.rows = rows
	if v.cursor >= len(rows) {
		v.cursor = max(len(rows)-1, 0)
	}
}

func (v *diffView) fileHeaderText(f pr.DiffFile) string {
	fold := iconFoldOpen
	if v.folded[f.Path] {
		fold = iconFoldClosed
	}
	name := f.Path
	if f.OldPath != "" {
		name = f.OldPath + " → " + f.Path
	}
	return fmt.Sprintf("%s %s (+%d -%d)", fold, name, f.Additions, f.Deletions)
}

// renderThreadSummary formats a thread's opening comment as one overlay row.
// expanded controls the fold marker, so the row shows whether replies are hidden.
func renderThreadSummary(t pr.ReviewThread, expanded bool) string {
	author, body := "", ""
	if len(t.Comments) > 0 {
		author = t.Comments[0].Author.Login
		// Bot reviews open with badges and HTML; show the finding, not the markup.
		body = commentPreview(t.Comments[0].Body)
	}
	var meta []string
	if n := len(t.Comments); n > 1 {
		meta = append(meta, fmt.Sprintf("%d replies", n-1))
	}
	if threadIsResolved(t) {
		meta = append(meta, "resolved")
	} else if !t.ResolutionKnown {
		// Not the same as unresolved: say so rather than letting the absence of a
		// tag read as "still open".
		meta = append(meta, "resolution unknown")
	}
	// A multi-line thread names its range so the anchor row isn't misleading.
	if lo, hi, ok := threadRange(t); ok {
		meta = append(meta, fmt.Sprintf("lines %d-%d", lo, hi))
	}

	// Only a thread with replies gets a fold marker; a single comment has
	// nothing to expand and the glyph would just be noise.
	marker := " "
	if len(t.Comments) > 1 {
		marker = iconFoldClosed
		if expanded {
			marker = iconFoldOpen
		}
	}
	s := fmt.Sprintf("%s %s %s: %s", marker, iconComment, author, body)
	if len(meta) > 0 {
		s += " [" + strings.Join(meta, "] [") + "]"
	}
	return s
}

// renderThreadReply formats one reply, indented under its thread.
func renderThreadReply(c pr.ThreadComment) string {
	return fmt.Sprintf("    ↳ %s: %s", c.Author.Login, commentPreview(c.Body))
}

// orphanThreadsFor returns this file's threads whose anchor is absent from the
// diff, so they can be listed under the file header rather than dropped.
func (v *diffView) orphanThreadsFor(f pr.DiffFile) []pr.ReviewThread {
	anchored := v.anchorSet(f)
	var out []pr.ReviewThread
	for _, t := range v.threads {
		if t.Path != f.Path {
			continue
		}
		side := orDefault(t.DiffSide, "RIGHT")
		if anchored[threadKey(t.Path, side, threadLine(t))] {
			continue
		}
		out = append(out, t)
	}
	return out
}

// anchorSet collects every (path, side, line) the file's hunks can host.
func (v *diffView) anchorSet(f pr.DiffFile) map[string]bool {
	set := make(map[string]bool)
	for _, h := range f.Hunks {
		for _, l := range h.Lines {
			if side, anchor, ok := diff.CommentTarget(l); ok {
				set[threadKey(f.Path, side, anchor)] = true
			}
		}
	}
	return set
}

// threadsForMissingFiles returns threads whose file the diff does not contain at
// all. Those have no header to sit under, so the caller lists them separately.
func (v *diffView) threadsForMissingFiles() []pr.ReviewThread {
	inDiff := make(map[string]bool, len(v.files))
	for _, f := range v.files {
		inDiff[f.Path] = true
	}
	var out []pr.ReviewThread
	for _, t := range v.threads {
		if !inDiff[t.Path] {
			out = append(out, t)
		}
	}
	return out
}

func orDefault(s, def string) string {
	if s == "" {
		return def
	}
	return s
}

// --- navigation ---

func (v *diffView) moveCursor(delta int) {
	if len(v.rows) == 0 {
		return
	}
	v.cursor = clamp(v.cursor+delta, 0, len(v.rows)-1)
}

// moveDown steps the cursor by delta rows as the screen shows them. In the
// paired layout that is a screen row, which can hold two diff rows — stepping
// the flat index there would move the cursor without the view seeming to change.
func (v *diffView) moveDown(delta int) {
	if v.sideBySide {
		v.moveSideCursor(delta)
		return
	}
	v.moveCursor(delta)
}

func (v *diffView) page(delta int, height int) {
	v.moveDown(delta * max(height-2, 1))
}

func (v *diffView) toGutter(top bool) {
	if top {
		v.cursor = 0
		return
	}
	v.cursor = max(len(v.rows)-1, 0)
}

// toggleFold folds/unfolds the file the cursor is in, keeping the cursor on
// that file's header so the view doesn't jump somewhere unrelated.
func (v *diffView) toggleFold() {
	if len(v.rows) == 0 {
		return
	}
	v.setFold(v.rows[v.cursor].path, !v.folded[v.rows[v.cursor].path])
}

// setFold folds or unfolds one file and leaves the cursor on its header, which
// is the only row guaranteed to still exist afterwards.
//
// Reports false when nothing changed, so a caller bound to a directional key
// can fall through rather than consuming a keypress that did nothing — h on an
// already-folded file should still be free to mean something else.
func (v *diffView) setFold(path string, fold bool) bool {
	if path == "" || v.folded[path] == fold {
		return false
	}
	v.folded[path] = fold
	v.rebuild()
	for i, r := range v.rows {
		if r.kind == rowFileHeader && r.path == path {
			v.cursor = i
			break
		}
	}
	return true
}

// foldPathForCursor is the file h/l would act on: the one the cursor is in.
//
// Only a header row qualifies. Inside a hunk, h and l are the paired layout's
// column keys — taking them there would cost the only way to aim a comment at
// a deleted line, since a modification puts both sides on one screen row and
// the cursor cannot be moved to the other half any other way.
func (v *diffView) foldPathForCursor() (string, bool) {
	if len(v.rows) == 0 {
		return "", false
	}
	r := v.rows[v.cursor]
	if r.kind != rowFileHeader || r.path == "" {
		return "", false
	}
	return r.path, true
}

// commentTarget reports what the cursor is pointing at, for the composer.
// ok is false on header rows, which cannot host a comment.
func (v *diffView) commentTarget() (path, side string, line, startLine int, ok bool) {
	if len(v.rows) == 0 {
		return "", "", 0, 0, false
	}
	r := v.rows[v.cursor]
	if r.kind != rowDiffLine || r.side == "" || r.anchorLine == 0 {
		return "", "", 0, 0, false
	}
	if !v.visual {
		return r.path, r.side, r.anchorLine, 0, true
	}
	// Visual mode spans visualStart to the cursor. The other end is always
	// visualStart itself — ordering the two indices and reading the lower one
	// returns the cursor's own row whenever the selection was dragged upward,
	// which collapses the range to a single line.
	if v.visualStart < 0 || v.visualStart >= len(v.rows) {
		return r.path, r.side, r.anchorLine, 0, true
	}
	start := v.rows[v.visualStart]
	// A range must stay inside one file, one side, and one hunk. Crossing a hunk
	// boundary would describe lines that the diff does not contain, and crossing
	// sides has no meaning for GitHub; either way, fall back to commenting on
	// just the cursor's line rather than sending something invalid.
	if start.kind != rowDiffLine || start.path != r.path || start.side != r.side ||
		start.hunkIdx != r.hunkIdx || start.anchorLine == 0 ||
		start.anchorLine == r.anchorLine {
		return r.path, r.side, r.anchorLine, 0, true
	}
	return r.path, r.side, r.anchorLine, start.anchorLine, true
}

// threadUnderCursor returns the thread id when the cursor is on a thread row.
func (v *diffView) threadUnderCursor() (string, bool) {
	if len(v.rows) == 0 {
		return "", false
	}
	r := v.rows[v.cursor]
	if r.kind != rowThread {
		return "", false
	}
	return r.threadID, true
}

// toggleThread expands or collapses the replies of the thread under the cursor,
// keeping the cursor on that thread's opening row so the view does not jump.
func (v *diffView) toggleThread() bool {
	id, ok := v.threadUnderCursor()
	if !ok {
		return false
	}
	v.expanded[id] = !v.expanded[id]
	v.rebuild()
	for i, r := range v.rows {
		if r.kind == rowThread && r.threadID == id && r.commentIdx == 0 {
			v.cursor = i
			break
		}
	}
	return true
}

// threadHasReplies reports whether the thread under the cursor can be expanded.
func (v *diffView) threadHasReplies() bool {
	id, ok := v.threadUnderCursor()
	if !ok {
		return false
	}
	for _, t := range v.threads {
		if t.ID == id {
			return len(t.Comments) > 1
		}
	}
	return false
}

// jumpTo moves the cursor to a path/line, used when selecting from the Files
// or Comments tab.
func (v *diffView) jumpTo(path string, line int) {
	for i, r := range v.rows {
		if r.path != path {
			continue
		}
		if line <= 0 && r.kind == rowFileHeader {
			v.setCursor(i)
			return
		}
		if r.kind == rowDiffLine && r.anchorLine == line {
			v.setCursor(i)
			return
		}
	}
}

// setCursor moves the cursor and invalidates both scroll offsets so the next
// render brings the new position into view.
//
// Both layouts keep their own offset; updating one leaves the other showing
// wherever it was, which is what made a jump from the Comments tab appear to do
// nothing in side-by-side. -1 marks them stale so the render centres on the
// cursor rather than scrolling it to a screen edge.
func (v *diffView) setCursor(i int) {
	if i < 0 || i >= len(v.rows) {
		return
	}
	v.cursor = i
	v.offset = -1
	v.sideOffset = -1
	// Keep the focused column in step with the row landed on, or the paired
	// layout would highlight the opposite half of the pair.
	v.syncSideFocus()
}

// --- render ---

// render draws the visible window. Only the rows on screen are styled, so a
// multi-thousand-line diff costs the same per frame as a short one.
func (v *diffView) render(width, height int) string {
	if len(v.rows) == 0 {
		return dimStyle.Render("No diff.")
	}
	// Two columns need room for two gutters plus code; below that the unified
	// layout is the only one that can show anything useful.
	if v.sideBySide && width >= 80 {
		return v.renderSideBySide(width, height)
	}
	v.clampOffset(height)
	var b strings.Builder
	end := min(v.offset+height, len(v.rows))
	written := 0
	for i := v.offset; i < end && written < height; i++ {
		// A comment is prose and rarely fits one line; wrapping it keeps the
		// finding readable instead of cutting it off at the pane edge.
		lines := v.rowLines(i, width)
		for _, line := range lines {
			if written >= height {
				break
			}
			if written > 0 {
				b.WriteByte('\n')
			}
			b.WriteString(line)
			written++
		}
	}
	return b.String()
}

// rowLines renders a row as the one or more screen lines it needs.
func (v *diffView) rowLines(i, width int) []string {
	r := v.rows[i]
	if r.kind != rowThread {
		return []string{v.renderRow(i, width)}
	}
	wrapped := wrapText(v.threadText(r), max(width-6, 20))
	if len(wrapped) <= 1 {
		return []string{v.renderRow(i, width)}
	}
	out := make([]string, 0, len(wrapped))
	for n := range wrapped {
		out = append(out, v.renderThreadWrapped(i, width, n))
	}
	return out
}

// clampOffset scrolls just enough to keep the cursor visible. A negative offset
// means the position is stale (the cursor was moved by a jump), so centre on it
// instead of scrolling it to whichever edge happens to be nearer.
func (v *diffView) clampOffset(height int) {
	if height <= 0 {
		return
	}
	maxOffset := max(len(v.rows)-height, 0)
	if v.offset < 0 {
		v.offset = clamp(v.cursor-height/2, 0, maxOffset)
		return
	}
	if v.cursor < v.offset {
		v.offset = v.cursor
	}
	if v.cursor >= v.offset+height {
		v.offset = v.cursor - height + 1
	}
	v.offset = clamp(v.offset, 0, maxOffset)
}

func (v *diffView) renderRow(i, width int) string {
	r := v.rows[i]
	selected := i == v.cursor
	inVisual := v.visual && v.rowInSelection(i)

	// A highlighted row is rendered as plain text so the background covers the
	// whole line. Styling the content first leaves reset sequences inside it,
	// and a background wrapped around that stops at the first reset — the row
	// ends up striped instead of selected.
	if selected || inVisual {
		plain := v.rowPlainText(r)
		plain, _ = truncateExact(plain, width)
		if pad := width - lipglossWidth(plain); pad > 0 {
			plain += strings.Repeat(" ", pad)
		}
		if selected {
			return diffCursorStyle.Render(plain)
		}
		return selectedRowStyle.Render(plain)
	}

	var s string
	switch r.kind {
	case rowFileHeader:
		s = diffFileStyle.Render(r.text)
	case rowHunkHeader:
		s = diffHunkStyle.Render(r.text)
	case rowThread:
		// An orphan's anchor is not in view, so name the line it came from —
		// otherwise the comment reads as if it belongs to whatever is above it.
		text := r.text
		if r.orphan && r.commentIdx == 0 {
			text = fmt.Sprintf("%s (line %d, not in this diff)", text, r.anchorLine)
		}
		if strings.Contains(text, "[resolved]") {
			s = threadResolved.Render("    " + text)
		} else {
			s = threadStyle.Render("    " + text)
		}
	case rowDiffLine:
		s = renderDiffLine(r.path, r.line)
	}

	s, _ = truncateExact(s, width)
	if pad := width - lipglossWidth(s); pad > 0 {
		s += strings.Repeat(" ", pad)
	}
	return s
}

// rowPlainText renders a row without any styling, for rows that will be given a
// background instead.
func (v *diffView) rowPlainText(r diffRow) string {
	switch r.kind {
	case rowFileHeader, rowHunkHeader:
		return r.text
	case rowThread:
		text := r.text
		if r.orphan && r.commentIdx == 0 {
			text = fmt.Sprintf("%s (line %d, not in this diff)", text, r.anchorLine)
		}
		return "    " + text
	case rowDiffLine:
		marker := " "
		switch r.line.Kind {
		case pr.DiffLineAddition:
			marker = "+"
		case pr.DiffLineDeletion:
			marker = "-"
		}
		gutter := fmt.Sprintf("%5s %5s %s",
			lineNoStr(r.line.OldLineNo), lineNoStr(r.line.NewLineNo), marker)
		return gutter + expandTabs(r.line.Content, lipglossWidth(gutter))
	}
	return ""
}

func withinVisual(i, start, cursor int) bool {
	lo, hi := start, cursor
	if lo > hi {
		lo, hi = hi, lo
	}
	return i >= lo && i <= hi
}

// rowInSelection reports whether row i is part of the range that would actually
// be commented on. The raw span between visualStart and the cursor can cross a
// hunk or file boundary, which is not a valid range — highlighting those rows
// anyway would promise a comment the post cannot make.
func (v *diffView) rowInSelection(i int) bool {
	if !withinVisual(i, v.visualStart, v.cursor) {
		return false
	}
	if v.visualStart < 0 || v.visualStart >= len(v.rows) || v.cursor >= len(v.rows) {
		return false
	}
	start, cur := v.rows[v.visualStart], v.rows[v.cursor]
	// Outside a usable range, only the cursor's own row is really selected.
	if start.kind != rowDiffLine || cur.kind != rowDiffLine ||
		start.path != cur.path || start.side != cur.side || start.hunkIdx != cur.hunkIdx {
		return i == v.cursor
	}
	// Within the range, thread rows interleaved between diff lines are not part
	// of the comment; leave them unhighlighted so the span reads accurately.
	return v.rows[i].kind == rowDiffLine && v.rows[i].side == cur.side
}

// renderDiffLine styles one diff line: marker + line numbers + content, with
// the lightweight comment/keyword highlighting layered on top.
func renderDiffLine(path string, l pr.DiffLine) string {
	var marker string
	var style = diffCtxStyle
	switch l.Kind {
	case pr.DiffLineAddition:
		marker, style = "+", diffAddStyle
	case pr.DiffLineDeletion:
		marker, style = "-", diffDelStyle
	default:
		marker = " "
	}
	gutter := fmt.Sprintf("%5s %5s ", lineNoStr(l.OldLineNo), lineNoStr(l.NewLineNo))
	// The terminal expands a tab to the next tab stop, while width calculations
	// count it as one cell. Expanding it here keeps the measured width equal to
	// the drawn width, which is what stops later columns from drifting.
	content := expandTabs(l.Content, lipglossWidth(gutter)+1)
	// Comment lines read as secondary; dim them regardless of +/- color.
	if diff.IsCommentLine(path, l.Content) {
		return dimStyle.Render(gutter) + style.Render(marker) + dimStyle.Render(content)
	}
	// The keyword offset is into the original text, so highlight before expanding
	// tabs — slicing the expanded string with that offset would cut mid-word.
	if _, end, ok := diff.LeadingKeyword(path, l.Content); ok && end <= len(l.Content) {
		head := expandTabs(l.Content[:end], lipglossWidth(gutter)+1)
		tail := expandTabs(l.Content[end:], lipglossWidth(gutter)+1+lipglossWidth(head))
		return dimStyle.Render(gutter) + style.Render(marker) +
			diffHunkStyle.Render(head) + style.Render(tail)
	}
	return dimStyle.Render(gutter) + style.Render(marker+content)
}

func lineNoStr(n int) string {
	if n == 0 {
		return ""
	}
	return fmt.Sprint(n)
}

// helpLine returns the diff-tab footer hints. In visual mode it also states the
// range that would actually be commented on: the highlight can span a hunk
// boundary while the range cannot, and the operator needs to see which it is
// before pressing c.
func (v *diffView) helpLine() string {
	if !v.visual {
		layout := "side-by-side"
		if v.sideBySide {
			layout = "unified"
		}
		// On a thread row the keys mean different things, so say which ones.
		if _, onThread := v.threadUnderCursor(); onThread {
			action := "reply"
			if v.threadHasReplies() {
				action = "replies"
			}
			return fmtHints(
				"j/k", "line",
				"enter", action,
				"c", "reply",
				"s", layout,
				"esc", "back",
			)
		}
		// On a file header h/l fold rather than switch columns, so the footer
		// says which — the same two keys mean different things a row apart.
		if _, onHeader := v.foldPathForCursor(); onHeader {
			return fmtHints(
				"j/k", "line",
				"J/K", "hunk",
				"H/L", "file",
				"h/l", "fold",
				"s", layout,
				"esc", "back",
			)
		}
		hints := fmtHints(
			"j/k", "line",
			"J/K", "hunk",
			"H/L", "file",
			"h/l", "column",
			"c", "comment",
			"v", "visual",
			"s", layout,
			"esc", "back",
		)
		if !v.sideBySide {
			// o:fold is dropped rather than H/L: h/l fold from a file header,
			// which is where you are when folding, and the line stops fitting a
			// 100-column terminal at ten hints. o still works, and is in ?.
			hints = fmtHints(
				"j/k", "line",
				"J/K", "hunk",
				"H/L", "file",
				"c", "comment",
				"v", "visual",
				"s", layout,
				"enter", "thread",
				"esc", "back",
			)
		}
		return v.sidePrefix() + hints
	}
	hints := fmtHints("j/k", "extend", "c", "comment", "v", "cancel", "esc", "back")
	_, _, line, startLine, ok := v.commentTarget()
	if !ok {
		return dimStyle.Render("(not a commentable line) ") + hints
	}
	if startLine == 0 {
		// The selection is not a usable range — say so rather than letting the
		// highlight imply otherwise.
		return v.sidePrefix() + diffHunkStyle.Render(fmt.Sprintf("line %d only", line)) + " " + hints
	}
	lo, hi := startLine, line
	if lo > hi {
		lo, hi = hi, lo
	}
	return v.sidePrefix() + diffHunkStyle.Render(fmt.Sprintf("lines %d-%d", lo, hi)) + " " + hints
}

// sidePrefix names the column the cursor is in, so it is clear which side of a
// modification a comment would attach to. Only meaningful in the paired layout.
func (v *diffView) sidePrefix() string {
	if !v.sideBySide {
		return ""
	}
	label := "old"
	if v.sideFocus == sideRight {
		label = "new"
	}
	return tabActiveStyle.Render("["+label+"]") + " "
}

func clamp(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}
