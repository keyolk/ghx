package gh

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/keyolk/ghx/internal/pr"
)

// REST equivalents of the GraphQL-backed listings, used when GraphQL refuses.
// See fallback.go for why these exist and what deliberately has no REST answer.

// restPRState normalizes REST's state to the OPEN/CLOSED/MERGED vocabulary the
// rest of ghx uses. REST calls a merged PR "closed", and taking that at face
// value would blank the M marker on every merged row.
func restPRState(state string, merged bool) string {
	if merged {
		return "MERGED"
	}
	return strings.ToUpper(state)
}

// ListPRsREST lists one repository's PRs over REST.
//
// It goes through search rather than `GET /repos/{owner}/{repo}/pulls` even
// though the listing endpoint has the far larger budget. The listing takes one
// `state` parameter and nothing else, so every other qualifier a source uses —
// author:@me, review-requested:@me, label: — would have to be re-implemented
// client-side against a page of results that was never narrowed by it. That
// filter would silently disagree with the GraphQL path it stands in for, which
// is worse than spending a search request: the tab would look fine and show the
// wrong PRs.
func (c *Client) ListPRsREST(ctx context.Context, query string, limit int) ([]pr.Summary, error) {
	owner, repo, err := c.RepoSlug(ctx)
	if err != nil {
		return nil, err
	}
	scoped := query
	if !strings.Contains(strings.ToLower(query), "repo:") {
		scoped = strings.TrimSpace("repo:" + owner + "/" + repo + " " + query)
	}
	return c.SearchPRsREST(ctx, scoped, limit)
}

// restSearchItem is the subset of `GET /search/issues` ghx reads.
type restSearchItem struct {
	NodeID  string `json:"node_id"`
	Number  int    `json:"number"`
	Title   string `json:"title"`
	State   string `json:"state"`
	Draft   bool   `json:"draft"`
	HTMLURL string `json:"html_url"`
	Updated string `json:"updated_at"`
	User    struct {
		Login string `json:"login"`
		Type  string `json:"type"`
	} `json:"user"`
	// RepositoryURL is the only place the repository appears — the search item
	// has no repository object. Without parsing it every later call for this PR
	// would have no repo to scope to.
	RepositoryURL string `json:"repository_url"`
	PullRequest   *struct {
		MergedAt string `json:"merged_at"`
	} `json:"pull_request"`
}

type restSearchResponse struct {
	Items []restSearchItem `json:"items"`
}

// SearchPRsREST runs a cross-repository PR search over REST.
//
// The query goes to `q=` verbatim: REST search speaks the same qualifier syntax
// the user typed, so the flag-lifting SearchPRs does for `gh search prs` would
// have to be undone here.
func (c *Client) SearchPRsREST(ctx context.Context, query string, limit int) ([]pr.Summary, error) {
	if limit <= 0 {
		limit = 50
	}
	q := strings.TrimSpace(query + " is:pr")
	endpoint := fmt.Sprintf("search/issues?q=%s&sort=updated&order=desc&per_page=%d",
		urlQueryEscape(q), min(limit, 100))
	// --cache disabled: a review queue that answers from a stale cache is worse
	// than one that says it could not refresh.
	raw, err := c.execRaw(ctx, "api", endpoint)
	if err != nil {
		return nil, err
	}
	var resp restSearchResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		return nil, fmt.Errorf("decode REST search results: %w", err)
	}
	out := make([]pr.Summary, 0, len(resp.Items))
	for _, item := range resp.Items {
		merged := item.PullRequest != nil && item.PullRequest.MergedAt != ""
		s := pr.Summary{
			ID:      item.NodeID,
			Number:  item.Number,
			Title:   item.Title,
			State:   restPRState(item.State, merged),
			IsDraft: item.Draft,
			Repo:    repoFromAPIURL(item.RepositoryURL),
			URL:     item.HTMLURL,
		}
		s.HeadRefName = s.Repo
		s.Author.Login = item.User.Login
		s.Author.IsBot = item.User.Type == "Bot"
		if t, err := parseGitHubTime(item.Updated); err == nil {
			s.UpdatedAt = t
		}
		if c.credentialExplicit {
			s.CredentialRepo = c.credentialSelector()
		}
		out = append(out, s)
	}
	return out, nil
}

