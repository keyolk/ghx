package gh

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// Admin API wrappers. These go through `gh api` against the REST endpoints,
// because gh has no first-class subcommands for collaborators, branch
// protection, or webhooks.

// Collaborator is a user with access to the repository.
type Collaborator struct {
	Login      string `json:"login"`
	RoleName   string `json:"role_name"`
	Permission struct {
		Admin    bool `json:"admin"`
		Maintain bool `json:"maintain"`
		Push     bool `json:"push"`
		Triage   bool `json:"triage"`
		Pull     bool `json:"pull"`
	} `json:"permissions"`

	// Direct is true when the user was granted access on the repository itself
	// rather than through a team. The collaborators endpoint flattens both into
	// one list of people and says nothing about which is which — on a repo whose
	// access is all team-based that reads as a hundred and fifty individual
	// grants, none of which can be revoked from here. Filled in by
	// ListCollaborators from the affiliation=direct list.
	Direct bool `json:"-"`
}

// ListCollaborators returns the repository's collaborators.
//
// Two calls rather than one: affiliation=all is the full roster with team
// members flattened in, and affiliation=direct is the subset granted on the
// repository itself. The difference is what tells a real collaborator apart
// from someone who is only here because of a team — a distinction the API
// exposes nowhere in the row itself, and the one that decides whether removing
// them from this repo can work at all.
//
// A failure on the direct call is not fatal: the roster is still the truth
// about who has access, and losing the marker is better than showing nothing.
func (c *Client) ListCollaborators(ctx context.Context) ([]Collaborator, error) {
	out, err := c.execRaw(ctx, "api", "--paginate",
		fmt.Sprintf("repos/%s/collaborators?affiliation=all&per_page=100", c.repoPath()))
	if err != nil {
		return nil, err
	}
	all, err := decodeJSON[[]Collaborator](out)
	if err != nil {
		return nil, err
	}
	directOut, err := c.execRaw(ctx, "api", "--paginate",
		fmt.Sprintf("repos/%s/collaborators?affiliation=direct&per_page=100", c.repoPath()))
	if err != nil {
		return all, nil
	}
	direct, err := decodeJSON[[]Collaborator](directOut)
	if err != nil {
		return all, nil
	}
	isDirect := make(map[string]bool, len(direct))
	for _, d := range direct {
		isDirect[strings.ToLower(d.Login)] = true
	}
	for i := range all {
		all[i].Direct = isDirect[strings.ToLower(all[i].Login)]
	}
	return all, nil
}

// Team is a team with access to the repository.
type Team struct {
	Name        string `json:"name"`
	Slug        string `json:"slug"`
	Description string `json:"description"`
	Privacy     string `json:"privacy"`
	Permission  string `json:"permission"`
	Parent      *struct {
		Slug string `json:"slug"`
	} `json:"parent"`
}

// ListTeams returns the teams granted access to the repository.
//
// This is the structure the collaborators endpoint throws away. On an org repo
// the roster is mostly team-derived, so without this the panel can say who has
// access but not why, and not what to change to alter it.
func (c *Client) ListTeams(ctx context.Context) ([]Team, error) {
	out, err := c.execRaw(ctx, "api", "--paginate",
		fmt.Sprintf("repos/%s/teams?per_page=100", c.repoPath()))
	if err != nil {
		return nil, err
	}
	return decodeJSON[[]Team](out)
}

// TeamMember is one member of a team.
type TeamMember struct {
	Login string `json:"login"`
	// Role is "member" or "maintainer"; it comes from the membership endpoint,
	// not the member list, so it is only set when asked for separately.
	Role string `json:"-"`
}

// ListTeamMembers returns a team's members. org is the organization the team
// belongs to, which is the owner half of the repository slug.
func (c *Client) ListTeamMembers(ctx context.Context, org, slug string) ([]TeamMember, error) {
	out, err := c.execRaw(ctx, "api", "--paginate",
		fmt.Sprintf("orgs/%s/teams/%s/members?per_page=100", org, slug))
	if err != nil {
		return nil, err
	}
	return decodeJSON[[]TeamMember](out)
}

