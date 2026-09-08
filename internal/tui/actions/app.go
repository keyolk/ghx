// Package actions is the GitHub Actions TUI, launched via `ghx actions`.
// It is a separate tea.Model from the PR review app: it manages workflow runs
// and workflow files repo-wide, not per-PR.
package actions

import (
	"context"
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/keyolk/ghx/internal/gh"
	"github.com/keyolk/ghx/internal/tui"
)

// tab identifies the active section.
type tab int

const (
	tabRuns tab = iota
	tabWorkflows
)

var tabNames = []string{"Runs", "Workflows"}

// App is the actions TUI root model.
type App struct {
	client *gh.Client
	repo   string

	active tab
	width  int
	height int

	// data
	runs      []gh.Run
	workflows []gh.Workflow

	// log viewer
	logView string
	logBusy bool
	logOff  int

	// filter
	failedOnly bool

	// state
	loading bool
	err     error
	toast   string
	toastAt time.Time

	// list scrolls inside its own rows; see tui.ListPane.
	pane tui.ListPane

	// query filters the active list; searching is true while it is being typed.
	query     string
	searching bool
}

// NewApp constructs the actions TUI.
func NewApp(client *gh.Client, repo string) *App {
	return &App{client: client, repo: repo, active: tabRuns}
}

// Init loads the runs list.
func (a *App) Init() tea.Cmd {
	return a.loadRuns()
}

func (a *App) loadRuns() tea.Cmd {
	a.loading = true
	a.err = nil
	client, ctx := a.client, context.Background()
	return func() tea.Msg {
		runs, err := client.ListRuns(ctx, 50)
		return runsMsg{runs: runs, err: err}
	}
}

func (a *App) loadWorkflows() tea.Cmd {
	a.loading = true
	a.err = nil
	client, ctx := a.client, context.Background()
	return func() tea.Msg {
		wf, err := client.ListWorkflows(ctx)
		return workflowsMsg{workflows: wf, err: err}
	}
}

// runsMsg carries the runs list.
type runsMsg struct {
	runs []gh.Run
	err  error
}

// workflowsMsg carries the workflows list.
type workflowsMsg struct {
	workflows []gh.Workflow
	err       error
}

// logMsg carries fetched run logs.
type logMsg struct {
	logs string
	err  error
}

// toastMsg surfaces a transient message.
type toastMsg struct{ text string }

func (a *App) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		a.width, a.height = msg.Width, msg.Height
		return a, nil

	case tea.KeyMsg:
		return a, a.handleKey(msg)

	case runsMsg:
		a.loading = false
		if msg.err != nil {
			a.err = msg.err
			return a, nil
		}
		a.runs = msg.runs
		a.pane.Reset()
		return a, nil

	case workflowsMsg:
		a.loading = false
		if msg.err != nil {
			a.err = msg.err
			return a, nil
		}
		a.workflows = msg.workflows
		a.pane.Reset()
		return a, nil

	case logMsg:
		a.logBusy = false
		if msg.err != nil {
			a.toast = "error: " + msg.err.Error()
			a.toastAt = time.Now()
			return a, nil
		}
		a.logView = msg.logs
		a.logOff = 0
		return a, nil

	case toastMsg:
		a.toast = msg.text
		a.toastAt = time.Now()
		return a, nil
	}
	return a, nil
}

