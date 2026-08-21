package tui

import (
	"fmt"
	"strings"
	"testing"

	"github.com/keyolk/ghx/internal/gh"
	"github.com/keyolk/ghx/internal/pr"
)

// The reported problem: the tab index strip at the top of the PR detail view
// was sometimes missing. It was a frame-height bug, not a tab-strip bug — the
// body was sized to the full terminal height while View also draws a title line
// and a footer, so a tall tab overflowed the frame by two rows. bubbletea's
// renderer keeps the LAST height lines of an overflowing frame, which drops the
// title and the tab strip off the top.
//
// Anything that can fill the body must therefore be checked, and the check is
// on the whole frame rather than on any one view.
func TestViewNeverOverflowsTerminalHeight(t *testing.T) {
	var raw strings.Builder
	raw.WriteString("diff --git a/a.txt b/a.txt\n--- a/a.txt\n+++ b/a.txt\n@@ -1,80 +1,80 @@\n")
	for i := 0; i < 80; i++ {
		raw.WriteString("+a line of added code\n")
	}

	threads := make([]pr.ReviewThread, 0, 40)
	for i := 0; i < 40; i++ {
		threads = append(threads, pr.ReviewThread{
			ID: string(rune('A'+i%26)) + "-thread", Path: "a.txt", Line: 1,
			Comments: []pr.ThreadComment{{Body: strings.Repeat("long finding text ", 12)}},
		})
	}

	checks := make([]pr.Check, 0, 40)
	for i := 0; i < 40; i++ {
		checks = append(checks, pr.Check{Name: "check", Bucket: "pass"})
	}

	rows := make([]pr.Summary, 0, 60)
	for i := 0; i < 60; i++ {
		rows = append(rows, pr.Summary{Number: i + 1, Title: "a pull request", Repo: "o/n", State: "OPEN"})
	}

	// Sizes span what the bug was actually reported at: a narrow pane, and a
	// full-screen 250x62 terminal. Overflow is a function of content height,
	// not terminal size, so a wide screen is not automatically safe — it just
	// needs more content to overflow, which a long PR description supplies.
	for _, dim := range []struct{ w, h int }{
		{120, 8}, {120, 12}, {120, 24}, {120, 30}, {120, 50},
		{250, 62}, {200, 45}, {80, 20},
	} {
		w, h := dim.w, dim.h
		a := testApp(t, rows)
		a.width, a.height = w, h
		a.list.resize(w, h)

		a.state = viewPRDetail
		a.detail = newPRDetailModel(a.cfg, a.client, a.km, 1, "o/n")
		a.detail.resize(w, h)
		if err := a.detail.diff.setContent(raw.String(), threads); err != nil {
			t.Fatal(err)
		}
		a.detail.comments.setThreads(threads)
		a.detail.checks.setChecks(checks)
		// A long description is what makes the Overview tab overflow, and
		// Overview is where the header vanished in the field. Without a body
		// this tab is a dozen rows and fits anywhere.
		a.detail.detail = &pr.Detail{
			Number: 1, Title: "a pull request", State: "OPEN",
			Body: strings.Repeat("A line of the PR description.\n", 150),
		}

		for _, tab := range a.detail.tabs {
			a.detail.activeTab = tab
			assertFrameFits(t, a, h, fmt.Sprintf("%s at %dx%d",
				detailTabNames[tab], w, h))
		}

		// Every overlay pads the body out to the height it is handed, so each is
		// its own chance to overflow the frame.
		a.detail.activeTab = tabOverview
		for _, ov := range overlayCases(a) {
			ov.open()
			assertFrameFits(t, a, h, fmt.Sprintf("detail+%s at %dx%d", ov.name, w, h))
			ov.close()
		}

		a.state = viewPRList
		assertFrameFits(t, a, h, fmt.Sprintf("list at %dx%d", w, h))
		for _, ov := range overlayCases(a) {
			ov.open()
			assertFrameFits(t, a, h, fmt.Sprintf("list+%s at %dx%d", ov.name, w, h))
			ov.close()
		}
	}
}

// The title line and the tab strip are the first two rows of the frame, and the
// bug was that they were the ones the terminal dropped. Assert they are present
// with a body tall enough to have caused it.
func TestDetailTabStripSurvivesAFullHeightBody(t *testing.T) {
	var raw strings.Builder
	raw.WriteString("diff --git a/a.txt b/a.txt\n--- a/a.txt\n+++ b/a.txt\n@@ -1,80 +1,80 @@\n")
	for i := 0; i < 80; i++ {
		raw.WriteString("+a line of added code\n")
	}
	a := testApp(t, []pr.Summary{{Number: 1, Repo: "o/n", State: "OPEN"}})
	const h = 30
	a.width, a.height = 120, h
	a.state = viewPRDetail
	a.detail = newPRDetailModel(a.cfg, a.client, a.km, 1, "o/n")
	a.detail.resize(120, h)
	a.detail.activeTab = tabDiff
	if err := a.detail.diff.setContent(raw.String(), nil); err != nil {
		t.Fatal(err)
	}

	lines := strings.Split(a.View(), "\n")
	if len(lines) < 2 {
		t.Fatalf("frame has %d lines", len(lines))
	}
	if !strings.Contains(lines[0], "ghx") {
		t.Errorf("first row is not the title line: %q", lines[0])
	}
	if got := leadingDigits(lines[1]); got != "123456" {
		t.Errorf("second row is not the tab strip: %q (indices %q)", lines[1], got)
	}
}

type overlayCase struct {
	name  string
	open  func()
	close func()
}

func overlayCases(a *App) []overlayCase {
	return []overlayCase{
		{"help", func() { a.helpOpen = true }, func() { a.helpOpen = false }},
		{"palette", func() { a.palette.open() }, func() { a.palette.active = false }},
		{"search", func() { a.search.open("") }, func() { a.search.active = false }},
		{"merge", func() { a.mergePrompt = &mergePrompt{strategy: "squash"} },
			func() { a.mergePrompt = nil }},
		{"confirm", func() {
			a.confirm = &confirmPrompt{kind: confirmApprove, targets: []actionTarget{{number: 1, repo: "o/n"}}}
		}, func() { a.confirm = nil }},
		{"labels", func() {
			// Through the real constructor: the picker's render assumes at least
			// one target, and a hand-built zero value would only prove that a
			// state the app cannot reach also does not overflow.
			a.openLabelPickerTargets([]actionTarget{{number: 1, repo: "o/n"}})
			a.labels.all = manyLabels(40)
			a.labels.loading = false
		}, func() { a.labels = nil }},
		{"statusFilter", func() { a.statusFilter = newStatusFilterPicker(map[prStatus]bool{}) },
			func() { a.statusFilter = nil }},
		{"composer", func() { a.composer.active = true }, func() { a.composer.active = false }},
	}
}

func manyLabels(n int) []gh.RepoLabel {
	out := make([]gh.RepoLabel, 0, n)
	for i := 0; i < n; i++ {
		out = append(out, gh.RepoLabel{Name: "label", Description: "a described label"})
	}
	return out
}

func assertFrameFits(t *testing.T, a *App, h int, label string) {
	t.Helper()
	got := len(strings.Split(a.View(), "\n"))
	if got > h {
		t.Errorf("%s at height %d rendered %d rows — the terminal would drop the top %d",
			label, h, got, got-h)
	}
}
