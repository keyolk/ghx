package tui

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/keyolk/ghx/internal/gh"
)

// Acting on a PR needs three things: which repo, which number, and a client
// scoped to that repo. The detail view holds them, but the list view has only a
// row — so the target is modelled as a value both can produce, and the actions
// take it rather than reaching into a view.

// actionTarget identifies the PR an action applies to.
type actionTarget struct {
	number  int
	repo    string // "owner/name"
	title   string // for confirmation text; not used in requests
	isDraft bool
	state   string // OPEN | CLOSED | MERGED
}

func (t actionTarget) valid() bool { return t.number > 0 }

func (t actionTarget) key() string {
	return fmt.Sprintf("%s#%d", strings.ToLower(t.repo), t.number)
}

// label renders the target for a prompt, e.g. `#842 in keyolk/ghx`.
func (t actionTarget) label() string {
	if t.repo == "" {
		return fmt.Sprintf("#%d", t.number)
	}
	return fmt.Sprintf("#%d in %s", t.number, t.repo)
}

// client returns a client scoped to the target's repo. ghx runs outside any
// checkout, so an unscoped client would try to infer the repo from the cwd.
func (a *App) clientFor(t actionTarget) *gh.Client {
	if t.repo == "" {
		return a.client
	}
	return a.client.WithRepo(t.repo)
}

// currentTarget returns the PR the user is looking at: the open detail view, or
// the selected list row.
func (a *App) currentTarget() (actionTarget, bool) {
	if a.state == viewPRDetail && a.detail != nil {
		t := actionTarget{number: a.detail.number}
		if a.detail.owner != "" && a.detail.repo != "" {
			t.repo = a.detail.owner + "/" + a.detail.repo
		}
		if d := a.detail.detail; d != nil {
			t.title, t.isDraft, t.state = d.Title, d.IsDraft, d.State
		}
		return t, true
	}
	if a.list != nil {
		if item, ok := a.list.selectedItem(); ok {
			return targetFromSummary(item.pr), true
		}
	}
	return actionTarget{}, false
}

func targetFromSummary(p prSummary) actionTarget {
	return actionTarget{
		number: p.Number, repo: p.Repo, title: p.Title,
		isDraft: p.IsDraft, state: p.State,
	}
}

// actionTargets returns the explicit multi-selection in list view, falling back
// to the focused PR when nothing is marked or a detail view is open.
func (a *App) actionTargets() ([]actionTarget, bool) {
	if a.state == viewPRList && a.list != nil && len(a.list.selected) > 0 {
		summaries := a.list.selectedSummaries()
		targets := make([]actionTarget, 0, len(summaries))
		for _, summary := range summaries {
			targets = append(targets, targetFromSummary(summary))
		}
		return targets, true
	}
	target, ok := a.currentTarget()
	if !ok {
		return nil, false
	}
	return []actionTarget{target}, true
}

// --- confirmation ---

// confirmKind names the pending action so the prompt can describe it and the
// handler can dispatch it.
type confirmKind int

const (
	confirmNone confirmKind = iota
	confirmApprove
	confirmClose
	confirmReopen
	confirmToggleState
	confirmReady
	confirmMerge
)

// confirmPrompt gates an action behind a yes/no. target preserves the focused
// PR for the single-item UI; targets carries the full multi-selection.
type confirmPrompt struct {
	kind    confirmKind
	target  actionTarget
	targets []actionTarget
	bulk    bool
}

func (a *App) askConfirm(kind confirmKind, t actionTarget) tea.Cmd {
	if !t.valid() {
		return errCmd(fmt.Errorf("no pull request selected"))
	}
	a.confirm = &confirmPrompt{kind: kind, target: t, targets: []actionTarget{t}}
	return nil
}

func (a *App) askConfirmTargets(kind confirmKind, targets []actionTarget) tea.Cmd {
	if len(targets) == 0 {
		return errCmd(fmt.Errorf("no pull request selected"))
	}
	for _, target := range targets {
		if !target.valid() {
			return errCmd(fmt.Errorf("invalid pull request selection"))
		}
	}
	bulk := a.state == viewPRList && a.list != nil && len(a.list.selected) > 0
	a.confirm = &confirmPrompt{kind: kind, target: targets[0], targets: targets, bulk: bulk}
	return nil
}

