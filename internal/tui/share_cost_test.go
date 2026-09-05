package tui

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/keyolk/ghx/internal/config"
	"github.com/keyolk/ghx/internal/gh"
	"github.com/keyolk/ghx/internal/pr"
)

// The measurement that matters for ghx is not one window's cost, it is the
// account's. A ghx parked in every tmux window is the normal way this is used,
// and the six instances measured in gh/budget.go each polled the same queues
// against one 5,000/hour pool. Sharing what they fetch is the only thing that
// makes the total scale with the work rather than with the number of windows.
//
// These assert the sharing on the same gate the other cost tests use: a gh on
// PATH that logs every invocation.

// drive runs a cmd tree and feeds every message it produces back through the
// app, which is what a real bubbletea loop does. Without the feedback the fetch
// happens but its response is discarded, and nothing is ever written to the
// cache the next window is supposed to read.
func drive(a *App, cmd tea.Cmd) {
	if cmd == nil {
		return
	}
	switch msg := cmd().(type) {
	case tea.BatchMsg:
		for _, c := range msg {
			drive(a, c)
		}
	case nil:
	default:
		_, next := a.Update(msg)
		// Only the response matters here; whatever it arms is a timer.
		_ = next
	}
}

// sharedCacheApp builds an app whose list cache points at dir, so several apps
// in one test are several ghx processes looking at one cache directory.
func sharedCacheApp(t *testing.T, dir string, sources []config.SourceDef) *App {
	t.Helper()
	cfg := config.DefaultConfig()
	cfg.Sources = sources
	a := NewApp(cfg, DefaultKeymap(), gh.NewClient(0))
	a.width, a.height = 160, 40
	a.list.fileCache = &prFileCache{dir: dir}
	for i := range a.list.caches {
		a.list.caches[i] = nil
		a.list.fetchedAt[i] = time.Time{}
	}
	a.list.syncListItems()
	return a
}

var oneSource = []config.SourceDef{
	{Name: "My reviews", Query: "review-requested:@me state:open"},
}

// A second window opening on a queue the first one just fetched must not fetch
// it again. This is the whole shape of the saving: the cost of a source is paid
// once per interval by whichever instance got there first.
func TestASecondWindowReusesAFreshQueue(t *testing.T) {
	calls := countingGH(t)
	dir := t.TempDir()

	first := sharedCacheApp(t, dir, oneSource)
	drive(first, first.list.init())
	if n := len(calls()); n == 0 {
		t.Fatal("the first window sent no request at all")
	}
	before := len(calls())

	second := sharedCacheApp(t, dir, oneSource)
	drive(second, second.list.init())
	if n := len(calls()); n != before {
		t.Errorf("the second window sent %d requests for a queue the first had "+
			"just fetched:\n%s", n-before, strings.Join(calls()[before:], "\n"))
	}
}

// The hand-off has to be visible as rows, not just as a skipped request. A
// window that saves a fetch and shows an empty pane has traded the budget for
// the thing the budget was being spent on.
func TestTheReusedQueueShowsItsRows(t *testing.T) {
	countingGH(t)
	dir := t.TempDir()
	cache := &prFileCache{dir: dir}
	rows := []pr.Summary{
		{Number: 7, Repo: "acme/one", State: "OPEN", Title: "from the other window"},
	}
	cache.save(oneSource[0], rows)

	a := sharedCacheApp(t, dir, oneSource)
	cmd := a.list.fetchSource(0)
	if cmd == nil {
		t.Fatal("no fetch was issued")
	}
	msg, ok := cmd().(prListMsg)
	if !ok {
		t.Fatalf("fetch produced %T, want a prListMsg", cmd())
	}
	if len(msg.prs) != 1 || msg.prs[0].Number != 7 {
		t.Fatalf("the shared answer did not carry its rows: %+v", msg.prs)
	}
	if msg.fetchedAt.IsZero() {
		t.Error("a handed-over answer must date the rows to the fetch that made " +
			"them, not to the window that received them")
	}
}

