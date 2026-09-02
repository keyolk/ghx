package tui

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/keyolk/ghx/internal/config"
	"github.com/keyolk/ghx/internal/pr"
)

// A polled list looks exactly the same whether the poll is working or died
// twenty minutes ago — the rows are the same rows either way. Nothing else on
// screen distinguishes an expired credential, a suspended poll, and a queue
// that genuinely has not changed.

func freshApp(t *testing.T) (*App, *time.Time) {
	t.Helper()
	a, now := idleApp(t)
	a.list.nowFunc = func() time.Time { return *now }
	return a, now
}

func TestTitleDatesTheRowsOnScreen(t *testing.T) {
	a, now := freshApp(t)

	// Nothing has answered yet. An empty tab already says "Loading…" or names
	// its error; "never" beside that is a second, vaguer version of the same.
	if got := a.list.lastFetchNote(); got != "" {
		t.Errorf("an unfetched source claims an age: %q", got)
	}

	a.list.handlePRListMsg(prListMsg{sourceIdx: 0, prs: sampleRows})
	if got := a.list.lastFetchNote(); got != "just now" {
		t.Errorf("straight after a fetch the note is %q, want just now", got)
	}

	*now = now.Add(7 * time.Minute)
	if got := a.list.lastFetchNote(); got != "7m ago" {
		t.Errorf("note is %q, want 7m ago", got)
	}
	if !strings.Contains(stripANSISeqs(a.titleLine()), "7m ago") {
		t.Error("the title line does not carry the age")
	}
}

// The cadence is stated even when it is the configured one: "polling every 30s"
// and "not polling at all" must not render identically, which is the whole
// question this corner of the title answers.
func TestTitleAlwaysStatesTheCadence(t *testing.T) {
	a, _ := freshApp(t)
	a.list.handlePRListMsg(prListMsg{sourceIdx: 0, prs: sampleRows})

	note := stripANSISeqs(a.freshnessNote())
	if !strings.Contains(note, "poll 30s") {
		t.Errorf("note %q does not state the running cadence", note)
	}

	// An unfocused window arms no timer at all, so it must not name an interval
	// — saying "poll 30s" while nothing is scheduled is the specific lie here.
	a.blur()
	note = stripANSISeqs(a.freshnessNote())
	if !strings.Contains(note, "paused") {
		t.Errorf("a blurred app does not say polling stopped: %q", note)
	}
	if strings.Contains(note, "poll 30s") {
		t.Errorf("a blurred app advertises a cadence it is not running: %q", note)
	}
	// The age still shows: rows frozen at whatever they were is exactly when
	// their age matters most.
	if !strings.Contains(note, "just now") {
		t.Errorf("a blurred app dropped the age: %q", note)
	}
}

// A refresh with rows already on screen replaces nothing until it lands, so the
// title is the only place it is visible at all.
func TestTitleSaysWhenAFetchIsRunning(t *testing.T) {
	a, _ := freshApp(t)
	a.list.handlePRListMsg(prListMsg{sourceIdx: 0, prs: sampleRows})

	a.list.handlePollTick()
	note := a.list.lastFetchNote()
	if !strings.Contains(note, "fetching") {
		t.Errorf("a poll in flight is invisible: %q", note)
	}
	// The spinner only advances while something reports being busy, and a poll
	// sets no per-source loading flag — so without this the glyph sits frozen.
	if !a.anyLoading() {
		t.Error("a poll in flight does not animate the spinner")
	}

	a.list.handlePRListMsg(prListMsg{sourceIdx: 0, prs: sampleRows})
	if got := a.list.lastFetchNote(); got != "just now" {
		t.Errorf("after the poll landed the note is %q, want just now", got)
	}
}

