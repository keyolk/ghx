package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/keyolk/ghx/internal/config"
	"github.com/keyolk/ghx/internal/gh"
	"github.com/keyolk/ghx/internal/pr"
)

// Acting on a PR straight from the list is the point of these keys, but the
// cursor moves under the same fingers that press them — so the tests below pin
// down both that the actions reach the right PR and that none of them fire
// without an explicit confirmation.

func testApp(t *testing.T, rows []pr.Summary) *App {
	t.Helper()
	cfg := config.DefaultConfig()
	cfg.Sources = []config.SourceDef{{Name: "test", Query: "state:open"}}
	a := NewApp(cfg, DefaultKeymap(), gh.NewClient(0))
	a.width, a.height = 160, 40
	a.list.caches[0] = rows
	a.list.syncListItems()
	return a
}

func keyMsg(s string) tea.KeyMsg {
	if len(s) == 1 {
		return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
	}
	switch s {
	case "enter":
		return tea.KeyMsg{Type: tea.KeyEnter}
	case "esc":
		return tea.KeyMsg{Type: tea.KeyEsc}
	case "space":
		return tea.KeyMsg{Type: tea.KeySpace}
	}
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
}

var sampleRows = []pr.Summary{
	{Number: 101, Title: "first pr", Repo: "acme/one", State: "OPEN"},
	{Number: 202, Title: "second pr", Repo: "acme/two", State: "OPEN"},
}

// The action must apply to the row under the cursor, not the first row or the
// last one acted on.
func TestListActionTargetsSelectedRow(t *testing.T) {
	a := testApp(t, sampleRows)

	got, ok := a.currentTarget()
	if !ok {
		t.Fatal("no target for the first row")
	}
	if got.number != 101 || got.repo != "acme/one" {
		t.Errorf("target = #%d in %s, want #101 in acme/one", got.number, got.repo)
	}

	a.list.list.Select(1)
	got, ok = a.currentTarget()
	if !ok {
		t.Fatal("no target after moving the cursor")
	}
	if got.number != 202 || got.repo != "acme/two" {
		t.Errorf("target = #%d in %s, want #202 in acme/two", got.number, got.repo)
	}
}

// Approve and close must not act on the first keypress. This is the property
// that makes the keys safe to have in a list you scroll through.
func TestDestructiveListActionsAskFirst(t *testing.T) {
	for _, key := range []string{"a", "x"} {
		t.Run(key, func(t *testing.T) {
			a := testApp(t, sampleRows)
			cmd, handled := a.prActionKey(keyMsg(key))
			if !handled {
				t.Fatalf("%q was not handled in the list", key)
			}
			if cmd != nil {
				t.Errorf("%q returned a command instead of only opening a prompt", key)
			}
			if a.confirm == nil {
				t.Fatalf("%q did not raise a confirmation", key)
			}
			if a.confirm.target.number != 101 {
				t.Errorf("prompt targets #%d, want #101", a.confirm.target.number)
			}
		})
	}
}

// Only y proceeds; every other key must leave the PR untouched.
func TestConfirmOnlyProceedsOnYes(t *testing.T) {
	for _, key := range []string{"n", "esc", "j", "a", "space"} {
		t.Run("dismiss with "+key, func(t *testing.T) {
			a := testApp(t, sampleRows)
			a.prActionKey(keyMsg("a"))
			if a.confirm == nil {
				t.Fatal("no prompt to dismiss")
			}
			cmd := a.handleConfirmKey(keyMsg(key))
			if cmd != nil {
				t.Errorf("%q produced an action; only y may act", key)
			}
		})
	}

	a := testApp(t, sampleRows)
	a.prActionKey(keyMsg("a"))
	if cmd := a.handleConfirmKey(keyMsg("y")); cmd == nil {
		t.Error("y should run the approve")
	}
	if a.confirm != nil {
		t.Error("the prompt should close once answered")
	}
}