// AddCollaborator invites a user with the given permission level.
// permission is one of: pull, triage, push, maintain, admin.
func (c *Client) AddCollaborator(ctx context.Context, user, permission string) error {
	endpoint := fmt.Sprintf("repos/%s/collaborators/%s", c.repoPath(), user)
	args := []string{"api", "--method", "PUT", endpoint}
	if permission != "" {
		args = append(args, "-f", "permission="+permission)
	}
	_, err := c.execRaw(ctx, args...)
	return err
}

// RemoveCollaborator revokes a user's access.
func (c *Client) RemoveCollaborator(ctx context.Context, user string) error {
	_, err := c.execRaw(ctx, "api", "--method", "DELETE",
		fmt.Sprintf("repos/%s/collaborators/%s", c.repoPath(), user))
	return err
}

// BranchProtection holds the rules protecting a branch.
type BranchProtection struct {
	RequiredReviews *struct {
		RequiredCount       int      `json:"required_approving_review_count"`
		DismissStale        bool     `json:"dismiss_stale_reviews"`
		RequireCodeOwner    bool     `json:"require_code_owner_reviews"`
		DismissRestrictions []string `json:"dismissal_restrictions"`
	} `json:"required_pull_request_reviews"`
	RequiredStatusChecks *struct {
		Strict   bool     `json:"strict"`
		Contexts []string `json:"contexts"`
	} `json:"required_status_checks"`
	EnforceAdmins struct {
		Enabled bool `json:"enabled"`
	} `json:"enforce_admins"`
	Restrictions *struct {
		Users []string `json:"users"`
		Teams []string `json:"teams"`
	} `json:"restrictions"`
}

// GetBranchProtection returns the protection rules for a branch.
// Returns nil, nil if the branch is not protected.
func (c *Client) GetBranchProtection(ctx context.Context, branch string) (*BranchProtection, error) {
	out, err := c.execRaw(ctx, "api",
		fmt.Sprintf("repos/%s/branches/%s/protection", c.repoPath(), branch))
	if err != nil {
		if strings.Contains(err.Error(), "Branch not protected") {
			return nil, nil
		}
		return nil, err
	}
	return decodeJSON[*BranchProtection](out)
}

// DeleteBranchProtection removes all protection rules from a branch.
func (c *Client) DeleteBranchProtection(ctx context.Context, branch string) error {
	_, err := c.execRaw(ctx, "api", "--method", "DELETE",
		fmt.Sprintf("repos/%s/branches/%s/protection", c.repoPath(), branch))
	return err
}

// SetRequiredReviews sets the number of required approving reviews on a branch.
// This is the most common protection edit; the full PUT body is built here so
// the TUI does not need to assemble the complex nested JSON.
func (c *Client) SetRequiredReviews(ctx context.Context, branch string, count int, dismissStale, requireCodeOwner bool) error {
	endpoint := fmt.Sprintf("repos/%s/branches/%s/protection", c.repoPath(), branch)
	body := fmt.Sprintf(`{
		"required_status_checks": null,
		"enforce_admins": false,
		"required_pull_request_reviews": {
			"required_approving_review_count": %d,
			"dismiss_stale_reviews": %t,
			"require_code_owner_reviews": %t
		},
		"restrictions": null
	}`, count, dismissStale, requireCodeOwner)
	_, err := c.execRaw(ctx, "api", "--method", "PUT", endpoint,
		"-H", "Accept: application/vnd.github+json",
		"--input", "-",
		"--raw-field", body)
	return err
}

// Release is a GitHub release.
type Release struct {
	TagName      string    `json:"tagName"`
	Name         string    `json:"name"`
	IsDraft      bool      `json:"isDraft"`
	IsPrerelease bool      `json:"isPrerelease"`
	CreatedAt    time.Time `json:"createdAt"`
}