func (a *App) handleKey(msg tea.KeyMsg) tea.Cmd {
	// ctrl+c quits from anywhere, ahead of the log viewer and the search prompt.
	if msg.Type == tea.KeyCtrlC {
		return tea.Quit
	}

	// Under a Korean input source the shortcut keys arrive as jamo (`q` -> `ㅂ`).
	// Rewrite them to the Latin key at the same physical position so shortcuts
	// fire without switching the input source back. Skipped while the search
	// prompt owns keys, where the jamo is the intended input.
	if !a.searching {
		msg = tui.NormalizeCJKKey(msg)
	}

	// log viewer mode: j/k scrolls, esc exits
	if a.logView != "" {
		switch msg.String() {
		case "esc":
			a.logView = ""
			return nil
		case "j", "down":
			a.logOff++
			return nil
		case "k", "up":
			a.logOff = max(a.logOff-1, 0)
			return nil
		case "q":
			return tea.Quit
		}
		return nil
	}

	// The search prompt owns the keyboard while it is open, so a query can
	// contain j, k, q, f, and the digits without triggering navigation.
	if a.searching {
		return a.handleSearchKey(msg)
	}

	switch msg.String() {
	case "q":
		return tea.Quit
	case "/":
		a.searching = true
		return nil
	case "esc":
		if a.query != "" {
			a.query = ""
			a.pane.Reset()
		}
		return nil
	case "1":
		a.active = tabRuns
		a.query = ""
		a.pane.Reset()
		if a.runs == nil {
			return a.loadRuns()
		}
		return nil
	case "2":
		a.active = tabWorkflows
		a.query = ""
		a.pane.Reset()
		if a.workflows == nil {
			return a.loadWorkflows()
		}
		return nil
	case "j", "down":
		a.pane.Move(1, a.itemCount())
		return nil
	case "k", "up":
		a.pane.Move(-1, a.itemCount())
		return nil
	case "ctrl+d", "pgdown":
		a.pane.Move(a.listRows()/2, a.itemCount())
		return nil
	case "ctrl+u", "pgup":
		a.pane.Move(-a.listRows()/2, a.itemCount())
		return nil
	case "g":
		a.pane.Top()
		return nil
	case "G":
		a.pane.Bottom(a.itemCount())
		return nil
	case "f":
		// Toggle failed-only filter (runs tab)
		a.failedOnly = !a.failedOnly
		a.pane.Reset()
		return nil
	case "enter":
		return a.onEnter()
	case "r":
		return a.rerun(false)
	case "R":
		return a.rerun(true)
	case "c":
		return a.cancelRun()
	case "e":
		return a.toggleWorkflow(true)
	case "d":
		if a.active == tabWorkflows {
			return a.toggleWorkflow(false)
		}
		return nil
	}
	return nil
}

func (a *App) onEnter() tea.Cmd {
	if a.active == tabRuns && a.logView == "" {
		return a.fetchLogs()
	}
	return nil
}

func (a *App) fetchLogs() tea.Cmd {
	items := a.visibleRuns()
	if a.pane.Cursor >= len(items) {
		return nil
	}
	run := items[a.pane.Cursor]
	a.logBusy = true
	client, ctx := a.client, context.Background()
	runID := fmt.Sprint(run.DatabaseID)
	return func() tea.Msg {
		logs, err := client.RunLogs(ctx, runID, "")
		return logMsg{logs: logs, err: err}
	}
}

func (a *App) rerun(failedOnly bool) tea.Cmd {
	if a.active != tabRuns {
		return nil
	}
	items := a.visibleRuns()
	if a.pane.Cursor >= len(items) {
		return nil
	}
	run := items[a.pane.Cursor]
	client, ctx := a.client, context.Background()
	runID := fmt.Sprint(run.DatabaseID)
	verb := "rerun"
	if failedOnly {
		verb = "rerun failed"
	}
	return func() tea.Msg {
		var err error
		if failedOnly {
			err = client.RerunFailedJobs(ctx, runID)
		} else {
			err = client.RerunRun(ctx, runID)
		}
		if err != nil {
			return toastMsg{text: "error: " + err.Error()}
		}
		return toastMsg{text: verb + " #" + runID}
	}
}

func (a *App) cancelRun() tea.Cmd {
	if a.active != tabRuns {
		return nil
	}
	items := a.visibleRuns()
	if a.pane.Cursor >= len(items) {
		return nil
	}
	run := items[a.pane.Cursor]
	client, ctx := a.client, context.Background()
	runID := fmt.Sprint(run.DatabaseID)
	return func() tea.Msg {
		err := client.CancelRun(ctx, runID)
		if err != nil {
			return toastMsg{text: "error: " + err.Error()}
		}
		return toastMsg{text: "cancelled #" + runID}
	}
}

func (a *App) toggleWorkflow(enable bool) tea.Cmd {
	if a.active != tabWorkflows {
		return nil
	}
	items := a.visibleWorkflows()
	if a.pane.Cursor >= len(items) {
		return nil
	}
	wf := items[a.pane.Cursor]
	client, ctx := a.client, context.Background()
	wfID := fmt.Sprint(wf.ID)
	verb := "disabled"
	if enable {
		verb = "enabled"
	}
	return func() tea.Msg {
		var err error
		if enable {
			err = client.EnableWorkflow(ctx, wfID)
		} else {
			err = client.DisableWorkflow(ctx, wfID)
		}
		if err != nil {
			return toastMsg{text: "error: " + err.Error()}
		}
		return toastMsg{text: verb + " " + wf.Name}
	}
}

