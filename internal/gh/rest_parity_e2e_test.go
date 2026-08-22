//go:build e2e

package gh

import (
	"os"
	"strconv"
	"testing"
	"time"
)

// The REST fallback's field mapping is the part of it that cannot be checked
// against a fixture: a fixture asserts what someone believed REST returns. This
// reads the same PR both ways and compares, which is the only way to find out
// that `line` means something different on the two paths.
//
//	GHX_E2E_REPO=keyolk/ghx GHX_E2E_PR=16 go test -tags e2e -run Parity ./internal/gh/
func TestE2ERESTThreadsMatchGraphQL(t *testing.T) {
	repo, number := parityTarget(t)
	owner, name, _ := cutSlug(t, repo)
	c := NewClient(60 * time.Second).WithRepo(repo)

	want, err := c.ReviewThreads(t.Context(), owner, name, number)
	if err != nil {
		t.Fatalf("GraphQL threads: %v", err)
	}
	got, err := c.ReviewThreadsREST(t.Context(), owner, name, number)
	if err != nil {
		t.Fatalf("REST threads: %v", err)
	}
	if len(got) != len(want) {
		t.Fatalf("thread count: REST %d, GraphQL %d", len(got), len(want))
	}
	for i := range want {
		w, g := want[i], got[i]
		if g.Path != w.Path {
			t.Errorf("thread %d path: REST %q, GraphQL %q", i, g.Path, w.Path)
		}
		if g.DiffSide != w.DiffSide {
			t.Errorf("thread %d side: REST %q, GraphQL %q", i, g.DiffSide, w.DiffSide)
		}
		if g.Line != w.Line {
			t.Errorf("thread %d line: REST %d, GraphQL %d", i, g.Line, w.Line)
		}
		if g.OriginalLine != w.OriginalLine {
			t.Errorf("thread %d originalLine: REST %d, GraphQL %d",
				i, g.OriginalLine, w.OriginalLine)
		}
		if g.StartLine != w.StartLine {
			t.Errorf("thread %d startLine: REST %d, GraphQL %d", i, g.StartLine, w.StartLine)
		}
		if g.OriginalStartLine != w.OriginalStartLine {
			t.Errorf("thread %d originalStartLine: REST %d, GraphQL %d",
				i, g.OriginalStartLine, w.OriginalStartLine)
		}
		if len(g.Comments) != len(w.Comments) {
			t.Errorf("thread %d comment count: REST %d, GraphQL %d",
				i, len(g.Comments), len(w.Comments))
			continue
		}
		for j := range w.Comments {
			if g.Comments[j].DatabaseID != w.Comments[j].DatabaseID {
				t.Errorf("thread %d comment %d databaseId: REST %d, GraphQL %d",
					i, j, g.Comments[j].DatabaseID, w.Comments[j].DatabaseID)
			}
			if g.Comments[j].Body != w.Comments[j].Body {
				t.Errorf("thread %d comment %d body differs", i, j)
			}
		}
		// REST cannot answer resolution, and must say so rather than guess.
		if g.ResolutionKnown {
			t.Errorf("thread %d claims to know its resolution over REST", i)
		}
		if g.ID != "" {
			t.Errorf("thread %d has an ID over REST (%q) — resolve would send a "+
				"comment id where a thread id belongs", i, g.ID)
		}
		// isOutdated is GraphQL-only too. A REST thread that reported false here
		// would let A apply a stale suggestion.
		if g.IsOutdated {
			t.Errorf("thread %d claims to know it is outdated over REST", i)
		}
		if w.IsOutdated && !g.IsOutdated {
			t.Logf("thread %d is outdated on GraphQL and REST cannot tell — "+
				"apply is gated on this", i)
		}
	}
}

// TestE2ERESTSearchMatchesGraphQL checks the row fields the markers are drawn
// from, which is where a REST/GraphQL disagreement shows up as a wrong badge
// rather than as an error.
func TestE2ERESTSearchMatchesGraphQL(t *testing.T) {
	repo, _ := parityTarget(t)
	c := NewClient(60 * time.Second).WithRepo(repo)
	query := "repo:" + repo + " is:pr"

	want, err := c.SearchPRs(t.Context(), query, 20)
	if err != nil {
		t.Fatalf("GraphQL search: %v", err)
	}
	got, err := c.SearchPRsREST(t.Context(), query, 20)
	if err != nil {
		t.Fatalf("REST search: %v", err)
	}
	byNumber := make(map[int]int, len(got))
	for i, s := range got {
		byNumber[s.Number] = i
	}
	for _, w := range want {
		i, ok := byNumber[w.Number]
		if !ok {
			t.Errorf("#%d is in the GraphQL results but not the REST ones", w.Number)
			continue
		}
		g := got[i]
		if g.State != w.State {
			t.Errorf("#%d state: REST %q, GraphQL %q", w.Number, g.State, w.State)
		}
		if g.IsDraft != w.IsDraft {
			t.Errorf("#%d draft: REST %v, GraphQL %v", w.Number, g.IsDraft, w.IsDraft)
		}
		if g.Repo != w.Repo {
			t.Errorf("#%d repo: REST %q, GraphQL %q", w.Number, g.Repo, w.Repo)
		}
		if g.Title != w.Title {
			t.Errorf("#%d title: REST %q, GraphQL %q", w.Number, g.Title, w.Title)
		}
		if g.Author.Login != w.Author.Login {
			t.Errorf("#%d author: REST %q, GraphQL %q",
				w.Number, g.Author.Login, w.Author.Login)
		}
		if !g.UpdatedAt.Equal(w.UpdatedAt) {
			// updatedAt is the disk cache's validity key, so a REST/GraphQL
			// mismatch here would make a cached entry look stale forever.
			t.Errorf("#%d updatedAt: REST %s, GraphQL %s",
				w.Number, g.UpdatedAt, w.UpdatedAt)
		}
	}
}

func parityTarget(t *testing.T) (string, int) {
	t.Helper()
	repo, num := os.Getenv("GHX_E2E_REPO"), os.Getenv("GHX_E2E_PR")
	if repo == "" || num == "" {
		t.Skip("set GHX_E2E_REPO and GHX_E2E_PR to run the REST parity checks")
	}
	n, err := strconv.Atoi(num)
	if err != nil {
		t.Fatalf("GHX_E2E_PR=%q: %v", num, err)
	}
	return repo, n
}

func cutSlug(t *testing.T, repo string) (string, string, bool) {
	t.Helper()
	o, r, ok := splitSlug(repo)
	if !ok {
		t.Fatalf("GHX_E2E_REPO=%q is not owner/name", repo)
	}
	return o, r, ok
}
