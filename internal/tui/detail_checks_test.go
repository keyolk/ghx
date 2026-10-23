package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/keyolk/ghx/internal/gh"
	"github.com/keyolk/ghx/internal/pr"
)

func TestCheckDurationFormats(t *testing.T) {
	base := time.Date(2026, 7, 26, 3, 0, 0, 0, time.UTC)
	cases := []struct {
		name  string
		check pr.Check
		want  string
	}{
		{"seconds", pr.Check{StartedAt: base, CompletedAt: base.Add(3 * time.Second)}, "3s"},
		{"minutes", pr.Check{StartedAt: base, CompletedAt: base.Add(107 * time.Second)}, "1m47s"},
		{"hours", pr.Check{StartedAt: base, CompletedAt: base.Add(75 * time.Minute)}, "1h15m"},
		{"not started", pr.Check{}, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := checkDuration(c.check); got != c.want {
				t.Errorf("checkDuration = %q, want %q", got, c.want)
			}
		})
	}
}

// A still-running check has no CompletedAt; the elapsed time must be measured
// against now rather than rendering as empty or negative.
func TestCheckDurationRunningUsesNow(t *testing.T) {
	c := pr.Check{StartedAt: time.Now().Add(-90 * time.Second)}
	got := checkDuration(c)
	if got == "" {
		t.Fatal("a running check should still report elapsed time")
	}
	if !strings.HasPrefix(got, "1m") {
		t.Errorf("checkDuration = %q, want ~1m30s", got)
	}
}

func TestHasPendingDrivesPolling(t *testing.T) {
	v := newChecksView()
	v.setChecks([]pr.Check{{Bucket: "pass"}, {Bucket: "fail"}})
	if v.hasPending() {
		t.Error("settled checks must not keep the poll alive")
	}
	v.setChecks([]pr.Check{{Bucket: "pass"}, {Bucket: "pending"}})
	if !v.hasPending() {
		t.Error("a pending check must keep the poll alive")
	}
}

// Log output arrives as one blob; the viewer has to split it into scrollable
// lines and clamp scrolling to the content.
func TestSetLogsSplitsAndClamps(t *testing.T) {
	v := newChecksView()
	v.logBusy = true
	v.setLogs("drongo coverage", "line one\nline two\nline three\n")

	if v.logBusy {
		t.Error("logBusy must clear once logs arrive")
	}
	if !v.showingLogs() {
		t.Error("showingLogs must be true after logs load")
	}
	if len(v.logs) != 3 {
		t.Fatalf("logs = %d lines, want 3 (trailing newline must not add one)", len(v.logs))
	}

	// Scrolling past the end must clamp instead of panicking on render.
	v.scrollLogs(1000)
	if v.logOff > len(v.logs)-1 {
		t.Errorf("logOff = %d, want <= %d", v.logOff, len(v.logs)-1)
	}
	v.scrollLogs(-1000)
	if v.logOff != 0 {
		t.Errorf("logOff = %d after scrolling up, want 0", v.logOff)
	}

	out := v.render(80, 10)
	if !strings.Contains(out, "drongo coverage") {
		t.Errorf("log view should name the check: %s", out)
	}
	if !strings.Contains(out, "line one") {
		t.Errorf("log view should show content: %s", out)
	}
}

// A large log must render only the visible window, not the whole blob — this is
// what keeps a 9000-line run log from costing a full re-render every frame.
func TestRenderLogsOnlyVisibleWindow(t *testing.T) {
	var b strings.Builder
	for i := 0; i < 9000; i++ {
		b.WriteString("log line\n")
	}
	v := newChecksView()
	v.setLogs("big", b.String())

	const height = 20
	out := v.render(80, height)
	got := len(strings.Split(out, "\n"))
	if got > height {
		t.Errorf("rendered %d lines for height %d — the window is not being clipped", got, height)
	}
}

func TestClosingLogsReturnsToList(t *testing.T) {
	v := newChecksView()
	v.setChecks([]pr.Check{{Bucket: "fail", Name: "ci/test"}})
	v.setLogs("ci/test", "boom")
	v.closeLogs()
	if v.showingLogs() {
		t.Error("closeLogs must return to the check list")
	}
	out := v.render(80, 5)
	if !strings.Contains(out, "ci/test") {
		t.Errorf("check list should render after closing logs: %s", out)
	}
}

// The run id has to come out of the check's details URL; without it there is no
// way to ask for logs, and the UI must say so rather than silently doing nothing.
func TestParseRunURL(t *testing.T) {
	cases := []struct {
		name             string
		link             string
		wantRun, wantJob string
		wantOK           bool
	}{
		{
			"run and job",
			"https://github.com/keyolk/ghx/actions/runs/30186219614/job/94822137755",
			"30186219614", "94822137755", true,
		},
		{
			"run only",
			"https://github.com/keyolk/ghx/actions/runs/30186219614",
			"30186219614", "", true,
		},
		{
			"external status has no run",
			"https://atlantis.example.com/jobs/abc123",
			"", "", false,
		},
		{"empty", "", "", "", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			run, job, ok := gh.ParseRunURL(c.link)
			if ok != c.wantOK || run != c.wantRun || job != c.wantJob {
				t.Errorf("got (%q, %q, %v), want (%q, %q, %v)",
					run, job, ok, c.wantRun, c.wantJob, c.wantOK)
			}
		})
	}
}

func TestCheckRowFitsWidth(t *testing.T) {
	c := pr.Check{
		Bucket: "fail", Name: "a-very-long-check-name-that-would-overflow-a-narrow-pane",
		// The name is an OSC 8 link, which must take no width.
		Link:     "https://github.com/acme/one/actions/runs/1/job/2",
		Workflow: "some-workflow", StartedAt: time.Now().Add(-time.Minute),
		CompletedAt: time.Now(),
	}
	for _, w := range []int{40, 80, 200} {
		row := checkRow(c, w)
		if !strings.Contains(row, "\x1b]8;;"+c.Link) {
			t.Fatalf("width %d: the check name is not linked: %q", w, row)
		}
		if got := lipglossWidth(row); got > w {
			t.Errorf("width %d: row renders %d cells", w, got)
		}
	}
}
