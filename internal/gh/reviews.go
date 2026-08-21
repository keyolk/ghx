package gh

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/keyolk/ghx/internal/pr"
)

// Review threads carry the line-position data that `gh pr view --json` omits,
// so they must come from GraphQL. Posting inline comments likewise has no gh
// subcommand — it goes through `gh api` against the REST endpoints.

// graphQLThreadsQuery fetches review threads with line positions.
// Field notes (verified against the schema): the thread exposes `diffSide`,
// NOT `side`; comments inherit the thread's side and have no `diffSide` field.
const graphQLThreadsQuery = `query($owner:String!,$repo:String!,$number:Int!,$after:String){
  repository(owner:$owner,name:$repo){
    pullRequest(number:$number){
      reviewThreads(first:100,after:$after){
        totalCount
        pageInfo{hasNextPage endCursor}
        nodes{
          id isResolved isCollapsed path line originalLine diffSide startLine originalStartLine
          comments(first:100){nodes{id databaseId body author{login} path line originalLine createdAt}}
        }
      }
    }
  }
}`

// threadsResponse mirrors the GraphQL response shape.
type threadsResponse struct {
	Data struct {
		Repository struct {
			PullRequest struct {
				ReviewThreads struct {
					TotalCount int `json:"totalCount"`
					PageInfo   struct {
						HasNextPage bool   `json:"hasNextPage"`
						EndCursor   string `json:"endCursor"`
					} `json:"pageInfo"`
					Nodes []struct {
						ID                string `json:"id"`
						IsResolved        bool   `json:"isResolved"`
						IsCollapsed       bool   `json:"isCollapsed"`
						Path              string `json:"path"`
						Line              int    `json:"line"`
						OriginalLine      int    `json:"originalLine"`
						DiffSide          string `json:"diffSide"`
						StartLine         int    `json:"startLine"`
						OriginalStartLine int    `json:"originalStartLine"`
						Comments          struct {
							Nodes []pr.ThreadComment `json:"nodes"`
						} `json:"comments"`
					} `json:"nodes"`
				} `json:"reviewThreads"`
			} `json:"pullRequest"`
		} `json:"repository"`
	} `json:"data"`
}

// ReviewThreads fetches all inline review threads, paginating past 100 so
// large reviews aren't silently truncated.
func (c *Client) ReviewThreads(ctx context.Context, owner, repo string, number int) ([]pr.ReviewThread, error) {
	var out []pr.ReviewThread
	cursor := ""
	for {
		args := []string{
			"api", "graphql",
			"-f", "query=" + graphQLThreadsQuery,
			"-F", "owner=" + owner,
			"-F", "repo=" + repo,
			"-F", fmt.Sprintf("number=%d", number),
		}
		if cursor != "" {
			args = append(args, "-F", "after="+cursor)
		}
		// `gh api graphql` must not be repo-scoped via --repo; owner/repo are
		// query variables here.
		raw, err := c.execRaw(ctx, args...)
		if err != nil {
			// Inline threads are the substance of a review, so losing GraphQL must
			// not empty the Comments tab. What comes back over REST has no
			// resolution bit — see ReviewThreadsREST.
			if isGraphQLUnavailable(err) {
				return c.ReviewThreadsREST(ctx, owner, repo, number)
			}
			return nil, err
		}
		var resp threadsResponse
		if err := json.Unmarshal(raw, &resp); err != nil {
			return nil, fmt.Errorf("decode review threads: %w", err)
		}
		rt := resp.Data.Repository.PullRequest.ReviewThreads
		for _, n := range rt.Nodes {
			out = append(out, pr.ReviewThread{
				ID:                n.ID,
				IsResolved:        n.IsResolved,
				ResolutionKnown:   true,
				IsCollapsed:       n.IsCollapsed,
				Path:              n.Path,
				Line:              n.Line,
				OriginalLine:      n.OriginalLine,
				DiffSide:          n.DiffSide,
				StartLine:         n.StartLine,
				OriginalStartLine: n.OriginalStartLine,
				Comments:          n.Comments.Nodes,
			})
		}
		if !rt.PageInfo.HasNextPage || rt.PageInfo.EndCursor == "" {
			break
		}
		cursor = rt.PageInfo.EndCursor
	}
	return out, nil
}