// handleConfirmKey resolves the prompt. Only an explicit y proceeds.
func (a *App) handleConfirmKey(msg tea.KeyMsg) tea.Cmd {
	p := a.confirm
	switch msg.String() {
	case "y", "Y":
		a.confirm = nil
		if p.bulk {
			return a.runConfirmedMany(p.kind, p.targets)
		}
		return a.runConfirmed(p.kind, p.target)
	case "n", "N", "esc", "q":
		a.confirm = nil
		return nil
	}
	// Anything else is ignored rather than treated as consent.
	return nil
}

func (a *App) performConfirmed(ctx context.Context, kind confirmKind, t actionTarget) (string, error) {
	client := a.clientFor(t)
	n := t.number
	switch kind {
	case confirmApprove:
		return "approved", client.ReviewPR(ctx, n, "approve", "")
	case confirmClose:
		return "closed", client.Close(ctx, n, "")
	case confirmReopen:
		return "reopened", client.Reopen(ctx, n)
	case confirmToggleState:
		if t.state == "CLOSED" {
			return "reopened", client.Reopen(ctx, n)
		}
		return "closed", client.Close(ctx, n, "")
	case confirmReady:
		if t.isDraft {
			return "marked ready", client.Ready(ctx, n, false)
		}
		return "converted to draft", client.Ready(ctx, n, true)
	case confirmMerge:
		return "merged", client.Merge(ctx, n, "squash")
	}
	return "", fmt.Errorf("unsupported confirmation action")
}

func (a *App) runConfirmed(kind confirmKind, t actionTarget) tea.Cmd {
	return func() tea.Msg {
		timeout := 30 * time.Second
		if kind == confirmMerge {
			timeout = 60 * time.Second
		}
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		defer cancel()
		verb, err := a.performConfirmed(ctx, kind, t)
		if kind == confirmApprove {
			return reviewPostedMsg{action: "approve", err: err}
		}
		return actionDoneMsg{label: fmt.Sprintf("%s #%d", verb, t.number), err: err}
	}
}

func (a *App) runConfirmedMany(kind confirmKind, targets []actionTarget) tea.Cmd {
	return func() tea.Msg {
		var (
			mu        sync.Mutex
			completed []string
			errs      []error
			wg        sync.WaitGroup
		)
		// Keep enough parallelism for a queue without spawning an unbounded number
		// of gh processes when an entire source is selected.
		sem := make(chan struct{}, 4)
		for _, target := range targets {
			target := target
			wg.Add(1)
			go func() {
				defer wg.Done()
				sem <- struct{}{}
				defer func() { <-sem }()

				timeout := 30 * time.Second
				if kind == confirmMerge {
					timeout = 60 * time.Second
				}
				ctx, cancel := context.WithTimeout(context.Background(), timeout)
				defer cancel()
				_, err := a.performConfirmed(ctx, kind, target)
				mu.Lock()
				defer mu.Unlock()
				if err != nil {
					errs = append(errs, fmt.Errorf("%s: %w", target.label(), err))
					return
				}
				completed = append(completed, target.key())
			}()
		}
		wg.Wait()

		noun := "PRs"
		if len(completed) == 1 {
			noun = "PR"
		}
		label := fmt.Sprintf("updated %d %s", len(completed), noun)
		if kind == confirmApprove {
			label = fmt.Sprintf("approved %d %s", len(completed), noun)
		} else if kind == confirmMerge {
			label = fmt.Sprintf("merged %d %s", len(completed), noun)
		}
		return bulkActionDoneMsg{label: label, completed: completed, err: errors.Join(errs...)}
	}
}

