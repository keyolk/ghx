package tui

import (
	"strings"
	"testing"

	"github.com/keyolk/ghx/internal/pr"
)

// Whether a conversation is resolved was carried by colour, a strikethrough,
// and a "[resolved]" tag buried mid-row — and the selection band overwrote the
// first two, so the row under the cursor was the one row with no state signal
// at all. That is the row being read.
//
// The state now leads the row as a glyph: it lines up in a column, it survives
// the band because it sits outside it, and it is a character rather than an
// attribute, so it reads with NO_COLOR too.

func threeThreads() []pr.ReviewThread {
	return []pr.ReviewThread{
		{ID: "T1", Path: "app.go", Line: 10, ResolutionKnown: true, IsResolved: false,
			Comments: []pr.ThreadComment{{Author: pr.User{Login: "alice"}, Body: "needs a guard"}}},
		{ID: "T2", Path: "main.go", Line: 20, ResolutionKnown: true, IsResolved: true,
			Comments: []pr.ThreadComment{{Author: pr.User{Login: "bob"}, Body: "fixed"}}},
		{ID: "", Path: "rest.go", Line: 30, ResolutionKnown: false,
			Comments: []pr.ThreadComment{{Author: pr.User{Login: "carol"}, Body: "over REST"}}},
	}
}

func commentsWithAll() *commentsView {
	c := newCommentsView()
	c.hideResolved = false
	c.threads = threeThreads()
	return c
}

// The three states must be told apart by a leading character, not by colour
// alone — a NO_COLOR terminal and a screenshot both drop the colour.
func TestThreadRowsLeadWithAStateGlyph(t *testing.T) {
	c := commentsWithAll()
	want := []string{iconThreadOpen, iconThreadResolved, iconThreadUnknown}
	for i, glyph := range want {
		row := stripANSISeqs(c.threadHeader(c.threads[i], false, 90))
		if !strings.HasPrefix(row, glyph+" ") {
			t.Errorf("row %d = %q, want it to start with %q", i, row, glyph)
		}
	}

	// The glyphs must differ from each other, or the column says nothing.
	seen := map[string]bool{}
	for _, g := range want {
		if seen[g] {
			t.Fatalf("two states share the glyph %q", g)
		}
		seen[g] = true
	}
}

// The regression: the selection band used to cover the whole row, taking the
// colour and the strikethrough with it.
func TestSelectedRowKeepsItsStateGlyph(t *testing.T) {
	forceColor(t)
	c := commentsWithAll()

	for i, glyph := range []string{iconThreadOpen, iconThreadResolved, iconThreadUnknown} {
		row := c.threadHeader(c.threads[i], true, 90)
		if !strings.HasPrefix(stripANSISeqs(row), glyph+" ") {
			t.Errorf("selected row %d lost its glyph: %q", i, stripANSISeqs(row))
		}
		// The glyph keeps its own colour: it is rendered before the band starts.
		if !bgOpen.MatchString(row) {
			t.Errorf("selected row %d has no selection band at all", i)
		}
		if idx := bgOpen.FindStringIndex(row); idx != nil {
			if !strings.Contains(stripANSISeqs(row[:idx[0]]), glyph) {
				t.Errorf("row %d: the glyph is inside the band, so the band overwrites it", i)
			}
		}
	}
}

// The row must still be exactly as wide as the pane; the glyph is part of the
// budget, not an addition to it.
func TestThreadRowFitsTheWidth(t *testing.T) {
	forceColor(t)
	c := commentsWithAll()
	for _, width := range []int{40, 80, 90, 200} {
		for i := range c.threads {
			for _, selected := range []bool{false, true} {
				row := c.threadHeader(c.threads[i], selected, width)
				if got := lipglossWidth(row); got > width {
					t.Errorf("row %d (selected=%v) is %d cells wide at width %d",
						i, selected, got, width)
				}
			}
		}
	}
}

// A comment can quote the word: the style must come from the thread's state,
// not from whether its rendered text happens to contain "[resolved]".
func TestResolvedStylingComesFromStateNotText(t *testing.T) {
	v := &diffView{
		files: []pr.DiffFile{{Path: "app.go", Hunks: []pr.DiffHunk{{
			Lines: []pr.DiffLine{{Kind: pr.DiffLineContext, Content: "x", NewLineNo: 1, OldLineNo: 1}},
		}}}},
		threads: []pr.ReviewThread{{
			ID: "T1", Path: "app.go", Line: 1, ResolutionKnown: true, IsResolved: false,
			Comments: []pr.ThreadComment{{
				Author: pr.User{Login: "alice"},
				// The literal string the old code keyed off.
				Body: "I marked the other one [resolved] already",
			}},
		}},
	}
	v.rebuild()

	var found bool
	for _, r := range v.rows {
		if r.kind != rowThread {
			continue
		}
		found = true
		if r.state != threadOpen {
			t.Errorf("a thread quoting [resolved] was classified as %v, want open", r.state)
		}
	}
	if !found {
		t.Fatal("no thread row was built")
	}
}

