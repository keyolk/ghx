package tui

import (
	"strings"
	"testing"

	"github.com/keyolk/ghx/internal/pr"
)

// A config value often carries a whole document as a single YAML/JSON scalar,
// its line breaks written as `\n` escapes. The diff then has one line hundreds
// of cells wide, and truncated to the pane it shows nothing of what changed —
// the case that prompted this was a helm values.yaml whose `updatedNodePool:`
// string grew by 90 lines and rendered as one row ending in "…".

func TestSplitEscapedNewlines(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want []string
	}{
		{"no escapes", `plain: value`, nil},
		{"one break", `a\nb`, []string{"a", "b"}},
		{"leading break", `\na`, []string{"", "a"}},
		{"trailing break", `a\n`, []string{"a", ""}},
		// A doubled backslash is a backslash followed by the letter n — a
		// Windows path or a regex — not a line break. It unquotes to one
		// backslash, which is what the file actually holds.
		{"escaped backslash", `a\\nb`, []string{`a\nb`}},
		{"backslash then break", `a\\\nb`, []string{`a\`, "b"}},
		// The other escapes are resolved too: a segment still wearing them reads
		// no better than the folded line did.
		{"tab becomes a tab", `a\tb\nc`, []string{"a\tb", "c"}},
		{"quotes unquote", `say \"hi\"\nthen`, []string{`say "hi"`, "then"}},
		// An escape with no meaning is left alone rather than silently dropped.
		{"unknown escape kept", `a\qb\nc`, []string{`a\qb`, "c"}},
		// A CRLF document would otherwise show two stray characters per segment.
		{"crlf", `a\r\nb`, []string{"a", "b"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := splitEscapedNewlines(c.in)
			if strings.Join(got, "\x00") != strings.Join(c.want, "\x00") {
				t.Errorf("splitEscapedNewlines(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}

// Unfolding is for embedded documents, not for every string with a newline in
// it. A Go format string has one `\n` and splitting those would double the row
// count of an ordinary diff for nothing.
func TestUnfoldOnlyAppliesToEmbeddedDocuments(t *testing.T) {
	long := strings.Repeat("x", 200)
	cases := []struct {
		name    string
		content string
		avail   int
		want    bool
	}{
		{"single escape is a format string", `fmt.Errorf("boom: %w\n", err)`, 20, false},
		{"two escapes is a document", `a\nb\n` + long, 20, true},
		{"three escapes is a document", `a\nb\nc\n` + long, 20, true},
		// Nothing is being lost to truncation, so extra rows would only push the
		// rest of the diff off screen.
		{"fits on one row", `a\nb\nc\nd`, 80, false},
		{"no width to speak of", `a\nb\nc\n` + long, 0, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := unfoldContent(c.content, c.avail) != nil
			if got != c.want {
				t.Errorf("unfoldContent unfolded = %v, want %v", got, c.want)
			}
		})
	}
}

// escapedDiff is one addition whose content is a whole YAML document, the shape
// the helm `updatedNodePool:` value has.
const escapedDiff = "diff --git a/values.yaml b/values.yaml\n" +
	"--- a/values.yaml\n" +
	"+++ b/values.yaml\n" +
	"@@ -10,3 +10,3 @@\n" +
	" before\n" +
	`-  updatedNodePool: "spot:\n  weight: 60\n  limits:\n    cpu: 1000"` + "\n" +
	`+  updatedNodePool: "spot:\n  weight: 60\n  limits:\n    cpu: 2000\n  taints: []"` + "\n" +
	" after\n"

func escapedView(t *testing.T) *diffView {
	t.Helper()
	v := newDiffView()
	if err := v.setContent(escapedDiff, nil); err != nil {
		t.Fatalf("setContent: %v", err)
	}
	return v
}

// The point of the whole thing: every segment of the value is on screen, not
// just the first pane-width of it.
func TestUnifiedRenderShowsEverySegment(t *testing.T) {
	forceColor(t)
	v := escapedView(t)
	out := v.render(60, 40)
	for _, want := range []string{"weight: 60", "cpu: 2000", "taints: []"} {
		if !strings.Contains(out, want) {
			t.Errorf("unified render is missing %q — the value was truncated:\n%s", want, out)
		}
	}
}

// The row model is untouched: one diff line stays one row, so the cursor, the
// visual range, and the comment anchor all keep meaning what they meant.
func TestUnfoldingDoesNotChangeTheRowModel(t *testing.T) {
	v := escapedView(t)
	got := 0
	for _, r := range v.rows {
		if r.kind == rowDiffLine {
			got++
		}
	}
	if got != 4 {
		t.Fatalf("%d diff rows, want 4 — unfolding must not add rows", got)
	}
	// The addition anchors to the line it actually is, not to a segment of it.
	for i, r := range v.rows {
		if r.kind == rowDiffLine && r.line.Kind == pr.DiffLineAddition {
			v.cursor = i
			break
		}
	}
	path, side, line, _, ok := v.commentTarget()
	if !ok || path != "values.yaml" || side != "RIGHT" || line != 11 {
		t.Errorf("commentTarget = (%q, %q, %d, ok=%v), want (values.yaml, RIGHT, 11, true)",
			path, side, line, ok)
	}
}

// A continuation is not a line of the file. Numbering it would claim the file
// has lines it does not, and those numbers are what a comment anchors to.
func TestOnlyTheFirstSegmentCarriesLineNumbers(t *testing.T) {
	forceColor(t)
	v := escapedView(t)
	row := -1
	for i, r := range v.rows {
		if r.kind == rowDiffLine && r.line.Kind == pr.DiffLineAddition {
			row = i
			break
		}
	}
	if row < 0 {
		t.Fatal("no addition row")
	}
	segs := v.unfoldUnified(row, 60)
	if segs == nil {
		t.Fatal("the value did not unfold at width 60")
	}
	first := stripANSI(v.renderUnfoldedLine(row, 60, 0, segs))
	if !strings.Contains(first, "11") {
		t.Errorf("the first segment lost its line number: %q", first)
	}
	for n := 1; n < len(segs); n++ {
		cont := stripANSI(v.renderUnfoldedLine(row, 60, n, segs))
		gutter := cont
		if len(gutter) > unifiedGutterCells {
			gutter = gutter[:unifiedGutterCells]
		}
		if strings.ContainsAny(gutter, "0123456789") {
			t.Errorf("segment %d numbered its gutter %q — it is not a line of the file", n, gutter)
		}
	}
}

// Without a marker the block reads as lines the file has. The escape is where
// the break came from, so it is drawn.
func TestSegmentsMarkWhereTheEscapeWas(t *testing.T) {
	forceColor(t)
	v := escapedView(t)
	out := stripANSI(v.render(60, 40))
	if !strings.Contains(out, iconEscapedBreak) {
		t.Errorf("no break marker in the unfolded value:\n%s", out)
	}
	// The last segment ends the string, so it carries no marker.
	lines := strings.Split(out, "\n")
	var last string
	for _, l := range lines {
		if strings.Contains(l, "taints: []") {
			last = l
		}
	}
	if last == "" {
		t.Fatal("the final segment was not rendered")
	}
	if strings.Contains(last, iconEscapedBreak) {
		t.Errorf("the final segment claims a break that is not there: %q", last)
	}
}

// The cursor has to stay on screen, and the window is measured in screen rows
// while the offset is a row index. An unfolded row can be taller than the whole
// window, and counting it as one would leave the cursor off the bottom — j
// would look like it had stopped working.
func TestScrollingKeepsAnUnfoldedCursorOnScreen(t *testing.T) {
	forceColor(t)
	var b strings.Builder
	b.WriteString("diff --git a/v.yaml b/v.yaml\n--- a/v.yaml\n+++ b/v.yaml\n@@ -1,30 +1,30 @@\n")
	for i := 0; i < 30; i++ {
		b.WriteString(` config: "a\nb\nc\nd\ne\nf\ng\nh` + "\"\n")
	}
	v := newDiffView()
	if err := v.setContent(b.String(), nil); err != nil {
		t.Fatalf("setContent: %v", err)
	}
	const width, height = 40, 12

	for step := 0; step < 30; step++ {
		v.moveDown(1)
		out := v.render(width, height)
		if n := len(strings.Split(out, "\n")); n > height {
			t.Fatalf("step %d: render produced %d rows, over the %d-row window", step, n, height)
		}
		// The cursor's row must begin inside the drawn window.
		used := 0
		for i := v.offset; i < v.cursor; i++ {
			used += v.screenLines(i, width)
		}
		if v.offset > v.cursor || used >= height {
			t.Fatalf("step %d: cursor row %d starts %d rows into a %d-row window (offset %d)",
				step, v.cursor, used, height, v.offset)
		}
	}
}

// Side-by-side has the same problem in a narrower column, and one more: the two
// halves of a modification unfold to different lengths, so the pair must be as
// tall as the taller one or the shorter side's tail is dropped.
func TestSideBySideShowsBothSidesOfAnUnfoldedChange(t *testing.T) {
	forceColor(t)
	v := escapedView(t)
	v.sideBySide = true
	out := stripANSI(v.renderSideBySide(120, 40))
	for _, want := range []string{"cpu: 1000", "cpu: 2000", "taints: []"} {
		if !strings.Contains(out, want) {
			t.Errorf("side-by-side is missing %q:\n%s", want, out)
		}
	}
	// Every screen row must be the same width, or the divider walks from row to
	// row — which is the alignment this layout exists to provide.
	rows := strings.Split(out, "\n")
	want := lipglossWidth(rows[0])
	for i, line := range rows {
		if w := lipglossWidth(line); w != want {
			t.Errorf("row %d is %d cells wide, want %d: %q", i, w, want, line)
		}
	}
}

// The shorter half runs out first. Its remaining rows are blank rather than a
// repeat of its last segment, which would read as content the file does not have.
func TestTheShorterHalfGoesBlankNotRepeated(t *testing.T) {
	forceColor(t)
	v := escapedView(t)
	v.sideBySide = true
	const half = 50
	pairs := v.sideRowsWidth(half)
	var tall sidePair
	for _, p := range pairs {
		if p.left >= 0 && p.right >= 0 && v.rows[p.left].kind == rowDiffLine &&
			v.rows[p.left].line.Kind != v.rows[p.right].line.Kind && p.wrapLine > 0 {
			tall = p
		}
	}
	if tall.left < 0 || tall.right < 0 {
		t.Fatal("no paired modification with continuation rows")
	}
	left := v.unfoldHalf(tall.left, half)
	if tall.wrapLine < len(left) {
		t.Skipf("both halves still have content at wrap line %d", tall.wrapLine)
	}
	cell := stripANSI(v.renderHalf(tall.left, half, sideLeft, tall.wrapLine))
	if strings.TrimSpace(cell) != "" {
		t.Errorf("the exhausted half repeated content: %q", cell)
	}
	if lipglossWidth(cell) != half {
		t.Errorf("the blank half is %d cells, want %d — the divider would move",
			lipglossWidth(cell), half)
	}
}
