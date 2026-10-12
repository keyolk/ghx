package cachefile

import (
	"context"
	"os"
	"path/filepath"
	"time"
)

// Lock serializes one piece of work across every ghx process.
//
// The caches let one instance answer another's poll, but only after the first
// has written its entry. Several instances that miss at the same moment — a
// tmux session restored with a ghx in every window, or a push that every
// window watching that repository notices within one sweep — each see no
// entry and each fetch, which is the duplication the sharing exists to remove,
// arriving all at once. Holding a per-entry lock across "check, fetch, write"
// turns that into one fetch and N-1 waiters who then read its answer.
//
// It returns a release func, which is always safe to call. A lock that cannot
// be taken — no cache directory, a filesystem without flock, or a holder that
// outlives ctx — is not an error: the caller proceeds unlocked and pays for
// its own fetch, which is exactly what it would have done without the lock.
func Lock(ctx context.Context, path string) (release func()) {
	noop := func() {}
	if path == "" {
		return noop
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return noop
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return noop
	}
	// Polled rather than a blocking flock: a blocking call cannot be abandoned
	// when ctx ends, and a holder wedged on a hung gh must not take its
	// waiters with it.
	for {
		ok, supported := tryLock(f)
		if ok {
			// Closing the descriptor is the release: flock locks belong to the
			// open file, so there is no separate unlock call to forget.
			return func() { f.Close() }
		}
		if !supported {
			f.Close()
			return noop
		}
		select {
		case <-ctx.Done():
			f.Close()
			return noop
		case <-time.After(lockPoll):
		}
	}
}

// lockPoll is how often a waiter retries. The work behind a lock is a gh
// round trip — hundreds of milliseconds at least — so a waiter that notices
// within 50ms of the release is not what anyone waits on.
const lockPoll = 50 * time.Millisecond
