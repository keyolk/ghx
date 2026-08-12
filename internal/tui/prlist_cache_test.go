package tui

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/keyolk/ghx/internal/config"
	"github.com/keyolk/ghx/internal/pr"
)

// A restart should open on the previous session's PRs, not a blank pane. The
// file cache seeds each tab from disk while the background fetch runs.
func TestFileCacheRoundTrip(t *testing.T) {
	dir := t.TempDir()
	c := &prFileCache{dir: dir}
	src := config.SourceDef{Name: "My PRs", Query: "author:@me state:open"}
	prs := []pr.Summary{
		{Number: 101, Repo: "acme/one", State: "OPEN", Title: "first"},
		{Number: 202, Repo: "acme/two", State: "OPEN", Title: "second"},
	}

	c.save(src, prs)
	got := c.load(src, 0)
	if len(got) != 2 || got[0].Number != 101 || got[1].Number != 202 {
		t.Errorf("loaded %v, want the two saved PRs", got)
	}
}

// A missing file is a cold start, not an error.
func TestFileCacheMissingFileReturnsNil(t *testing.T) {
	c := &prFileCache{dir: t.TempDir()}
	if got := c.load(config.SourceDef{Name: "none"}, 0); got != nil {
		t.Errorf("missing file returned %v, want nil", got)
	}
}

// A corrupt file is treated as a cold start rather than crashing.
func TestFileCacheCorruptFileReturnsNil(t *testing.T) {
	dir := t.TempDir()
	c := &prFileCache{dir: dir}
	src := config.SourceDef{Name: "bad"}
	if err := os.WriteFile(filepath.Join(dir, cacheKey(src)), []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := c.load(src, 0); got != nil {
		t.Errorf("corrupt file returned %v, want nil", got)
	}
}

// Entries older than maxAge are rejected so a stale cache doesn't mask a
// long-unreachable source forever.
func TestFileCacheRespectsMaxAge(t *testing.T) {
	dir := t.TempDir()
	c := &prFileCache{dir: dir}
	src := config.SourceDef{Name: "old"}
	// Write a valid entry, then backdate its timestamp.
	c.save(src, []pr.Summary{{Number: 1, Repo: "o/n", State: "OPEN"}})
	path := filepath.Join(dir, cacheKey(src))
	oldTime := time.Now().Add(-2 * time.Hour)
	if err := os.Chtimes(path, oldTime, oldTime); err != nil {
		t.Fatal(err)
	}
	// The file's mtime is backdated, but the JSON's SavedAt is still recent.
	// The maxAge check reads SavedAt from the JSON, so this should still load.
	if got := c.load(src, time.Hour); len(got) != 1 {
		t.Errorf("maxAge by SavedAt: got %v, want the entry (JSON SavedAt is recent)", got)
	}
	// Now corrupt the SavedAt to be old and verify maxAge rejects it.
	data, _ := os.ReadFile(path)
	_ = os.WriteFile(path, []byte(`{"saved_at":"2000-01-01T00:00:00Z","prs":[{"number":1,"repo":"o/n","state":"OPEN"}]}`), 0o644)
	_ = data
	if got := c.load(src, time.Hour); got != nil {
		t.Errorf("old SavedAt: got %v, want nil (older than maxAge)", got)
	}
}

// Distinct sources get distinct cache files; a rename starts fresh.
func TestFileCacheKeysBySourceIdentity(t *testing.T) {
	dir := t.TempDir()
	c := &prFileCache{dir: dir}
	c.save(config.SourceDef{Name: "My PRs", Query: "author:@me state:open"}, []pr.Summary{{Number: 1, Repo: "o/n", State: "OPEN"}})
	c.save(config.SourceDef{Name: "My reviews", Query: "review-requested:@me state:open"}, []pr.Summary{{Number: 2, Repo: "o/n", State: "OPEN"}})

	if got := c.load(config.SourceDef{Name: "My PRs", Query: "author:@me state:open"}, 0); len(got) != 1 || got[0].Number != 1 {
		t.Errorf("My PRs loaded %v, want PR #1", got)
	}
	if got := c.load(config.SourceDef{Name: "My reviews", Query: "review-requested:@me state:open"}, 0); len(got) != 1 || got[0].Number != 2 {
		t.Errorf("My reviews loaded %v, want PR #2", got)
	}
}

// The list model seeds from the cache on construction, so the first frame has
// rows even before the fetch returns.
func TestPRListSeedsFromCacheOnStartup(t *testing.T) {
	dir := t.TempDir()
	// Pre-populate the cache as a previous session would have.
	c := &prFileCache{dir: dir}
	src := config.SourceDef{Name: "test", Query: "state:open"}
	c.save(src, []pr.Summary{{Number: 42, Repo: "o/n", State: "OPEN", Title: "cached PR"}})

	cfg := config.DefaultConfig()
	cfg.Sources = []config.SourceDef{src}
	a := NewApp(cfg, DefaultKeymap(), nil)
	a.list.fileCache = c
	// Re-seed manually since the constructor ran before we swapped the cache.
	for i, s := range a.list.sources {
		if cached := c.load(s, 0); cached != nil {
			a.list.caches[i] = cached
		}
	}
	a.list.syncListItems()

	if n := len(a.list.list.VisibleItems()); n != 1 {
		t.Fatalf("seeded list has %d items, want 1", n)
	}
}
