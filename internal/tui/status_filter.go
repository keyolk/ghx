package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

type prStatus string

const (
	statusDraft            prStatus = "draft"
	statusMerged           prStatus = "merged"
	statusApproved         prStatus = "approved"
	statusChangesRequested prStatus = "changes requested"
	statusUnresolved       prStatus = "unresolved conversations"
)

var prStatusOptions = []prStatus{
	statusDraft,
	statusMerged,
	statusApproved,
	statusChangesRequested,
	statusUnresolved,
}

type statusFilterPicker struct {
	cursor  int
	pending map[prStatus]bool
}

func newStatusFilterPicker(active map[prStatus]bool) *statusFilterPicker {
	pending := make(map[prStatus]bool, len(active))
	for status, enabled := range active {
		pending[status] = enabled
	}
	return &statusFilterPicker{pending: pending}
}

func matchesStatusFilters(p prSummary, active map[prStatus]bool) bool {
	if len(active) == 0 {
		return true
	}
	for status, enabled := range active {
		if enabled && prHasStatus(p, status) {
			return true
		}
	}
	return false
}

func prHasStatus(p prSummary, status prStatus) bool {
	switch status {
	case statusDraft:
		return p.IsDraft
	case statusMerged:
		return strings.EqualFold(p.State, "MERGED")
	case statusApproved:
		return p.ReviewDecision == "APPROVED"
	case statusChangesRequested:
		return p.ReviewDecision == "CHANGES_REQUESTED"
	case statusUnresolved:
		return p.ConversationsKnown && p.UnresolvedConversations > 0
	}
	return false
}

func activeStatusLabels(active map[prStatus]bool) []string {
	labels := make([]string, 0, len(active))
	for _, status := range prStatusOptions {
		if active[status] {
			labels = append(labels, string(status))
		}
	}
	return labels
}

func (a *App) openStatusFilter() tea.Cmd {
	if a.state != viewPRList || a.list == nil {
		return nil
	}
	a.statusFilter = newStatusFilterPicker(a.list.statusFilters)
	return nil
}

func (a *App) handleStatusFilterKey(msg tea.KeyMsg) tea.Cmd {
	picker := a.statusFilter
	if picker == nil {
		return nil
	}
	switch msg.String() {
	case "j", "down":
		picker.cursor = min(picker.cursor+1, len(prStatusOptions)-1)
	case "k", "up":
		picker.cursor = max(picker.cursor-1, 0)
	case " ", "tab":
		status := prStatusOptions[picker.cursor]
		if picker.pending[status] {
			delete(picker.pending, status)
		} else {
			picker.pending[status] = true
		}
	case "c":
		clear(picker.pending)
	case "enter":
		a.list.statusFilters = picker.pending
		a.list.syncListItems()
		a.statusFilter = nil
	case "esc", "q":
		a.statusFilter = nil
	}
	return nil
}

func (a *App) renderStatusFilter(width, height int) string {
	picker := a.statusFilter
	if picker == nil {
		return ""
	}
	var b strings.Builder
	b.WriteString(dimStyle.Render("Match any selected status; text search still applies.") + "\n\n")
	for i, status := range prStatusOptions {
		mark := " "
		if picker.pending[status] {
			mark = iconCheck
		}
		line := fmt.Sprintf("[%s] %s", mark, status)
		if i == picker.cursor {
			line = selectedRowStyle.Render(padCell(line, 34))
		} else if picker.pending[status] {
			line = checkPassStyle.Render(line)
		}
		b.WriteString(line + "\n")
	}
	b.WriteString("\n" + fmtHints("sp", "toggle", "enter", "apply", "c", "clear", "esc", "cancel"))
	// The box is sized from the option list rather than a literal: adding a
	// status used to leave the last one clipped off the bottom.
	//   header + blank + one row per option + blank + hints + two border rows
	h := len(prStatusOptions) + 6
	return decoratedPane("status filter", b.String(), min(width-4, 58), h, true)
}
