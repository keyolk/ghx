package tui

import (
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
	SavedAt time.Time    `json:"saved_at"`
	PRs     []pr.Summary `json:"prs"`
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
	if c == nil || c.dir == "" || maxAge <= 0 {
		return nil, time.Time{}, false
	}
	var entry cachedPRList
	if err := cachefile.ReadJSON(filepath.Join(c.dir, cacheKey(s)), &entry); err != nil {
		return nil, time.Time{}, false
	}
	if entry.SavedAt.IsZero() || time.Since(entry.SavedAt) > maxAge {
		return nil, time.Time{}, false
	}
	return entry.PRs, entry.SavedAt, true
}

// save writes a source's PRs. A failure is not surfaced — the cache is an
// optimization, not a guarantee.
//
// The write is atomic (see cachefile), which matters more here than it would
// for a single-process cache: several instances write the same file, and a
// reader that caught a half-written one would discard it and fetch, turning
// the sharing into extra requests rather than fewer.
func (c *prFileCache) save(s config.SourceDef, prs []pr.Summary) {
	if c == nil || c.dir == "" {
		return
	}
	_ = cachefile.WriteJSON(filepath.Join(c.dir, cacheKey(s)),
		cachedPRList{SavedAt: time.Now(), PRs: prs})
}
