package tui

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

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

// detailFileCache stores per-PR detail payloads under ~/.config/ghx/cache/pr/.
type detailFileCache struct {
	dir string
}

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
}

func newDetailFileCache() *detailFileCache {
	home, err := os.UserHomeDir()
	if err != nil {
		return &detailFileCache{dir: ""}
	}
	return &detailFileCache{dir: filepath.Join(home, ".config", "ghx", "cache", "pr")}
}

// detailCacheKey identifies one PR. The queue spans repositories, so the number
// alone is not unique.
func detailCacheKey(repo string, number int) string {
	// The path separator goes first, which is what keeps a repo name from
	// climbing out of the cache directory: with no "/" left, ".." is an
	// ordinary two-character filename component.
	safe := strings.NewReplacer("/", "_", ":", "_", " ", "_").Replace(repo)
	return fmt.Sprintf("%s_%d.json", safe, number)
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
	data, err := os.ReadFile(filepath.Join(c.dir, detailCacheKey(repo, number)))
	if err != nil {
		return nil
	}
	var entry cachedDetail
	if err := json.Unmarshal(data, &entry); err != nil {
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
	if err := os.MkdirAll(c.dir, 0o755); err != nil {
		return
	}
	e.SavedAt = time.Now()
	e.UpdatedAt = updatedAt
	data, err := json.Marshal(e)
	if err != nil {
		return
	}
	_ = os.WriteFile(filepath.Join(c.dir, detailCacheKey(repo, number)), data, 0o644)
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