// renderConfirm draws the prompt, naming the exact PRs so the user can catch a
// stale or accidental selection before acting on it.
func (a *App) renderConfirm(width, height int) string {
	p := a.confirm
	count := len(p.targets)
	var question, note string
	if count > 1 {
		switch p.kind {
		case confirmApprove:
			question = fmt.Sprintf("Approve %d selected PRs?", count)
		case confirmToggleState:
			question = fmt.Sprintf("Close or reopen %d selected PRs?", count)
			note = "Open PRs will close; closed PRs will reopen."
		case confirmMerge:
			question = fmt.Sprintf("Squash-merge %d selected PRs?", count)
			note = "Each PR is merged independently using its repository credential."
		}
	} else {
		switch p.kind {
		case confirmApprove:
			question = "Approve " + p.target.label() + "?"
		case confirmClose:
			question = "Close " + p.target.label() + "?"
			note = "The PR stays on GitHub and can be reopened."
		case confirmReopen:
			question = "Reopen " + p.target.label() + "?"
		case confirmReady:
			if p.target.isDraft {
				question = "Mark " + p.target.label() + " ready for review?"
			} else {
				question = "Convert " + p.target.label() + " back to a draft?"
			}
		case confirmMerge:
			question = "Squash-merge " + p.target.label() + "?"
			note = "This cannot be undone."
		}
	}

	var b strings.Builder
	b.WriteString(question + "\n")
	if count > 1 {
		limit := min(count, 5)
		for _, target := range p.targets[:limit] {
			b.WriteString(dimStyle.Render("• "+target.label()) + "\n")
		}
		if count > limit {
			b.WriteString(dimStyle.Render(fmt.Sprintf("… and %d more", count-limit)) + "\n")
		}
	} else if p.target.title != "" {
		b.WriteString(dimStyle.Render(fitCell(p.target.title, max(width-8, 20))) + "\n")
	}
	if note != "" {
		b.WriteString(dimStyle.Render(note) + "\n")
	}
	b.WriteString("\n" + fmtHints("y", "yes", "n", "no"))
	boxHeight := min(max(8, 6+min(count, 5)), max(height-2, 8))
	return decoratedPane("confirm", b.String(), min(width-4, 76), boxHeight, true)
}

// --- label picker ---

// labelPicker lets the user toggle labels on the selected PR. It loads the
// repo's labels lazily: the list is per-repo and a cross-repo queue would
// otherwise fetch dozens of label sets nobody asked for.
type labelPicker struct {
	target  actionTarget
	all     []gh.RepoLabel
	applied map[string]bool
	// pending holds edits not yet submitted, so the picker can show the result
	// before committing and submit adds and removes in one pass.
	pending map[string]bool
	cursor  int
	offset  int
	loading bool
	err     error
	query   string
}

func (a *App) openLabelPicker(t actionTarget) tea.Cmd {
	if !t.valid() {
		return errCmd(fmt.Errorf("no pull request selected"))
	}
	a.labels = &labelPicker{
		target:  t,
		applied: map[string]bool{},
		pending: map[string]bool{},
		loading: true,
	}
	client := a.clientFor(t)
	n := t.number
	return func() tea.Msg {
		c, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		all, err := client.RepoLabels(c)
		if err != nil {
			return labelsLoadedMsg{err: err}
		}
		on, err := client.PRLabels(c, n)
		if err != nil {
			return labelsLoadedMsg{err: err}
		}
		return labelsLoadedMsg{all: all, applied: on}
	}
}

// visible returns the labels matching the filter.
func (p *labelPicker) visible() []gh.RepoLabel {
	if p.query == "" {
		return p.all
	}
	q := strings.ToLower(p.query)
	out := make([]gh.RepoLabel, 0, len(p.all))
	for _, l := range p.all {
		if strings.Contains(strings.ToLower(l.Name), q) ||
			strings.Contains(strings.ToLower(l.Description), q) {
			out = append(out, l)
		}
	}
	return out
}

// isOn reports the label's state including any un-submitted toggle.
func (p *labelPicker) isOn(name string) bool {
	if v, ok := p.pending[name]; ok {
		return v
	}
	return p.applied[name]
}

// dirty reports whether there is anything to submit.
func (p *labelPicker) dirty() bool {
	for name, want := range p.pending {
		if want != p.applied[name] {
			return true
		}
	}
	return false
}

// diff splits the pending edits into labels to add and to remove.
func (p *labelPicker) diff() (add, remove []string) {
	for name, want := range p.pending {
		switch {
		case want && !p.applied[name]:
			add = append(add, name)
		case !want && p.applied[name]:
			remove = append(remove, name)
		}
	}
	return add, remove
}