// An unknown key inside the prompt must not dismiss it either — the prompt has
// to stay until answered, or a stray keystroke silently cancels the intent.
func TestConfirmIgnoresUnrelatedKeys(t *testing.T) {
	a := testApp(t, sampleRows)
	a.prActionKey(keyMsg("a"))
	a.handleConfirmKey(keyMsg("k"))
	if a.confirm == nil {
		t.Error("an unrelated key should neither act nor dismiss the prompt")
	}
}

// A closed PR reopens rather than closes: offering "close" on something already
// closed would be a no-op the user has to discover by doing it.
func TestCloseKeyReopensClosedPR(t *testing.T) {
	a := testApp(t, []pr.Summary{
		{Number: 303, Title: "closed pr", Repo: "acme/one", State: "CLOSED"},
	})
	a.prActionKey(keyMsg("x"))
	if a.confirm == nil {
		t.Fatal("no prompt raised")
	}
	if a.confirm.kind != confirmReopen {
		t.Errorf("kind = %v, want confirmReopen for a closed PR", a.confirm.kind)
	}
	out := a.renderConfirm(120, 20)
	if !contains(out, "Reopen") {
		t.Errorf("prompt should say reopen: %s", out)
	}
}

// The prompt names the PR so a mis-selected row can be caught before acting.
func TestConfirmPromptNamesThePR(t *testing.T) {
	a := testApp(t, sampleRows)
	a.list.list.Select(1)
	a.prActionKey(keyMsg("a"))
	out := a.renderConfirm(140, 20)
	if !contains(out, "#202") || !contains(out, "acme/two") {
		t.Errorf("prompt must identify the PR: %s", out)
	}
	if !contains(out, "second pr") {
		t.Errorf("prompt should show the title: %s", out)
	}
}

// `l` is the vim right-alias that opens the preview pane; the label picker must
// not steal it. `o` folds a file inside the diff tab and must not be stolen
// either — both were real regressions when the actions were first wired up.
func TestActionKeysDoNotStealNavigationKeys(t *testing.T) {
	a := testApp(t, sampleRows)
	if _, handled := a.prActionKey(keyMsg("l")); handled {
		t.Error("lowercase l must stay the preview/right key")
	}
	if _, handled := a.prActionKey(keyMsg("L")); !handled {
		t.Error("L should open the label picker")
	}
}

func TestDiffTabKeepsFoldKey(t *testing.T) {
	a := testApp(t, sampleRows)
	a.state = viewPRDetail
	a.detail = newPRDetailModel(a.cfg, a.client, a.km, 101, "acme/one")
	a.detail.activeTab = tabDiff
	if err := a.detail.diff.setContent(threadsDiff, nil); err != nil {
		t.Fatalf("setContent: %v", err)
	}
	a.detail.diff.cursor = 0 // file header

	before := len(a.detail.diff.rows)
	a.handleKey(keyMsg("o"))
	if len(a.detail.diff.rows) >= before {
		t.Error("o should still fold the file in the diff tab, not open a browser")
	}
}

// The label picker stages edits and submits adds and removes together; nothing
// should be sent when the user only looked.
func TestLabelPickerSubmitsOnlyRealChanges(t *testing.T) {
	a := testApp(t, sampleRows)
	a.openLabelPicker(actionTarget{number: 101, repo: "acme/one"})
	p := a.labels
	p.loading = false
	p.all = []gh.RepoLabel{{Name: "bug"}, {Name: "docs"}, {Name: "keep"}}
	p.applied = map[string]bool{"keep": true}

	// Toggling something on and back off is not a change.
	p.pending["bug"] = true
	p.pending["bug"] = false
	if p.dirty() {
		t.Error("toggling a label on and off again should leave nothing to submit")
	}

	p.pending["bug"] = true   // add
	p.pending["keep"] = false // remove
	add, remove := p.diff()
	if strings.Join(add, ",") != "bug" {
		t.Errorf("add = %v, want [bug]", add)
	}
	if strings.Join(remove, ",") != "keep" {
		t.Errorf("remove = %v, want [keep]", remove)
	}

	// Submitting with nothing staged closes the picker without a request.
	p.pending = map[string]bool{}
	if cmd := a.submitLabels(); cmd != nil {
		t.Error("an unchanged picker should not send a request")
	}
	if a.labels != nil {
		t.Error("the picker should close on submit")
	}
}

