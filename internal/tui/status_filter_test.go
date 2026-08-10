package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/bubbles/list"
	"github.com/charmbracelet/lipgloss"

	"github.com/keyolk/ghx/internal/pr"
)

func TestPRStatusMatching(t *testing.T) {
	p := pr.Summary{
		State:                   "MERGED",
		ReviewDecision:          "APPROVED",
		ConversationsKnown:      true,
		UnresolvedConversations: 2,
	}
	for _, status := range []prStatus{statusMerged, statusApproved, statusUnresolved} {
		if !prHasStatus(p, status) {
			t.Errorf("expected PR to match %q", status)
		}
	}
	if prHasStatus(p, statusChangesRequested) {
		t.Error("approved PR should not match changes requested")
	}

	unknown := pr.Summary{UnresolvedConversations: 2}
	if prHasStatus(unknown, statusUnresolved) {
		t.Error("unknown conversation state must not be treated as unresolved")
	}
}

func TestStatusFiltersUseORAndCombineWithTextUsingAND(t *testing.T) {
	rows := []pr.Summary{
		{Number: 1, Title: "release alpha", Repo: "acme/one", State: "MERGED"},
		{Number: 2, Title: "release beta", Repo: "acme/two", State: "OPEN", ReviewDecision: "APPROVED"},
		{Number: 3, Title: "docs alpha", Repo: "acme/three", State: "OPEN", ReviewDecision: "CHANGES_REQUESTED"},
	}
	a := testApp(t, rows)
	a.list.statusFilters[statusMerged] = true
	a.list.statusFilters[statusApproved] = true
	a.list.applyQuery("release")

	visible := a.list.list.VisibleItems()
	if len(visible) != 2 {
		t.Fatalf("got %d visible PRs, want merged OR approved intersected with release", len(visible))
	}
	for _, item := range visible {
		if !strings.Contains(item.(prListItem).pr.Title, "release") {
			t.Errorf("text filter did not intersect status filter: %#v", item)
		}
	}
}

func TestStatusFilterPickerApplyCancelAndClear(t *testing.T) {
	a := testApp(t, sampleRows)
	a.openStatusFilter()
	if a.statusFilter == nil {
		t.Fatal("f should open the status picker")
	}
	a.handleStatusFilterKey(keyMsg("space"))
	a.handleStatusFilterKey(keyMsg("enter"))
	if !a.list.statusFilters[statusMerged] {
		t.Error("enter should apply the toggled merged filter")
	}

	a.openStatusFilter()
	a.handleStatusFilterKey(keyMsg("j"))
	a.handleStatusFilterKey(keyMsg("space"))
	a.handleStatusFilterKey(keyMsg("esc"))
	if a.list.statusFilters[statusApproved] {
		t.Error("esc should discard pending picker changes")
	}

	a.openStatusFilter()
	a.handleStatusFilterKey(keyMsg("c"))
	a.handleStatusFilterKey(keyMsg("enter"))
	if len(a.list.statusFilters) != 0 {
		t.Errorf("clear left %d active filters", len(a.list.statusFilters))
	}
}

func TestEscapeClearsTextThenStatusThenSelection(t *testing.T) {
	a := testApp(t, sampleRows)
	a.list.selected[selectionKey(sampleRows[0])] = sampleRows[0]
	a.list.statusFilters[statusApproved] = true
	a.list.query = "first"

	a.list.update(keyMsg("esc"))
	if a.list.query != "" || len(a.list.statusFilters) != 1 || len(a.list.selected) != 1 {
		t.Fatalf("first esc should clear only text: query=%q filters=%d selected=%d",
			a.list.query, len(a.list.statusFilters), len(a.list.selected))
	}
	a.list.update(keyMsg("esc"))
	if len(a.list.statusFilters) != 0 || len(a.list.selected) != 1 {
		t.Fatalf("second esc should clear only statuses: filters=%d selected=%d",
			len(a.list.statusFilters), len(a.list.selected))
	}
	a.list.update(keyMsg("esc"))
	if len(a.list.selected) != 0 {
		t.Errorf("third esc left %d selected PRs", len(a.list.selected))
	}
}

func TestPRStatusCellShowsCombinedFixedWidthFlags(t *testing.T) {
	forceColor(t)
	p := pr.Summary{
		Number:                  101,
		Title:                   "combined state",
		Repo:                    "acme/one",
		State:                   "MERGED",
		ReviewDecision:          "APPROVED",
		ConversationsKnown:      true,
		UnresolvedConversations: 1,
	}
	plain := prStatusMark(p)
	if plain != "M A · U" {
		t.Errorf("status mark = %q, want %q", plain, "M A · U")
	}
	if got := lipgloss.Width(prStatusCell(p)); got != 7 {
		t.Errorf("styled status cell width = %d, want 7", got)
	}

	items := []list.Item{prListItem{pr: p}}
	l := list.New(items, prListDelegate{}, 120, 10)
	initListBase(&l)
	var b strings.Builder
	prListDelegate{}.Render(&b, l, 0, items[0])
	if got := lipgloss.Width(b.String()); got != 120 {
		t.Errorf("status row spans %d cells, want 120", got)
	}
	if !strings.Contains(stripANSISeqs(b.String()), "M A · U") {
		t.Errorf("status row does not show combined flags: %q", b.String())
	}
}

func TestConversationStatusLabelDistinguishesUnknownResolvedAndUnresolved(t *testing.T) {
	if got := stripANSISeqs(conversationStatusLabel(pr.Summary{})); got != "unknown" {
		t.Errorf("unknown label = %q", got)
	}
	if got := stripANSISeqs(conversationStatusLabel(pr.Summary{ConversationsKnown: true})); got != "resolved" {
		t.Errorf("resolved label = %q", got)
	}
	if got := stripANSISeqs(conversationStatusLabel(pr.Summary{
		ConversationsKnown: true, UnresolvedConversations: 3,
	})); got != "3 unresolved" {
		t.Errorf("unresolved label = %q", got)
	}
}
