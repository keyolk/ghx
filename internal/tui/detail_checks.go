package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/keyolk/tuikit/keys"

	"github.com/keyolk/ghx/internal/pr"
)

// The Checks tab lists CI results and, on request, the failing job's log. Logs
// are fetched lazily — a run's log can be megabytes, so it only loads when the
// reviewer actually asks for that check.

// checksView owns the check list cursor and the log viewport.
type checksView struct {
	// km renders the footer; the zero value falls back to the defaults.
	km keys.Keymap

	checks []pr.Check
	cursor int
	offset int

	// log viewer state; non-empty logCheck means the log pane is showing
	logCheck string
	logs     []string
	logOff   int
	logBusy  bool
}

func newChecksView() *checksView { return &checksView{} }

func (c *checksView) setChecks(checks []pr.Check) {
	c.checks = checks
	if c.cursor >= len(checks) {
		c.cursor = max(len(checks)-1, 0)
	}
}

func (c *checksView) setLogs(checkName, logs string) {
	c.logCheck = checkName
	c.logs = strings.Split(strings.TrimRight(logs, "\n"), "\n")
	c.logOff = 0
	c.logBusy = false
}

func (c *checksView) closeLogs() {
	c.logCheck = ""
	c.logs = nil
	c.logOff = 0
	c.logBusy = false
}

func (c *checksView) showingLogs() bool { return c.logCheck != "" || c.logBusy }

// selected returns the check under the cursor.
func (c *checksView) selected() (pr.Check, bool) {
	if c.cursor < 0 || c.cursor >= len(c.checks) {
		return pr.Check{}, false
	}
	return c.checks[c.cursor], true
}

// hasPending reports whether any check is still running, so the caller knows
// whether to keep polling. Terminal states stop the poll — no idle redraws.
func (c *checksView) hasPending() bool {
	for _, ck := range c.checks {
		if ck.Bucket == "pending" {
			return true
		}
	}
	return false
}

func (c *checksView) moveCursor(delta int) {
	if len(c.checks) == 0 {
		return
	}
	c.cursor = clamp(c.cursor+delta, 0, len(c.checks)-1)
}

func (c *checksView) scrollLogs(delta int) {
	if len(c.logs) == 0 {
		return
	}
	c.logOff = clamp(c.logOff+delta, 0, max(len(c.logs)-1, 0))
}

// render draws either the log pane or the check list.
func (c *checksView) render(width, height int) string {
	if c.logBusy {
		return renderSpinner(0, "Fetching logs for "+c.logCheck+"…")
	}
	if c.logCheck != "" {
		return c.renderLogs(width, height)
	}
	return c.renderList(width, height)
}

func (c *checksView) renderList(width, height int) string {
	if len(c.checks) == 0 {
		return dimStyle.Render("No checks reported for this PR.")
	}
	c.clampOffset(height)
	var b strings.Builder
	end := min(c.offset+height, len(c.checks))
	for i := c.offset; i < end; i++ {
		if i == c.cursor {
			// Themed whole: a background wrapped around styled cells stops at the
			// first reset inside them, leaving the row striped rather than selected.
			plain := checkRowPlain(c.checks[i], width)
			plain, _ = truncateExact(plain, width)
			if pad := width - lipglossWidth(plain); pad > 0 {
				plain += strings.Repeat(" ", pad)
			}
			b.WriteString(selectedRowStyle.Render(plain))
		} else {
			b.WriteString(checkRow(c.checks[i], width))
		}
		if i < end-1 {
			b.WriteByte('\n')
		}
	}
	return b.String()
}

func (c *checksView) clampOffset(height int) {
	if height <= 0 {
		return
	}
	if c.cursor < c.offset {
		c.offset = c.cursor
	}
	if c.cursor >= c.offset+height {
		c.offset = c.cursor - height + 1
	}
	c.offset = clamp(c.offset, 0, max(len(c.checks)-height, 0))
}

func (c *checksView) renderLogs(width, height int) string {
	if len(c.logs) == 0 {
		return dimStyle.Render("No log output.")
	}
	header := diffFileStyle.Render(c.logCheck) + dimStyle.Render(
		fmt.Sprintf("  (%d lines)", len(c.logs)))
	body := make([]string, 0, height)
	body = append(body, header)
	end := min(c.logOff+max(height-1, 1), len(c.logs))
	for i := c.logOff; i < end; i++ {
		line, _ := truncateExact(c.logs[i], width)
		body = append(body, line)
	}
	return strings.Join(body, "\n")
}

// checkRow formats one check: status icon, name, duration, workflow. Cells are
// padded as plain text before styling, so ANSI escapes never count toward width.
func checkRow(ck pr.Check, width int) string {
	icon, name, dur, workflow := checkCells(ck, width)
	_, style := checkStyleFor(ck.Bucket)
	row := style.Render(icon) + " " + style.Render(name) + " " + dimStyle.Render(dur)
	if workflow != "" {
		row += "  " + dimStyle.Render(workflow)
	}
	return row
}

// checkRowPlain is the same row without styling, for when the caller applies a
// theme to the whole line.
func checkRowPlain(ck pr.Check, width int) string {
	icon, name, dur, workflow := checkCells(ck, width)
	row := icon + " " + name + " " + dur
	if workflow != "" {
		row += "  " + workflow
	}
	return row
}

// checkCells lays out the row's columns once, so the styled and plain forms
// cannot drift apart in width.
func checkCells(ck pr.Check, width int) (icon, name, dur, workflow string) {
	const (
		durW = 8
		// Past this the name is mostly padding; the rest goes to the workflow
		// column rather than stretching one field across a wide terminal.
		nameMaxW = 60
	)
	workflowW := max(width-2-nameMaxW-durW-4, 0)
	nameW := clamp(width-2-durW-4-min(workflowW, 24), 12, nameMaxW)

	icon, _ = checkStyleFor(ck.Bucket)
	name = padCell(fitCell(ck.Name, nameW), nameW)
	dur = leftPadCell(checkDuration(ck), durW)
	if workflowW > 6 && ck.Workflow != "" {
		workflow = fitCell(ck.Workflow, workflowW)
	}
	return icon, name, dur, workflow
}

func checkStyleFor(bucket string) (icon string, style lipgloss.Style) {
	switch bucket {
	case "pass":
		return iconCheck, checkPassStyle
	case "fail":
		return iconFail, checkFailStyle
	case "pending":
		return iconPending, checkPendingStyle
	default:
		return iconSkip, checkSkipStyle
	}
}

// checkDuration renders how long a check took, or how long it has been running.
func checkDuration(ck pr.Check) string {
	if ck.StartedAt.IsZero() {
		return ""
	}
	end := ck.CompletedAt
	if end.IsZero() {
		end = time.Now()
	}
	d := end.Sub(ck.StartedAt)
	if d < 0 {
		return ""
	}
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm%02ds", int(d.Minutes()), int(d.Seconds())%60)
	default:
		return fmt.Sprintf("%dh%02dm", int(d.Hours()), int(d.Minutes())%60)
	}
}