func (a *App) handleLabelKey(msg tea.KeyMsg) tea.Cmd {
	p := a.labels
	if p.loading {
		if msg.String() == "esc" {
			a.labels = nil
		}
		return nil
	}
	switch msg.String() {
	case "esc":
		a.labels = nil
		return nil
	case "enter":
		return a.submitLabels()
	case "up", "k", "ctrl+p":
		if n := len(p.visible()); n > 0 {
			p.cursor = clamp(p.cursor-1, 0, n-1)
		}
		return nil
	case "down", "j", "ctrl+n":
		if n := len(p.visible()); n > 0 {
			p.cursor = clamp(p.cursor+1, 0, n-1)
		}
		return nil
	case " ", "tab":
		vis := p.visible()
		if p.cursor < len(vis) {
			name := vis[p.cursor].Name
			p.pending[name] = !p.isOn(name)
		}
		return nil
	case "backspace":
		if r := []rune(p.query); len(r) > 0 {
			p.query = string(r[:len(r)-1])
			p.cursor = 0
		}
		return nil
	}
	// Typing filters. j/k are consumed above for navigation, so a name
	// containing them is still reachable by typing the rest of it.
	if msg.Type == tea.KeyRunes {
		p.query += string(msg.Runes)
		p.cursor = 0
	}
	return nil
}

// submitLabels applies the pending toggles. Adds and removes go in separate
// calls because gh models them as distinct flags.
func (a *App) submitLabels() tea.Cmd {
	p := a.labels
	if !p.dirty() {
		a.labels = nil
		return nil
	}
	add, remove := p.diff()
	client := a.clientFor(p.target)
	n := p.target.number
	a.labels = nil
	return func() tea.Msg {
		c, cancel := context.WithTimeout(context.Background(), 45*time.Second)
		defer cancel()
		if len(add) > 0 {
			if err := client.AddLabels(c, n, add); err != nil {
				return actionDoneMsg{label: "label", err: err}
			}
		}
		if len(remove) > 0 {
			if err := client.RemoveLabels(c, n, remove); err != nil {
				return actionDoneMsg{label: "label", err: err}
			}
		}
		return actionDoneMsg{
			label: fmt.Sprintf("labels updated on #%d (+%d -%d)", n, len(add), len(remove)),
		}
	}
}

func (a *App) renderLabelPicker(width, height int) string {
	p := a.labels
	boxW := min(width-4, 70)
	boxH := min(max(height/2, 8), 18)

	if p.loading {
		return decoratedPane("labels",
			renderSpinner(a.spinnerFrame, "Loading labels…"), boxW, 5, true)
	}
	if p.err != nil {
		return decoratedPane("labels",
			errorStyle.Render(p.err.Error())+"\n\n"+fmtHints("esc", "close"),
			boxW, 6, true)
	}

	vis := p.visible()
	var b strings.Builder
	prompt := helpKeyStyle.Render("filter: ") + p.query + blockCursor()
	b.WriteString(prompt + "\n")

	rows := max(boxH-5, 1)
	if p.cursor < p.offset {
		p.offset = p.cursor
	}
	if p.cursor >= p.offset+rows {
		p.offset = p.cursor - rows + 1
	}
	p.offset = clamp(p.offset, 0, max(len(vis)-rows, 0))

	if len(vis) == 0 {
		b.WriteString(dimStyle.Render("no labels match") + "\n")
	}
	end := min(p.offset+rows, len(vis))
	for i := p.offset; i < end; i++ {
		l := vis[i]
		mark := " "
		if p.isOn(l.Name) {
			mark = iconCheck
		}
		if i == p.cursor {
			// Plain text under the theme: the description is dimmed, and a
			// background over that would break at its reset code.
			line := fmt.Sprintf("%s %s", mark, l.Name)
			if l.Description != "" {
				line += "  " + l.Description
			}
			line = padCell(fitCell(line, boxW-4), boxW-4)
			b.WriteString(selectedRowStyle.Render(line) + "\n")
			continue
		}
		line := fmt.Sprintf("%s %s", mark, l.Name)
		if l.Description != "" {
			line += dimStyle.Render("  " + l.Description)
		}
		b.WriteString(fitCell(line, boxW-4) + "\n")
	}

	hint := fmtHints("sp", "toggle", "enter", "apply", "esc", "cancel")
	if p.dirty() {
		add, remove := p.diff()
		hint = diffHunkStyle.Render(fmt.Sprintf("+%d -%d ", len(add), len(remove))) + hint
	}
	b.WriteString("\n" + hint)
	return decoratedPane("labels · "+p.target.label(), b.String(), boxW, boxH, true)
}