// repoFromAPIURL turns "https://api.github.com/repos/owner/name" into
// "owner/name".
func repoFromAPIURL(u string) string {
	const marker = "/repos/"
	i := strings.Index(u, marker)
	if i < 0 {
		return ""
	}
	slug := strings.Trim(u[i+len(marker):], "/")
	if owner, name, ok := strings.Cut(slug, "/"); ok && owner != "" && name != "" {
		return owner + "/" + name
	}
	return ""
}

// urlQueryEscape percent-encodes a search query for a gh api path. Only the
// characters that would otherwise break the path are escaped, so the qualifier
// syntax stays readable in error messages.
func urlQueryEscape(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == '-' || r == '_' || r == '.' || r == '~' || r == ':' || r == '/' || r == '@':
			b.WriteRune(r)
		case r == ' ':
			b.WriteByte('+')
		default:
			for _, c := range []byte(string(r)) {
				b.WriteString("%" + strings.ToUpper(strconv.FormatInt(int64(c), 16)))
			}
		}
	}
	return b.String()
}

// --- status enrichment ---

// restPullStatus is what `GET /repos/{owner}/{repo}/pulls/{n}` contributes to a
// row's markers.
type restPullStatus struct {
	State    string `json:"state"`
	Draft    bool   `json:"draft"`
	Merged   bool   `json:"merged"`
	MergedAt string `json:"merged_at"`
}

// restReview is one entry of `GET /repos/{owner}/{repo}/pulls/{n}/reviews`.
type restReview struct {
	State string `json:"state"`
	User  struct {
		Login string `json:"login"`
	} `json:"user"`
	SubmittedAt string `json:"submitted_at"`
}

// EnrichPRStatusREST fills one PR's markers over REST.
//
// Two things GraphQL answers have no REST equivalent and are left alone rather
// than approximated:
//
//   - Thread resolution. There is no resolution bit anywhere in REST, so
//     ConversationsKnown stays false and the U marker stays dark — the row says
//     "not known", not "none".
//   - GitHub's own reviewDecision, which folds in branch protection and can
//     read REVIEW_REQUIRED with no reviews submitted. What is computed here is
//     only what the A and C markers need: the latest verdict per reviewer.
func (c *Client) EnrichPRStatusREST(ctx context.Context, owner, repo string, number int) (state string, isDraft bool, decision string, err error) {
	raw, err := c.execRaw(ctx, "api",
		fmt.Sprintf("repos/%s/%s/pulls/%d", owner, repo, number))
	if err != nil {
		return "", false, "", err
	}
	var p restPullStatus
	if err := json.Unmarshal(raw, &p); err != nil {
		return "", false, "", fmt.Errorf("decode REST pull %d: %w", number, err)
	}

	raw, err = c.execRaw(ctx, "api", "--paginate",
		fmt.Sprintf("repos/%s/%s/pulls/%d/reviews?per_page=100", owner, repo, number))
	if err != nil {
		return "", false, "", err
	}
	var reviews []restReview
	if err := json.Unmarshal(raw, &reviews); err != nil {
		return "", false, "", fmt.Errorf("decode REST reviews for %d: %w", number, err)
	}
	return restPRState(p.State, p.Merged || p.MergedAt != ""), p.Draft,
		restReviewDecision(reviews), nil
}

// restReviewDecision reduces a review list to APPROVED / CHANGES_REQUESTED / "".
//
// Only a reviewer's latest verdict counts, and COMMENTED is not a verdict — it
// neither approves nor blocks, and letting it overwrite an earlier approval
// would drop the A marker off a PR that is in fact approved. Reviews arrive
// oldest-first, so a later entry replaces an earlier one.
func restReviewDecision(reviews []restReview) string {
	latest := make(map[string]string, len(reviews))
	for _, r := range reviews {
		switch strings.ToUpper(r.State) {
		case "APPROVED", "CHANGES_REQUESTED", "DISMISSED":
			latest[r.User.Login] = strings.ToUpper(r.State)
		}
	}
	approved := false
	for _, state := range latest {
		switch state {
		case "CHANGES_REQUESTED":
			// One block outranks any number of approvals, which is how GitHub
			// itself reports the decision.
			return "CHANGES_REQUESTED"
		case "APPROVED":
			approved = true
		}
	}
	if approved {
		return "APPROVED"
	}
	return ""
}

// --- review threads ---

