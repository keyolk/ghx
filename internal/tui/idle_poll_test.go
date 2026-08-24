package tui

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/keyolk/ghx/internal/gh"
	"github.com/keyolk/ghx/internal/pr"
)

// The background poll is per-instance but the budget is per-account, so what
// this is really about is the sixth ghx window: six of them polling every 30
// seconds costs one account the same as one window polling every five seconds,
// and none of them is being looked at. Backing off when nobody has pressed a
// key is what makes the count stop mattering.

// idleApp is a testApp with the clock under the test's control.
func idleApp(t *testing.T) (*App, *time.Time) {
	t.Helper()
	a := testApp(t, sampleRows)
	now := time.Date(2026, 8, 24, 12, 0, 0, 0, time.UTC)
	a.nowFunc = func() time.Time { return now }
	a.lastKeyAt = now
	return a, &now
}

func TestPollBacksOffWhenIdle(t *testing.T) {
	a, now := idleApp(t)

	if got := a.pollInterval(); got != a.cfg.PollDuration() {
		t.Errorf("a freshly opened app polls at %s, want the active %s",
			got, a.cfg.PollDuration())
	}

	// Just short of the threshold is still active: a reviewer reading a long
	// diff has not stopped using ghx.
	*now = now.Add(a.cfg.IdleAfterDuration() - time.Second)
	if a.idle() {
		t.Error("app went idle before idle_after elapsed")
	}

	*now = now.Add(2 * time.Second)
	if !a.idle() {
		t.Fatal("app did not go idle after idle_after elapsed")
	}
	if got := a.pollInterval(); got != a.cfg.IdlePollDuration() {
		t.Errorf("idle poll interval is %s, want %s", got, a.cfg.IdlePollDuration())
	}
}

// A keypress after an idle stretch has to refresh right away: what is on screen
// is up to a whole idle interval old, and the person who pressed the key is the
// one reading it.
func TestKeypressWakesTheApp(t *testing.T) {
	a, now := idleApp(t)
	*now = now.Add(a.cfg.IdleAfterDuration() + time.Second)
	if !a.idle() {
		t.Fatal("setup: app should be idle")
	}

	cmd := a.noteActivity()
	if cmd == nil {
		t.Fatal("waking from idle produced no command — the list was not refreshed")
	}
	if a.idle() {
		t.Error("app is still idle after a keypress")
	}
	if got := a.pollInterval(); got != a.cfg.PollDuration() {
		t.Errorf("poll interval after waking is %s, want the active %s",
			got, a.cfg.PollDuration())
	}
}

// Waking up arms a new timer while the idle one is still pending — bubbletea
// cannot cancel a tea.Tick. Without the generation check both would fire and
// the cadence would silently double, which is the bug this whole change exists
// to avoid.
func TestStaleTickIsIgnoredAfterWaking(t *testing.T) {
	a, now := idleApp(t)
	*now = now.Add(a.cfg.IdleAfterDuration() + time.Second)

	stale := prListTickMsg{at: *now, gen: a.pollGen}
	a.noteActivity()

	a.list.inFlight = false
	if _, cmd := a.Update(stale); cmd != nil {
		t.Error("a tick from the superseded generation still triggered a fetch")
	}

	fresh := prListTickMsg{at: *now, gen: a.pollGen}
	a.list.inFlight = false
	if _, cmd := a.Update(fresh); cmd == nil {
		t.Error("the current generation's tick did not trigger a fetch")
	}
}

// The single-chain invariant. Only Init and the prListMsg that settles a fetch
// may arm a timer; anything else that arms one leaves two timers of the same
// generation running, which the generation check cannot separate because both
// are current. That doubled the cadence on every wake — in a change whose whole
// point is polling less — and the stale-generation test passed it, because
// neither timer was stale.
func TestWakingDoesNotDuplicateThePollChain(t *testing.T) {
	a, now := idleApp(t)
	*now = now.Add(a.cfg.IdleAfterDuration() + time.Second)

	// A fetch already in flight makes handlePollTick return nil, so whatever is
	// left in noteActivity's result is arming, not fetching. That isolates the
	// question this test asks without needing a network stub.
	a.list.inFlight = true
	if cmd := a.noteActivity(); cmd != nil {
		t.Errorf("waking produced %T besides the fetch — it armed a timer of its own", cmd())
	}

	// The fetch lands. This is one of the only two places allowed to arm, and it
	// must arm exactly one timer.
	a.list.inFlight = false
	_, cmd := a.Update(prListMsg{
		sourceIdx:  a.list.curTab,
		generation: a.list.generations[a.list.curTab],
	})
	if cmd == nil {
		t.Fatal("a settled fetch armed nothing — the poll chain died")
	}
	if n := countTicks(t, cmd, a.pollGen); n != 1 {
		t.Errorf("a settled fetch armed %d timers at the current generation, want 1", n)
	}
}

