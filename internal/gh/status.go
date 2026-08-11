package gh

import (
	"context"
	"encoding/json"
	"errors"
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

type statusGroup struct {
	client  *Client
	indices map[string][]int
}

// EnrichPRStatuses fills merge/review/conversation state without turning an
// optional status lookup into a failed base list. PRs are grouped by the token
// selected through git credential fill, so one cross-repository queue can span
// accounts without sending a node ID through the wrong GitHub identity.
func (c *Client) EnrichPRStatuses(ctx context.Context, summaries []pr.Summary) ([]pr.Summary, error) {
	out := append([]pr.Summary(nil), summaries...)
	groups := make(map[string]*statusGroup)
	for i := range out {
		if out[i].ID == "" {
			continue
		}
		repo := out[i].Repo
		if repo == "" {
			repo = c.repo
		}
		scoped := c.WithRepo(repo)
		key := "active-gh-account"
		if c.credentials != nil {
			if token, ok := c.credentials.token(ctx, repo); ok {
				key = "token:" + token
			}
		}
		group := groups[key]
		if group == nil {
			group = &statusGroup{client: scoped, indices: make(map[string][]int)}
			groups[key] = group
		}
		group.indices[out[i].ID] = append(group.indices[out[i].ID], i)
	}

	var errs []error
	for _, group := range groups {
		if err := enrichPRStatusGroup(ctx, group.client, out, group.indices); err != nil {
			errs = append(errs, err)
		}
	}
	return out, errors.Join(errs...)
}

func enrichPRStatusGroup(ctx context.Context, client *Client, out []pr.Summary, indices map[string][]int) error {
	args := []string{"api", "graphql", "-f", "query=" + prStatusBatchQuery}
	for id := range indices {
		args = append(args, "-F", "ids[]="+id)
	}
	raw, err := client.execRaw(ctx, args...)
	if err != nil {
		return err
	}
	var resp statusBatchResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		return fmt.Errorf("decode PR statuses: %w", err)
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
			threads, err = client.fetchStatusThreadPage(ctx, node.ID, threads.PageInfo.EndCursor)
			if err != nil {
				return fmt.Errorf("load conversations for %s: %w", node.ID, err)
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
			return fmt.Errorf("status missing for PR node %s", id)
		}
	}
	return nil
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