// The picker shows what is already on the PR, so the user is not re-adding
// labels blind.
func TestLabelPickerMarksAppliedLabels(t *testing.T) {
	a := testApp(t, sampleRows)
	a.openLabelPicker(actionTarget{number: 101, repo: "acme/one"})
	a.labels.loading = false
	a.labels.all = []gh.RepoLabel{{Name: "bug", Description: "a defect"}, {Name: "docs"}}
	a.labels.applied = map[string]bool{"bug": true}

	out := a.renderLabelPicker(120, 24)
	if !contains(out, "bug") || !contains(out, "docs") {
		t.Errorf("picker should list the repo labels: %s", out)
	}
	if !contains(out, iconCheck) {
		t.Errorf("an applied label needs a mark: %s", out)
	}
	if !contains(out, "#101") {
		t.Errorf("picker should name the PR it edits: %s", out)
	}
}

func TestLabelPickerFilters(t *testing.T) {
	a := testApp(t, sampleRows)
	a.openLabelPicker(actionTarget{number: 101, repo: "acme/one"})
	p := a.labels
	p.loading = false
	p.all = []gh.RepoLabel{{Name: "bug"}, {Name: "documentation"}, {Name: "duplicate"}}

	p.query = "du"
	names := []string{}
	for _, l := range p.visible() {
		names = append(names, l.Name)
	}
	if strings.Join(names, ",") != "duplicate" {
		t.Errorf("filter 'du' matched %v, want [duplicate]", names)
	}

	// Filtering must not leave the cursor past the end of the shorter list.
	p.cursor = 2
	p.query = "zzz"
	if len(p.visible()) != 0 {
		t.Fatal("expected no matches")
	}
	if out := a.renderLabelPicker(120, 24); !contains(out, "no labels match") {
		t.Errorf("an empty filter result should say so: %s", out)
	}
}

// With no rows there is nothing to act on; the keys must report that rather
// than acting on a zero-valued target.
func TestListActionsWithEmptyList(t *testing.T) {
	a := testApp(t, nil)
	if _, ok := a.currentTarget(); ok {
		t.Error("an empty list should yield no target")
	}
	cmd, handled := a.prActionKey(keyMsg("a"))
	if !handled {
		t.Fatal("the key should still be consumed")
	}
	if cmd == nil {
		t.Error("expected an error message, got no command")
	}
	if a.confirm != nil {
		t.Error("no prompt should open without a target")
	}
}

// Requesting changes needs a reason, so it opens the composer — and the composer
// has to carry the PR, because the list has no detail model to fall back on.
func TestRequestChangesFromListCarriesPR(t *testing.T) {
	a := testApp(t, sampleRows)
	a.list.list.Select(1)
	cmd, handled := a.prActionKey(keyMsg("r"))
	if !handled || cmd == nil {
		t.Fatal("r should open the composer")
	}
	msg := cmd()
	open, ok := msg.(openComposerMsg)
	if !ok {
		t.Fatalf("got %T, want openComposerMsg", msg)
	}
	if open.target.review != "request-changes" {
		t.Errorf("review = %q, want request-changes", open.target.review)
	}
	if open.target.prNumber != 202 || open.target.repo != "acme/two" {
		t.Errorf("composer target = #%d in %s, want #202 in acme/two",
			open.target.prNumber, open.target.repo)
	}
}

func TestListMultiSelectToggleAndClear(t *testing.T) {
	a := testApp(t, sampleRows)

	a.list.update(keyMsg("space"))
	if len(a.list.selected) != 1 || !a.list.isSelected(sampleRows[0]) {
		t.Fatalf("space selected %d rows, want only the first", len(a.list.selected))
	}
	a.list.list.Select(1)
	a.list.update(keyMsg("space"))
	if len(a.list.selected) != 2 || !a.list.isSelected(sampleRows[1]) {
		t.Fatalf("second space selected %d rows, want both", len(a.list.selected))
	}

	a.list.update(keyMsg("esc"))
	if len(a.list.selected) != 0 {
		t.Errorf("esc left %d selections, want none", len(a.list.selected))
	}
}

