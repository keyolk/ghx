package gh

import (
	"context"
	"strings"
	"testing"
)

// The Comments tab used to fetch only reviewThreads. A PR whose whole
// discussion is PR-level — CI reports, an Atlantis plan failure, "LGTM" —
// therefore read as having no comments at all.
func TestConversationsReturnsIssueCommentsAndReviewBodies(t *testing.T) {
	fakeGH(t, `
case "$*" in
  *graphql*)
    cat <<'JSON'
{"data":{"repository":{"pullRequest":{
  "comments":{"pageInfo":{"hasNextPage":false,"endCursor":null},
    "nodes":[{"id":"IC_1","body":"Plan Failed","author":{"login":"bot"},"createdAt":"2026-08-30T16:37:20Z","url":"u1"}]},
  "reviews":{"pageInfo":{"hasNextPage":false,"endCursor":null},
    "nodes":[
      {"id":"PRR_1","body":"LGTM!","state":"APPROVED","author":{"login":"jinsekim"},"submittedAt":"2026-08-30T16:40:07Z","url":"u2"},
      {"id":"PRR_2","body":"","state":"APPROVED","author":{"login":"kairo"},"submittedAt":"2026-08-30T16:38:28Z","url":"u3"}
    ]}
}}}}
JSON
    ;;
  *) echo "unexpected: $*" >&2; exit 1 ;;
esac
`)
	got, err := NewClient(0).Conversations(context.Background(), "sendbird", "delight-ops-nest", 205)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d conversations, want the comment and the review that had a body", len(got))
	}
	// Chronological: a reply only reads as a reply in the order it happened.
	if !got[0].CreatedAt.Before(got[1].CreatedAt) {
		t.Errorf("conversations are not in chronological order: %v", got)
	}
	if got[0].Kind != "comment" || got[0].Author.Login != "bot" {
		t.Errorf("first = %+v, want the issue comment", got[0])
	}
	if got[1].Kind != "APPROVED" || got[1].Body != "LGTM!" {
		t.Errorf("second = %+v, want the review body", got[1])
	}
}

// A GraphQL refusal must not empty the tab: REST answers both halves.
func TestConversationsFallsBackToREST(t *testing.T) {
	fakeGH(t, `
case "$*" in
  *graphql*)
    echo "GraphQL: Resource not accessible by personal access token" >&2
    exit 1
    ;;
  *"issues/205/comments"*)
    printf '%s\n' '[{"node_id":"IC_1","body":"Plan Failed","html_url":"u1","created_at":"2026-08-30T16:37:20Z","user":{"login":"bot","type":"Bot"}}]'
    ;;
  *"pulls/205/reviews"*)
    printf '%s\n' '[{"node_id":"PRR_1","body":"LGTM!","state":"APPROVED","html_url":"u2","submitted_at":"2026-08-30T16:40:07Z","user":{"login":"jinsekim","type":"User"}}]'
    ;;
  *) echo "unexpected: $*" >&2; exit 1 ;;
esac
`)
	got, err := NewClient(0).Conversations(context.Background(), "sendbird", "delight-ops-nest", 205)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("REST fallback returned %d, want both halves of the discussion", len(got))
	}
	if !got[0].Author.IsBot {
		t.Error("a Bot author was not marked as one")
	}
	if got[1].Kind != "APPROVED" {
		t.Errorf("review kind = %q, want APPROVED", got[1].Kind)
	}
}

// Losing one REST endpoint must not discard the other: half the discussion is
// the answer here, and "no comments" on a PR that has them is the bug this
// whole path exists to fix.
func TestConversationsRESTKeepsWhatOneEndpointReturned(t *testing.T) {
	fakeGH(t, `
case "$*" in
  *"issues/205/comments"*)
    printf '%s\n' '[{"node_id":"IC_1","body":"Plan Failed","created_at":"2026-08-30T16:37:20Z","user":{"login":"bot","type":"Bot"}}]'
    ;;
  *"pulls/205/reviews"*) echo "boom" >&2; exit 1 ;;
  *) echo "unexpected: $*" >&2; exit 1 ;;
esac
`)
	got, err := NewClient(0).ConversationsREST(context.Background(), "sendbird", "delight-ops-nest", 205)
	if err != nil {
		t.Fatalf("a failure on one endpoint discarded the other: %v", err)
	}
	if len(got) != 1 || got[0].Body != "Plan Failed" {
		t.Errorf("got %+v, want the issue comment that did arrive", got)
	}
}

// Both endpoints failing is a real failure, and the error must name both.
func TestConversationsRESTFailsWhenBothEndpointsDo(t *testing.T) {
	fakeGH(t, "echo boom >&2; exit 1\n")

	_, err := NewClient(0).ConversationsREST(context.Background(), "sendbird", "delight-ops-nest", 205)
	if err == nil {
		t.Fatal("both endpoints failed but no error was reported")
	}
	for _, want := range []string{"issue comments", "reviews"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error does not name %q: %v", want, err)
		}
	}
}
