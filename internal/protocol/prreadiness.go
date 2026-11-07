package protocol

// ReviewThread is an unresolved, resolvable GitHub review conversation, not an
// issue comment (which has no resolved state). Preview is plain text only.
type ReviewThread struct {
	ID       string `json:"id"`
	Path     string `json:"path"`
	Line     int    `json:"line"`
	Outdated bool   `json:"outdated"`
	Author   string `json:"author"`
	Preview  string `json:"preview"`
	URL      string `json:"url"`
}

// PRReadiness contains facts, not a promise that GitHub will permit a merge.
type PRReadinessData struct {
	Repo       string         `json:"repo"`
	Number     int            `json:"number"`
	URL        string         `json:"url"`
	State      string         `json:"state"`
	Draft      bool           `json:"draft"`
	Mergeable  string         `json:"mergeable"`
	MergeState string         `json:"mergeState"`
	Review     string         `json:"review"`
	Checks     string         `json:"checks"`
	Head       string         `json:"head"`
	Threads    []ReviewThread `json:"threads"`
}

// Number pins actions to the PR the user saw.
type PRReadinessArgs struct {
	Number int `json:"number"`
}
