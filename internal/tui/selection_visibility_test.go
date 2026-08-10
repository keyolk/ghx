package tui

import (
	"fmt"
	"regexp"
	"strings"
	"testing"

	"github.com/charmbracelet/bubbles/list"
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"

	"github.com/keyolk/ghx/internal/gh"
	"github.com/keyolk/ghx/internal/pr"
)

// A selected row has to read as one continuous band. Wrapping a background
// around cells that were already styled ends the highlight at the first reset
// sequence inside them, which leaves the row striped — visible in a screenshot
// as a selection you cannot pick out from its neighbours.

// resetSeq matches the SGR reset that ends a styled span.
var resetSeq = regexp.MustCompile(`\x1b\[0?m`)

// bgOpen matches a background-colour introducer (24-bit or 256-colour).
var bgOpen = regexp.MustCompile(`\x1b\[(?:[0-9;]*;)?4[87];`)

// forceColor makes lipgloss emit escape sequences during tests. Under `go test`
// stdout is not a terminal, so lipgloss strips all styling by default and every
// assertion about highlighting would trivially pass.
func forceColor(t *testing.T) {
	t.Helper()
	saved := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	t.Cleanup(func() { lipgloss.SetColorProfile(saved) })
}

// hasUnbrokenBackground reports whether the highlighted span is continuous.
//
// A row inside a bordered pane legitimately ends its background before the
// closing border, so the check is on the span itself: from the first background
// introducer to the reset that closes it, there must be no second introducer —
// that is what striping looks like (highlight, gap, highlight).
func hasUnbrokenBackground(line string) bool {
	locs := bgOpen.FindAllStringIndex(line, -1)
	if len(locs) == 0 {
		return false
	}
	if len(locs) > 1 {
		// More than one background span on a row that should have exactly one.
		return false
	}
	// The span must carry actual content, not just the padding between cells.
	start := locs[0][1]
	rest := line[start:]
	end := len(rest)
	if idx := resetSeq.FindStringIndex(rest); idx != nil {
		end = idx[0]
	}
	return strings.TrimSpace(stripANSISeqs(rest[:end])) != ""
}

// stripANSISeqs removes escape sequences so the visible text can be inspected.
func stripANSISeqs(s string) string {
	return regexp.MustCompile(`\x1b\[[0-9;]*m`).ReplaceAllString(s, "")
}

func TestSelectedListRowHasUnbrokenBackground(t *testing.T) {
	forceColor(t)
	items := []list.Item{
		prListItem{pr: pr.Summary{
			Number: 7535, Title: "jarvis: v-26.07.03 -> v-26.07.04",
			Repo: "acme/ops", State: "OPEN",
			Author: pr.User{Login: "miles-park"},
		}},
		prListItem{pr: pr.Summary{
			Number: 7528, Title: "Point Pulse publicBaseUrl at final example.com",
			Repo: "acme/ops", State: "OPEN",
			Author: pr.User{Login: "longlg88"},
		}},
	}
	l := list.New(items, prListDelegate{}, 140, 10)
	initListBase(&l)
	l.Select(0)

	var sb strings.Builder
	prListDelegate{}.Render(&sb, l, 0, items[0])
	selected := sb.String()

	if !bgOpen.MatchString(selected) {
		t.Fatal("the selected row sets no background at all")
	}
	if !hasUnbrokenBackground(selected) {
		t.Errorf("the selected row's background is interrupted mid-line:\n%q", selected)
	}
	// It must cover the full width, or the band stops short of the edge.
	if got := lipgloss.Width(selected); got != 140 {
		t.Errorf("selected row spans %d cells, want the full 140", got)
	}

	// An unselected row must not carry a background, or every row looks selected.
	sb.Reset()
	prListDelegate{}.Render(&sb, l, 1, items[1])
	if bgOpen.MatchString(sb.String()) {
		t.Error("an unselected row should not set a background")
	}
}

func TestMultiSelectedListRowShowsCheckMark(t *testing.T) {
	forceColor(t)
	first := prListItem{pr: pr.Summary{Number: 101, Repo: "acme/one", Title: "first"}}
	second := prListItem{pr: pr.Summary{Number: 202, Repo: "acme/two", Title: "second"}}
	items := []list.Item{first, second}
	delegate := prListDelegate{isSelected: func(summary prSummary) bool {
		return summary.Number == 202 && summary.Repo == "acme/two"
	}}
	l := list.New(items, delegate, 120, 10)
	initListBase(&l)
	l.Select(0)

	var sb strings.Builder
	delegate.Render(&sb, l, 1, second)
	rendered := sb.String()
	if !strings.Contains(stripANSISeqs(rendered), iconCheck) {
		t.Errorf("multi-selected row has no check mark: %q", rendered)
	}
	if got := lipgloss.Width(rendered); got != 120 {
		t.Errorf("multi-selected row spans %d cells, want 120", got)
	}
}

func TestSelectedRowContrastsWithUnselected(t *testing.T) {
	// The two must differ by more than boldness: on a black terminal a very dark
	// background is indistinguishable from none, which is the reported problem.
	bg := selectedRowStyle.GetBackground()
	if bg == nil {
		t.Fatal("selectedRowStyle sets no background")
	}
	r, g, b := hexToRGB(fmt.Sprint(bg))
	// Rec. 601 luma; a value this low reads as "unlit" next to pure black.
	luma := 0.299*float64(r) + 0.587*float64(g) + 0.114*float64(b)
	if luma < 45 {
		t.Errorf("selection background luma %.0f is too dark to notice (rgb %d,%d,%d)",
			luma, r, g, b)
	}
	if selectedRowStyle.GetForeground() == nil {
		t.Error("the selection should also set a foreground so text stays legible on it")
	}
}

