package tui

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/keyolk/ghx/internal/gh"
)

// Review actions and the merge gate. Kept out of app.go so the dispatcher there
// stays readable; these are the paths that mutate a PR, so they live together.

func (a *App) submitReview(action, body string) tea.Cmd {
	if a.detail == nil {
		return nil
	}
	// Use the detail view's repo-scoped client: ghx runs outside any checkout,
	// so an unscoped call would try to infer the repo from the cwd and fail.
	n, client := a.detail.number, a.detail.client
	return func() tea.Msg {
		c, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		err := client.ReviewPR(c, n, action, body)
		return reviewPostedMsg{action: action, err: err}
	}
}

// postComment routes the composer's body to the right gh call: a reply, an
// inline comment, a review with a body, or a plain PR comment.
func (a *App) postComment(msg postCommentMsg) tea.Cmd {
	t, body := msg.target, msg.body

	// The target may name its own PR — that is how the list view composes a
	// review without opening the PR first. Otherwise fall back to the open one.
	n, client := t.prNumber, a.client
	if t.repo != "" {
		client = a.client.WithRepo(t.repo)
	}
	if n == 0 {
		if a.detail == nil {
			return errCmd(fmt.Errorf("no pull request to comment on"))
		}
		n, client = a.detail.number, a.detail.client
	}

	// A review action carrying a body (request-changes) is a review, not a comment.
	if t.review != "" {
		action := t.review
		return func() tea.Msg {
			c, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			if err := client.ReviewPR(c, n, action, body); err != nil {
				return commentPostedMsg{err: err}
			}
			return commentPostedMsg{}
		}
	}

	if t.issue {
		return func() tea.Msg {
			c, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			return commentPostedMsg{err: client.IssueComment(c, n, body)}
		}
	}

	// The REST endpoints for inline comments need owner and repo in the path.
	// Prefer what the caller supplied, then the detail view's, and only resolve
	// as a last resort — resolving shells out and fails outside a checkout.
	owner, repo := splitRepoSlug(t.repo)
	if owner == "" && a.detail != nil {
		owner, repo = a.detail.owner, a.detail.repo
	}
	return func() tea.Msg {
		c, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if owner == "" || repo == "" {
			var err error
			owner, repo, err = client.RepoSlug(c)
			if err != nil {
				return commentPostedMsg{err: err}
			}
		}
		if t.replyTo != "" {
			return commentPostedMsg{err: client.ReplyToThread(c, owner, repo, n, t.replyTo, body)}
		}
		return commentPostedMsg{err: client.PostInlineComment(c, owner, repo, n, gh.InlineComment{
			Path: t.path, Line: t.line, Side: t.side, StartLine: t.startLine, Body: body,
		})}
	}
}

// splitRepoSlug splits "owner/name"; both are empty when the slug is not set.
func splitRepoSlug(slug string) (owner, repo string) {
	o, r, ok := strings.Cut(slug, "/")
	if !ok || o == "" || r == "" {
		return "", ""
	}
	return o, r
}

// startMerge opens the irreversible-action confirmation. Authorization is left
// to GitHub using the credential selected for the PR's repository.
func (a *App) startMerge() tea.Cmd {
	if a.detail == nil {
		return nil
	}
	a.mergePrompt = &mergePrompt{strategy: "squash"}
	return nil
}

func (a *App) handleMergePromptKey(msg tea.KeyMsg) tea.Cmd {
	p := a.mergePrompt
	switch msg.String() {
	case "esc", "n", "q":
		a.mergePrompt = nil
		return nil
	case "s":
		p.strategy = "squash"
		return nil
	case "m":
		p.strategy = "merge"
		return nil
	case "b":
		p.strategy = "rebase"
		return nil
	case "y":
		n, strategy, client := a.detail.number, p.strategy, a.detail.client
		return func() tea.Msg {
			c, cancel := context.WithTimeout(context.Background(), 60*time.Second)
			defer cancel()
			return mergeResultMsg{err: client.Merge(c, n, strategy)}
		}
	}
	return nil
}

// openBrowser opens a URL with the platform opener.
func (a *App) openBrowser(url string) tea.Cmd {
	if url == "" {
		return nil
	}
	opener := "xdg-open"
	if runtime.GOOS == "darwin" {
		opener = "open"
	}
	return func() tea.Msg {
		c, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := exec.CommandContext(c, opener, url).Run(); err != nil {
			return errMsg{err: fmt.Errorf("open %s: %w", url, err)}
		}
		return toastMsg{text: "opened in browser"}
	}
}

// openEditor hands the draft to $EDITOR and reads the result back.
func (a *App) openEditor(draft string) tea.Cmd {
	f, err := os.CreateTemp("", "ghx-comment-*.md")
	if err != nil {
		return errCmd(err)
	}
	name := f.Name()
	if _, err := f.WriteString(draft); err != nil {
		f.Close()
		os.Remove(name)
		return errCmd(err)
	}
	f.Close()

	editor := a.cfg.EditorCommand()
	// The editor needs the real terminal, so hand it over via ExecProcess and
	// read the file back when it exits.
	parts := strings.Fields(editor)
	args := append(parts[1:], name)
	cmd := exec.Command(parts[0], args...)
	return tea.ExecProcess(cmd, func(err error) tea.Msg {
		defer os.Remove(name)
		if err != nil {
			return editorDoneMsg{err: err}
		}
		data, readErr := os.ReadFile(name)
		if readErr != nil {
			return editorDoneMsg{err: readErr}
		}
		return editorDoneMsg{body: string(data)}
	})
}