// countTicks counts prListPollCmd timers in a cmd tree. An armed timer is a
// tea.Tick that blocks for the poll interval, so it is identified by still being
// pending after a deadline far shorter than that interval — never by running it
// to completion, which would take minutes.
func countTicks(t *testing.T, cmd tea.Cmd, gen uint64) int {
	t.Helper()
	if cmd == nil {
		return 0
	}
	done := make(chan tea.Msg, 1)
	go func() { done <- cmd() }()
	select {
	case msg := <-done:
		if bm, ok := msg.(tea.BatchMsg); ok {
			n := 0
			for _, c := range bm {
				n += countTicks(t, c, gen)
			}
			return n
		}
		if tick, ok := msg.(prListTickMsg); ok && tick.gen == gen {
			return 1
		}
		return 0
	case <-time.After(200 * time.Millisecond):
		return 1
	}
}

// idle_after: "0" turns the backoff off, for anyone who would rather spend the
// budget than ever see a stale row.
func TestIdleBackoffCanBeDisabled(t *testing.T) {
	a, now := idleApp(t)
	a.cfg.IdleAfter = "0"
	a.cfg.ResetDerivedForTest()

	*now = now.Add(24 * time.Hour)
	if a.idle() {
		t.Error("app went idle with idle_after disabled")
	}
	if got := a.pollInterval(); got != a.cfg.PollDuration() {
		t.Errorf("poll interval is %s, want the active %s", got, a.cfg.PollDuration())
	}
}

// A config that made the idle interval shorter than the active one would make
// going idle cost more, not less.
func TestIdleIntervalIsNeverShorterThanActive(t *testing.T) {
	a, _ := idleApp(t)
	a.cfg.PollInterval = "60s"
	a.cfg.IdlePollInterval = "5s"
	a.cfg.ResetDerivedForTest()

	if got := a.cfg.IdlePollDuration(); got < a.cfg.PollDuration() {
		t.Errorf("idle interval %s is shorter than the active %s",
			got, a.cfg.PollDuration())
	}
}

// The whole point, as a number: an idle window must cost strictly fewer
// requests per hour than an active one.
func TestIdleWindowCostsLess(t *testing.T) {
	a, _ := idleApp(t)
	active := int(time.Hour / a.cfg.PollDuration())
	idle := int(time.Hour / a.cfg.IdlePollDuration())
	if idle >= active {
		t.Fatalf("idle costs %d polls/hour, active %d — no saving", idle, active)
	}
	t.Logf("active %d polls/hour → idle %d polls/hour (%.0f%% fewer)",
		active, idle, 100*(1-float64(idle)/float64(active)))
}

// The list must still be reachable through the ordinary key path — the activity
// hook wraps every keypress, so a mistake there breaks all of them.
func TestActivityHookDoesNotSwallowKeys(t *testing.T) {
	a := testApp(t, []pr.Summary{{Number: 1, Repo: "o/n", State: "OPEN"}})
	before := a.list.list.Index()
	if _, cmd := a.Update(tea.KeyMsg(keyMsg("?"))); cmd != nil {
		_ = cmd()
	}
	if !a.helpOpen {
		t.Error("? did not open the help overlay through the activity hook")
	}
	if a.list.list.Index() != before {
		t.Error("the activity hook moved the list cursor")
	}
}

