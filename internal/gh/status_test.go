package gh

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/keyolk/ghx/internal/pr"
)

func TestEnrichPRStatusesCountsPaginatedUnresolvedThreads(t *testing.T) {
	dir := t.TempDir()
	fakeGH := filepath.Join(dir, "gh")
	script := `#!/bin/sh
case "$*" in
  *after=cursor-1*)
    printf '%s\n' '{"data":{"node":{"id":"PR_one","reviewThreads":{"nodes":[{"isResolved":false},{"isResolved":false}],"pageInfo":{"hasNextPage":false,"endCursor":""}}}}}'
    ;;
  *)
    printf '%s\n' '{"data":{"nodes":[{"id":"PR_one","state":"MERGED","reviewDecision":"APPROVED","reviewThreads":{"nodes":[{"isResolved":false},{"isResolved":true}],"pageInfo":{"hasNextPage":true,"endCursor":"cursor-1"}}}]}}'
    ;;
esac
`
	if err := os.WriteFile(fakeGH, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))

	got, err := NewClient(0).EnrichPRStatuses(context.Background(), []pr.Summary{
		{ID: "PR_one", Number: 101, State: "OPEN"},
	})
	if err != nil {
		t.Fatalf("EnrichPRStatuses: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d summaries, want 1", len(got))
	}
	if got[0].State != "MERGED" || got[0].ReviewDecision != "APPROVED" {
		t.Errorf("status = %s/%s, want MERGED/APPROVED", got[0].State, got[0].ReviewDecision)
	}
	if !got[0].ConversationsKnown || got[0].UnresolvedConversations != 3 {
		t.Errorf("conversations = known:%v unresolved:%d, want true/3",
			got[0].ConversationsKnown, got[0].UnresolvedConversations)
	}
}

func TestEnrichPRStatusesLeavesMissingIDsUnknown(t *testing.T) {
	in := []pr.Summary{{Number: 101, ReviewDecision: "CHANGES_REQUESTED"}}
	got, err := NewClient(0).EnrichPRStatuses(context.Background(), in)
	if err != nil {
		t.Fatalf("summaries without node IDs should not fail: %v", err)
	}
	if got[0].ConversationsKnown {
		t.Error("missing node ID must not be treated as a known resolved conversation state")
	}
	if got[0].ReviewDecision != "CHANGES_REQUESTED" {
		t.Errorf("base review decision was lost: %q", got[0].ReviewDecision)
	}
}

func TestSearchAndListPreserveNodeID(t *testing.T) {
	dir := t.TempDir()
	fakeGH := filepath.Join(dir, "gh")
	script := `#!/bin/sh
case "$1 $2" in
  "search prs")
    printf '%s\n' '[{"id":"PR_search","number":11,"title":"search","state":"open","updatedAt":"2026-08-05T00:00:00Z","author":{"login":"a"},"repository":{"name":"one","nameWithOwner":"acme/one"}}]'
    ;;
  "pr list")
    printf '%s\n' '[{"id":"PR_list","number":22,"title":"list","state":"OPEN","author":{"login":"b"}}]'
    ;;
  *) exit 1 ;;
esac
`
	if err := os.WriteFile(fakeGH, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	client := NewClient(0)

	search, err := client.SearchPRs(context.Background(), "state:open", 1)
	if err != nil {
		t.Fatalf("SearchPRs: %v", err)
	}
	if len(search) != 1 || search[0].ID != "PR_search" {
		t.Errorf("search ID = %#v, want PR_search", search)
	}

	listed, err := client.ListPRs(context.Background(), "state:open", 1)
	if err != nil {
		t.Fatalf("ListPRs: %v", err)
	}
	if len(listed) != 1 || listed[0].ID != "PR_list" {
		t.Errorf("list ID = %#v, want PR_list", listed)
	}
}
