package gh

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/keyolk/ghx/internal/pr"
)

// prStatusBatchQuery enriches a page of PR summaries in one request. GitHub's
// reviewThreads connection has no unresolved-only filter, so the first 100
// resolution bits are fetched here and exceptional larger threads paginate.
const prStatusBatchQuery = `query($ids:[ID!]!){
  nodes(ids:$ids){
    ... on PullRequest{
      id state reviewDecision
      reviewThreads(first:100){
        nodes{isResolved}
        pageInfo{hasNextPage endCursor}
      }
    }
  }
}`

const prStatusThreadsPageQuery = `query($id:ID!,$after:String!){
  node(id:$id){
    ... on PullRequest{
      reviewThreads(first:100,after:$after){
        nodes{isResolved}
        pageInfo{hasNextPage endCursor}
      }
    }
  }
}`

type statusThreadConnection struct {
	Nodes []struct {
		IsResolved bool `json:"isResolved"`
	} `json:"nodes"`
	PageInfo struct {
		HasNextPage bool   `json:"hasNextPage"`
		EndCursor   string `json:"endCursor"`
	} `json:"pageInfo"`
}

type statusNode struct {
	ID             string                 `json:"id"`
	State          string                 `json:"state"`
	ReviewDecision string                 `json:"reviewDecision"`
	ReviewThreads  statusThreadConnection `json:"reviewThreads"`
}

type statusBatchResponse struct {
	Data struct {
		Nodes []statusNode `json:"nodes"`
	} `json:"data"`
}

type statusPageResponse struct {
	Data struct {
		Node statusNode `json:"node"`
	} `json:"data"`
}

// EnrichPRStatuses fills merge/review/conversation state without turning an
// optional status lookup into a failed base list. Callers can keep the returned
// summaries and surface err as a warning.
func (c *Client) EnrichPRStatuses(ctx context.Context, summaries []pr.Summary) ([]pr.Summary, error) {
	out := append([]pr.Summary(nil), summaries...)
	indices := make(map[string][]int, len(out))
	args := []string{"api", "graphql", "-f", "query=" + prStatusBatchQuery}
	for i := range out {
		if out[i].ID == "" {
			continue
		}
		indices[out[i].ID] = append(indices[out[i].ID], i)
		args = append(args, "-F", "ids[]="+out[i].ID)
	}
	if len(indices) == 0 {
		return out, nil
	}

	raw, err := c.execRaw(ctx, args...)
	if err != nil {
		return out, err
	}
	var resp statusBatchResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		return out, fmt.Errorf("decode PR statuses: %w", err)
	}

	seen := make(map[string]bool, len(resp.Data.Nodes))
	for _, node := range resp.Data.Nodes {
		if node.ID == "" {
			continue
		}
		seen[node.ID] = true
		unresolved := countUnresolved(node.ReviewThreads)
		threads := node.ReviewThreads
		for threads.PageInfo.HasNextPage && threads.PageInfo.EndCursor != "" {
			threads, err = c.fetchStatusThreadPage(ctx, node.ID, threads.PageInfo.EndCursor)
			if err != nil {
				return out, fmt.Errorf("load conversations for %s: %w", node.ID, err)
			}
			unresolved += countUnresolved(threads)
		}
		for _, i := range indices[node.ID] {
			out[i].State = node.State
			out[i].ReviewDecision = node.ReviewDecision
			out[i].UnresolvedConversations = unresolved
			out[i].ConversationsKnown = true
		}
	}

	for id := range indices {
		if !seen[id] {
			return out, fmt.Errorf("status missing for PR node %s", id)
		}
	}
	return out, nil
}

func (c *Client) fetchStatusThreadPage(ctx context.Context, id, cursor string) (statusThreadConnection, error) {
	args := []string{
		"api", "graphql",
		"-f", "query=" + prStatusThreadsPageQuery,
		"-F", "id=" + id,
		"-F", "after=" + cursor,
	}
	raw, err := c.execRaw(ctx, args...)
	if err != nil {
		return statusThreadConnection{}, err
	}
	var resp statusPageResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		return statusThreadConnection{}, fmt.Errorf("decode conversation page: %w", err)
	}
	return resp.Data.Node.ReviewThreads, nil
}

func countUnresolved(threads statusThreadConnection) int {
	count := 0
	for _, thread := range threads.Nodes {
		if !thread.IsResolved {
			count++
		}
	}
	return count
}