// ListReleases returns the repository's releases.
func (c *Client) ListReleases(ctx context.Context) ([]Release, error) {
	out, err := c.exec(ctx, "release", "list",
		"--json", "tagName,name,isDraft,isPrerelease,createdAt",
		"--limit", "50")
	if err != nil {
		return nil, err
	}
	return decodeJSON[[]Release](out)
}

// CreateRelease publishes a new release.
func (c *Client) CreateRelease(ctx context.Context, tag, title, notes string, prerelease bool) error {
	args := []string{"release", "create", tag, "--title", title, "--notes", notes}
	if prerelease {
		args = append(args, "--prerelease")
	}
	_, err := c.exec(ctx, args...)
	return err
}

// DeleteRelease removes a release (does not delete the tag).
func (c *Client) DeleteRelease(ctx context.Context, tag string) error {
	_, err := c.exec(ctx, "release", "delete", tag, "--yes")
	return err
}

// Branch is a repository branch.
type Branch struct {
	Name      string `json:"name"`
	Protected bool   `json:"protected"`
	Commit    struct {
		SHA string `json:"sha"`
	} `json:"commit"`
}

// ListBranches returns the repository's branches.
func (c *Client) ListBranches(ctx context.Context) ([]Branch, error) {
	out, err := c.execRaw(ctx, "api", "--paginate",
		fmt.Sprintf("repos/%s/branches?per_page=100", c.repoPath()))
	if err != nil {
		return nil, err
	}
	return decodeJSON[[]Branch](out)
}

// DeleteBranch removes a branch from the repository.
func (c *Client) DeleteBranch(ctx context.Context, name string) error {
	_, err := c.execRaw(ctx, "api", "--method", "DELETE",
		fmt.Sprintf("repos/%s/git/refs/heads/%s", c.repoPath(), name))
	return err
}

// Tag is a repository tag.
type Tag struct {
	Name   string `json:"name"`
	Commit struct {
		SHA string `json:"sha"`
	} `json:"commit"`
}

// ListTags returns the repository's tags.
func (c *Client) ListTags(ctx context.Context) ([]Tag, error) {
	out, err := c.execRaw(ctx, "api", "--paginate",
		fmt.Sprintf("repos/%s/tags?per_page=100", c.repoPath()))
	if err != nil {
		return nil, err
	}
	return decodeJSON[[]Tag](out)
}

// DeleteTag removes a tag reference from the repository.
func (c *Client) DeleteTag(ctx context.Context, name string) error {
	_, err := c.execRaw(ctx, "api", "--method", "DELETE",
		fmt.Sprintf("repos/%s/git/refs/tags/%s", c.repoPath(), name))
	return err
}

// Webhook is a repository webhook (read-only in this TUI).
type Webhook struct {
	ID     int      `json:"id"`
	URL    string   `json:"url"`
	Events []string `json:"events"`
	Active bool     `json:"active"`
	Config struct {
		URL         string `json:"url"`
		ContentType string `json:"content_type"`
	} `json:"config"`
}

// ListWebhooks returns the repository's webhooks.
func (c *Client) ListWebhooks(ctx context.Context) ([]Webhook, error) {
	out, err := c.execRaw(ctx, "api",
		fmt.Sprintf("repos/%s/hooks?per_page=100", c.repoPath()))
	if err != nil {
		return nil, err
	}
	return decodeJSON[[]Webhook](out)
}

// repoPath returns "owner/repo" from the client's configured repo, or resolves
// it via the API when not set.
func (c *Client) repoPath() string {
	if c.repo != "" {
		return c.repo
	}
	// Best-effort: resolve at call time. This is uncommon since admin/actions
	// subcommands always set --repo before reaching here.
	return "{owner}/{repo}"
}

// decodeJSON is a small generic helper so the admin wrappers stay one line each.
func decodeJSON[T any](data []byte) (T, error) {
	var v T
	if err := json.Unmarshal(data, &v); err != nil {
		return v, err
	}
	return v, nil
}
