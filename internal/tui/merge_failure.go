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

// mergeFailure is the modal listing every PR a merge — or the rebase offered
// to fix one — could not complete. A rebase failure uses the same box: it is
// the next step of the same attempt, and a toast would hide it the same way.
type mergeFailure struct {
	kind  confirmKind // confirmMerge or confirmRebase
	items []mergeFailureItem
	// done counts the PRs of a bulk action that did go through, so a partial
	// result is not read as nothing having happened.
	done int
}

type mergeFailureItem struct {
	pr     actionTarget
	reason string // short classification, e.g. "merge conflict"
	hint   string // what to do about it; may be empty
	detail string // gh's own message, prefix stripped
	// rebasable marks a failure a server-side rebase fixes: the branch is only
	// behind its base. A conflict is not offered — GitHub cannot resolve it
	// either, and the attempt would only fail a second time.
	rebasable bool
}

// newMergeFailureItem classifies err for the PR it came from.
func newMergeFailureItem(kind confirmKind, t actionTarget, err error) mergeFailureItem {
	reason, hint := classifyMergeError(kind, err)
	return mergeFailureItem{
		pr:        t,
		reason:    reason,
		hint:      hint,
		detail:    mergeErrorDetail(err),
		rebasable: kind == confirmMerge && reason == reasonBehind,
	}
}

const reasonBehind = "branch is behind its base"

// classifyMergeError turns gh's refusal into a headline. The strings come from
// gh's own blockedReason (DIRTY/BLOCKED/BEHIND) and from GitHub's GraphQL
// mergePullRequest errors; anything unrecognized still opens the box, just
// under a generic headline with gh's text below it.
func classifyMergeError(kind confirmKind, err error) (reason, hint string) {
	noun := "merge"
	if kind == confirmRebase {
		noun = "rebase"
	}
	if errors.Is(err, context.DeadlineExceeded) {
		// The request may have reached GitHub before the deadline, so this is
		// the one failure that does not mean "not merged".
		return "timed out", "the " + noun + " may still have gone through — refresh before retrying"
	}
	msg := strings.ToLower(err.Error())
	switch {
	case strings.Contains(msg, "cannot be cleanly created"),
		strings.Contains(msg, "merge conflict"):
		return "merge conflict", "check out the branch (co), resolve the conflict against the base, and push"
	case strings.Contains(msg, "not up to date with the base"):
		return reasonBehind, "press r to rebase it onto the base, then merge again once checks pass"
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
	return noun + " failed", ""
}

// mergeErrorDetail is gh's message without the "gh pr merge:" prefix the
// client wraps every failure in, and without gh's advice to add `--auto` or
// `--admin`: those are flags of a command line ghx does not expose.
func mergeErrorDetail(err error) string {
	s := strings.TrimSpace(err.Error())
	for _, prefix := range []string{"gh pr merge:", "gh pr update-branch:"} {
		if rest, ok := strings.CutPrefix(s, prefix); ok {
			s = strings.TrimSpace(rest)
		}
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
	case "r":
		// The box names the PRs and says what r does, so the key is the
		// consent; a second y/n prompt would only ask the same question again.
		targets := a.mergeFailure.rebaseTargets()
		if len(targets) == 0 {
			return nil
		}
		a.mergeFailure = nil
		a.beginPending(confirmRebase, targets)
		if len(targets) == 1 {
			return a.runConfirmed(confirmRebase, targets[0])
		}
		return a.runConfirmedMany(confirmRebase, targets)
	}
	// Everything else is swallowed: a stray key landing on the view behind
	// would act on a queue the user cannot see.
	return nil
}

// rebaseTargets are the failed PRs a server-side rebase can unblock.
func (f *mergeFailure) rebaseTargets() []actionTarget {
	var out []actionTarget
	for _, it := range f.items {
		if it.rebasable && it.pr.valid() {
			out = append(out, it.pr)
		}
	}
	return out
}

func (f *mergeFailure) noun() string {
	if f.kind == confirmRebase {
		return "rebase"
	}
	return "merge"
}

// toast is the footer line left after the box is dismissed.
func (f *mergeFailure) toast() string {
	if len(f.items) == 1 {
		it := f.items[0]
		return errorStyle.Render(f.noun()+" failed: ") + it.pr.label() + " — " + it.reason
	}
	return errorStyle.Render(fmt.Sprintf("%s failed for %d pull requests", f.noun(), len(f.items)))
}

func (a *App) renderMergeFailure(width, height int) string {
	f := a.mergeFailure
	w := min(width-4, 90)
	inner := max(w-4, 10)

	past := "merged"
	if f.kind == confirmRebase {
		past = "rebased"
	}
	var b strings.Builder
	if len(f.items) == 1 {
		b.WriteString(checkFailStyle.Render("Not "+past+": "+f.items[0].pr.label()) + "\n")
	} else {
		head := fmt.Sprintf("%d pull requests were not %s", len(f.items), past)
		if f.done > 0 {
			head += fmt.Sprintf(" (%d %s)", f.done, past)
		}
		b.WriteString(checkFailStyle.Render(head) + "\n")
	}
	for i, it := range f.items {
		b.WriteString("\n")
		if len(f.items) > 1 {
			b.WriteString(prNumberStyle.Render(it.pr.label()) + "  ")
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
	b.WriteString("\n" + f.hints())

	body := strings.TrimRight(b.String(), "\n")
	h := min(strings.Count(body, "\n")+3, max(height-1, 5))
	return borderedPane(f.noun()+" failed", body, w, h, colorError)
}

// hints offers r only when some failure is one a rebase fixes; advertising it
// on a conflict would promise an action that cannot help.
func (f *mergeFailure) hints() string {
	switch n := len(f.rebaseTargets()); {
	case n == 1:
		return fmtHints("r", "rebase onto base", "enter", "dismiss")
	case n > 1:
		return fmtHints("r", fmt.Sprintf("rebase %d onto base", n), "enter", "dismiss")
	}
	return fmtHints("enter", "dismiss")
}
