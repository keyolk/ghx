package tui

import (
	"time"

	"github.com/keyolk/ghx/internal/gh"
)

// tea.Msg types for async gh results. Each corresponds to a gh wrapper call
// dispatched as a tea.Cmd closure (func() tea.Msg{...}). Handlers in app.go
// switch on these to update App state.

// prListMsg carries the result of fetching a source's PR list.
type prListMsg struct {
	sourceIdx  int
	generation uint64
	prs        []prSummary
	warning    error
	err        error
}

// prListTickMsg fires the periodic background refresh of the current source.
type prListTickMsg time.Time

// detailDebounceMsg fires after the list-navigation debounce to load PR detail.
type detailDebounceMsg struct{ gen int }

// spinnerTickMsg drives the braille spinner; only advances while loading.
type spinnerTickMsg time.Time

// prDetailMsg carries the full PR detail (overview/files/commits).
type prDetailMsg struct {
	detail *prDetail
	err    error
}

// prDiffMsg carries the raw unified diff text.
type prDiffMsg struct {
	diff string
	err  error
}

// prChecksMsg carries the CI check list.
type prChecksMsg struct {
	checks []prCheck
	err    error
}

// errMsg surfaces a transient error to the footer.
type errMsg struct{ err error }

// toastMsg surfaces a transient success/acknowledgement to the footer.
type toastMsg struct{ text string }

// quitMsg terminates the program.
type quitMsg struct{}

// openDetailMsg asks app.go to transition from the list to the detail view
// for the currently selected PR. The list owns the selection; app.go owns the
// top-level view state, so it reads the selection and constructs the detail model.
type openDetailMsg struct{}

// prThreadsMsg carries inline review threads fetched via GraphQL.
type prThreadsMsg struct {
	threads []prReviewThread
	err     error
}

// postCommentMsg asks app.go to post the composer's body to its target.
type postCommentMsg struct {
	target composerTarget
	body   string
}

// commentPostedMsg reports the outcome of a comment/reply post.
type commentPostedMsg struct{ err error }

// reviewPostedMsg reports the outcome of an approve/request-changes/comment review.
type reviewPostedMsg struct {
	action string
	err    error
}

// openEditorMsg asks app.go to hand the draft to $EDITOR.
type openEditorMsg struct{ draft string }

// editorDoneMsg returns the edited draft (or an error) from $EDITOR.
type editorDoneMsg struct {
	body string
	err  error
}

// runLogsMsg carries fetched workflow run logs.
type runLogsMsg struct {
	checkName string
	logs      string
	err       error
}

// checksPollTickMsg drives the checks-tab refresh while checks are pending.
type checksPollTickMsg time.Time

// mergeResultMsg reports the outcome of a merge attempt.
type mergeResultMsg struct{ err error }

// actionDoneMsg reports a simple gh action's outcome (checkout, ready, ...).
type actionDoneMsg struct {
	label string
	err   error
}

// bulkActionDoneMsg reports a confirmed action across multiple selected PRs.
// completed contains stable selection keys so successful targets can be cleared
// while failures remain selected for inspection or retry.
type bulkActionDoneMsg struct {
	label     string
	completed []string
	err       error
}

// paletteRunMsg carries a submitted command line for app.go to dispatch.
type paletteRunMsg struct{ line string }

// searchSubmitMsg carries a submitted search query for the PR list.
type searchSubmitMsg struct{ query string }

// openComposerMsg asks app.go to open the comment composer for a target.
type openComposerMsg struct{ target composerTarget }

// listReturnMsg asks app.go to leave the detail view and show the PR list.
type listReturnMsg struct{}

// labelsLoadedMsg carries the repo's labels plus the ones already on the PR,
// for the label picker.
type labelsLoadedMsg struct {
	all     []gh.RepoLabel
	applied []string
	err     error
}

// openURLMsg asks app.go to open a resolved URL in the browser.
type openURLMsg struct{ url string }