func TestListToggleAllVisible(t *testing.T) {
	a := testApp(t, sampleRows)

	a.list.update(keyMsg("A"))
	if len(a.list.selected) != len(sampleRows) {
		t.Fatalf("A selected %d rows, want %d", len(a.list.selected), len(sampleRows))
	}
	a.list.update(keyMsg("A"))
	if len(a.list.selected) != 0 {
		t.Errorf("second A left %d selections, want none", len(a.list.selected))
	}
}

func TestBulkActionUsesExplicitCrossRepoSelection(t *testing.T) {
	a := testApp(t, sampleRows)
	a.list.update(keyMsg("A"))
	a.list.list.Select(1)

	cmd, handled := a.prActionKey(keyMsg("a"))
	if !handled || cmd != nil {
		t.Fatalf("bulk approve should only open confirmation; handled=%v cmd=%v", handled, cmd)
	}
	if a.confirm == nil || len(a.confirm.targets) != 2 {
		t.Fatalf("confirmation has %d targets, want 2", len(a.confirm.targets))
	}
	if a.confirm.targets[0].repo != "acme/one" || a.confirm.targets[1].repo != "acme/two" {
		t.Errorf("targets = %#v, want deterministic cross-repo order", a.confirm.targets)
	}
	out := a.renderConfirm(120, 24)
	if !contains(out, "Approve 2 selected PRs") || !contains(out, "#101 in acme/one") || !contains(out, "#202 in acme/two") {
		t.Errorf("bulk confirmation does not identify the selection: %s", out)
	}
}

func TestSingleExplicitSelectionUsesBulkResultPath(t *testing.T) {
	a := testApp(t, sampleRows)
	a.list.update(keyMsg("space"))
	a.prActionKey(keyMsg("a"))

	if a.confirm == nil || !a.confirm.bulk || len(a.confirm.targets) != 1 {
		t.Fatalf("single explicit selection must use bulk confirmation: %#v", a.confirm)
	}
}

func TestBulkCloseUsesEachPRState(t *testing.T) {
	rows := []pr.Summary{
		{Number: 101, Repo: "acme/one", State: "OPEN"},
		{Number: 202, Repo: "acme/two", State: "CLOSED"},
	}
	a := testApp(t, rows)
	a.list.update(keyMsg("A"))
	a.prActionKey(keyMsg("x"))

	if a.confirm == nil || a.confirm.kind != confirmToggleState {
		t.Fatalf("x confirmation kind = %v, want confirmToggleState", a.confirm.kind)
	}
	out := a.renderConfirm(120, 24)
	if !contains(out, "Open PRs will close; closed PRs will reopen") {
		t.Errorf("mixed-state confirmation must explain the action: %s", out)
	}
}

func TestBulkResultClearsOnlyCompletedSelections(t *testing.T) {
	a := testApp(t, sampleRows)
	a.list.update(keyMsg("A"))
	completed := selectionKey(sampleRows[0])

	model, _ := a.Update(bulkActionDoneMsg{
		label:     "approved 1 PR",
		completed: []string{completed},
		err:       fmt.Errorf("#202 failed"),
	})
	got := model.(*App)
	if got.list.isSelected(sampleRows[0]) {
		t.Error("completed PR stayed selected")
	}
	if !got.list.isSelected(sampleRows[1]) {
		t.Error("failed PR should remain selected for retry")
	}
}

func TestBulkResultInvalidatesAllSourceCaches(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Sources = []config.SourceDef{
		{Name: "one", Query: "state:open", Repo: "acme/one"},
		{Name: "two", Query: "state:open", Repo: "acme/two"},
	}
	a := NewApp(cfg, DefaultKeymap(), gh.NewClient(0))
	a.list.caches[0] = []pr.Summary{sampleRows[0]}
	a.list.caches[1] = []pr.Summary{sampleRows[1]}
	a.list.syncListItems()
	oldGenerations := append([]uint64(nil), a.list.generations...)

	_, cmd := a.Update(bulkActionDoneMsg{
		label:     "approved 1 PR",
		completed: []string{selectionKey(sampleRows[0])},
	})
	if cmd == nil {
		t.Fatal("bulk result should refresh the current source")
	}
	for i, cache := range a.list.caches {
		if cache != nil {
			t.Errorf("source %d cache was not invalidated", i)
		}
	}
	if !a.list.loadings[0] {
		t.Error("current source should begin reloading")
	}
	if a.list.loadings[1] {
		t.Error("inactive source should wait until it is visited")
	}
	if a.list.generations[0] <= oldGenerations[0] || a.list.generations[1] <= oldGenerations[1] {
		t.Errorf("generations = %v, want every source advanced past %v",
			a.list.generations, oldGenerations)
	}
}