// The budget guard is the half of this that covers the *other* windows: this
// instance's idle backoff does nothing about the five beside it, and the only
// thing any of them can see about the others is how much allowance is left.
func TestPollBacksOffAsTheBudgetDrains(t *testing.T) {
	base := 30 * time.Second
	for _, tc := range []struct {
		remaining int
		want      time.Duration
	}{
		{5000, base},
		{2600, base},     // 52% — untouched
		{2400, 2 * base}, // 48%
		{1400, 4 * base}, // 28%
		{700, 8 * base},  // 14%
		{200, 20 * base}, // 4%
	} {
		got := applyBudgetBackoff(base, gh.GraphQLBudget{
			Remaining: tc.remaining, Limit: 5000, Known: true,
		})
		if got != tc.want {
			t.Errorf("at %d/5000 remaining the interval is %s, want %s",
				tc.remaining, got, tc.want)
		}
	}
}

// Until a response has reported a rateLimit block nothing is known, and a zero
// Remaining must not be read as "exhausted" — that would back off hardest at
// startup, before a single request had been made.
func TestUnobservedBudgetDoesNotBackOff(t *testing.T) {
	base := 30 * time.Second
	if got := applyBudgetBackoff(base, gh.GraphQLBudget{}); got != base {
		t.Errorf("an unobserved budget stretched the interval to %s", got)
	}
	if got := (gh.GraphQLBudget{}).Fraction(); got != 1 {
		t.Errorf("an unobserved budget reports fraction %v, want 1", got)
	}
}

// Slowing the refresh without saying so reads as ghx being broken, which points
// at the wrong culprit: it is the account's budget, not the app.
func TestTitleSaysWhyThePollSlowedDown(t *testing.T) {
	a, now := idleApp(t)
	a.width = 120

	if note := a.pollStatusNote(); note != "" {
		t.Errorf("an active app advertises a slowdown: %q", note)
	}

	*now = now.Add(a.cfg.IdleAfterDuration() + time.Second)
	note := stripANSISeqs(a.pollStatusNote())
	if !strings.Contains(note, "idle") {
		t.Errorf("idle note is %q, want it to mention idle", note)
	}
	if !strings.Contains(note, shortDuration(a.cfg.IdlePollDuration())) {
		t.Errorf("idle note %q does not state the new interval", note)
	}
	if !strings.Contains(stripANSISeqs(a.titleLine()), "idle") {
		t.Error("the title line does not carry the note")
	}
}

// The note is right-aligned into whatever room is left, so a narrow terminal
// must drop it rather than push the breadcrumb off the line or overflow it.
func TestTitleNoteNeverOverflows(t *testing.T) {
	a, now := idleApp(t)
	*now = now.Add(a.cfg.IdleAfterDuration() + time.Second)
	for _, w := range []int{20, 40, 80, 120, 250} {
		a.width = w
		if got := lipglossWidth(a.titleLine()); got > w {
			t.Errorf("at width %d the title line is %d cells", w, got)
		}
	}
}

// The throttle and the reading feed each other: a low reading stretches the
// interval, and the stretched interval is what keeps a fresh reading from
// arriving. At 4% remaining the poll is 100 minutes apart, so the hourly reset
// passes unnoticed — and without this guard ghx keeps throttling on an expired
// number and keeps telling the user "API 4%" while the account is back to full.
func TestExpiredBudgetReadingIsDiscarded(t *testing.T) {
	a, now := idleApp(t)
	reset := now.Add(30 * time.Minute)
	a.client.ObserveBudgetForTest(200, 5000, reset)

	if got := a.budget(); !got.Known {
		t.Fatal("a reading inside its window was discarded")
	}
	if a.pollInterval() <= a.cfg.PollDuration() {
		t.Error("a nearly-empty budget did not stretch the interval")
	}
	if !strings.Contains(stripANSISeqs(a.pollStatusNote()), "API") {
		t.Error("the note does not mention the budget while it is low")
	}

	// Past the reset the reading means nothing. lastKeyAt moves with the clock
	// so this isolates the budget guard from the idle backoff — otherwise the
	// interval would be the idle one and the assertion would be about the wrong
	// mechanism.
	*now = reset.Add(time.Second)
	a.lastKeyAt = *now
	if a.budget().Known {
		t.Error("a reading past its resetAt is still treated as current")
	}
	if got := a.pollInterval(); got != a.cfg.PollDuration() {
		t.Errorf("still throttled to %s on an expired reading, want %s",
			got, a.cfg.PollDuration())
	}
	if strings.Contains(stripANSISeqs(a.pollStatusNote()), "API") {
		t.Error("the note still advertises the expired budget")
	}
}
