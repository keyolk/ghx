package tui

import (
	"context"
	"path/filepath"
	"time"

	"github.com/keyolk/ghx/internal/cachefile"
	"github.com/keyolk/ghx/internal/config"
	"github.com/keyolk/ghx/internal/pr"
)

// prFileCache stores one source's PR list on disk so a restart shows the
// previous results immediately while a fresh fetch runs in the background.
// Without it, every restart re-fetches every tab from scratch — slow on a
// cross-repo queue and costly against the search rate limit.
//
// The file is also how separate ghx processes avoid paying for the same answer.
// A source's identity is its name, query and repo — nothing about which window
// asked — so six windows showing "My reviews" share one entry, and a fetch by
// any of them is a fetch none of the others has to make. That is what `fresh`
// is for: a source answered seconds ago by the instance in the next pane does
// not need asking again, and on a shared 5,000/hour GraphQL pool the difference
// between one instance polling and six is the whole problem (see gh/budget.go).
//
// Measured on the counting shim, six windows polling one source forty times
// each: 240 gh invocations before, 40 after — exactly what a single window
// costs. The account now pays for the queue at one rate no matter how many
// windows are open, which is the arithmetic shareWindowNumerator sets out.
//
// The cache is best-effort: a missing or unreadable file is an ordinary cold
// start, never an error. Entries are keyed by source identity (name + query +
// repo) so two sources that differ only in name don't share a file, and a
// renamed source starts fresh.
type prFileCache struct {
	dir string
}

// cachedPRList is the on-disk shape. SavedAt is what lets a reader tell a fresh
// entry from a stale one — both for the age readout in the title and for
// deciding whether another instance has already answered this poll.
type cachedPRList struct {
	SavedAt time.Time `json:"saved_at"`
	// StartedAt is when the request that produced PRs was sent. It is what a
	// reader compares against, not SavedAt: a fetch that was already in flight
	// when a branch was pushed finished after the push and still describes the
	// queue from before it. Entries written before this field existed decode it
	// as zero and are never taken as fresh — one refetch per source, once.
	StartedAt time.Time    `json:"started_at"`
	PRs       []pr.Summary `json:"prs"`
}

func newPRFileCache() *prFileCache {
	return &prFileCache{dir: cachefile.Dir()}
}

// cacheKey derives a filesystem-safe key from a source's identity.
func cacheKey(s config.SourceDef) string {
	return cachefile.Key(s.Name, s.Query, s.Repo) + ".json"
}

// load reads a source's cached PRs. Returns nil (cold start) when the file is
// missing, unreadable, or older than maxAge when maxAge > 0.
func (c *prFileCache) load(s config.SourceDef, maxAge time.Duration) []pr.Summary {
	prs, _ := c.loadAt(s, maxAge)
	return prs
}

// loadAt is load plus when the entry was written.
//
// The age matters as much as the rows: seeded rows are shown as though they
// were just fetched, and a restart that stamped them "now" would say the queue
// is current when it is however old the file is. The caller stamps its
// per-source clock with this, so a resumed session admits what it is showing.
func (c *prFileCache) loadAt(s config.SourceDef, maxAge time.Duration) ([]pr.Summary, time.Time) {
	if c == nil || c.dir == "" {
		return nil, time.Time{}
	}
	var entry cachedPRList
	if err := cachefile.ReadJSON(filepath.Join(c.dir, cacheKey(s)), &entry); err != nil {
		return nil, time.Time{}
	}
	if maxAge > 0 && time.Since(entry.SavedAt) > maxAge {
		return nil, time.Time{}
	}
	return entry.PRs, entry.SavedAt
}

// fresh returns the rows another instance has already fetched within maxAge,
// and whether there were any.
//
// The separate return is what distinguishes "someone just fetched this and it
// is genuinely empty" from "nobody has fetched it". An empty queue is a real
// and common answer — most of the source tabs are empty most of the time — and
// treating it as a miss would make the emptiest tabs the most expensive ones,
// re-fetched by every instance on every poll precisely because there was
// nothing to remember.
func (c *prFileCache) fresh(s config.SourceDef, maxAge time.Duration) ([]pr.Summary, time.Time, bool) {
	if maxAge <= 0 {
		return nil, time.Time{}, false
	}
	return c.freshSince(s, time.Now().Add(-maxAge))
}

// freshSince returns the rows of a fetch that was sent at or after notBefore.
//
// This is the one question every caller is really asking. A poll asks "has
// anyone fetched within my window" — notBefore is now minus the window. A
// refresh caused by a push asks "has anyone fetched since the push landed" —
// notBefore is the push. The second used to be answered "never trust a file",
// so every window watching a repository paid for the same push separately,
// even when a sibling had fetched after it a second earlier.
func (c *prFileCache) freshSince(s config.SourceDef, notBefore time.Time) ([]pr.Summary, time.Time, bool) {
	if c == nil || c.dir == "" || notBefore.IsZero() {
		return nil, time.Time{}, false
	}
	var entry cachedPRList
	if err := cachefile.ReadJSON(filepath.Join(c.dir, cacheKey(s)), &entry); err != nil {
		return nil, time.Time{}, false
	}
	if entry.StartedAt.IsZero() || entry.StartedAt.Before(notBefore) {
		return nil, time.Time{}, false
	}
	return entry.PRs, entry.SavedAt, true
}

// lock serializes fetches of one source across every ghx process, so the
// windows that miss at the same moment make one request between them rather
// than one each. See cachefile.Lock.
func (c *prFileCache) lock(ctx context.Context, s config.SourceDef) func() {
	if c == nil || c.dir == "" {
		return func() {}
	}
	return cachefile.Lock(ctx, filepath.Join(c.dir, "locks", cacheKey(s)+".lock"))
}

// save writes a source's PRs. A failure is not surfaced — the cache is an
// optimization, not a guarantee.
//
// The write is atomic (see cachefile), which matters more here than it would
// for a single-process cache: several instances write the same file, and a
// reader that caught a half-written one would discard it and fetch, turning
// the sharing into extra requests rather than fewer.
func (c *prFileCache) save(s config.SourceDef, prs []pr.Summary) {
	c.saveStarted(s, prs, time.Now())
}

// saveStarted is save for a fetch that was sent at startedAt.
func (c *prFileCache) saveStarted(s config.SourceDef, prs []pr.Summary, startedAt time.Time) {
	if c == nil || c.dir == "" {
		return
	}
	_ = cachefile.WriteJSON(filepath.Join(c.dir, cacheKey(s)),
		cachedPRList{SavedAt: time.Now(), StartedAt: startedAt, PRs: prs})
}