func TestPRListIgnoresResponseFromInvalidatedGeneration(t *testing.T) {
	a := testApp(t, sampleRows)
	a.list.generations[0] = 7
	a.list.caches[0] = nil

	a.list.handlePRListMsg(prListMsg{
		sourceIdx:  0,
		generation: 6,
		prs:        []pr.Summary{{Number: 999, Repo: "acme/stale"}},
	})
	if a.list.caches[0] != nil {
		t.Errorf("stale response restored cache: %#v", a.list.caches[0])
	}
}

func TestBulkMergeUsesExplicitCrossRepoSelection(t *testing.T) {
	a := testApp(t, sampleRows)
	a.list.update(keyMsg("A"))

	cmd, handled := a.prActionKey(keyMsg("M"))
	if !handled || cmd != nil {
		t.Fatalf("bulk merge should only open confirmation; handled=%v cmd=%v", handled, cmd)
	}
	if a.confirm == nil || a.confirm.kind != confirmMerge || !a.confirm.bulk {
		t.Fatalf("merge confirmation = %#v, want explicit bulk merge", a.confirm)
	}
	if len(a.confirm.targets) != 2 {
		t.Fatalf("merge confirmation has %d targets, want 2", len(a.confirm.targets))
	}
	out := a.renderConfirm(120, 24)
	if !contains(out, "Squash-merge 2 selected PRs") ||
		!contains(out, "Each PR is merged independently using its repository credential") {
		t.Errorf("bulk merge confirmation is unclear: %s", out)
	}
	if cmd := a.handleConfirmKey(keyMsg("y")); cmd == nil {
		t.Error("explicit y should start the bulk merge command")
	}
}

func TestOpenSelectedPreservesAccountCredential(t *testing.T) {
	rows := []pr.Summary{{
		Number: 101, Repo: "acme/private", CredentialRepo: "work/selector", State: "OPEN",
	}}
	a := testApp(t, rows)
	if cmd := a.openSelected(); cmd == nil {
		t.Fatal("opening a selected PR should start detail loading")
	}
	if a.detail == nil || a.detail.credentialRepo != "work/selector" {
		t.Fatalf("detail credential = %#v, want work/selector", a.detail)
	}
	target, ok := a.currentTarget()
	if !ok || target.credentialRepo != "work/selector" {
		t.Errorf("detail action target = %#v, want work/selector", target)
	}
}

func TestListMergeWithoutMarksTargetsFocusedPR(t *testing.T) {
	a := testApp(t, sampleRows)
	a.list.list.Select(1)

	_, handled := a.prActionKey(keyMsg("M"))
	if !handled || a.confirm == nil {
		t.Fatal("M should open merge confirmation for the focused PR")
	}
	if a.confirm.bulk || len(a.confirm.targets) != 1 {
		t.Fatalf("focused merge confirmation = %#v, want one non-bulk target", a.confirm)
	}
	if got := a.confirm.target.repo; got != "acme/two" {
		t.Errorf("focused merge repo = %q, want acme/two", got)
	}
	if out := a.renderConfirm(120, 24); !contains(out, "Squash-merge #202 in acme/two") {
		t.Errorf("focused merge confirmation names wrong target: %s", out)
	}
}

