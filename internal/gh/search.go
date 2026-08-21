package gh

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/keyolk/ghx/internal/pr"
)

// parseGitHubTime accepts the timestamp formats gh emits across commands.
func parseGitHubTime(s string) (time.Time, error) {
	if s == "" {
		return time.Time{}, fmt.Errorf("empty timestamp")
	}
	for _, layout := range []string{time.RFC3339, "2006-01-02T15:04:05Z", "2006-01-02 15:04:05 -0700 MST"} {
		if t, err := time.Parse(layout, s); err == nil {
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("unrecognized timestamp %q", s)
}

// `gh pr list` resolves PRs relative to the current git repository, so it fails
// outright when ghx runs outside a checkout — which is the normal case for a
// cross-repo review queue. `gh search prs` has no such requirement and returns
// the repository on each hit, so that is what the source tabs use.

// searchResult mirrors `gh search prs --json` output.
type searchResult struct {
	ID        string `json:"id"`
	Number    int    `json:"number"`
	Title     string `json:"title"`
	State     string `json:"state"`
	IsDraft   bool   `json:"isDraft"`
	URL       string `json:"url"`
	UpdatedAt string `json:"updatedAt"`
	Author    struct {
		Login string `json:"login"`
		Name  string `json:"name"`
		IsBot bool   `json:"is_bot"`
	} `json:"author"`
	Repository struct {
		Name          string `json:"name"`
		NameWithOwner string `json:"nameWithOwner"`
	} `json:"repository"`
	Labels []struct {
		Name string `json:"name"`
	} `json:"labels"`
}

// SearchPRs runs `gh search prs` with a raw GitHub search query. The query is
// the same syntax as the web search box, e.g. "review-requested:@me is:open".
func (c *Client) SearchPRs(ctx context.Context, query string, limit int) ([]pr.Summary, error) {
	if limit <= 0 {
		limit = 50
	}
	args := []string{"search", "prs"}
	// Flags that gh exposes directly must not be smuggled in as query terms,
	// so split the query into recognized flags plus a free-text remainder.
	args = append(args, searchQueryArgs(query)...)
	args = append(args,
		"--limit", strconv.Itoa(limit),
		"--json", "id,number,title,state,isDraft,url,updatedAt,author,repository,labels",
	)
	raw, err := c.execRaw(ctx, args...)
	if err != nil {
		// `gh search prs` spends the GraphQL search budget, which is the smallest
		// of them all — this is the call that runs out first in practice. REST
		// search is metered separately, so the cross-repo tabs survive.
		if isGraphQLUnavailable(err) {
			return c.SearchPRsREST(ctx, query, limit)
		}
		return nil, err
	}
	var results []searchResult
	if err := json.Unmarshal(raw, &results); err != nil {
		return nil, fmt.Errorf("decode search results: %w", err)
	}
	out := make([]pr.Summary, 0, len(results))
	for _, r := range results {
		s := pr.Summary{
			ID:          r.ID,
			Number:      r.Number,
			Title:       r.Title,
			State:       strings.ToUpper(r.State),
			IsDraft:     r.IsDraft,
			HeadRefName: r.Repository.NameWithOwner,
			Repo:        r.Repository.NameWithOwner,
			URL:         r.URL,
		}
		s.Author.Login = r.Author.Login
		s.Author.Name = r.Author.Name
		s.Author.IsBot = r.Author.IsBot
		if t, err := parseGitHubTime(r.UpdatedAt); err == nil {
			s.UpdatedAt = t
		}
		if c.credentialExplicit {
			s.CredentialRepo = c.credentialSelector()
		}
		out = append(out, s)
	}
	return out, nil
}

// searchQueryArgs translates a search string into gh search flags. `gh search
// prs` rejects some qualifiers as free text (notably state:), so the ones it
// models as flags are lifted out and the rest is passed through verbatim.
func searchQueryArgs(query string) []string {
	var flags []string
	var terms []string
	for _, tok := range strings.Fields(query) {
		key, val, ok := strings.Cut(tok, ":")
		if !ok {
			terms = append(terms, tok)
			continue
		}
		switch strings.ToLower(key) {
		case "state", "is":
			switch strings.ToLower(val) {
			case "open", "closed":
				flags = append(flags, "--state", strings.ToLower(val))
			case "merged":
				flags = append(flags, "--merged")
			case "draft":
				flags = append(flags, "--draft")
			default:
				terms = append(terms, tok)
			}
		case "review-requested":
			flags = append(flags, "--review-requested", val)
		case "assignee":
			flags = append(flags, "--assignee", val)
		case "author":
			flags = append(flags, "--author", val)
		case "mentions":
			flags = append(flags, "--mentions", val)
		case "org":
			flags = append(flags, "--owner", val)
		case "repo":
			flags = append(flags, "--repo", val)
		case "label":
			flags = append(flags, "--label", val)
		case "review":
			flags = append(flags, "--review", val)
		default:
			terms = append(terms, tok)
		}
	}
	// Free-text terms must come before flags for gh to treat them as the query.
	return append(terms, flags...)
}