// countThreadStates feeds the tab count, which has to describe what opening the
// tab shows — resolved threads are hidden by default, so a bare total disagrees
// with the list underneath it.
func TestThreadStateCounts(t *testing.T) {
	open, done, unknown := countThreadStates(threeThreads())
	if open != 1 || done != 1 || unknown != 1 {
		t.Errorf("counts = open %d, done %d, unknown %d; want 1/1/1", open, done, unknown)
	}

	// An unknown-resolution thread counts as outstanding, because it is visible
	// for the same reason: nothing established that it is finished.
	if open+unknown != 2 {
		t.Errorf("outstanding = %d, want the open and the unknown one", open+unknown)
	}
}

// The legend names the states that are actually on screen. Keyed to the visible
// rows rather than to the filter: an unknown-resolution thread survives the
// default filter, so `?` can appear without `t` ever being pressed, and a glyph
// nobody can decode is not a signal.
func TestGlyphLegendNamesTheVisibleStates(t *testing.T) {
	c := commentsWithAll() // hideResolved = false, all three states
	got := stripANSISeqs(c.helpLine())
	for _, glyph := range []string{iconThreadOpen, iconThreadResolved, iconThreadUnknown} {
		if !strings.Contains(got, glyph) {
			t.Errorf("the legend does not name %q: %q", glyph, got)
		}
	}

	// Hiding resolved threads drops it from the legend, because it is no longer
	// on screen to be told apart from anything.
	c.hideResolved = true
	got = stripANSISeqs(c.helpLine())
	if strings.Contains(got, iconThreadResolved+" resolved") {
		t.Errorf("the legend names a state that is filtered out: %q", got)
	}
	if !strings.Contains(got, iconThreadUnknown+" unknown") {
		t.Errorf("the legend dropped a state that is still on screen: %q", got)
	}
}

// Hiding resolved threads is the default, and an unknown-resolution thread must
// survive it: hiding it would assert it is finished, which is the one thing
// nothing here knows.
func TestUnknownResolutionSurvivesTheFilter(t *testing.T) {
	c := newCommentsView()
	c.threads = threeThreads()

	visible := c.visible()
	if len(visible) != 2 {
		t.Fatalf("filter left %d threads, want the open one and the unknown one", len(visible))
	}
	for _, thread := range visible {
		if stateOf(thread) == threadDone {
			t.Error("a resolved thread survived the default filter")
		}
	}
}

// The footer holds itself to 100 cells, the width detail_footer_test.go pins
// for every other tab. It was passing only because the fixture had no threads:
// the legend appears with them, and it appears at the end, where truncation
// takes it. fitLegend drops it whole rather than showing half a key.
func TestCommentsFooterFitsACommonTerminalWithThreads(t *testing.T) {
	c := newCommentsView()
	c.threads = threeThreads()
	for _, hide := range []bool{true, false} {
		c.hideResolved = hide
		got := c.helpLine()
		if w := lipglossWidth(got); w > 100 {
			t.Errorf("hideResolved=%v: footer is %d cells — a 100-column terminal cuts it:\n%s",
				hide, w, stripANSISeqs(got))
		}
		// And the legend has to actually be there: dropping it at every width
		// would pass the check above while losing the thing it protects.
		if !strings.Contains(stripANSISeqs(got), iconThreadOpen+" open") {
			t.Errorf("hideResolved=%v: the legend was dropped at 100 columns:\n%s",
				hide, stripANSISeqs(got))
		}
	}
}

// A half-truncated legend reads as a label rather than a key, so it goes whole
// or not at all.
func TestLegendIsDroppedRatherThanTruncated(t *testing.T) {
	hints := "j/k:thread"
	legend := iconThreadOpen + " open " + iconThreadResolved + " resolved"
	if got := fitLegend(hints, legend, 200); !strings.Contains(got, legend) {
		t.Errorf("the legend was dropped despite fitting: %q", got)
	}
	if got := fitLegend(hints, legend, 12); got != hints {
		t.Errorf("a legend that does not fit was truncated instead of dropped: %q", got)
	}
}