func fakeActionApp(t *testing.T, rows []pr.Summary) (*App, string) {
	t.Helper()
	dir := t.TempDir()
	capture := filepath.Join(dir, "calls")
	writeTestExecutable(t, filepath.Join(dir, "git"), "#!/bin/sh\nexit 1\n")
	writeTestExecutable(t, filepath.Join(dir, "gh"), `#!/bin/sh
printf '%s\n' "$*" >> "$CAPTURE"
case "$*" in
  "label list"*) printf '%s\n' '[{"name":"common","description":"shared","color":"ffffff"}]' ;;
  *"--json labels"*) printf '%s\n' '{"labels":[]}' ;;
esac
`)
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("CAPTURE", capture)
	t.Setenv("GH_TOKEN", "test-token")
	return testApp(t, rows), capture
}

func writeTestExecutable(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o755); err != nil {
		t.Fatalf("write executable: %v", err)
	}
}

func capturedActionCalls(t *testing.T, path string) []string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		t.Fatalf("read calls: %v", err)
	}
	lines := strings.FieldsFunc(strings.TrimSpace(string(data)), func(r rune) bool { return r == '\n' })
	sort.Strings(lines)
	return lines
}

func runBulkKey(t *testing.T, a *App, key string) tea.Msg {
	t.Helper()
	a.Update(keyMsg("space"))
	a.Update(keyMsg("j"))
	a.Update(keyMsg("space"))
	if len(a.list.selected) != 2 {
		t.Fatalf("selected %d PRs, want two", len(a.list.selected))
	}
	a.Update(keyMsg(key))
	if a.confirm == nil || len(a.confirm.targets) != 2 {
		t.Fatalf("%s confirmation targets = %#v, want two", key, a.confirm)
	}
	_, cmd := a.Update(keyMsg("y"))
	if cmd == nil {
		t.Fatalf("%s confirmation produced no command", key)
	}
	return cmd()
}

