package gh

import (
	"context"
	"fmt"
	"strings"

	"github.com/keyolk/ghx/internal/pr"
)

// ListPRs lists a single repository's PRs via `gh pr list`.
//
// Qualifiers that gh models as flags (state:, author:, assignee:, label:, base:,
// draft:) are lifted out of the query so they go down the plain REST listing
// path. Anything left over is passed via --search, which is more expressive but
// spends the GraphQL search budget — worth avoiding for the common cases.
func (c *Client) ListPRs(ctx context.Context, query string, limit int) ([]pr.Summary, error) {
	if limit <= 0 {
		limit = 30
	}
	flags, leftover := listQueryArgs(query)
	args := []string{"pr", "list"}
	args = append(args, flags...)
	if leftover != "" {
		args = append(args, "--search", leftover)
	}
	args = append(args,
		"--json", "id,number,title,author,state,isDraft,reviewDecision,headRefName,updatedAt",
		"--limit", fmt.Sprintf("%d", limit),
	)
	var out []pr.Summary
	if err := c.execJSON(ctx, &out, args...); err != nil {
		// `gh pr list --json` is GraphQL under the hood despite the REST-shaped
		// subcommand, so a GraphQL refusal empties the tab entirely. REST search
		// answers the same question.
		if isGraphQLUnavailable(err) {
			return c.ListPRsREST(ctx, query, limit)
		}
		return nil, err
	}
	if c.credentialExplicit {
		for i := range out {
			out[i].CredentialRepo = c.credentialSelector()
		}
	}
	return out, nil
}

// listQueryArgs splits a query into gh pr list flags plus the unhandled remainder.
func listQueryArgs(query string) (flags []string, leftover string) {
	var rest []string
	for _, tok := range strings.Fields(query) {
		key, val, ok := strings.Cut(tok, ":")
		if !ok {
			rest = append(rest, tok)
			continue
		}
		switch strings.ToLower(key) {
		case "state", "is":
			switch strings.ToLower(val) {
			case "open", "closed", "merged", "all":
				flags = append(flags, "--state", strings.ToLower(val))
			case "draft":
				flags = append(flags, "--draft")
			default:
				rest = append(rest, tok)
			}
		case "author":
			flags = append(flags, "--author", val)
		case "assignee":
			flags = append(flags, "--assignee", val)
		case "label":
			flags = append(flags, "--label", val)
		case "base":
			flags = append(flags, "--base", val)
		case "head":
			flags = append(flags, "--head", val)
		default:
			// review-requested:, mentions:, and friends have no flag on gh pr
			// list, so they have to go through --search.
			rest = append(rest, tok)
		}
	}
	return flags, strings.Join(rest, " ")
}

// ViewPR runs `gh pr view N --json ...` and returns the full PR detail.
func (c *Client) ViewPR(ctx context.Context, number int) (*pr.Detail, error) {
	args := []string{
		"pr", "view", fmt.Sprintf("%d", number),
		"--json", "number,title,body,author,state,isDraft,baseRefName,headRefName," +
			"additions,deletions,changedFiles,mergeable,mergeStateStatus,reviewDecision," +
			"labels,reviewRequests,reviews,commits,files,url",
	}
	var out pr.Detail
	if err := c.execJSON(ctx, &out, args...); err != nil {
		return nil, err
	}
	return &out, nil
}

// PRDiff runs `gh pr diff N` and returns the raw unified diff text.
func (c *Client) PRDiff(ctx context.Context, number int) (string, error) {
	out, err := c.exec(ctx, "pr", "diff", fmt.Sprintf("%d", number))
	if err != nil {
		return "", err
	}
	return string(out), nil
}

// PRChecks runs `gh pr checks N --json ...`.
func (c *Client) PRChecks(ctx context.Context, number int) ([]pr.Check, error) {
	args := []string{
		"pr", "checks", fmt.Sprintf("%d", number),
		"--json", "bucket,name,state,link,workflow,event,startedAt,completedAt",
	}
	var out []pr.Check
	if err := c.execJSON(ctx, &out, args...); err != nil {
		// "no checks reported" exits 1, the same as a real failure. A PR whose
		// repository has no CI is an ordinary PR, not an error: reporting one
		// puts a red toast on screen every time such a PR is opened, and — since
		// the caller cannot tell the two apart — leaves the checks fetch
		// permanently unfinished.
		if isNoChecksReported(err) {
			return nil, nil
		}
		return nil, err
	}
	return out, nil
}

// isNoChecksReported recognizes gh's way of saying a PR has no CI at all.
// The exit status is 1 for this and for a genuine failure alike, so the message
// is what distinguishes them.
func isNoChecksReported(err error) bool {
	return err != nil &&
		strings.Contains(strings.ToLower(err.Error()), "no checks reported")
}
