package gh

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
)

// Applying a review suggestion. GitHub's web UI has a button for this; the API
// has no equivalent — there is no applySuggestion mutation, and the REST
// comment endpoints only read the block. What the UI does is committable
// directly: replace the anchored lines in the file and push the result to the
// PR's head branch.
//
// createCommitOnBranch is used rather than the Git data API's blob/tree/commit
// sequence because it is one round trip after the reads, and because it takes
// expectedHeadOid — the branch moves under a reviewer often enough (the author
// pushes, another suggestion lands) that a blind write would silently discard
// whatever arrived in between.

// SuggestionTarget locates the lines a suggestion replaces.
type SuggestionTarget struct {
	Owner  string
	Repo   string
	Branch string // the PR's head branch
	Path   string
	// StartLine and Line bound the replaced range, 1-based and inclusive. A
	// single-line suggestion has StartLine == 0, matching how GitHub reports a
	// comment that is not a multi-line one.
	StartLine int
	Line      int
}

// headRef is the branch a suggestion commits to, resolved together with the
// repository that owns it — a PR from a fork has its head on the fork, and
// committing to the base repository's branch of the same name would write to
// someone else's work, or fail.
type headRef struct {
	Owner  string
	Repo   string
	Branch string
	OID    string
}

// PRHeadRef resolves the PR's head repository, branch, and current commit.
func (c *Client) PRHeadRef(ctx context.Context, number int) (headRef, error) {
	owner, repo, err := c.RepoSlug(ctx)
	if err != nil {
		return headRef{}, err
	}
	const q = `query($owner:String!,$repo:String!,$number:Int!){
  repository(owner:$owner,name:$repo){
    pullRequest(number:$number){
      headRefName
      headRefOid
      headRepository{ name owner{ login } }
    }
  }
}`
	raw, err := c.execRaw(ctx, "api", "graphql",
		"-f", "query="+q,
		"-F", "owner="+owner, "-F", "repo="+repo,
		"-F", fmt.Sprintf("number=%d", number))
	if err != nil {
		return headRef{}, err
	}
	var resp struct {
		Data struct {
			Repository struct {
				PullRequest struct {
					HeadRefName    string `json:"headRefName"`
					HeadRefOid     string `json:"headRefOid"`
					HeadRepository *struct {
						Name  string `json:"name"`
						Owner struct {
							Login string `json:"login"`
						} `json:"owner"`
					} `json:"headRepository"`
				} `json:"pullRequest"`
			} `json:"repository"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		return headRef{}, fmt.Errorf("decode head ref: %w", err)
	}
	p := resp.Data.Repository.PullRequest
	if p.HeadRefName == "" || p.HeadRefOid == "" {
		return headRef{}, fmt.Errorf("could not resolve the head branch of #%d", number)
	}
	// A deleted fork leaves headRepository null; there is nothing to commit to.
	if p.HeadRepository == nil || p.HeadRepository.Name == "" {
		return headRef{}, fmt.Errorf(
			"#%d's head repository no longer exists — the fork was deleted", number)
	}
	return headRef{
		Owner:  p.HeadRepository.Owner.Login,
		Repo:   p.HeadRepository.Name,
		Branch: p.HeadRefName,
		OID:    p.HeadRefOid,
	}, nil
}

// FileAtRef returns a file's contents at a commit.
func (c *Client) FileAtRef(ctx context.Context, owner, repo, ref, path string) (string, error) {
	raw, err := c.execRaw(ctx, "api",
		fmt.Sprintf("repos/%s/%s/contents/%s?ref=%s",
			owner, repo, strings.TrimPrefix(path, "/"), ref),
		"--jq", ".content")
	if err != nil {
		return "", err
	}
	// The API returns base64 with newlines every 60 characters.
	encoded := strings.Map(func(r rune) rune {
		if r == '\n' || r == '\r' || r == '"' {
			return -1
		}
		return r
	}, string(raw))
	decoded, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return "", fmt.Errorf("decode %s: %w", path, err)
	}
	return string(decoded), nil
}

// ApplySuggestion replaces the target's lines with replacement and commits the
// result to the PR's head branch.
//
// expectedHeadOid makes this fail rather than clobber when the branch moved
// since the diff was read — the author pushing while a reviewer works through
// suggestions is the ordinary case, not the exotic one.
func (c *Client) ApplySuggestion(
	ctx context.Context, number int, t SuggestionTarget, replacement string, deletion bool,
) error {
	head, err := c.PRHeadRef(ctx, number)
	if err != nil {
		return err
	}
	original, err := c.FileAtRef(ctx, head.Owner, head.Repo, head.OID, t.Path)
	if err != nil {
		return fmt.Errorf("read %s at %s: %w", t.Path, head.OID[:min(7, len(head.OID))], err)
	}
	updated, err := ReplaceLines(original, t.StartLine, t.Line, replacement, deletion)
	if err != nil {
		return err
	}
	if updated == original {
		return fmt.Errorf("the suggestion for %s:%d matches what is already there",
			t.Path, t.Line)
	}

	const m = `mutation($input:CreateCommitOnBranchInput!){
  createCommitOnBranch(input:$input){ commit{ oid } }
}`
	input := map[string]any{
		"branch": map[string]any{
			"repositoryNameWithOwner": head.Owner + "/" + head.Repo,
			"branchName":              head.Branch,
		},
		"message": map[string]any{
			"headline": fmt.Sprintf("Apply suggestion to %s", t.Path),
		},
		"fileChanges": map[string]any{
			"additions": []map[string]string{{
				"path":     t.Path,
				"contents": base64.StdEncoding.EncodeToString([]byte(updated)),
			}},
		},
		"expectedHeadOid": head.OID,
	}
	payload, err := json.Marshal(input)
	if err != nil {
		return fmt.Errorf("encode commit input: %w", err)
	}
	// -f with a JSON string would be sent as a string, not an object; the raw
	// field form is what makes gh parse it as the input object.
	_, err = c.execRaw(ctx, "api", "graphql",
		"-f", "query="+m, "--raw-field", "input="+string(payload))
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "expected") {
			return fmt.Errorf(
				"the branch moved since this diff was loaded — press R and try again: %w", err)
		}
		return err
	}
	return nil
}

// ReplaceLines swaps the 1-based inclusive range [start, end] of src for
// replacement, or removes it when deletion is set.
//
// Exported for the tests to exercise without a network: the line arithmetic is
// where an off-by-one silently corrupts someone's file, which is the part worth
// pinning down.
func ReplaceLines(src string, start, end int, replacement string, deletion bool) (string, error) {
	if end <= 0 {
		return "", fmt.Errorf("suggestion has no target line")
	}
	if start <= 0 {
		start = end
	}
	if start > end {
		start, end = end, start
	}
	lines := strings.Split(src, "\n")
	// A file ending in a newline splits to a trailing empty element. Keeping it
	// out of the arithmetic and restoring it after is what preserves the final
	// newline; dropping it rewrites every such file with a spurious diff.
	trailing := ""
	if n := len(lines); n > 0 && lines[n-1] == "" {
		lines = lines[:n-1]
		trailing = "\n"
	}
	if end > len(lines) {
		return "", fmt.Errorf(
			"the suggestion targets line %d but %s has %d lines — the file changed",
			end, "the file", len(lines))
	}
	out := make([]string, 0, len(lines))
	out = append(out, lines[:start-1]...)
	if !deletion {
		out = append(out, strings.Split(replacement, "\n")...)
	}
	out = append(out, lines[end:]...)
	return strings.Join(out, "\n") + trailing, nil
}
