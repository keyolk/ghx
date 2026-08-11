// Package pr defines the domain types for ghx: PR, review, diff, and check
// data displayed by the TUI. These are the in-memory shapes the UI renders;
// the internal/gh package fills them from gh CLI output.
package pr

import "time"

// Summary is the compact PR shape shown in the PR list.
type Summary struct {
	ID             string    `json:"id"` // GitHub GraphQL node ID
	Number         int       `json:"number"`
	Title          string    `json:"title"`
	Author         User      `json:"author"`
	State          string    `json:"state"` // "OPEN" | "CLOSED" | "MERGED"
	IsDraft        bool      `json:"isDraft"`
	ReviewDecision string    `json:"reviewDecision"`
	HeadRefName    string    `json:"headRefName"`
	UpdatedAt      time.Time `json:"updatedAt"`

	// Conversation status is enriched in one GraphQL batch after the base list
	// loads. Known distinguishes a resolved PR from a failed/unavailable lookup.
	UnresolvedConversations int  `json:"-"`
	ConversationsKnown      bool `json:"-"`

	// Repo is "owner/name". A review queue spans repositories, so every
	// subsequent call (view, diff, checks, comments) has to be told which one
	// this PR belongs to — the current directory cannot be assumed.
	Repo string `json:"repo"`
	URL  string `json:"url"`

	// CredentialRepo is the credential selector that found this PR. It is not
	// display data: subsequent detail and action requests use it to stay on the
	// same GitHub account in a cross-account queue. Two forms exist — a bare
	// "owner/repo" resolved through the Git credential helper, or
	// GHUserSelectorPrefix + a gh CLI login.
	CredentialRepo string `json:"-"`
}

// GHUserSelectorPrefix marks a credential selector that names a gh CLI login
// rather than a repository. It lives here because the selector travels with
// each PR through the list, detail, and action paths.
const GHUserSelectorPrefix = "ghuser:"

// Detail is the full PR shape shown in the detail view.
type Detail struct {
	Number           int        `json:"number"`
	Title            string     `json:"title"`
	Body             string     `json:"body"`
	Author           User       `json:"author"`
	State            string     `json:"state"`
	IsDraft          bool       `json:"isDraft"`
	BaseRefName      string     `json:"baseRefName"`
	HeadRefName      string     `json:"headRefName"`
	Additions        int        `json:"additions"`
	Deletions        int        `json:"deletions"`
	ChangedFiles     int        `json:"changedFiles"`
	Mergeable        string     `json:"mergeable"`
	MergeStateStatus string     `json:"mergeStateStatus"`
	ReviewDecision   string     `json:"reviewDecision"`
	Labels           []Label    `json:"labels"`
	ReviewRequests   []User     `json:"reviewRequests"`
	Reviews          []Review   `json:"reviews"`
	Commits          []Commit   `json:"commits"`
	Files            []FileStat `json:"files"`
	URL              string     `json:"url"`
}

// User is a GitHub user/bot.
type User struct {
	ID    string `json:"id"`
	Login string `json:"login"`
	Name  string `json:"name"`
	IsBot bool   `json:"is_bot"`
}

// Label is a PR label.
type Label struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Color       string `json:"color"`
}

// Review is a review-level metadata entry (no line positions).
type Review struct {
	ID                string    `json:"id"`
	Author            User      `json:"author"`
	AuthorAssociation string    `json:"authorAssociation"`
	Body              string    `json:"body"`
	State             string    `json:"state"` // APPROVED | COMMENTED | CHANGES_REQUESTED | PENDING | DISMISSED
	SubmittedAt       time.Time `json:"submittedAt"`
}

// Commit is a PR commit entry.
type Commit struct {
	OID             string    `json:"oid"`
	MessageHeadline string    `json:"messageHeadline"`
	MessageBody     string    `json:"messageBody"`
	AuthoredDate    time.Time `json:"authoredDate"`
}

// FileStat is a per-file additions/deletions summary (no diff content).
type FileStat struct {
	Path      string `json:"path"`
	Additions int    `json:"additions"`
	Deletions int    `json:"deletions"`
}

// Check is a CI check entry from `gh pr checks --json`.
type Check struct {
	Bucket      string    `json:"bucket"` // pass | fail | pending | skipping | cancel
	Name        string    `json:"name"`
	State       string    `json:"state"`
	Link        string    `json:"link"`
	Workflow    string    `json:"workflow"`
	Event       string    `json:"event"`
	StartedAt   time.Time `json:"startedAt"`
	CompletedAt time.Time `json:"completedAt"`
}

// ReviewThread is an inline review thread with line-position data, fetched via GraphQL.
type ReviewThread struct {
	ID                string          `json:"id"`
	IsResolved        bool            `json:"isResolved"`
	IsCollapsed       bool            `json:"isCollapsed"`
	Path              string          `json:"path"`
	Line              int             `json:"line"`
	OriginalLine      int             `json:"originalLine"`
	DiffSide          string          `json:"diffSide"` // "LEFT" | "RIGHT" — NOT "side"
	StartLine         int             `json:"startLine"`
	OriginalStartLine int             `json:"originalStartLine"`
	Comments          []ThreadComment `json:"comments"`
}

// ThreadComment is a single comment inside a review thread.
type ThreadComment struct {
	ID string `json:"id"` // GraphQL node id (PRRC_…)
	// DatabaseID is the numeric REST id. Replying goes through the REST
	// endpoint, which rejects the node id with "Parent comment not found", so
	// this is the field to use when threading a reply.
	DatabaseID int64     `json:"databaseId"`
	Body       string    `json:"body"`
	Author     User      `json:"author"`
	Path       string    `json:"path"`
	Line       int       `json:"line"`
	CreatedAt  time.Time `json:"createdAt"`
}

// DiffFile is one file's parsed unified diff: hunks with LEFT/RIGHT line maps.
type DiffFile struct {
	Path      string
	OldPath   string // for renames; empty otherwise
	Additions int
	Deletions int
	Hunks     []DiffHunk
}

// DiffHunk is a `@@ -oldStart,oldCount +newStart,newCount @@` block.
type DiffHunk struct {
	OldStart int
	OldCount int
	NewStart int
	NewCount int
	Lines    []DiffLine
}

// DiffLineKind classifies a diff line.
type DiffLineKind int

const (
	DiffLineContext DiffLineKind = iota
	DiffLineAddition
	DiffLineDeletion
	DiffLineHunkHeader
	DiffLineFileHeader
)

// DiffLine is a single line within a hunk. OldLineNo/NewLineNo are 0 when the
// line doesn't exist on that side (additions have no LEFT line, deletions no RIGHT).
type DiffLine struct {
	Kind      DiffLineKind
	Content   string
	OldLineNo int
	NewLineNo int
}
