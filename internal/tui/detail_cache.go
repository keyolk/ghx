package tui

import (
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/keyolk/ghx/internal/cachefile"
	"github.com/keyolk/ghx/internal/pr"
)

// A PR's detail is four requests — view, diff, checks, and review threads — and
// none of it was kept anywhere. Leaving a PR and coming back paid all four
// again, which is the most common movement there is during a review: open a PR,
// read it, go back to the queue, return to check one more thing.
//
// The list already caches to disk (prlist_cache.go); this is the same idea for
// the far more expensive half.
//
// Validity is decided by the row's updatedAt, not by a timer. GitHub bumps it
// on every push, comment, review, and label change, so an entry stamped with
// the same value describes the same PR — no TTL can make that claim, and every
// TTL is either too short to help or long enough to show a stale diff.

// The entries are shared with the other ghx instances, like every other cache
// here: a PR opened in one window is a PR the window beside it does not have to
// fetch four times over. That makes the atomic write in cachefile load-bearing
// rather than tidy — a detail payload is the largest thing ghx stores, so it is
// also the one most likely to be caught half-written.

// detailFileCache stores per-PR detail payloads under ~/.config/ghx/cache/pr/.
type detailFileCache struct {
	dir string
	// swept bounds a directory that holds one file per PR ever opened. See
	// gh/status_cache.go for the same argument at greater volume.
	swept sync.Once
}

// detailCacheTTL is how long an unused entry survives. Like the status cache's
// it is a garbage collector, not a staleness guard: updatedAt already decides
// whether an entry may be served.
const detailCacheTTL = 7 * 24 * time.Hour

// cachedDetail is the on-disk shape. Everything d.load() fetches, plus the
// stamp that decides whether it may still be used.
//
// Threads carry ResolutionKnown, so a cached entry that was recovered over REST
// stays honest about what it does not know rather than being read back as if
// GraphQL had answered.
type cachedDetail struct {
	SavedAt   time.Time         `json:"saved_at"`
	UpdatedAt time.Time         `json:"updated_at"`
	Detail    *pr.Detail        `json:"detail"`
	Diff      string            `json:"diff"`
	Checks    []pr.Check        `json:"checks"`
	Threads   []pr.ReviewThread `json:"threads"`
	// Conversations is absent from entries written before the Comments tab
	// listed them. It decodes as nil, which renders as a PR with no PR-level
	// chatter until the next fetch — the same as any other stale field here.
	Conversations []pr.Conversation `json:"conversations"`
}

func newDetailFileCache() *detailFileCache {
	return &detailFileCache{dir: cachefile.Dir("pr")}
}

// sweep removes entries past the TTL, once per process.
func (c *detailFileCache) sweep() {
	entries, err := os.ReadDir(c.dir)
	if err != nil {
		return
	}
	cutoff := time.Now().Add(-detailCacheTTL)
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		info, err := e.Info()
		if err != nil || info.ModTime().After(cutoff) {
			continue
		}
		_ = os.Remove(filepath.Join(c.dir, e.Name()))
	}
}

// detailCacheKey identifies one PR. The queue spans repositories, so the number
// alone is not unique.
func detailCacheKey(repo string, number int) string {
	return cachefile.PRKey(repo, number)
}

// load returns the cached payload when it describes the PR as it is now.
//
// updatedAt is the row's timestamp. A zero value means the caller does not know
// when the PR last changed — from a direct `ghx <number>` with no list row, say
// — and the cache is skipped rather than trusted: showing a diff that may be
// several pushes old is worse than the four requests.
func (c *detailFileCache) load(repo string, number int, updatedAt time.Time) *cachedDetail {
	if c.dir == "" || updatedAt.IsZero() {
		return nil
	}
	var entry cachedDetail
	if err := cachefile.ReadJSON(filepath.Join(c.dir, detailCacheKey(repo, number)), &entry); err != nil {
		return nil
	}
	// Equal, not "not older than": a force-push can move updatedAt backwards,
	// and an entry from the future describes a PR this row has not seen.
	if !entry.UpdatedAt.Equal(updatedAt) {
		return nil
	}
	if entry.Detail == nil {
		return nil
	}
	return &entry
}

// save writes the payload. Best-effort, like the list cache: a failure here
// costs a future fetch, never correctness.
//
// A partial payload is not written. The four fetches land independently, and
// storing whichever arrived first would produce an entry that reads back as a
// PR with no diff or no checks — indistinguishable from one that genuinely has
// none.
func (c *detailFileCache) save(repo string, number int, updatedAt time.Time, e cachedDetail) {
	if c.dir == "" || updatedAt.IsZero() || e.Detail == nil {
		return
	}
	e.SavedAt = time.Now()
	e.UpdatedAt = updatedAt
	if err := cachefile.WriteJSON(filepath.Join(c.dir, detailCacheKey(repo, number)), e); err != nil {
		return
	}
	c.swept.Do(c.sweep)
}

// evict drops a PR's entry. Used after an action changes the PR, where the
// row's updatedAt has not caught up yet and the cache would otherwise look
// valid for the state that just stopped being true.
func (c *detailFileCache) evict(repo string, number int) {
	if c.dir == "" {
		return
	}
	_ = os.Remove(filepath.Join(c.dir, detailCacheKey(repo, number)))
}
