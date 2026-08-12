package tui

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/keyolk/ghx/internal/config"
	"github.com/keyolk/ghx/internal/pr"
)

// prFileCache stores one source's PR list on disk so a restart shows the
// previous results immediately while a fresh fetch runs in the background.
// Without it, every restart re-fetches every tab from scratch — slow on a
// cross-repo queue and costly against the search rate limit.
//
// The cache is best-effort: a missing or unreadable file is an ordinary cold
// start, never an error. Entries are keyed by source identity (name + query +
// repo) so two sources that differ only in name don't share a file, and a
// renamed source starts fresh.
type prFileCache struct {
	dir string
}

// cachedPRList is the on-disk shape. SavedAt lets a future TTL reject very stale
// entries without a fetch, though the current policy is to show them and
// refresh.
type cachedPRList struct {
	SavedAt time.Time    `json:"saved_at"`
	PRs     []pr.Summary `json:"prs"`
}

func newPRFileCache() *prFileCache {
	home, err := os.UserHomeDir()
	if err != nil {
		return &prFileCache{dir: ""}
	}
	dir := filepath.Join(home, ".config", "ghx", "cache")
	return &prFileCache{dir: dir}
}

// cacheKey derives a filesystem-safe key from a source's identity.
func cacheKey(s config.SourceDef) string {
	h := strings.NewReplacer("/", "_", ":", "_", " ", "_").Replace(s.Name + "|" + s.Query + "|" + s.Repo)
	return h + ".json"
}

// load reads a source's cached PRs. Returns nil (cold start) when the file is
// missing, unreadable, or older than maxAge when maxAge > 0.
func (c *prFileCache) load(s config.SourceDef, maxAge time.Duration) []pr.Summary {
	if c.dir == "" {
		return nil
	}
	data, err := os.ReadFile(filepath.Join(c.dir, cacheKey(s)))
	if err != nil {
		return nil
	}
	var entry cachedPRList
	if err := json.Unmarshal(data, &entry); err != nil {
		return nil
	}
	if maxAge > 0 && time.Since(entry.SavedAt) > maxAge {
		return nil
	}
	return entry.PRs
}

// save writes a source's PRs. A failure is logged but not surfaced — the cache
// is an optimization, not a guarantee.
func (c *prFileCache) save(s config.SourceDef, prs []pr.Summary) {
	if c.dir == "" {
		return
	}
	if err := os.MkdirAll(c.dir, 0o755); err != nil {
		return
	}
	entry := cachedPRList{SavedAt: time.Now(), PRs: prs}
	data, err := json.Marshal(entry)
	if err != nil {
		return
	}
	_ = os.WriteFile(filepath.Join(c.dir, cacheKey(s)), data, 0o644)
}
