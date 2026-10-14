package gh

import (
	"context"
	"encoding/json"
	"fmt"
)

// Actions API wrappers. `gh run` and `gh workflow` cover most of it; the rest
// goes through `gh api` against the Actions REST endpoints.

// Run is a workflow run, repo-wide (not tied to a PR).
type Run struct {
	DatabaseID   int64  `json:"databaseId"`
	DisplayTitle string `json:"displayTitle"`
	Status       string `json:"status"`     // queued, in_progress, completed
	Conclusion   string `json:"conclusion"` // success, failure, cancelled, null
	Event        string `json:"event"`      // push, pull_request, schedule, ...
	HeadBranch   string `json:"headBranch"`
	WorkflowName string `json:"workflowName"`
	CreatedAt    string `json:"createdAt"`
	URL          string `json:"url"`
}

// ListRuns returns recent workflow runs for the repository.
func (c *Client) ListRuns(ctx context.Context, limit int) ([]Run, error) {
	if limit <= 0 {
		limit = 30
	}
	out, err := c.exec(ctx, "run", "list",
		"--json", "databaseId,displayTitle,status,conclusion,event,headBranch,workflowName,createdAt,url",
		"--limit", fmt.Sprintf("%d", limit))
	if err != nil {
		return nil, err
	}
	return decodeJSON[[]Run](out)
}

// RerunRun triggers a full rerun of a workflow run.
func (c *Client) RerunRun(ctx context.Context, runID string) error {
	_, err := c.exec(ctx, "run", "rerun", runID)
	return err
}

// RerunFailedJobs reruns only the failed jobs of a workflow run.
func (c *Client) RerunFailedJobs(ctx context.Context, runID string) error {
	_, err := c.exec(ctx, "run", "rerun", runID, "--failed")
	return err
}

// CancelRun cancels an in-progress workflow run.
func (c *Client) CancelRun(ctx context.Context, runID string) error {
	_, err := c.exec(ctx, "run", "cancel", runID)
	return err
}

// Workflow is a repository workflow file.
type Workflow struct {
	ID    int64  `json:"id"`
	Name  string `json:"name"`
	Path  string `json:"path"`
	State string `json:"state"` // active, disabled_manually, disabled_inactivity
}

// ListWorkflows returns the repository's workflow files.
func (c *Client) ListWorkflows(ctx context.Context) ([]Workflow, error) {
	out, err := c.execRaw(ctx, "api",
		fmt.Sprintf("repos/%s/actions/workflows?per_page=100", c.repoPath()))
	if err != nil {
		return nil, err
	}
	var v struct {
		Workflows []Workflow `json:"workflows"`
	}
	if err := json.Unmarshal(out, &v); err != nil {
		return nil, fmt.Errorf("decode workflows: %w", err)
	}
	return v.Workflows, nil
}

// EnableWorkflow re-enables a disabled workflow.
func (c *Client) EnableWorkflow(ctx context.Context, workflowID string) error {
	_, err := c.exec(ctx, "workflow", "enable", workflowID)
	return err
}

// DisableWorkflow disables an active workflow.
func (c *Client) DisableWorkflow(ctx context.Context, workflowID string) error {
	_, err := c.exec(ctx, "workflow", "disable", workflowID)
	return err
}

// RunDetail holds a single run's jobs for the detail view.
type RunJobDetail struct {
	DatabaseID  int64  `json:"databaseId"`
	Name        string `json:"name"`
	Status      string `json:"status"`
	Conclusion  string `json:"conclusion"`
	StartedAt   string `json:"startedAt"`
	CompletedAt string `json:"completedAt"`
	URL         string `json:"htmlUrl"`
}

// RunDetail returns a run's metadata and jobs.
type RunDetail struct {
	DatabaseID   int64          `json:"databaseId"`
	DisplayTitle string         `json:"displayTitle"`
	Status       string         `json:"status"`
	Conclusion   string         `json:"conclusion"`
	Event        string         `json:"event"`
	HeadBranch   string         `json:"headBranch"`
	Jobs         []RunJobDetail `json:"jobs"`
}

// ViewRun returns a run's details including its jobs.
func (c *Client) ViewRun(ctx context.Context, runID string) (*RunDetail, error) {
	out, err := c.exec(ctx, "run", "view", runID,
		"--json", "databaseId,displayTitle,status,conclusion,event,headBranch,jobs")
	if err != nil {
		return nil, err
	}
	return decodeJSON[*RunDetail](out)
}