// ReviewPR submits a whole-PR review: action is "approve", "request-changes",
// or "comment". gh pr review has no inline-comment support.
func (c *Client) ReviewPR(ctx context.Context, number int, action, body string) error {
	var flag string
	switch action {
	case "approve":
		flag = "--approve"
	case "request-changes":
		flag = "--request-changes"
	case "comment":
		flag = "--comment"
	default:
		return fmt.Errorf("unknown review action %q", action)
	}
	args := []string{"pr", "review", fmt.Sprintf("%d", number), flag}
	if body != "" {
		args = append(args, "--body", body)
	}
	_, err := c.exec(ctx, args...)
	return err
}

// InlineComment is one pending inline comment for a batched review.
type InlineComment struct {
	Path      string
	Line      int
	Side      string // "LEFT" | "RIGHT"
	StartLine int    // 0 for single-line
	Body      string
	// CommitID is the head SHA the comment is anchored to. GitHub rejects a
	// standalone inline comment without it ("commit_id wasn't supplied"), so
	// callers must provide it or PostInlineComment will look it up.
	CommitID string
}

// PostInlineComment creates a standalone inline review comment on a line.
// Uses REST because neither `gh pr review` nor `gh pr comment` can target a line.
func (c *Client) PostInlineComment(ctx context.Context, owner, repo string, number int, ic InlineComment) error {
	if ic.Path == "" || ic.Line <= 0 {
		return fmt.Errorf("inline comment needs a path and line")
	}
	// A standalone comment (one not attached to a pending review) must name the
	// commit it applies to. Resolve the head SHA when the caller didn't supply it.
	if ic.CommitID == "" {
		sha, err := c.headSHA(ctx, number)
		if err != nil {
			return fmt.Errorf("resolve head commit for inline comment: %w", err)
		}
		ic.CommitID = sha
	}
	side := ic.Side
	if side == "" {
		side = "RIGHT"
	}
	// GitHub requires start_line < line; a visual selection dragged upward
	// arrives inverted, so normalize before sending rather than being rejected.
	line, startLine := ic.Line, ic.StartLine
	if startLine > 0 && startLine > line {
		line, startLine = startLine, line
	}
	endpoint := fmt.Sprintf("repos/%s/%s/pulls/%d/comments", owner, repo, number)
	args := []string{
		"api", "--method", "POST", endpoint,
		"-f", "body=" + ic.Body,
		"-f", "path=" + ic.Path,
		"-F", fmt.Sprintf("line=%d", line),
		"-f", "side=" + side,
		"-f", "commit_id=" + ic.CommitID,
	}
	// A multi-line comment needs both ends of the range on the same side.
	if startLine > 0 && startLine != line {
		args = append(args,
			"-F", fmt.Sprintf("start_line=%d", startLine),
			"-f", "start_side="+side,
		)
	}
	_, err := c.execRaw(ctx, args...)
	return err
}

// headSHA returns the PR's head commit, which inline comments must anchor to.
func (c *Client) headSHA(ctx context.Context, number int) (string, error) {
	out, err := c.exec(ctx, "pr", "view", fmt.Sprintf("%d", number), "--json", "headRefOid")
	if err != nil {
		return "", err
	}
	var v struct {
		HeadRefOid string `json:"headRefOid"`
	}
	if err := json.Unmarshal(out, &v); err != nil {
		return "", fmt.Errorf("decode head sha: %w", err)
	}
	if v.HeadRefOid == "" {
		return "", fmt.Errorf("pull request %d reported no head commit", number)
	}
	return v.HeadRefOid, nil
}

// PostReview submits a review with a batch of inline comments in one call.
// event is "COMMENT", "APPROVE", or "REQUEST_CHANGES".
func (c *Client) PostReview(ctx context.Context, owner, repo string, number int, event, body string, comments []InlineComment) error {
	endpoint := fmt.Sprintf("repos/%s/%s/pulls/%d/reviews", owner, repo, number)
	args := []string{"api", "--method", "POST", endpoint, "-f", "event=" + event}
	if body != "" {
		args = append(args, "-f", "body="+body)
	}
	for _, ic := range comments {
		side := ic.Side
		if side == "" {
			side = "RIGHT"
		}
		args = append(args,
			"-F", "comments[][path]="+ic.Path,
			"-F", fmt.Sprintf("comments[][line]=%d", ic.Line),
			"-F", "comments[][side]="+side,
			"-F", "comments[][body]="+ic.Body,
		)
	}
	_, err := c.execRaw(ctx, args...)
	return err
}