// handleSearchKey edits the query. The filter applies as it is typed so the
// list narrows under the cursor rather than after a commit.
func (a *App) handleSearchKey(msg tea.KeyMsg) tea.Cmd {
	switch msg.Type {
	case tea.KeyEsc:
		a.searching = false
		a.query = ""
		a.pane.Reset()
	case tea.KeyEnter:
		a.searching = false
	case tea.KeyBackspace:
		if r := []rune(a.query); len(r) > 0 {
			a.query = string(r[:len(r)-1])
			a.pane.Reset()
		}
	case tea.KeyRunes:
		a.query += string(msg.Runes)
		a.pane.Reset()
	case tea.KeySpace:
		a.query += " "
		a.pane.Reset()
	}
	return nil
}

// visibleRuns applies the failed-only filter and the text query, in that order:
// f narrows to what is broken, / then finds one of them.
//
// The cursor indexes this slice, never a.runs — indexing the source would act
// on whichever run happened to sit at that position before filtering, which for
// r and c means rerunning or cancelling the wrong one.
func (a *App) visibleRuns() []gh.Run {
	base := a.runs
	if a.failedOnly {
		base = make([]gh.Run, 0, len(a.runs))
		for _, r := range a.runs {
			if r.Conclusion == "failure" {
				base = append(base, r)
			}
		}
	}
	idx := tui.FilterRows(a.query, len(base), func(i int) string {
		r := base[i]
		return r.WorkflowName + " " + r.Conclusion + " " + r.Status + " " +
			r.Event + " " + r.HeadBranch
	})
	out := make([]gh.Run, 0, len(idx))
	for _, i := range idx {
		out = append(out, base[i])
	}
	return out
}

// visibleWorkflows applies the text query.
func (a *App) visibleWorkflows() []gh.Workflow {
	idx := tui.FilterRows(a.query, len(a.workflows), func(i int) string {
		w := a.workflows[i]
		return w.Name + " " + w.Path + " " + w.State
	})
	out := make([]gh.Workflow, 0, len(idx))
	for _, i := range idx {
		out = append(out, a.workflows[i])
	}
	return out
}

func (a *App) itemCount() int {
	switch a.active {
	case tabRuns:
		return len(a.visibleRuns())
	case tabWorkflows:
		return len(a.visibleWorkflows())
	}
	return 0
}

// contentRows is how many rows the body may occupy: the terminal minus the
// title, the tab strip, and the footer. Sizing to a.height instead is what
// pushed the title and the tab strip off the top of an overflowing frame —
// bubbletea keeps the LAST height lines, so the rows it drops are the ones the
// user needs to navigate with.
func (a *App) contentRows() int { return max(a.height-3, 1) }

// listRows is the height available to list rows themselves.
func (a *App) listRows() int { return a.contentRows() }

func (a *App) View() string {
	if a.width == 0 || a.height == 0 {
		return "Loading ghx actions…"
	}
	if a.width < 40 || a.height < 8 {
		return fmt.Sprintf("Terminal too small (%dx%d).\nghx actions needs at least 40x8.", a.width, a.height)
	}

	// log viewer takes over the whole screen
	if a.logView != "" {
		return a.renderLogs()
	}

	title := tui.TitleStyle.Render(" ghx actions ") + " " + tui.DimStyle.Render(a.repo)
	header := a.renderHeader()
	content := tui.FitRows(a.renderContent(), a.contentRows())
	footer := a.footer()

	return strings.Join([]string{title, header, content, footer}, "\n")
}

// renderHeader is the tab strip, or the search prompt while one is being typed.
// The prompt replaces the strip rather than adding a row: an extra row would
// resize the list mid-search, and the tabs are unreachable while the query owns
// the keyboard anyway.
func (a *App) renderHeader() string {
	if a.searching {
		return tui.RenderSearchBar(a.query, a.width)
	}
	tabs := make([]tui.TabLabel, len(tabNames))
	for i, name := range tabNames {
		tabs[i] = tui.TabLabel{Name: name, Active: tab(i) == a.active}
	}
	strip := tui.RenderTabStrip(tabs, a.width)
	// Both markers ride after the strip; they are state toggles, not tabs.
	if a.failedOnly && a.active == tabRuns {
		strip += " " + tui.TabActiveStyle.Render("[failed only]")
	}
	if a.query != "" {
		strip += " " + tui.TabActiveStyle.Render("["+a.query+"]")
	}
	return strip
}

