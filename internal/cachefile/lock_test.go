package cachefile

import (
	"context"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// The lock is what turns N simultaneous misses into one fetch, so the property
// to hold is mutual exclusion: two holders at once is N fetches again.
func TestLockExcludesConcurrentHolders(t *testing.T) {
	path := filepath.Join(t.TempDir(), "locks", "src.lock")
	var inside, maxInside int32
	var wg sync.WaitGroup
	for i := 0; i < 6; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			release := Lock(context.Background(), path)
			defer release()
			n := atomic.AddInt32(&inside, 1)
			for {
				m := atomic.LoadInt32(&maxInside)
				if n <= m || atomic.CompareAndSwapInt32(&maxInside, m, n) {
					break
				}
			}
			time.Sleep(20 * time.Millisecond)
			atomic.AddInt32(&inside, -1)
		}()
	}
	wg.Wait()
	if maxInside != 1 {
		t.Errorf("%d holders were inside the lock at once, want 1", maxInside)
	}
}

// A waiter must be able to give up: a holder wedged on a hung gh must not take
// every sibling with it. Giving up means proceeding unlocked, not failing.
func TestLockWaiterGivesUpWithItsContext(t *testing.T) {
	path := filepath.Join(t.TempDir(), "src.lock")
	release := Lock(context.Background(), path)
	defer release()

	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	start := time.Now()
	Lock(ctx, path)()
	if waited := time.Since(start); waited > 2*time.Second {
		t.Errorf("waiter blocked %v past its context", waited)
	}
}

// Release must actually free the lock for the next process.
func TestLockIsReusableAfterRelease(t *testing.T) {
	path := filepath.Join(t.TempDir(), "src.lock")
	Lock(context.Background(), path)()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	start := time.Now()
	Lock(ctx, path)()
	if time.Since(start) > 500*time.Millisecond {
		t.Error("a released lock was still held")
	}
}