// restReviewComment is one entry of `GET /repos/{o}/{r}/pulls/{n}/comments`.
type restReviewComment struct {
	ID       int64  `json:"id"`
	NodeID   string `json:"node_id"`
	Body     string `json:"body"`
	Path     string `json:"path"`
	Line     int    `json:"line"`
	OrigLine int    `json:"original_line"`
	// StartLine/OriginalStartLine describe a multi-line comment's upper bound.
	StartLine     int    `json:"start_line"`
	OrigStartLine int    `json:"original_start_line"`
	Side          string `json:"side"`
	StartSide     string `json:"start_side"`
	// InReplyToID is what makes thread reconstruction possible: a comment with
	// one is a reply to the comment it names, and one without opens a thread.
	InReplyToID int64  `json:"in_reply_to_id"`
	CreatedAt   string `json:"created_at"`
	User        struct {
		Login string `json:"login"`
		Type  string `json:"type"`
	} `json:"user"`
}

// ReviewThreadsREST reconstructs inline review threads from REST comments.
//
// REST has no thread object — it has a flat comment list where a reply carries
// in_reply_to_id. Grouping by that recovers the threads, with one thing
// permanently missing: whether a thread is resolved. That bit exists only in
// GraphQL, so every thread here reports ResolutionKnown false, and the UI must
// neither mark it resolved nor claim it is outstanding.
func (c *Client) ReviewThreadsREST(ctx context.Context, owner, repo string, number int) ([]pr.ReviewThread, error) {
	raw, err := c.execRaw(ctx, "api", "--paginate",
		fmt.Sprintf("repos/%s/%s/pulls/%d/comments?per_page=100", owner, repo, number))
	if err != nil {
		return nil, err
	}
	var comments []restReviewComment
	if err := json.Unmarshal(raw, &comments); err != nil {
		return nil, fmt.Errorf("decode REST review comments: %w", err)
	}
	return buildRESTThreads(comments), nil
}

// buildRESTThreads groups a flat comment list into threads by in_reply_to_id.
func buildRESTThreads(comments []restReviewComment) []pr.ReviewThread {
	// Roots in the order GitHub returned them, so threads appear in the order
	// they were opened rather than in map order.
	var order []int64
	roots := make(map[int64]*pr.ReviewThread)
	for _, cm := range comments {
		if cm.InReplyToID != 0 {
			continue
		}
		t := &pr.ReviewThread{
			// The node id is a real GraphQL id for the comment, but a *comment*
			// is not a *thread*: resolveReviewThread would reject it. Leaving the
			// thread ID empty is what makes the resolve path refuse rather than
			// send something GitHub will not accept.
			Path:              cm.Path,
			Line:              cm.Line,
			OriginalLine:      cm.OrigLine,
			DiffSide:          restSide(cm.Side),
			StartLine:         cm.StartLine,
			OriginalStartLine: cm.OrigStartLine,
			Comments:          []pr.ThreadComment{restThreadComment(cm)},
		}
		roots[cm.ID] = t
		order = append(order, cm.ID)
	}
	// A reply whose root is absent from this page still has to surface, so it
	// becomes its own thread rather than being dropped.
	for _, cm := range comments {
		if cm.InReplyToID == 0 {
			continue
		}
		root, ok := roots[cm.InReplyToID]
		if !ok {
			t := &pr.ReviewThread{
				Path: cm.Path, Line: cm.Line, OriginalLine: cm.OrigLine,
				DiffSide: restSide(cm.Side),
				Comments: []pr.ThreadComment{restThreadComment(cm)},
			}
			roots[cm.ID] = t
			order = append(order, cm.ID)
			continue
		}
		root.Comments = append(root.Comments, restThreadComment(cm))
	}

	out := make([]pr.ReviewThread, 0, len(order))
	for _, id := range order {
		out = append(out, *roots[id])
	}
	return out
}

func restThreadComment(cm restReviewComment) pr.ThreadComment {
	tc := pr.ThreadComment{
		ID:         cm.NodeID,
		DatabaseID: cm.ID,
		Body:       cm.Body,
		Path:       cm.Path,
		Line:       cm.Line,
	}
	tc.Author.Login = cm.User.Login
	tc.Author.IsBot = cm.User.Type == "Bot"
	if t, err := parseGitHubTime(cm.CreatedAt); err == nil {
		tc.CreatedAt = t
	}
	return tc
}

// restSide normalizes the diff side. REST already answers in upper case, but a
// comment predating the side field arrives empty, and RIGHT is where a comment
// without one belongs.
func restSide(side string) string {
	if side == "" {
		return "RIGHT"
	}
	return strings.ToUpper(side)
}