func (a *App) renderContent() string {
	if a.loading {
		return "  Loading…"
	}
	if a.err != nil {
		return tui.ErrorStyle.Render("  error: "+a.err.Error()) + "\n\n  Press 1/2 to retry."
	}
	if a.logBusy {
		return "  Fetching logs…"
	}

	switch a.active {
	case tabRuns:
		return a.renderRuns()
	case tabWorkflows:
		return a.renderWorkflows()
	}
	return ""
}

// emptyOr renders the list, or a note when the filter or the source left it
// with nothing. The two are worth distinguishing: an empty source is a fact
// about the repository, an over-narrow filter is one keystroke from being
// wrong, and they call for opposite responses.
func (a *App) emptyOr(rows []string, empty string) string {
	if len(rows) == 0 {
		if a.query != "" {
			return tui.DimStyle.Render(fmt.Sprintf("  Nothing matches %q.", a.query))
		}
		return tui.DimStyle.Render("  " + empty)
	}
	return a.pane.RenderList(rows, a.width, a.listRows())
}

func (a *App) renderRuns() string {
	items := a.visibleRuns()
	rows := make([]string, 0, len(items))
	for _, r := range items {
		icon, style := runStyle(r.Status, r.Conclusion)
		rows = append(rows, fmt.Sprintf("  %s %-30s %-10s %-12s %s",
			style.Render(icon), r.WorkflowName, r.Conclusion, r.Event, r.HeadBranch))
	}
	empty := "No workflow runs."
	if a.failedOnly {
		empty = "No failed runs."
	}
	return a.emptyOr(rows, empty)
}

func (a *App) renderWorkflows() string {
	items := a.visibleWorkflows()
	rows := make([]string, 0, len(items))
	for _, wf := range items {
		state := tui.CheckPassStyle.Render("active")
		if wf.State != "active" {
			state = tui.DimStyle.Render(wf.State)
		}
		rows = append(rows, fmt.Sprintf("  %-30s %s  %s", wf.Name, state, wf.Path))
	}
	return a.emptyOr(rows, "No workflows.")
}

func (a *App) renderLogs() string {
	lines := strings.Split(strings.TrimRight(a.logView, "\n"), "\n")
	h := max(a.height-1, 1)
	a.logOff = clamp(a.logOff, 0, max(len(lines)-h, 0))
	end := min(a.logOff+h, len(lines))

	header := tui.TitleStyle.Render(" run logs ") +
		tui.DimStyle.Render("esc:back j/k:scroll q:quit")
	// Each line is clipped to the terminal width. A log line is routinely wider
	// than the pane, and a wrapped one costs two rows — enough of them and the
	// frame overflows, which costs the header at the top rather than the excess
	// at the bottom.
	body := make([]string, 0, h)
	for _, line := range lines[a.logOff:end] {
		clipped, _ := tui.TruncateExact(line, a.width)
		body = append(body, clipped)
	}
	return header + "\n" + tui.FitRows(strings.Join(body, "\n"), h)
}

func (a *App) footer() string {
	if a.toast != "" && time.Since(a.toastAt) < 4*time.Second {
		return tui.TruncateFooter(a.toast, a.width)
	}
	var line string
	if a.active == tabRuns {
		line = tui.FmtHints("j/k", "move", "/", "search", "enter", "logs",
			"r", "rerun", "R", "rerun failed", "c", "cancel", "f", "failed", "q", "quit")
	} else {
		line = tui.FmtHints("j/k", "move", "/", "search",
			"e", "enable", "d", "disable", "q", "quit")
	}
	// The position is only worth a footer slot when the list does not fit; a
	// counter that always reads 1/1 is noise.
	if pos := a.pane.ScrollHint(a.itemCount(), a.listRows()); pos != "" {
		line += "  " + tui.DimStyle.Render(pos)
	}
	return tui.TruncateFooter(line, a.width)
}

func runStyle(status, conclusion string) (string, lipgloss.Style) {
	switch conclusion {
	case "success":
		return "✓", tui.CheckPassStyle
	case "failure":
		return "✗", tui.CheckFailStyle
	case "cancelled":
		return "⊘", tui.CheckSkipStyle
	}
	switch status {
	case "in_progress", "queued":
		return "●", tui.CheckPendingStyle
	}
	return "·", tui.DimStyle
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func clamp(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}
