package protocol

import "time"

// ReviewThread is an unresolved, resolvable GitHub review conversation, not an
// issue comment (which has no resolved state). Preview is plain text only.
type ReviewThread struct {
	ID       string `json:"id"`
	Path     string `json:"path"`
	Line     int    `json:"line"`
	Outdated bool   `json:"outdated"`
	Author   string `json:"author"`
	Preview  string `json:"preview"`
	Summary  string `json:"summary,omitempty"` // display only; original stays on GitHub
	URL      string `json:"url"`
}

// PRReadiness contains facts, not a promise that GitHub will permit a merge.
type PRReadinessData struct {
	Repo             string         `json:"repo"`
	Number           int            `json:"number"`
	URL              string         `json:"url"`
	State            string         `json:"state"`
	Draft            bool           `json:"draft"`
	Mergeable        string         `json:"mergeable"`
	MergeState       string         `json:"mergeState"`
	Review           string         `json:"review"`
	Checks           string         `json:"checks"`
	Head             string         `json:"head"`
	Base             string         `json:"base,omitempty"`
	ChecksHead       string         `json:"checksHead,omitempty"`
	ObservedAt       time.Time      `json:"observedAt,omitzero"`
	Threads          []ReviewThread `json:"threads"`
	SummariesPending bool           `json:"summariesPending,omitempty"`
	MergeMethod      string         `json:"mergeMethod"`
	MergeBlocked     string         `json:"mergeBlocked"`
	// CheckList is every check and commit status on ChecksHead. It is absent
	// when the checks could not be bound to the head (or from an older
	// daemon); a client then links to GitHub instead. ChecksTruncated says
	// GitHub holds more entries than the list carries.
	CheckList       []PRCheck `json:"checkList,omitempty"`
	ChecksTruncated bool      `json:"checksTruncated,omitempty"`
}

// PRCheck is one check on the PR head. Display only: nothing acts on it. Name,
// Workflow and Description are GitHub's (or a third party's) text, clipped;
// URL is https or empty, never another scheme.
type PRCheck struct {
	Name        string `json:"name"`
	Workflow    string `json:"workflow,omitempty"`
	State       string `json:"state"`  // GitHub's word, e.g. SUCCESS, FAILURE, IN_PROGRESS, EXPECTED
	Bucket      string `json:"bucket"` // pass | fail | pending | skipped
	URL         string `json:"url,omitempty"`
	Description string `json:"description,omitempty"`
	// Required: the base branch's rulesets require this check. False also
	// when the requirements are unknown.
	Required bool `json:"required,omitempty"`
	// Gate: one of lola's own review statuses (lola/*), a review verdict and
	// never CI.
	Gate        bool      `json:"gate,omitempty"`
	StartedAt   time.Time `json:"startedAt,omitzero"`
	CompletedAt time.Time `json:"completedAt,omitzero"`
}

// Number pins actions to the PR the user saw.
type PRReadinessArgs struct {
	Number         int    `json:"number"`
	Repo           string `json:"repo,omitempty"`
	Head           string `json:"head,omitempty"`
	Method         string `json:"method,omitempty"`
	ConfirmUnready bool   `json:"confirmUnready,omitempty"`
}
