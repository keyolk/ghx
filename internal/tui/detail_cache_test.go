package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/keyolk/ghx/internal/pr"
)

const cacheTestDiff = "diff --git a/a.txt b/a.txt\n--- a/a.txt\n+++ b/a.txt\n@@ -1,2 +1,2 @@\n context\n+added\n"

// primeDetail fills a model as a completed fetch would, so saveCache writes.
func primeDetail(d *prDetailModel, t *testing.T) {
	t.Helper()
	d.detail = &pr.Detail{Number: d.number, Title: "cached pr", BaseRefName: "main"}
	d.rawDiff = cacheTestDiff
	if err := d.diff.setContent(cacheTestDiff, nil); err != nil {
		t.Fatal(err)
	}
	d.checks.setChecks([]pr.Check{{Name: "build", Bucket: "pass"}})
	d.comments.setThreads([]pr.ReviewThread{{ID: "T1", Path: "a.txt"}})
	d.fetchedDetail, d.fetchedDiff = true, true
	d.fetchedChecks, d.fetchedThreads = true, true
}

func detailForCache(t *testing.T, updatedAt time.Time) *prDetailModel {
	t.Helper()
	a := testApp(t, nil)
	d := newPRDetailModel(a.cfg, a.client, a.km, 42, "acme/one")
	d.updatedAt = updatedAt
	return d
}

// A PR's detail is four requests, and returning to one you just left is the
// most common movement in a review. Nothing kept it, so every return paid all
// four again.
func TestDetailCacheServesASecondVisitWithoutFetching(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	stamp := time.Date(2026, 8, 21, 12, 0, 0, 0, time.UTC)

	first := detailForCache(t, stamp)
	primeDetail(first, t)
	first.saveCache()

	second := detailForCache(t, stamp)
	if cmd := second.load(); cmd != nil {
		t.Fatal("a cached PR still issued fetches")
	}
	if second.detail == nil || second.detail.Title != "cached pr" {
		t.Error("the cached detail did not come back")
	}
	if second.rawDiff != cacheTestDiff {
		t.Error("the cached diff did not come back")
	}
	if len(second.checks.checks) != 1 {
		t.Errorf("checks = %d, want 1 from cache", len(second.checks.checks))
	}
	if len(second.comments.threads) != 1 {
		t.Errorf("threads = %d, want 1 from cache", len(second.comments.threads))
	}
	if second.loading() {
		t.Error("a cache hit must not leave the view in a loading state")
	}
	// The diff view has to be usable, not just populated: the rows are what the
	// cursor and `c` work off.
	if len(second.diff.rows) == 0 {
		t.Error("the cached diff produced no rows to navigate")
	}
}

// updatedAt is the whole validity rule. GitHub bumps it on every push, comment,
// review, and label change, so a differing stamp means the stored entry
// describes a PR that no longer exists.
func TestDetailCacheRejectsAStaleStamp(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	old := time.Date(2026, 8, 21, 12, 0, 0, 0, time.UTC)

	first := detailForCache(t, old)
	primeDetail(first, t)
	first.saveCache()

	moved := detailForCache(t, old.Add(time.Minute))
	if cmd := moved.load(); cmd == nil {
		t.Fatal("a PR that changed was served from cache")
	}
	if moved.detail != nil {
		t.Error("stale cached detail leaked into the view")
	}
}

// A force-push can move updatedAt backwards. "Not older than" would accept an
// entry describing commits that no longer exist, so the check is equality.
func TestDetailCacheRejectsAnEntryFromTheFuture(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	stamp := time.Date(2026, 8, 21, 12, 0, 0, 0, time.UTC)

	first := detailForCache(t, stamp)
	primeDetail(first, t)
	first.saveCache()

	rewound := detailForCache(t, stamp.Add(-time.Hour))
	if cmd := rewound.load(); cmd == nil {
		t.Fatal("an entry newer than the row was served from cache")
	}
}

// Opened without a list row — `ghx <number>` — there is no timestamp to
// validate against. Showing a diff that may be several pushes old is worse than
// paying the four requests.
func TestDetailCacheSkippedWithoutATimestamp(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	stamp := time.Date(2026, 8, 21, 12, 0, 0, 0, time.UTC)

	first := detailForCache(t, stamp)
	primeDetail(first, t)
	first.saveCache()

	unknown := detailForCache(t, time.Time{})
	if cmd := unknown.load(); cmd == nil {
		t.Fatal("the cache was used with no timestamp to validate it")
	}
}