// Rows taken from another window are as old as that window's fetch. Stamping
// them "just now" would make the age readout — the one thing on screen that
// says whether the queue can be trusted — report a fetch that never happened.
func TestAReusedQueueKeepsTheOriginalAge(t *testing.T) {
	countingGH(t)
	dir := t.TempDir()
	a := sharedCacheApp(t, dir, oneSource)

	saved := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	a.list.nowFunc = func() time.Time { return saved.Add(20 * time.Second) }
	a.list.handlePRListMsg(prListMsg{
		sourceIdx: 0, generation: a.list.generations[0],
		prs:       []pr.Summary{{Number: 1, Repo: "acme/one", State: "OPEN"}},
		fetchedAt: saved,
	})
	if got := a.list.fetchedAt[0]; !got.Equal(saved) {
		t.Errorf("age stamp = %v, want the original fetch time %v", got, saved)
	}
	if note := a.list.lastFetchNote(); note != "20s ago" {
		t.Errorf("the title said %q, want the handed-over rows dated honestly", note)
	}
}

// Reusing an entry must not rewrite it. If every reader pushed SavedAt forward,
// the instances would keep handing each other an answer whose stamp says it is
// current while no request had been made for however long that went on.
func TestReusingAQueueDoesNotRefreshItsStamp(t *testing.T) {
	countingGH(t)
	dir := t.TempDir()
	cache := &prFileCache{dir: dir}
	cache.save(oneSource[0], []pr.Summary{{Number: 1, Repo: "acme/one", State: "OPEN"}})
	_, saved := cache.loadAt(oneSource[0], 0)

	a := sharedCacheApp(t, dir, oneSource)
	a.list.handlePRListMsg(prListMsg{
		sourceIdx: 0, generation: a.list.generations[0],
		prs:       []pr.Summary{{Number: 1, Repo: "acme/one", State: "OPEN"}},
		fetchedAt: saved,
	})

	_, after := cache.loadAt(oneSource[0], 0)
	if !after.Equal(saved) {
		t.Errorf("the entry's stamp moved from %v to %v without a request", saved, after)
	}
}

// An empty queue is a real answer, and most source tabs are empty most of the
// time. Treating "no rows" as a miss would make the emptiest tabs the most
// expensive ones — re-fetched by every window on every poll precisely because
// there was nothing to remember.
func TestAnEmptyQueueIsSharedToo(t *testing.T) {
	calls := countingGH(t)
	dir := t.TempDir()
	(&prFileCache{dir: dir}).save(oneSource[0], nil)

	a := sharedCacheApp(t, dir, oneSource)
	runAll(a.list.fetchSource(0))
	if n := len(calls()); n != 0 {
		t.Errorf("a known-empty queue cost %d requests:\n%s",
			n, strings.Join(calls(), "\n"))
	}
}

// R, the palette, and every action taken in this window are asking about a
// change no already-written entry can contain. Answering those from a
// neighbour's cache would show the queue exactly as it was before the thing the
// user just did.
func TestAManualRefreshNeverTakesANeighboursAnswer(t *testing.T) {
	calls := countingGH(t)
	dir := t.TempDir()
	(&prFileCache{dir: dir}).save(oneSource[0],
		[]pr.Summary{{Number: 1, Repo: "acme/one", State: "OPEN"}})

	a := sharedCacheApp(t, dir, oneSource)
	runAll(a.list.refreshCurrent())
	if n := len(calls()); n == 0 {
		t.Error("R was answered from the cache — a refresh that sends no request " +
			"is not a refresh")
	}
}

// The git-event path fires because a commit or push landed seconds ago. Every
// cached entry for that source predates it, so taking one would reset the age
// readout while still showing the queue from before the push.
func TestAGitEventNeverTakesANeighboursAnswer(t *testing.T) {
	calls := countingGH(t)
	dir := t.TempDir()
	src := config.SourceDef{Name: "ghx", Query: "state:open", Repo: "keyolk/ghx"}
	(&prFileCache{dir: dir}).save(src,
		[]pr.Summary{{Number: 1, Repo: "keyolk/ghx", State: "OPEN"}})

	a := sharedCacheApp(t, dir, oneSource)
	a.list.appendSource(src, nil)
	runAll(a.list.reloadSource(len(a.list.sources) - 1))
	if n := len(calls()); n == 0 {
		t.Error("a push was answered from a cache written before it happened")
	}
}

