package gh

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"

	"github.com/keyolk/ghx/internal/pr"
)

// prStatusBatchQuery enriches a page of PR summaries in one request. GitHub's
// reviewThreads connection has no unresolved-only filter, so the first 100
// resolution bits are fetched here and exceptional larger threads paginate.
const prStatusBatchQuery = `query($ids:[ID!]!){
  rateLimit{remaining limit resetAt}
  nodes(ids:$ids){
    ... on PullRequest{
      id state isDraft reviewDecision
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
	ID    string `json:"id"`
	State string `json:"state"`
	// IsDraft is asked for here rather than trusted from the base listing: a PR
	// marked ready (or sent back to draft) after the list was cached would keep
	// showing the stale marker, and the enrichment pass is what everything else
	// on the row is refreshed from.
	IsDraft        bool                   `json:"isDraft"`
	ReviewDecision string                 `json:"reviewDecision"`
	ReviewThreads  statusThreadConnection `json:"reviewThreads"`
}

// statusRateLimit is the rateLimit block the batch query carries so the
// account's remaining GraphQL allowance costs nothing extra to learn.
type statusRateLimit struct {
	Remaining int    `json:"remaining"`
	Limit     int    `json:"limit"`
	ResetAt   string `json:"resetAt"`
}

type statusBatchResponse struct {
	Data struct {
		RateLimit statusRateLimit `json:"rateLimit"`
		Nodes     []statusNode    `json:"nodes"`
	} `json:"data"`
}

type statusPageResponse struct {
	Data struct {
		Node statusNode `json:"node"`
	} `json:"data"`
}

// statusRepo is the slug a summary's cache entry is filed under. A row from a
// repo-scoped list carries no slug of its own, so the client's own repo stands
// in — the same substitution EnrichPRStatuses makes when it groups the row, so
// the read and the write agree on the key.
func statusRepo(c *Client, s pr.Summary) string {
	if s.Repo != "" {
		return s.Repo
	}
	return c.repo
}

type statusGroup struct {
	client  *Client
	indices map[string][]int
}

// EnrichPRStatuses fills merge/review/conversation state without turning an
// optional status lookup into a failed base list. PRs are grouped by the token
// selected through git credential fill, so one cross-repository queue can span
// accounts without sending a node ID through the wrong GitHub identity.
//
// A row whose status is already cached under the same updatedAt is filled from
// disk and left out of the request entirely. That is where most of the saving
// is: on a steady queue nearly every row is unchanged between polls, and the
// batch that would have carried fifty nodes carries the two that moved. The
// cache is shared across ghx processes, so the six windows in budget.go's
// measurement now answer each other's polls instead of each paying in full.
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
		// The cached entry is checked before the row is grouped, so a hit costs
		// nothing at all — not a node in the batch, not a credential lookup.
		if entry, ok := c.status.get(repo, out[i].Number, out[i].UpdatedAt); ok {
			entry.apply(&out[i])
			continue
		}
		selector := out[i].CredentialRepo
		scoped := c.WithRepo(repo)
		if selector != "" {
			scoped = c.WithCredentialRepo(selector).WithRepo(repo)
		} else {
			selector = repo
		}
		key := "active-gh-account"
		if c.credentials != nil {
			if token, ok := c.credentials.token(ctx, selector); ok {
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
		err := enrichPRStatusGroup(ctx, group.client, out, group.indices)
		if err == nil {
			continue
		}
		// Without these the row's D/M/A/C/U markers are all dark, which reads as
		// "nothing to see" on a PR that may be blocked on changes. REST can answer
		// all but the conversation count, so ask it rather than showing a queue
		// stripped of the signals it exists to surface.
		if isGraphQLUnavailable(err) {
			if restErr := enrichPRStatusGroupREST(ctx, group.client, out, group.indices); restErr != nil {
				errs = append(errs, fmt.Errorf("%w (REST fallback: %v)", err, restErr))
			}
			continue
		}
		errs = append(errs, err)
	}
	return out, errors.Join(errs...)
}

// enrichPRStatusGroupREST fills the markers REST can answer.
//
// The GraphQL path resolves a whole page in one request; REST has no batch
// equivalent for reviews, so this costs one round trip per PR and the group is
// walked concurrently — a 78-row queue is 78 sequential round trips otherwise,
// which does not fit the fetch timeout. The cap keeps a large queue from
// opening a connection per PR and tripping abuse detection.
//
// One round trip per PR, not two. Both search paths already report state,
// draft, and merged for every row they return, so re-fetching `pulls/{n}` to
// learn them again is pure duplication — measured at 156 requests for a 78-row
// queue, enough to exhaust the REST budget in about thirty tab switches. Only
// reviews genuinely need asking, because no list endpoint embeds them.
//
// A row that somehow arrived without a state still gets the full lookup: the
// D/M markers are the ones this exists to light up, and guessing them from an
// empty string would put a wrong marker on screen.
func enrichPRStatusGroupREST(ctx context.Context, client *Client, out []pr.Summary, indices map[string][]int) error {
	type job struct {
		owner, repo string
		number      int
		rows        []int
		// full asks for state and draft too, for a row that did not carry them.
		full bool
	}
	var jobs []job
	for _, rows := range indices {
		if len(rows) == 0 {
			continue
		}
		s := out[rows[0]]
		owner, repo, ok := splitSlug(s.Repo)
		if !ok || s.Number <= 0 {
			// Without a repository there is nothing to ask REST about. The row
			// keeps whatever the base listing gave it.
			continue
		}
		jobs = append(jobs, job{
			owner: owner, repo: repo, number: s.Number, rows: rows,
			full: s.State == "",
		})
	}
	if len(jobs) == 0 {
		return nil
	}

	const workers = 8
	sem := make(chan struct{}, workers)
	var mu sync.Mutex
	var wg sync.WaitGroup
	var errs []error
	for _, j := range jobs {
		wg.Add(1)
		go func(j job) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			var state string
			var isDraft bool
			var decision string
			var err error
			if j.full {
				state, isDraft, decision, err = client.EnrichPRStatusREST(
					ctx, j.owner, j.repo, j.number)
			} else {
				decision, err = client.reviewDecisionREST(ctx, j.owner, j.repo, j.number)
			}

			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				errs = append(errs, err)
				return
			}
			for _, i := range j.rows {
				if j.full {
					out[i].State = state
					out[i].IsDraft = isDraft
				}
				out[i].ReviewDecision = decision
				// Deliberately not set: REST has no thread-resolution bit, so the
				// unresolved count stays unknown rather than being reported as zero.
				out[i].ConversationsKnown = false
				out[i].UnresolvedConversations = 0
				// Cached with the unknown flag intact. This path costs one round
				// trip per PR and runs precisely when GraphQL is unavailable, so
				// it is the one that most needs not to be repeated — and a later
				// GraphQL enrichment of the same row overwrites the entry with
				// the resolution bits REST could not supply.
				client.status.put(statusRepo(client, out[i]), out[i].Number,
					out[i].UpdatedAt, statusEntryOf(out[i]))
			}
		}(j)
	}
	wg.Wait()
	return errors.Join(errs...)
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
	// Record the allowance this response reported. Every poll goes through here,
	// so the reading is as fresh as the rows without costing a request.
	if rl := resp.Data.RateLimit; rl.Limit > 0 {
		resetAt, _ := parseGitHubTime(rl.ResetAt)
		client.budget.observe(rl.Remaining, rl.Limit, resetAt)
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
			out[i].IsDraft = node.IsDraft
			out[i].ReviewDecision = node.ReviewDecision
			out[i].UnresolvedConversations = unresolved
			out[i].ConversationsKnown = true
			client.status.put(statusRepo(client, out[i]), out[i].Number,
				out[i].UpdatedAt, statusEntryOf(out[i]))
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
