package gh

import (
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/keyolk/ghx/internal/cachefile"
	"github.com/keyolk/ghx/internal/pr"
)

// Status enrichment is the single largest recurring GraphQL cost in ghx, and
// almost all of it is spent re-learning what has not changed.
//
// Every poll sends the whole visible page through prStatusBatchQuery, whose
// price is driven by the reviewThreads(first:100) connection on each node — the
// list request itself is one point, the enrichment is the expensive half. A
// 50-row queue costs the same on the poll where nothing happened as on the one
// where a PR was pushed to. Multiply by the six windows measured in budget.go,
// none of which know about each other, and the shared 5,000/hour pool is spent
// on an answer the account has already been given five times in the same
// minute.
//
// The row's updatedAt decides validity, exactly as it does for the PR detail
// cache (tui/detail_cache.go). GitHub moves it on every push, comment, review,
// label change and state transition — which is a superset of everything this
// query reports — so an entry stamped with the same value describes the same
// status. That is what makes a cross-process cache safe here: a stale entry is
// not "probably still fine", it is provably the same PR, and no TTL can make
// that claim.
//
// The cache lives on disk because the instances that need to share it are
// separate processes. An in-memory map would help a single window scrolling its
// own tabs and do nothing for the case that actually empties the budget.
//
// Measured against keyolk/ghx: enriching 27 rows cost 2 GraphQL points, and a
// second Client — a second window — enriched the same page for 0. The points
// are small per page and it is the repetition that adds up: that same page, on
// six windows at the 30s cadence, was 720 chargeable enrichments an hour.

// statusCacheTTL bounds how long an entry survives regardless of updatedAt.
//
// updatedAt already proves the PR is unchanged, so this is not a staleness
// guard — it is a garbage collector. Without it a queue that has churned
// through thousands of PRs leaves an entry per PR forever, and the directory
// becomes the thing that costs time to read. A week is far longer than a PR
// stays interesting and short enough that the directory tracks recent work.
const statusCacheTTL = 7 * 24 * time.Hour

// statusEntry is one PR's enriched status as it sits on disk.
//
// ConversationsKnown is stored, so a row enriched over REST — where thread
// resolution cannot be learned — reads back as unknown rather than as zero
// unresolved conversations. Writing it as a plain count would turn "we could
// not find out" into "there is nothing to look at", on a PR that may be blocked.
type statusEntry struct {
	SavedAt   time.Time `json:"saved_at"`
	UpdatedAt time.Time `json:"updated_at"`

	State          string `json:"state"`
	IsDraft        bool   `json:"is_draft"`
	ReviewDecision string `json:"review_decision"`

	UnresolvedConversations int  `json:"unresolved"`
	ConversationsKnown      bool `json:"conversations_known"`
}

// statusCache reads and writes per-PR status entries under
// ~/.config/ghx/cache/status/.
//
// The in-process layer is not an optimization on top of the files; it is what
// keeps a tab switch from stat-ing fifty paths on the UI's behalf. Both layers
// are keyed the same way, so a hit in either is the same answer.
type statusCache struct {
	dir string

	mu  sync.Mutex
	mem map[string]statusEntry

	// swept ensures the directory is walked once per process rather than per
	// write. The sweep is the only thing that bounds a directory holding one
	// file per PR ever seen, and it is cheap exactly once.
	swept sync.Once
}

func newStatusCache() *statusCache {
	return &statusCache{
		dir: cachefile.Dir("status"),
		mem: make(map[string]statusEntry),
	}
}

