package gh

import (
	"testing"
	"time"
)

// The pool being measured belongs to the account, so a window that has spent
// nothing still has to know what the other five spent. From inside any single
// instance the drain is invisible — which is exactly the failure budget.go
// documents: six windows each believing they were polling one queue.
//
// Serving unchanged rows from the status cache sharpens this. An instance can
// now go a long time without sending the query that carries the free rateLimit
// block, so the free observation stops arriving precisely when the queues are
// quiet, and a window would otherwise throttle on a reading from an hour ago.
func TestOneInstancesReadingReachesAnother(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	reset := time.Now().Add(30 * time.Minute)

	first := NewClient(0)
	first.ObserveBudgetForTest(400, 5000, reset)

	second := NewClient(0)
	b := second.GraphQLBudget()
	if !b.Known {
		t.Fatal("a window that has made no request learned nothing about the pool")
	}
	if b.Remaining != 400 || b.Limit != 5000 {
		t.Errorf("read %d/%d, want the 400/5000 the other window observed",
			b.Remaining, b.Limit)
	}
}

// The lowest remaining within a window wins, on disk as in memory: it is the
// most recent reading, and the direction that errs toward polling less.
func TestTheTighterReadingWins(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	reset := time.Now().Add(30 * time.Minute)

	NewClient(0).ObserveBudgetForTest(300, 5000, reset)

	c := NewClient(0)
	c.ObserveBudgetForTest(4800, 5000, reset)
	if got := c.GraphQLBudget().Remaining; got != 300 {
		t.Errorf("remaining = %d, want the tighter 300 another window reported", got)
	}
}

// A reading whose window has closed describes an allowance that has since been
// restored. Folding it in would throttle on a number the reset invalidated —
// the same hazard App.budget guards in memory.
func TestAnExpiredReadingIsNotAdopted(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	NewClient(0).ObserveBudgetForTest(50, 5000, time.Now().Add(-time.Minute))

	if b := NewClient(0).GraphQLBudget(); b.Known {
		t.Errorf("adopted an expired reading: %d/%d resetting %v",
			b.Remaining, b.Limit, b.ResetAt)
	}
}

// A reading published after a reset is recognized by its later resetAt, so the
// pool refilling is not mistaken for an out-of-order low reading.
func TestAReadingFromTheNextWindowReplacesTheOldOne(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	c := NewClient(0)
	c.ObserveBudgetForTest(100, 5000, time.Now().Add(time.Minute))
	c.ObserveBudgetForTest(5000, 5000, time.Now().Add(time.Hour))

	if got := c.GraphQLBudget().Remaining; got != 5000 {
		t.Errorf("remaining = %d, want the refilled 5000", got)
	}
}

// With no home directory there is no file to share through, and the tracker
// must still work as the in-process one it has always been.
func TestAHomelessTrackerStillTracksInProcess(t *testing.T) {
	c := NewClient(0)
	c.budget = &budgetTracker{}
	c.ObserveBudgetForTest(1000, 5000, time.Now().Add(time.Hour))
	if got := c.GraphQLBudget().Remaining; got != 1000 {
		t.Errorf("remaining = %d, want 1000", got)
	}
}