// Each cursor-bearing view has the same trap; check them together so a new one
// cannot regress quietly.
func TestSelectedRowsAcrossViewsHaveUnbrokenBackground(t *testing.T) {
	forceColor(t)
	t.Run("checks", func(t *testing.T) {
		v := newChecksView()
		v.setChecks([]pr.Check{
			{Bucket: "fail", Name: "atlantis/plan", Workflow: "terraform"},
			{Bucket: "pass", Name: "ci/test", Workflow: "build"},
		})
		v.cursor = 0
		assertSelectedLine(t, v.render(120, 5))
	})

	t.Run("files", func(t *testing.T) {
		d := &prDetailModel{width: 120, height: 20}
		d.detail = &pr.Detail{Files: []pr.FileStat{
			{Path: "internal/tui/app.go", Additions: 12, Deletions: 3},
			{Path: "internal/gh/pr.go", Additions: 1, Deletions: 1},
		}}
		d.filesCursor = 0
		assertSelectedLine(t, d.renderFiles(120, 5))
	})

	t.Run("comments", func(t *testing.T) {
		c := newCommentsView()
		c.setThreads([]pr.ReviewThread{
			{ID: "a", Path: "app.go", Line: 10,
				Comments: []pr.ThreadComment{{Body: "first", Author: pr.User{Login: "alice"}}}},
			{ID: "b", Path: "app.go", Line: 20,
				Comments: []pr.ThreadComment{{Body: "second", Author: pr.User{Login: "bob"}}}},
		})
		c.cursor = 0
		assertSelectedLine(t, c.render(120, 6))
	})

	t.Run("diff", func(t *testing.T) {
		v := newDiffView()
		if err := v.setContent(threadsDiff, nil); err != nil {
			t.Fatalf("setContent: %v", err)
		}
		for i, r := range v.rows {
			if r.kind == rowDiffLine {
				v.cursor = i
				break
			}
		}
		assertSelectedLine(t, v.render(120, 10))
	})

	t.Run("labels", func(t *testing.T) {
		a := &App{width: 120, height: 30, client: gh.NewClient(0)}
		a.labels = &labelPicker{
			target:  actionTarget{number: 1, repo: "acme/one"},
			applied: map[string]bool{},
			pending: map[string]bool{},
			all: []gh.RepoLabel{
				{Name: "bug", Description: "a defect"},
				{Name: "docs", Description: "documentation"},
			},
		}
		// The filter prompt's block cursor also sets a background, so look for the
		// row carrying a label name rather than the first highlighted line.
		assertSelectedLineContaining(t, a.renderLabelPicker(120, 20), "bug")
	})
}

// assertSelectedLine finds the highlighted line in a rendered block and checks
// its background survives to the end.
func assertSelectedLine(t *testing.T, out string) {
	t.Helper()
	assertSelectedLineContaining(t, out, "")
}

// assertSelectedLineContaining is the same check narrowed to the highlighted
// line holding a given substring, for views that draw more than one.
func assertSelectedLineContaining(t *testing.T, out, want string) {
	t.Helper()
	for _, line := range strings.Split(out, "\n") {
		if !bgOpen.MatchString(line) {
			continue
		}
		if want != "" && !strings.Contains(line, want) {
			continue
		}
		if !hasUnbrokenBackground(line) {
			t.Errorf("highlighted line is striped:\n%q", line)
		}
		return
	}
	t.Errorf("no highlighted line containing %q found in the render", want)
}

// Losing colour must not lose the selection: reverse video is what tells the
// user which row the keys will act on when NO_COLOR is set.
func TestNoColorKeepsSelectionVisible(t *testing.T) {
	// SetNoColor mutates package-level styles and the global colour profile, so
	// snapshot everything it touches.
	savedRow, savedCursor := selectedRowStyle, diffCursorStyle
	savedProfile := lipgloss.ColorProfile()
	t.Cleanup(func() {
		selectedRowStyle, diffCursorStyle = savedRow, savedCursor
		lipgloss.SetColorProfile(savedProfile)
	})

	SetNoColor()

	if !selectedRowStyle.GetReverse() {
		t.Error("with NO_COLOR the selected row must use reverse video")
	}
	if !diffCursorStyle.GetReverse() {
		t.Error("with NO_COLOR the diff cursor must use reverse video")
	}

	// What matters is the emitted sequence: the row has to be distinguishable
	// from plain text, and must carry no colour.
	rendered := selectedRowStyle.Render("row")
	if rendered == "row" {
		t.Fatal("the selection renders identically to plain text under NO_COLOR")
	}
	if !strings.Contains(rendered, "\x1b[7m") {
		t.Errorf("expected the reverse-video attribute, got %q", rendered)
	}
	if bgOpen.MatchString(rendered) {
		t.Errorf("NO_COLOR must not emit a colour background: %q", rendered)
	}
	if regexp.MustCompile(`\x1b\[[0-9;]*3[0-9];?`).MatchString(rendered) {
		t.Errorf("NO_COLOR must not emit a foreground colour: %q", rendered)
	}
}
