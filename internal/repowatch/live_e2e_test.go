//go:build e2e

package repowatch

import (
	"testing"
	"time"
)

// The unit tests assert one operation at a time against a snapshot taken by
// hand. This drives the loop the way ghx actually runs it — a watcher polling
// on a timer while git work happens underneath — because that is the claim the
// feature rests on and the shape a per-operation test cannot make.
//
// It is gated behind -tags e2e like the other network-free-but-slow checks:
// it sleeps through real intervals, so it does not belong in `make test`.
//
//	go test -tags e2e ./internal/repowatch/ -run TestLiveWatchLoop -v
func TestLiveWatchLoop(t *testing.T) {
	work := initRepo(t)
	w := NewWatcher(work)
	if w == nil {
		t.Fatal("no watcher for a real checkout")
	}

	// The sweep cadence ghx uses is 5s; this polls faster so the test does not
	// take a minute, and the question — does the change register at all — is the
	// same at either rate.
	const tick = 300 * time.Millisecond

	events := make(chan string, 16)
	stop := make(chan struct{})
	go func() {
		prev := w.Snapshot()
		for {
			select {
			case <-stop:
				return
			case <-time.After(tick):
			}
			now := w.Snapshot()
			if now.Changed(prev) {
				prev = now
				select {
				case events <- "change":
				default:
				}
			}
		}
	}()
	defer close(stop)

	// await drains any straggling event, does the work, and requires that the
	// watcher notices within a bounded time.
	await := func(label string, do func()) {
		t.Helper()
		for len(events) > 0 {
			<-events
		}
		do()
		select {
		case <-events:
		case <-time.After(5 * time.Second):
			t.Errorf("%s was never observed by the watch loop", label)
		}
	}

	await("commit", func() { commit(t, work, "e2e-one") })
	await("branch checkout", func() { git(t, work, "checkout", "-qb", "e2e/branch") })
	await("push", func() { git(t, work, "push", "-q", "-u", "origin", "HEAD") })
	await("fetch", func() { git(t, work, "fetch", "-q", "origin") })

	// The half that keeps this affordable on an unfocused window: no git work
	// must produce no events, or every parked ghx would fetch on every sweep.
	for len(events) > 0 {
		<-events
	}
	select {
	case <-events:
		t.Error("an untouched checkout produced an event — a quiet repo must cost nothing")
	case <-time.After(2 * time.Second):
	}
}

// A second checkout of the same repository must not report the first one's
// work. Two panes in unrelated repos are the ordinary case, and a watcher that
// keyed off shared state would refetch the wrong tab.
func TestLiveWatchIsPerRepository(t *testing.T) {
	a := initRepo(t)
	b := initRepo(t)

	wa, wb := NewWatcher(a), NewWatcher(b)
	beforeA, beforeB := wa.Snapshot(), wb.Snapshot()

	settle()
	commit(t, a, "only-in-a")

	if !wa.Snapshot().Changed(beforeA) {
		t.Error("the repository that was committed to reported no change")
	}
	if wb.Snapshot().Changed(beforeB) {
		t.Error("an unrelated repository reported a change")
	}
}