// Only the visible tab polls, so the tabs age independently. One clock for all
// of them would call an hour-old tab current because the tab beside it just
// refreshed.
func TestEachSourceCarriesItsOwnAge(t *testing.T) {
	a, now := freshApp(t)
	a.list.appendSource(config.SourceDef{Name: "other", Query: "state:open"}, nil)

	a.list.handlePRListMsg(prListMsg{sourceIdx: 0, prs: sampleRows})
	*now = now.Add(2 * time.Hour)
	a.list.handlePRListMsg(prListMsg{sourceIdx: 1, prs: sampleRows})

	if got := a.list.lastFetchNote(); got != "2h ago" {
		t.Errorf("tab 0 reports %q, want 2h ago", got)
	}
	a.list.selectTab(1)
	if got := a.list.lastFetchNote(); got != "just now" {
		t.Errorf("tab 1 reports %q, want just now", got)
	}
}

// A superseded response releases the poll slot even though its rows are thrown
// away: the slot tracks an outstanding request, not a usable answer. Holding it
// would wedge the chain and spin "fetching" forever.
func TestSupersededResponseReleasesThePollSlot(t *testing.T) {
	a, _ := freshApp(t)
	a.list.handlePollTick()
	a.list.generations[0]++ // a bulk action invalidated the source mid-flight

	a.list.handlePRListMsg(prListMsg{sourceIdx: 0, prs: sampleRows})
	if a.list.inFlight {
		t.Fatal("the poll slot is still held by a discarded response")
	}
	if strings.Contains(a.list.lastFetchNote(), "fetching") {
		t.Error("the title still claims a fetch is running")
	}
}

// Keeping the rows when an account fails is deliberate; keeping their date is
// the other half of it. Restamping would call a queue current at exactly the
// moment the account that owns its PRs could not be reached.
func TestAWarningWithNoRowsDoesNotRestampTheAge(t *testing.T) {
	a, now := freshApp(t)
	a.list.handlePRListMsg(prListMsg{sourceIdx: 0, prs: sampleRows})

	*now = now.Add(20 * time.Minute)
	a.list.handlePRListMsg(prListMsg{
		sourceIdx: 0,
		prs:       nil,
		warning:   errors.New("account work: bad credentials"),
	})
	if got := a.list.lastFetchNote(); got != "20m ago" {
		t.Errorf("note is %q, want the rows' real age 20m ago", got)
	}
}

// A restart shows the previous session's rows immediately. Stamping them "now"
// would be the one lie the readout exists to prevent — they are as old as the
// file says.
func TestSeededRowsCarryTheCacheAge(t *testing.T) {
	dir := t.TempDir()
	cache := &prFileCache{dir: dir}
	src := config.SourceDef{Name: "test", Query: "state:open"}
	cache.save(src, []pr.Summary{{Number: 1, Title: "cached", Repo: "acme/one"}})

	cfg := config.DefaultConfig()
	cfg.Sources = []config.SourceDef{src}
	m := newPRListModel(cfg, nil, DefaultKeymap())
	m.fileCache = cache
	if cached, savedAt := cache.loadAt(src, 0); cached != nil {
		m.caches[0] = cached
		m.fetchedAt[0] = savedAt
	}
	m.nowFunc = func() time.Time { return time.Now().Add(3 * time.Hour) }

	if got := m.lastFetchNote(); got != "3h ago" {
		t.Errorf("seeded rows report %q, want 3h ago", got)
	}
}

func TestRelativeAgeBuckets(t *testing.T) {
	cases := []struct {
		d    time.Duration
		want string
	}{
		{time.Second, "just now"},
		{9 * time.Second, "just now"},
		// Bucketed to 5s so the readout is not a ticking clock: the question is
		// "is this current", not "what time is it".
		{22 * time.Second, "20s ago"},
		{90 * time.Second, "1m ago"},
		{59 * time.Minute, "59m ago"},
		{90 * time.Minute, "1h ago"},
		{50 * time.Hour, "2d ago"},
	}
	for _, c := range cases {
		if got := relativeAge(c.d); got != c.want {
			t.Errorf("relativeAge(%s) = %q, want %q", c.d, got, c.want)
		}
	}
}
