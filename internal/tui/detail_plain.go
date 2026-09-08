package tui

import (
	"fmt"
	"strings"
)

// Rendering for the plain (non-interactive) detail tabs: Overview, Files, and
// Commits. The tabs with their own cursor state live in detail_diff.go,
// detail_comments.go, and detail_checks.go.

func (d *prDetailModel) renderOverview(w, h int) string {
	if d.detail == nil {
		return dimStyle.Render("No detail loaded.")
	}
	dt := d.detail
	var b strings.Builder
	b.WriteString(prTitleStyle.Render(dt.Title) + "\n\n")
	b.WriteString(field("Author", prAuthorStyle.Render(dt.Author.Login)))
	state := dt.State
	if dt.IsDraft {
		state += prDraftStyle.Render(" (draft)")
	}
	b.WriteString(field("State", state))
	b.WriteString(field("Branch", fmt.Sprintf("%s → %s", dt.HeadRefName, dt.BaseRefName)))
	b.WriteString(field("Review", reviewDecisionLabel(dt.ReviewDecision)))
	b.WriteString(field("Merge", mergeStateLabel(dt.Mergeable, dt.MergeStateStatus)))
	b.WriteString(field("Changes", fmt.Sprintf("%s %s across %d files",
		diffAddStyle.Render(fmt.Sprintf("+%d", dt.Additions)),
		diffDelStyle.Render(fmt.Sprintf("-%d", dt.Deletions)),
		dt.ChangedFiles)))
	if len(dt.Labels) > 0 {
		names := make([]string, 0, len(dt.Labels))
		for _, l := range dt.Labels {
			names = append(names, l.Name)
		}
		b.WriteString(field("Labels", strings.Join(names, ", ")))
	}
	if len(dt.ReviewRequests) > 0 {
		names := make([]string, 0, len(dt.ReviewRequests))
		for _, u := range dt.ReviewRequests {
			names = append(names, u.Login)
		}
		b.WriteString(field("Requested", strings.Join(names, ", ")))
	}
	if len(dt.Reviews) > 0 {
		b.WriteString("\n" + titleStyle.Render("Reviews") + "\n")
		for _, r := range dt.Reviews {
			b.WriteString(fmt.Sprintf("  %s %s\n",
				reviewStateLabel(r.State), prAuthorStyle.Render(r.Author.Login)))
		}
	}
	b.WriteString("\n" + titleStyle.Render("Description") + "\n")
	body := strings.TrimSpace(dt.Body)
	if body == "" {
		b.WriteString(dimStyle.Render("  (no description)"))
	} else {
		// A PR description is markdown too, and the ones worth reading closely —
		// a migration plan, a before/after — are exactly the ones a prose wrap
		// destroys.
		for _, seg := range renderCommentBody(body, max(w-2, 20)) {
			if seg == "" {
				b.WriteString("\n")
				continue
			}
			b.WriteString("  " + seg + "\n")
		}
	}
	return scrollBlock(b.String(), d.overviewOff, h)
}

func field(label, value string) string {
	return fmt.Sprintf("%s %s\n", dimStyle.Render(fmt.Sprintf("%-10s", label+":")), value)
}

func mergeStateLabel(mergeable, state string) string {
	switch mergeable {
	case "CONFLICTING":
		return checkFailStyle.Render("conflicting")
	case "MERGEABLE":
		switch state {
		case "CLEAN":
			return checkPassStyle.Render("clean")
		case "BLOCKED":
			return checkPendingStyle.Render("blocked")
		case "BEHIND":
			return checkPendingStyle.Render("behind base")
		}
		return dimStyle.Render(strings.ToLower(state))
	}
	return dimStyle.Render(strings.ToLower(mergeable))
}

func reviewStateLabel(s string) string {
	switch s {
	case "APPROVED":
		return checkPassStyle.Render(iconCheck)
	case "CHANGES_REQUESTED":
		return checkFailStyle.Render(iconFail)
	case "COMMENTED":
		return threadStyle.Render(iconComment)
	}
	return dimStyle.Render("·")
}

func (d *prDetailModel) renderFiles(w, h int) string {
	if d.detail == nil || len(d.detail.Files) == 0 {
		return dimStyle.Render("No changed files.")
	}
	files := d.detail.Files
	// Keep the cursor on screen.
	if d.filesCursor < d.filesOff {
		d.filesOff = d.filesCursor
	}
	if d.filesCursor >= d.filesOff+h {
		d.filesOff = d.filesCursor - h + 1
	}
	d.filesOff = clamp(d.filesOff, 0, max(len(files)-h, 0))

	var b strings.Builder
	end := min(d.filesOff+h, len(files))
	for i := d.filesOff; i < end; i++ {
		f := files[i]
		addCell := fmt.Sprintf("+%-4d", f.Additions)
		delCell := fmt.Sprintf("-%-4d", f.Deletions)
		pathW := max(w-16, 10)
		p := f.Path
		if lipglossWidth(p) > pathW {
			// Truncate from the left: the filename matters more than the root.
			p = "…" + p[len(p)-pathW+1:]
		}
		p = padCell(p, pathW)

		if i == d.filesCursor {
			// Themed whole, from plain cells: wrapping a background around the
			// coloured +/- counts would end the highlight at their reset codes.
			plain := addCell + " " + delCell + " " + p
			plain, _ = truncateExact(plain, w)
			if pad := w - lipglossWidth(plain); pad > 0 {
				plain += strings.Repeat(" ", pad)
			}
			b.WriteString(selectedRowStyle.Render(plain))
		} else {
			b.WriteString(diffAddStyle.Render(addCell) + " " +
				diffDelStyle.Render(delCell) + " " + p)
		}
		if i < end-1 {
			b.WriteByte('\n')
		}
	}
	return b.String()
}

func (d *prDetailModel) renderCommits(w, h int) string {
	if d.detail == nil || len(d.detail.Commits) == 0 {
		return dimStyle.Render("No commits.")
	}
	var b strings.Builder
	for _, c := range d.detail.Commits {
		sha := c.OID
		if len(sha) > 7 {
			sha = sha[:7]
		}
		b.WriteString(fmt.Sprintf("%s %s %s\n",
			prNumberStyle.Render(sha),
			dimStyle.Render(c.AuthoredDate.Format("01-02")),
			c.MessageHeadline))
	}
	return scrollBlock(b.String(), d.commitsOff, h)
}

// scrollBlock renders a plain text block at a scroll offset.
func scrollBlock(s string, offset, height int) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	offset = clamp(offset, 0, max(len(lines)-1, 0))
	end := min(offset+height, len(lines))
	return strings.Join(lines[offset:end], "\n")
}