// The window widens with the cadence. A window that has backed off to the idle
// interval — or been throttled by a draining budget — should accept a
// correspondingly older answer, not keep re-fetching on the assumption of a
// cadence it is no longer using.
func TestTheSharingWindowFollowsThePollCadence(t *testing.T) {
	a := sharedCacheApp(t, t.TempDir(), oneSource)
	if got, want := a.list.shareWindow(), 27*time.Second; got != want {
		t.Errorf("share window = %v at the default 30s cadence, want %v", got, want)
	}

	// Idle backs the poll off to five minutes; the window follows.
	a.nowFunc = func() time.Time { return time.Now().Add(time.Hour) }
	if got, want := a.list.shareWindow(), 270*time.Second; got != want {
		t.Errorf("share window = %v once idle, want %v", got, want)
	}
}

// The window must stay strictly under the interval. At exactly the interval a
// lone ghx — whose own entry is precisely one interval old when its next poll
// comes round — would reuse or refetch on scheduling jitter, skipping every
// other refresh at random with nothing on screen explaining why.
func TestALoneWindowAlwaysRefetchesAtItsOwnCadence(t *testing.T) {
	a := sharedCacheApp(t, t.TempDir(), oneSource)
	interval := a.pollInterval()
	if w := a.list.shareWindow(); w >= interval {
		t.Fatalf("share window %v is not under the %v cadence", w, interval)
	}
	// The margin has to outlast process scheduling, not merely be nonzero.
	if margin := interval - a.list.shareWindow(); margin < time.Second {
		t.Errorf("margin of %v is within jitter of the cadence", margin)
	}
}

// The steady-state fetch rate must not depend on how many windows are open —
// that is the whole claim. With a window of f·I, a fetch happens once every f·I
// no matter how many instances are polling, so the account pays the same for
// one ghx as for eight.
func TestTheFetchRateDoesNotGrowWithTheNumberOfWindows(t *testing.T) {
	countingGH(t)
	dir := t.TempDir()
	cache := &prFileCache{dir: dir}

	windows := make([]*App, 6)
	for i := range windows {
		windows[i] = sharedCacheApp(t, dir, oneSource)
	}
	interval := windows[0].pollInterval()
	window := windows[0].list.shareWindow()

	// Walk a simulated hour, each instance polling every interval, staggered.
	// Only the age of the shared entry decides a fetch, so the clock is the
	// entry's stamp rather than a real one.
	var fetches int
	saved := time.Time{}
	step := interval / time.Duration(len(windows))
	for now := time.Duration(0); now < time.Hour; now += step {
		at := time.Time{}.Add(now)
		if saved.IsZero() || at.Sub(saved) > window {
			fetches++
			saved = at
			cache.save(oneSource[0], nil)
		}
	}

	// One window alone would fetch 3600/30 = 120 times. The shared rate is
	// 1/(f·I), so a ninth more — never six times more.
	if fetches > 140 {
		t.Errorf("six windows fetched %d times an hour; one window alone would "+
			"fetch %d, and the point of sharing is not to exceed that materially",
			fetches, int(time.Hour/interval))
	}
	if fetches < int(time.Hour/interval) {
		t.Errorf("six windows fetched %d times an hour, fewer than the %d a "+
			"single window would — the queue is going stale, not being shared",
			fetches, int(time.Hour/interval))
	}
}

// A model built without an App has no cadence to derive a window from, and must
// then fetch rather than serve an entry it cannot bound.
func TestNoCadenceMeansNoSharing(t *testing.T) {
	calls := countingGH(t)
	dir := t.TempDir()
	(&prFileCache{dir: dir}).save(oneSource[0],
		[]pr.Summary{{Number: 1, Repo: "acme/one", State: "OPEN"}})

	cfg := config.DefaultConfig()
	cfg.Sources = oneSource
	m := newPRListModel(cfg, gh.NewClient(0), DefaultKeymap())
	m.fileCache = &prFileCache{dir: dir}
	m.pollIntervalFunc = nil
	runAll(m.fetchSource(0))
	if n := len(calls()); n == 0 {
		t.Error("a model with no cadence served an entry it had no window for")
	}
}

var _ tea.Cmd = (tea.Cmd)(nil)