func TestBulkActionsExecuteEverySelectedPRThroughAppUpdate(t *testing.T) {
	rows := []pr.Summary{
		{Number: 101, Repo: "acme/one", State: "OPEN", IsDraft: false},
		{Number: 202, Repo: "acme/two", State: "OPEN", IsDraft: true},
	}
	cases := []struct {
		name string
		key  string
		want []string
	}{
		{"approve", "a", []string{"pr review 101 --repo acme/one --approve", "pr review 202 --repo acme/two --approve"}},
		{"close", "x", []string{"pr close 101 --repo acme/one", "pr close 202 --repo acme/two"}},
		{"merge", "M", []string{"pr merge 101 --repo acme/one --squash", "pr merge 202 --repo acme/two --squash"}},
		{"ready-draft", "d", []string{"pr ready 101 --repo acme/one --undo", "pr ready 202 --repo acme/two"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a, capture := fakeActionApp(t, rows)
			msg := runBulkKey(t, a, tc.key)
			result, ok := msg.(bulkActionDoneMsg)
			if !ok || result.err != nil || len(result.completed) != 2 {
				t.Fatalf("result = %#v, want two successes", msg)
			}
			if got := capturedActionCalls(t, capture); strings.Join(got, "\n") != strings.Join(tc.want, "\n") {
				t.Fatalf("calls = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestBulkLabelsExecuteEverySelectedPR(t *testing.T) {
	a, capture := fakeActionApp(t, sampleRows)
	a.Update(keyMsg("A"))
	_, load := a.Update(keyMsg("L"))
	if load == nil || a.labels == nil || len(a.labels.targets) != 2 {
		t.Fatal("L should open a bulk label picker")
	}
	a.Update(load())
	if len(a.labels.all) != 1 || a.labels.all[0].Name != "common" {
		t.Fatalf("available labels = %#v", a.labels.all)
	}
	a.labels.pending["common"] = true
	_, apply := a.Update(keyMsg("enter"))
	result, ok := apply().(bulkActionDoneMsg)
	if !ok || result.err != nil || len(result.completed) != 2 {
		t.Fatalf("label result = %#v", result)
	}
	calls := capturedActionCalls(t, capture)
	var edits []string
	for _, call := range calls {
		if strings.HasPrefix(call, "pr edit ") {
			edits = append(edits, call)
		}
	}
	want := []string{
		"pr edit 101 --repo acme/one --add-label common",
		"pr edit 202 --repo acme/two --add-label common",
	}
	if strings.Join(edits, "\n") != strings.Join(want, "\n") {
		t.Fatalf("label edits = %q, want %q", edits, want)
	}
}

// fakeOpener puts the platform opener on PATH so tests observe the URLs that
// were actually launched, rather than trusting the target struct.
func fakeOpener(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	capture := filepath.Join(dir, "opened")
	script := "#!/bin/sh\nprintf '%s\\n' \"$1\" >> \"$OPEN_CAPTURE\"\n"
	for _, name := range []string{"open", "xdg-open"} {
		writeTestExecutable(t, filepath.Join(dir, name), script)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("OPEN_CAPTURE", capture)
	return capture
}

func openedURLs(t *testing.T, capture string) []string {
	t.Helper()
	data, err := os.ReadFile(capture)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		t.Fatal(err)
	}
	var out []string
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		if line != "" {
			out = append(out, line)
		}
	}
	sort.Strings(out)
	return out
}

var openRows = []pr.Summary{
	{Number: 101, Repo: "acme/one", State: "OPEN", URL: "https://github.com/acme/one/pull/101"},
	{Number: 202, Repo: "acme/two", State: "OPEN", URL: "https://github.com/acme/two/pull/202"},
}

// runCmds drains a command (including tea.Batch) and feeds each resulting
// message back through Update, which is how the real loop reaches openBrowser.
func runCmds(t *testing.T, a *App, cmd tea.Cmd) {
	t.Helper()
	if cmd == nil {
		return
	}
	msg := cmd()
	switch m := msg.(type) {
	case tea.BatchMsg:
		for _, sub := range m {
			runCmds(t, a, sub)
		}
	case nil:
	default:
		_, next := a.Update(msg)
		runCmds(t, a, next)
	}
}

// Opening was the one list action still bound to the focused row: `o` opened the
// cursor's PR no matter how many were marked. Every selected PR must open its
// own page.
func TestOpenActionOpensEverySelectedPR(t *testing.T) {
	capture := fakeOpener(t)
	a := testApp(t, openRows)
	a.Update(keyMsg("A"))
	if len(a.list.selected) != 2 {
		t.Fatalf("selected %d PRs, want two", len(a.list.selected))
	}

	_, cmd := a.Update(keyMsg("o"))
	runCmds(t, a, cmd)

	got := openedURLs(t, capture)
	want := []string{openRows[0].URL, openRows[1].URL}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("opened %q, want %q", got, want)
	}
}

// With nothing marked, `o` still opens the focused PR — and only that one.
func TestOpenActionWithoutSelectionOpensFocusedPR(t *testing.T) {
	capture := fakeOpener(t)
	a := testApp(t, openRows)

	_, cmd := a.Update(keyMsg("o"))
	runCmds(t, a, cmd)

	if got, want := openedURLs(t, capture), []string{openRows[0].URL}; strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("opened %q, want %q", got, want)
	}
}

// The selection survives an open: unlike close or merge, reading a PR does not
// consume the mark that chose it, and it must not sit behind a confirmation.
func TestOpenKeepsSelectionAndSkipsConfirmation(t *testing.T) {
	fakeOpener(t)
	a := testApp(t, openRows)
	a.Update(keyMsg("A"))
	_, cmd := a.Update(keyMsg("o"))
	runCmds(t, a, cmd)

	if len(a.list.selected) != 2 {
		t.Errorf("selection = %d PRs after open, want both kept", len(a.list.selected))
	}
	if a.confirm != nil {
		t.Errorf("open raised a confirmation prompt: %#v", a.confirm)
	}
}

// The palette built its URL from the PR number alone, producing
// github.com/pull/N — a link to nothing. It now shares the `o` key's path.
func TestPaletteOpenUsesTheRowsRealURL(t *testing.T) {
	capture := fakeOpener(t)
	a := testApp(t, openRows)

	runCmds(t, a, a.runPalette("open"))

	if got, want := openedURLs(t, capture), []string{openRows[0].URL}; strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("palette opened %q, want %q", got, want)
	}
}
