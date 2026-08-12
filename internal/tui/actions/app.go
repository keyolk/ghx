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

	cursor int
	offset int
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
		a.cursor = 0
		return a, nil

	case workflowsMsg:
		a.loading = false
		if msg.err != nil {
			a.err = msg.err
			return a, nil
		}
		a.workflows = msg.workflows
		a.cursor = 0
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

	switch msg.String() {
	case "q", "ctrl+c":
		return tea.Quit
	case "1":
		a.active = tabRuns
		a.cursor = 0
		if a.runs == nil {
			return a.loadRuns()
		}
		return nil
	case "2":
		a.active = tabWorkflows
		a.cursor = 0
		if a.workflows == nil {
			return a.loadWorkflows()
		}
		return nil
	case "j", "down":
		a.cursor = min(a.cursor+1, a.itemCount()-1)
		return nil
	case "k", "up":
		a.cursor = max(a.cursor-1, 0)
		return nil
	case "g":
		a.cursor = 0
		return nil
	case "G":
		a.cursor = max(a.itemCount()-1, 0)
		return nil
	case "f":
		// Toggle failed-only filter (runs tab)
		a.failedOnly = !a.failedOnly
		a.cursor = 0
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
	if a.cursor >= len(items) {
		return nil
	}
	run := items[a.cursor]
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
	if a.cursor >= len(items) {
		return nil
	}
	run := items[a.cursor]
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
	if a.cursor >= len(items) {
		return nil
	}
	run := items[a.cursor]
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
	if a.cursor >= len(a.workflows) {
		return nil
	}
	wf := a.workflows[a.cursor]
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

// visibleRuns applies the failed-only filter.
func (a *App) visibleRuns() []gh.Run {
	if !a.failedOnly {
		return a.runs
	}
	out := make([]gh.Run, 0, len(a.runs))
	for _, r := range a.runs {
		if r.Conclusion == "failure" {
			out = append(out, r)
		}
	}
	return out
}

func (a *App) itemCount() int {
	switch a.active {
	case tabRuns:
		return len(a.visibleRuns())
	case tabWorkflows:
		return len(a.workflows)
	}
	return 0
}

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
	tabs := a.renderTabs()
	content := a.renderContent()
	footer := a.footer()

	return strings.Join([]string{title, tabs, content, footer}, "\n")
}

func (a *App) renderTabs() string {
	tabs := make([]tui.TabLabel, len(tabNames))
	for i, name := range tabNames {
		tabs[i] = tui.TabLabel{Name: name, Active: tab(i) == a.active}
	}
	strip := tui.RenderTabStrip(tabs, a.width)
	if a.failedOnly && a.active == tabRuns {
		// Append the failed-only filter marker after the strip; it is a state
		// toggle, not a tab, so it rides along at the end.
		strip += " " + tui.TabActiveStyle.Render("[failed only]")
	}
	return strip
}

func (a *App) renderContent() string {
	h := a.height - 4
	if h < 1 {
		h = 1
	}
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
		return a.renderRuns(h)
	case tabWorkflows:
		return a.renderWorkflows(h)
	}
	return ""
}

func (a *App) renderRuns(h int) string {
	items := a.visibleRuns()
	if len(items) == 0 {
		return tui.DimStyle.Render("  No workflow runs.")
	}
	var b strings.Builder
	for i, r := range items {
		icon, style := runStyle(r.Status, r.Conclusion)
		line := fmt.Sprintf("  %s %-30s %-10s %-12s %s",
			style.Render(icon), r.WorkflowName, r.Conclusion, r.Event, r.HeadBranch)
		if i == a.cursor {
			line = tui.SelectedRowStyle.Render(padLine(line, a.width))
		}
		b.WriteString(line + "\n")
	}
	return b.String()
}

func (a *App) renderWorkflows(h int) string {
	if len(a.workflows) == 0 {
		return tui.DimStyle.Render("  No workflows.")
	}
	var b strings.Builder
	for i, wf := range a.workflows {
		state := tui.CheckPassStyle.Render("active")
		if wf.State != "active" {
			state = tui.DimStyle.Render(wf.State)
		}
		line := fmt.Sprintf("  %-30s %s  %s", wf.Name, state, wf.Path)
		if i == a.cursor {
			line = tui.SelectedRowStyle.Render(padLine(line, a.width))
		}
		b.WriteString(line + "\n")
	}
	return b.String()
}

func (a *App) renderLogs() string {
	lines := strings.Split(strings.TrimRight(a.logView, "\n"), "\n")
	a.logOff = clamp(a.logOff, 0, max(len(lines)-1, 0))
	h := a.height - 3
	end := min(a.logOff+h, len(lines))

	header := tui.TitleStyle.Render(" run logs ") +
		tui.DimStyle.Render("esc:back j/k:scroll q:quit")
	body := strings.Join(lines[a.logOff:end], "\n")
	return header + "\n" + body
}

func (a *App) footer() string {
	if a.toast != "" && time.Since(a.toastAt) < 4*time.Second {
		return tui.TruncateFooter(a.toast, a.width)
	}
	if a.active == tabRuns {
		return tui.TruncateFooter(
			tui.FmtHints("j/k", "move", "enter", "logs", "r", "rerun",
				"R", "rerun failed", "c", "cancel", "f", "filter", "q", "quit"),
			a.width)
	}
	return tui.TruncateFooter(
		tui.FmtHints("j/k", "move", "e", "enable", "d", "disable", "q", "quit"),
		a.width)
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

func padLine(s string, w int) string {
	width := lipgloss.Width(s)
	if width >= w {
		return s
	}
	return s + strings.Repeat(" ", w-width)
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