// sweep removes entries past the TTL.
//
// updatedAt already decides correctness, so nothing here is about staleness —
// it is about a directory that otherwise grows one file per PR the queues have
// ever shown, until reading it costs more than the requests it saves. Run once
// per process, off the write path's critical section, and every failure is
// ignored: a file that cannot be removed is a file that will be offered again
// next week.
func (c *statusCache) sweep() {
	entries, err := os.ReadDir(c.dir)
	if err != nil {
		return
	}
	cutoff := time.Now().Add(-statusCacheTTL)
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

func (c *statusCache) path(repo string, number int) string {
	return filepath.Join(c.dir, cachefile.PRKey(repo, number))
}

// get returns the stored status when it describes the PR as the row sees it now.
//
// A zero updatedAt means the caller cannot say when the PR last changed, and
// there is then nothing to validate an entry against — the enrichment runs, the
// same way the detail cache refuses to serve an unstamped row.
func (c *statusCache) get(repo string, number int, updatedAt time.Time) (statusEntry, bool) {
	if c == nil || c.dir == "" || repo == "" || updatedAt.IsZero() {
		return statusEntry{}, false
	}
	key := c.path(repo, number)

	c.mu.Lock()
	entry, ok := c.mem[key]
	c.mu.Unlock()
	if ok {
		if entry.UpdatedAt.Equal(updatedAt) {
			return entry, true
		}
		return statusEntry{}, false
	}

	if err := cachefile.ReadJSON(key, &entry); err != nil {
		return statusEntry{}, false
	}
	// Equal, not "at least as new": a force-push can move updatedAt backwards,
	// and an entry stamped later than the row describes a PR this row has not
	// seen yet.
	if !entry.UpdatedAt.Equal(updatedAt) {
		return statusEntry{}, false
	}
	if time.Since(entry.SavedAt) > statusCacheTTL {
		return statusEntry{}, false
	}
	c.mu.Lock()
	c.mem[key] = entry
	c.mu.Unlock()
	return entry, true
}

// put stores one PR's enriched status. Best-effort: a failure costs a future
// request, never correctness.
//
// An unknown-conversations entry is still written. It is what REST could
// answer, and re-deriving it costs one round trip per PR — the expensive path
// that the fallback is already on.
func (c *statusCache) put(repo string, number int, updatedAt time.Time, e statusEntry) {
	if c == nil || c.dir == "" || repo == "" || updatedAt.IsZero() {
		return
	}
	e.UpdatedAt = updatedAt
	e.SavedAt = time.Now()
	key := c.path(repo, number)
	c.mu.Lock()
	c.mem[key] = e
	c.mu.Unlock()
	if err := cachefile.WriteJSON(key, e); err != nil {
		return
	}
	// After the first successful write, so the directory is known to exist.
	c.swept.Do(c.sweep)
}

// evict drops one PR's entry, for an action taken inside ghx.
//
// The row's updatedAt has not caught up at that point — the list has not
// re-fetched — so the stored entry still validates against a state that just
// stopped being true. This is the same hazard the detail cache handles in
// evictCache, and it is worse here: the markers are what the queue is read for.
func (c *statusCache) evict(repo string, number int) {
	if c == nil || c.dir == "" || repo == "" {
		return
	}
	key := c.path(repo, number)
	c.mu.Lock()
	delete(c.mem, key)
	c.mu.Unlock()
	_ = os.Remove(key)
}

// apply copies a cached entry onto a summary.
func (e statusEntry) apply(s *pr.Summary) {
	s.State = e.State
	s.IsDraft = e.IsDraft
	s.ReviewDecision = e.ReviewDecision
	s.UnresolvedConversations = e.UnresolvedConversations
	s.ConversationsKnown = e.ConversationsKnown
}

// statusEntryOf snapshots a summary's enriched fields for storage.
func statusEntryOf(s pr.Summary) statusEntry {
	return statusEntry{
		State:                   s.State,
		IsDraft:                 s.IsDraft,
		ReviewDecision:          s.ReviewDecision,
		UnresolvedConversations: s.UnresolvedConversations,
		ConversationsKnown:      s.ConversationsKnown,
	}
}

// EvictStatus drops the cached status for one PR. The TUI calls it after an
// action, where the row's updatedAt has not moved yet.
func (c *Client) EvictStatus(repo string, number int) {
	c.status.evict(repo, number)
}
