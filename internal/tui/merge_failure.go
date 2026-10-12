package tui

import (
	"context"
	"errors"
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

// A failed merge used to surface as a toast: one footer line, truncated to the
// terminal width, gone after four seconds. gh's reason for refusing ("the merge
// commit cannot be cleanly created") sits after a long "gh pr merge:" prefix,
// so the part that said *why* was the part that got cut — and a merge that did
// not happen looked much like one that did. A failure now opens a box that
// stays until it is dismissed and names the reason in words the user can act on.

// mergeFailure is the modal listing every PR a merge attempt could not merge.
type mergeFailure struct {
	items []mergeFailureItem
	// merged counts the PRs of a bulk merge that did go through, so a partial
	// result is not read as nothing having happened.
	merged int
}

type mergeFailureItem struct {
	target string // "#12 in owner/name"
	reason string // short classification, e.g. "merge conflict"
	hint   string // what to do about it; may be empty
	detail string // gh's own message, prefix stripped
}

// newMergeFailureItem classifies err for the PR named by target.
func newMergeFailureItem(target string, err error) mergeFailureItem {
	reason, hint := classifyMergeError(err)
	return mergeFailureItem{
		target: target,
		reason: reason,
		hint:   hint,
		detail: mergeErrorDetail(err),
	}
}

// classifyMergeError turns gh's refusal into a headline. The strings come from
// gh's own blockedReason (DIRTY/BLOCKED/BEHIND) and from GitHub's GraphQL
// mergePullRequest errors; anything unrecognized still opens the box, just
// under a generic headline with gh's text below it.
func classifyMergeError(err error) (reason, hint string) {
	if errors.Is(err, context.DeadlineExceeded) {
		// The request may have reached GitHub before the deadline, so this is
		// the one failure that does not mean "not merged".
		return "timed out", "the merge may still have gone through — refresh before retrying"
	}
	msg := strings.ToLower(err.Error())
	switch {
	case strings.Contains(msg, "cannot be cleanly created"),
		strings.Contains(msg, "merge conflict"):
		return "merge conflict", "check out the branch (co), merge the base into it, and push"
	case strings.Contains(msg, "not up to date with the base"):
		return "branch is behind its base", "update the branch with the base, then merge again"
	case strings.Contains(msg, "approving review"),
		strings.Contains(msg, "review is required"),
		strings.Contains(msg, "changes requested"):
		return "approval required", "branch protection wants an approving review first"
	case strings.Contains(msg, "status check"):
		return "required checks not passing", "wait for the required checks to pass"
	case strings.Contains(msg, "base branch policy prohibits"):
		return "blocked by branch protection", "a required review or check is still missing"
	case strings.Contains(msg, "head branch was modified"):
		return "branch changed during the merge", "reload the PR and merge again"
	case strings.Contains(msg, "draft"):
		return "pull request is a draft", "mark it ready for review (rd) first"
	case strings.Contains(msg, "already merged"):
		return "already merged", ""
	case strings.Contains(msg, "not mergeable"):
		return "not mergeable", ""
	}
	return "merge failed", ""
}

// mergeErrorDetail is gh's message without the "gh pr merge:" prefix the
// client wraps every failure in, and without gh's advice to add `--auto` or
// `--admin`: those are flags of a command line ghx does not expose.
func mergeErrorDetail(err error) string {
	s := strings.TrimSpace(err.Error())
	if rest, ok := strings.CutPrefix(s, "gh pr merge:"); ok {
		s = strings.TrimSpace(rest)
	}
	var keep []string
	for _, line := range strings.Split(s, "\n") {
		line = strings.TrimRight(line, " \t")
		if strings.Contains(line, "`--auto`") || strings.Contains(line, "`--admin`") {
			continue
		}
		// gh marks the refusal with its failure icon; the box already says it.
		line = strings.TrimPrefix(line, "X ")
		keep = append(keep, line)
	}
	return strings.TrimSpace(strings.Join(keep, "\n"))
}

func (a *App) handleMergeFailureKey(msg tea.KeyMsg) tea.Cmd {
	switch msg.String() {
	case "enter", "esc", "q", " ":
		a.mergeFailure = nil
	}
	// Everything else is swallowed: a stray key landing on the view behind
	// would act on a queue the user cannot see.
	return nil
}

// mergeFailureToast is the footer line left after the box is dismissed.
func (f *mergeFailure) toast() string {
	if len(f.items) == 1 {
		it := f.items[0]
		return errorStyle.Render("merge failed: ") + it.target + " — " + it.reason
	}
	return errorStyle.Render(fmt.Sprintf("merge failed for %d pull requests", len(f.items)))
}

func (a *App) renderMergeFailure(width, height int) string {
	f := a.mergeFailure
	w := min(width-4, 90)
	inner := max(w-4, 10)

	var b strings.Builder
	if len(f.items) == 1 {
		b.WriteString(checkFailStyle.Render("Not merged: "+f.items[0].target) + "\n")
	} else {
		head := fmt.Sprintf("%d pull requests were not merged", len(f.items))
		if f.merged > 0 {
			head += fmt.Sprintf(" (%d merged)", f.merged)
		}
		b.WriteString(checkFailStyle.Render(head) + "\n")
	}
	for i, it := range f.items {
		b.WriteString("\n")
		if len(f.items) > 1 {
			b.WriteString(prNumberStyle.Render(it.target) + "  ")
		}
		b.WriteString(errorStyle.Render("✗ "+it.reason) + "\n")
		if it.hint != "" {
			for _, l := range wrapText("→ "+it.hint, inner) {
				b.WriteString(l + "\n")
			}
		}
		for _, raw := range strings.Split(it.detail, "\n") {
			if strings.TrimSpace(raw) == "" {
				continue
			}
			// wrapText collapses whitespace, which would flatten the indent gh
			// uses to set its resolution command apart from the prose.
			indent := "  " + raw[:len(raw)-len(strings.TrimLeft(raw, " "))]
			for _, l := range wrapText(raw, max(inner-len(indent), 10)) {
				b.WriteString(dimStyle.Render(indent+l) + "\n")
			}
		}
		// A long bulk failure would push the hints off the box; the rows stay
		// selected, so what is cut here is still on screen behind it.
		if i == 4 && len(f.items) > 5 {
			b.WriteString("\n" + dimStyle.Render(fmt.Sprintf("… and %d more", len(f.items)-5)) + "\n")
			break
		}
	}
	b.WriteString("\n" + fmtHints("enter", "dismiss"))

	body := strings.TrimRight(b.String(), "\n")
	h := min(strings.Count(body, "\n")+3, max(height-1, 5))
	return borderedPane("merge failed", body, w, h, colorError)
}
