package gh

import (
	"sync"
	"time"
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

// budgetTracker holds the most recent rateLimit reading. Shared by pointer
// across derived clients, so it is written from whichever goroutine's fetch
// landed last.
type budgetTracker struct {
	mu        sync.Mutex
	remaining int
	limit     int
	resetAt   time.Time
	seen      bool
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
	// Readings can arrive out of order from concurrent per-account fetches. The
	// lower remaining within the same window is the more recent one; a higher one
	// after a reset is recognized by its later resetAt.
	if t.seen && !resetAt.After(t.resetAt) && remaining > t.remaining {
		return
	}
	t.remaining, t.limit, t.resetAt, t.seen = remaining, limit, resetAt, true
}

// GraphQLBudget reports the last observed allowance.
func (c *Client) GraphQLBudget() GraphQLBudget {
	if c.budget == nil {
		return GraphQLBudget{}
	}
	c.budget.mu.Lock()
	defer c.budget.mu.Unlock()
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