// ReplyToThread posts a reply into an existing review thread. The reply
// inherits the thread's path/line, so only the parent comment ID is needed.
func (c *Client) ReplyToThread(ctx context.Context, owner, repo string, number int, commentID, body string) error {
	endpoint := fmt.Sprintf("repos/%s/%s/pulls/%d/comments/%s/replies", owner, repo, number, commentID)
	_, err := c.execRaw(ctx, "api", "--method", "POST", endpoint, "-f", "body="+body)
	return err
}

// IssueComment posts a PR-level (non-inline) comment.
func (c *Client) IssueComment(ctx context.Context, number int, body string) error {
	_, err := c.exec(ctx, "pr", "comment", fmt.Sprintf("%d", number), "--body", body)
	return err
}

// ResolveThread marks a review thread as resolved. threadID is the GraphQL node
// ID returned in ReviewThread.ID.
func (c *Client) ResolveThread(ctx context.Context, threadID string) error {
	return c.threadResolveMutation(ctx, threadID, true)
}

// UnresolveThread marks a review thread as unresolved.
func (c *Client) UnresolveThread(ctx context.Context, threadID string) error {
	return c.threadResolveMutation(ctx, threadID, false)
}

func (c *Client) threadResolveMutation(ctx context.Context, threadID string, resolve bool) error {
	if threadID == "" {
		return fmt.Errorf("resolve thread: empty thread ID")
	}
	mutation := "mutation ResolveThread($id: ID!) { resolveReviewThread(input:{threadId:$id}) { thread { isResolved } } }"
	if !resolve {
		mutation = "mutation UnresolveThread($id: ID!) { unresolveReviewThread(input:{threadId:$id}) { thread { isResolved } } }"
	}
	_, err := c.execRaw(ctx, "api", "graphql", "-f", "query="+mutation, "-F", "id="+threadID)
	return err
}

// Checkout runs `gh pr checkout N`.
func (c *Client) Checkout(ctx context.Context, number int) error {
	_, err := c.exec(ctx, "pr", "checkout", fmt.Sprintf("%d", number))
	return err
}

// Ready marks a draft PR ready for review, or converts back with undo=true.
func (c *Client) Ready(ctx context.Context, number int, undo bool) error {
	args := []string{"pr", "ready", fmt.Sprintf("%d", number)}
	if undo {
		args = append(args, "--undo")
	}
	_, err := c.exec(ctx, args...)
	return err
}

// Merge merges a PR with the given strategy ("squash", "merge", "rebase").
// Callers must gate this behind an explicit confirmation — it is irreversible.
func (c *Client) Merge(ctx context.Context, number int, strategy string) error {
	var flag string
	switch strategy {
	case "squash":
		flag = "--squash"
	case "merge":
		flag = "--merge"
	case "rebase":
		flag = "--rebase"
	default:
		return fmt.Errorf("unknown merge strategy %q", strategy)
	}
	_, err := c.exec(ctx, "pr", "merge", fmt.Sprintf("%d", number), flag)
	return err
}

// RepoSlug resolves the "owner/repo" for the current context, needed by the
// REST/GraphQL calls above. Falls back to the client's configured repo.
func (c *Client) RepoSlug(ctx context.Context) (owner, repo string, err error) {
	if c.repo != "" {
		if o, r, ok := splitSlug(c.repo); ok {
			return o, r, nil
		}
	}
	out, err := c.execRaw(ctx, "repo", "view", "--json", "owner,name")
	if err != nil {
		return "", "", err
	}
	var v struct {
		Owner struct{ Login string } `json:"owner"`
		Name  string                 `json:"name"`
	}
	if err := json.Unmarshal(out, &v); err != nil {
		return "", "", fmt.Errorf("decode repo slug: %w", err)
	}
	if v.Owner.Login == "" || v.Name == "" {
		return "", "", fmt.Errorf("could not resolve owner/repo")
	}
	return v.Owner.Login, v.Name, nil
}

func splitSlug(s string) (owner, repo string, ok bool) {
	parts := strings.SplitN(s, "/", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", "", false
	}
	return parts[0], parts[1], true
}
