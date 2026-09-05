package gh

import (
	"path/filepath"
	"sync"
	"time"

	"github.com/keyolk/ghx/internal/cachefile"
)

// The GraphQL budget is 5,000 points per hour per *account*, not per process.
// Almost everything ghx reads is GraphQL — pr list, pr view, pr checks, search
// prs, plus the status enrichment — so several ghx windows open at once share
// one pool without any of them knowing.
//
// Measured on a real day: six instances left open in tmux, each polling every
// 30 seconds, burned ~141 points/minute between them. That empties the hourly
// budget in about 35 minutes, at which point every window's PR list and status
// markers go blank at the same time.
//
// Rather than spending a request to ask, the enrichment query carries a
// rateLimit block and reports what it saw. That makes the observation free, and
// it comes from the same response as the data — so it is never staler than the
// rows on screen.

// The reading is also shared *between* processes, through a file.
//
// Two things make that necessary rather than merely tidy. The pool being
// measured is the account's, so a window that has spent nothing still has to
// know what the other five spent — the whole failure this guards against is
// invisible from inside any single instance. And now that unchanged rows are
// served from the status cache, an instance can go a long time without sending
// the query that carries the rateLimit block: the free observation stops
// arriving exactly when the queues are quiet, which is when a window is most
// likely to be sitting on a reading from an hour ago. Reading what another
// instance last saw closes both.
//
// One file for every account, matching the in-memory rule this has always
// used: the lowest remaining within a window wins. It conflates accounts, and
// that is the safe direction — the throttle it feeds slows polling down, so the
// tightest account governs and no account is polled as though it had the
// allowance of another.

// budgetReading is the on-disk shape.
type budgetReading struct {
	Remaining int       `json:"remaining"`
	Limit     int       `json:"limit"`
	ResetAt   time.Time `json:"reset_at"`
}

// budgetDiskInterval is how often the file is consulted. GraphQLBudget is read
// once per rendered frame, and the answer only changes when some instance makes
// a request, so re-reading per frame would be a stat storm for a number that
// moves every few seconds at most.
const budgetDiskInterval = 3 * time.Second

// budgetTracker holds the most recent rateLimit reading. Shared by pointer
// across derived clients, so it is written from whichever goroutine's fetch
// landed last.
type budgetTracker struct {
	mu        sync.Mutex
	remaining int
	limit     int
	resetAt   time.Time
	seen      bool

	// path is the shared file. Empty disables the cross-process half, which is
	// what a machine with no home directory gets — and what a test that must
	// not see the developer's real reading sets.
	path string
	// lastRead throttles the disk consult.
	lastRead time.Time
}

func newBudgetTracker() *budgetTracker {
	dir := cachefile.Dir()
	if dir == "" {
		return &budgetTracker{}
	}
	return &budgetTracker{path: filepath.Join(dir, "budget.json")}
}

// mergeLocked folds a reading into the tracker under the same rule for both
// sources, in-process and on-disk.
//
// Readings arrive out of order from concurrent per-account fetches and from
// other instances. The lower remaining within the same window is the more
// recent one; a higher one after a reset is recognized by its later resetAt.
func (t *budgetTracker) mergeLocked(remaining, limit int, resetAt time.Time) bool {
	if limit <= 0 {
		return false
	}
	if t.seen && !resetAt.After(t.resetAt) && remaining > t.remaining {
		return false
	}
	t.remaining, t.limit, t.resetAt, t.seen = remaining, limit, resetAt, true
	return true
}

// loadLocked folds in what another instance last observed. force skips the
// throttle, for the one caller that must not decide anything without it.
func (t *budgetTracker) loadLocked(now time.Time, force bool) {
	if t.path == "" || (!force && now.Sub(t.lastRead) < budgetDiskInterval) {
		return
	}
	t.lastRead = now
	var r budgetReading
	if err := cachefile.ReadJSON(t.path, &r); err != nil {
		return
	}
	// An expired window is not folded in: its remaining describes an allowance
	// that has since been restored, and merging it would throttle on a number
	// the reset already invalidated.
	if !r.ResetAt.IsZero() && !r.ResetAt.After(now) {
		return
	}
	t.mergeLocked(r.Remaining, r.Limit, r.ResetAt)
}

// GraphQLBudget is a point-in-time view of the account's GraphQL allowance.
type GraphQLBudget struct {
	Remaining int
	Limit     int
	ResetAt   time.Time
	// Known is false until a query has reported a rateLimit block. Callers must
	// not read a zero Remaining as "exhausted" — on startup nothing has been
	// observed yet, and treating that as empty would back off before the first
	// fetch.
	Known bool
}

// Fraction is Remaining/Limit, or 1 when nothing has been observed — an unknown
// budget is treated as a full one, so the guard only ever slows things down on
// evidence.
func (b GraphQLBudget) Fraction() float64 {
	if !b.Known || b.Limit <= 0 {
		return 1
	}
	return float64(b.Remaining) / float64(b.Limit)
}

func (t *budgetTracker) observe(remaining, limit int, resetAt time.Time) {
	if t == nil || limit <= 0 {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	// What is on disk is folded in *before* this reading is judged against it.
	// Without that, a window whose own tracker is empty accepts its 4,800 and
	// publishes it over the 300 another window just measured — the fresh
	// instance would raise everyone's estimate of a pool it had not looked at,
	// which is the wrong direction for a guard whose whole job is to slow down
	// on evidence.
	t.loadLocked(time.Now(), true)
	if !t.mergeLocked(remaining, limit, resetAt) {
		return
	}
	// Published even though this instance is the one that just learned it: the
	// windows that did not send this request are the ones that need telling.
	if t.path != "" {
		_ = cachefile.WriteJSON(t.path, budgetReading{
			Remaining: t.remaining, Limit: t.limit, ResetAt: t.resetAt,
		})
	}
}

// GraphQLBudget reports the last observed allowance, this instance's and every
// other instance's.
func (c *Client) GraphQLBudget() GraphQLBudget {
	if c.budget == nil {
		return GraphQLBudget{}
	}
	c.budget.mu.Lock()
	defer c.budget.mu.Unlock()
	c.budget.loadLocked(time.Now(), false)
	return GraphQLBudget{
		Remaining: c.budget.remaining,
		Limit:     c.budget.limit,
		ResetAt:   c.budget.resetAt,
		Known:     c.budget.seen,
	}
}

// ObserveBudgetForTest injects a reading. The real one arrives inside a GraphQL
// response, which a test asserting on the throttle has no way to produce.
func (c *Client) ObserveBudgetForTest(remaining, limit int, resetAt time.Time) {
	c.budget.observe(remaining, limit, resetAt)
}