// The four fetches land independently. Writing whichever arrived first would
// store a PR with no diff, which reads back indistinguishably from one that
// genuinely has none.
func TestDetailCacheDoesNotStoreAPartialFetch(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	stamp := time.Date(2026, 8, 21, 12, 0, 0, 0, time.UTC)

	partial := detailForCache(t, stamp)
	partial.detail = &pr.Detail{Number: 42, Title: "half"}
	partial.fetchedDetail = true // diff, checks, threads still in flight
	partial.saveCache()

	next := detailForCache(t, stamp)
	if cmd := next.load(); cmd == nil {
		t.Error("a partial payload was written and served as complete")
	}
}

// After an action the PR has changed but the row's updatedAt has not caught up,
// so the entry still looks valid for a state that just stopped being true.
func TestReloadBypassesAndEvictsTheCache(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	stamp := time.Date(2026, 8, 21, 12, 0, 0, 0, time.UTC)

	first := detailForCache(t, stamp)
	primeDetail(first, t)
	first.saveCache()

	acted := detailForCache(t, stamp)
	if cmd := acted.reload(); cmd == nil {
		t.Fatal("reload did not issue fetches")
	}
	// The entry must be gone, or the next visit would serve the pre-action PR.
	again := detailForCache(t, stamp)
	if cmd := again.load(); cmd == nil {
		t.Error("reload left the stale entry on disk")
	}
}

// A corrupt file is a reason to fetch, not a reason to show nothing or to fail.
// Truncation is the realistic case: a write interrupted by a crash or a full
// disk leaves valid JSON's opening bytes and nothing else.
func TestDetailCacheTreatsACorruptFileAsAColdStart(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	stamp := time.Date(2026, 8, 21, 12, 0, 0, 0, time.UTC)

	first := detailForCache(t, stamp)
	primeDetail(first, t)
	first.saveCache()

	path := filepath.Join(home, ".config", "ghx", "cache", "pr",
		detailCacheKey("acme/one", 42))
	if err := os.WriteFile(path, []byte(`{"saved_at":"2026-08`), 0o644); err != nil {
		t.Fatal(err)
	}

	next := detailForCache(t, stamp)
	if cmd := next.load(); cmd == nil {
		t.Fatal("a truncated cache file was served as a valid entry")
	}
	if next.detail != nil {
		t.Error("the rejected entry left partial state behind")
	}
}

// A diff the parser cannot turn into rows must not be presented as an empty
// PR. Parse tolerates junk by returning no files, so the guard is on the rows
// the view actually needs, not on an error the parser does not raise.
func TestDetailCacheRejectsAnEntryThatYieldsNoDiffRows(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	stamp := time.Date(2026, 8, 21, 12, 0, 0, 0, time.UTC)

	d := detailForCache(t, stamp)
	d.cache.save("acme/one", 42, stamp, cachedDetail{
		Detail: &pr.Detail{Number: 42, Title: "corrupt", ChangedFiles: 3},
		Diff:   "@@ this is not a diff @@",
	})

	next := detailForCache(t, stamp)
	if cmd := next.load(); cmd == nil {
		t.Fatal("a cached diff with no rows was accepted for a PR with 3 files")
	}
}

// Two PRs with the same number in different repos must not share a file — the
// queue spans repositories.
func TestDetailCacheKeysByRepoAndNumber(t *testing.T) {
	if detailCacheKey("acme/one", 42) == detailCacheKey("acme/two", 42) {
		t.Error("PRs in different repos share a cache key")
	}
	// A repo name must not be able to climb out of the cache directory. What
	// makes that impossible is that no separator survives — with none left,
	// ".." is just two characters in a filename.
	for _, repo := range []string{"../../etc", "a/../../b", ".."} {
		got := detailCacheKey(repo, 1)
		if strings.ContainsAny(got, `/\`) {
			t.Errorf("detailCacheKey(%q) = %q — a path separator survived", repo, got)
		}
	}
}
