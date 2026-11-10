package hub

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"regexp"
	"strings"
	"time"

	"github.com/sushidev-team/lola/hubproto"
)

// Findings come from GitHub, never from an inventory upload: for each
// opted-in repo the collector reads the open Dependabot alerts and the open
// bot-authored dependency-bump PRs whose checks fail, maps them to hub items,
// and keeps ONLY items that pass hubproto.Validate. Nothing else about the
// repository (manifest paths, the dependency list, titles) leaves the machine.

// GhFunc runs `gh <args>` and returns its stdout. It is the exec seam; a
// non-nil error should carry gh's stderr so 403/404 can be told apart.
type GhFunc func(ctx context.Context, args ...string) ([]byte, error)

const (
	// DefaultGhTimeout bounds one gh call.
	DefaultGhTimeout = 60 * time.Second
	// DefaultGhMaxOutput bounds one gh call's stdout.
	DefaultGhMaxOutput = 8 << 20
	// DefaultMaxFindings caps the items one Collect returns.
	DefaultMaxFindings = 1000
	// maxPRList is the --limit of the open-PR listing.
	maxPRList = 100
	// maxTitleLen bounds a PR title before it is parsed.
	maxTitleLen = 300
)

// ErrOutputTooLarge is returned by ExecGh when stdout exceeds its cap.
var ErrOutputTooLarge = errors.New("hub: gh output exceeds the cap")

// ExecGh is the real GhFunc: bin ("" = "gh" from PATH), stdout capped at
// maxOut bytes (0 = DefaultGhMaxOutput), stderr's tail kept for the error.
// The caller's context bounds it; Collector adds a per-call timeout.
func ExecGh(bin string, maxOut int) GhFunc {
	if bin == "" {
		bin = "gh"
	}
	if maxOut <= 0 {
		maxOut = DefaultGhMaxOutput
	}
	return func(ctx context.Context, args ...string) ([]byte, error) {
		cmd := exec.CommandContext(ctx, bin, args...)
		out := &cappedWriter{max: maxOut}
		var errb tailWriter
		cmd.Stdout = out
		cmd.Stderr = &errb
		cmd.WaitDelay = 2 * time.Second
		err := cmd.Run()
		if out.over {
			return nil, ErrOutputTooLarge
		}
		if err != nil {
			return nil, &GhError{
				Cmd:        firstArg(args),
				HTTPStatus: httpStatusIn(string(errb.buf)),
				Stderr:     cleanText(lastRunes(string(errb.buf), maxErrorText)),
				Err:        err,
			}
		}
		return out.buf.Bytes(), nil
	}
}

// GhError is a failed gh call. HTTPStatus is the API status gh reported as
// "(HTTP nnn)" on stderr, 0 when it named none.
type GhError struct {
	Cmd        string
	HTTPStatus int
	Stderr     string
	Err        error
}

func (e *GhError) Error() string {
	s := "gh " + e.Cmd + ": " + e.Err.Error()
	if e.Stderr != "" {
		s += ": " + e.Stderr
	}
	return s
}

func (e *GhError) Unwrap() error { return e.Err }

var httpStatusRe = regexp.MustCompile(`\(HTTP ([1-5][0-9][0-9])\)`)

func httpStatusIn(s string) int {
	m := httpStatusRe.FindAllStringSubmatch(s, -1)
	if len(m) == 0 {
		return 0
	}
	n := 0
	fmt.Sscanf(m[len(m)-1][1], "%d", &n)
	return n
}

func lastRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[len(r)-n:])
}

func firstArg(args []string) string {
	if len(args) == 0 {
		return ""
	}
	return args[0]
}

type cappedWriter struct {
	buf  bytes.Buffer
	max  int
	over bool
}

func (w *cappedWriter) Write(p []byte) (int, error) {
	if w.buf.Len()+len(p) > w.max {
		w.over = true
		return 0, ErrOutputTooLarge
	}
	return w.buf.Write(p)
}

// tailWriter keeps the LAST 2 KiB: a CLI's fatal error is what it prints last.
type tailWriter struct{ buf []byte }

func (w *tailWriter) Write(p []byte) (int, error) {
	const max = 2 << 10
	w.buf = append(w.buf, p...)
	if len(w.buf) > max {
		w.buf = w.buf[len(w.buf)-max:]
	}
	return len(p), nil
}

// Collector gathers findings for a list of repos.
type Collector struct {
	Gh GhFunc
	// Timeout bounds each gh call (0 = DefaultGhTimeout).
	Timeout time.Duration
	// MaxItems caps the returned items (0 = DefaultMaxFindings).
	MaxItems int
}

// Result is one collection pass.
type Result struct {
	// Items are validated findings (no ID), deduplicated by hubproto.DedupKey.
	Items []hubproto.Item
	// Dropped counts candidates that failed hubproto.Validate (or could not be
	// mapped, e.g. a bump PR whose title names no package).
	Dropped int
	// Skipped names repos whose alerts were unavailable (403/404: Dependabot
	// alerts disabled, or no permission). Not an error for the batch.
	Skipped []string
	// Errors are per-repo failures (gh error, timeout, bad JSON). The other
	// repos' findings are still returned.
	Errors []error
}

// Collect reads every repo (owner/name, validated first) and returns what it
// found. It never fails as a whole: per-repo problems land in Result.
func (c *Collector) Collect(ctx context.Context, repos []string) Result {
	var res Result
	maxItems := c.MaxItems
	if maxItems <= 0 {
		maxItems = DefaultMaxFindings
	}
	seen := map[string]bool{}
	add := func(it hubproto.Item) {
		if hubproto.Validate(it) != nil {
			res.Dropped++
			return
		}
		k := hubproto.DedupKey(it)
		if seen[k] || len(res.Items) >= maxItems {
			return
		}
		seen[k] = true
		res.Items = append(res.Items, it)
	}
	for _, repo := range repos {
		if ctx.Err() != nil {
			res.Errors = append(res.Errors, ctx.Err())
			break
		}
		if err := hubproto.ValidateRepo(repo); err != nil {
			res.Errors = append(res.Errors, fmt.Errorf("hub: findings: %w", err))
			continue
		}
		alerts, err := c.alerts(ctx, repo)
		switch {
		case err == nil:
			for _, it := range alerts {
				add(it)
			}
		case isNotAvailable(err):
			res.Skipped = append(res.Skipped, repo)
		default:
			res.Errors = append(res.Errors, fmt.Errorf("%s alerts: %w", repo, err))
		}
		prs, dropped, err := c.redBumps(ctx, repo)
		res.Dropped += dropped
		if err != nil {
			res.Errors = append(res.Errors, fmt.Errorf("%s pull requests: %w", repo, err))
			continue
		}
		for _, it := range prs {
			add(it)
		}
	}
	return res
}

func (c *Collector) gh(ctx context.Context, args ...string) ([]byte, error) {
	if c.Gh == nil {
		return nil, errors.New("hub: findings collector has no gh")
	}
	t := c.Timeout
	if t <= 0 {
		t = DefaultGhTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, t)
	defer cancel()
	return c.Gh(ctx, args...)
}

// isNotAvailable: Dependabot alerts disabled (403) or the repo/feature not
// visible to this token (404). gh reports the status as "(HTTP 403)".
func isNotAvailable(err error) bool {
	var ge *GhError
	st := 0
	if errors.As(err, &ge) {
		st = ge.HTTPStatus
	} else {
		st = httpStatusIn(err.Error())
	}
	return st == 403 || st == 404
}

// ghAlert is the part of a Dependabot alert the collector reads.
type ghAlert struct {
	State      string `json:"state"`
	Dependency struct {
		Package struct {
			Ecosystem string `json:"ecosystem"`
			Name      string `json:"name"`
		} `json:"package"`
	} `json:"dependency"`
	SecurityAdvisory struct {
		GHSAID   string `json:"ghsa_id"`
		CVEID    string `json:"cve_id"`
		Severity string `json:"severity"`
	} `json:"security_advisory"`
	SecurityVulnerability struct {
		Severity               string `json:"severity"`
		VulnerableVersionRange string `json:"vulnerable_version_range"`
		FirstPatchedVersion    *struct {
			Identifier string `json:"identifier"`
		} `json:"first_patched_version"`
	} `json:"security_vulnerability"`
}

func (c *Collector) alerts(ctx context.Context, repo string) ([]hubproto.Item, error) {
	out, err := c.gh(ctx, "api", "repos/"+repo+"/dependabot/alerts?state=open&per_page=100", "--paginate")
	if err != nil {
		return nil, err
	}
	// --paginate concatenates one JSON array per page.
	dec := json.NewDecoder(bytes.NewReader(out))
	var items []hubproto.Item
	for {
		var page []ghAlert
		if err := dec.Decode(&page); err == io.EOF {
			break
		} else if err != nil {
			return nil, errors.New("alerts JSON does not decode")
		}
		for _, a := range page {
			if a.State != "" && a.State != "open" {
				continue
			}
			items = append(items, alertItem(repo, a))
		}
	}
	return items, nil
}

func alertItem(repo string, a ghAlert) hubproto.Item {
	advisory := a.SecurityAdvisory.GHSAID
	if advisory == "" {
		advisory = a.SecurityAdvisory.CVEID
	}
	sev := a.SecurityVulnerability.Severity
	if sev == "" {
		sev = a.SecurityAdvisory.Severity
	}
	fixed := ""
	if a.SecurityVulnerability.FirstPatchedVersion != nil {
		fixed = strings.TrimSpace(a.SecurityVulnerability.FirstPatchedVersion.Identifier)
	}
	return hubproto.Item{
		Kind: hubproto.KindDepAdvisory,
		DepAdvisory: &hubproto.DepAdvisory{
			Repo:       repo,
			Ecosystem:  strings.ToLower(strings.TrimSpace(a.Dependency.Package.Ecosystem)),
			Package:    strings.TrimSpace(a.Dependency.Package.Name),
			Vulnerable: strings.TrimSpace(a.SecurityVulnerability.VulnerableVersionRange),
			Fixed:      fixed,
			Advisory:   strings.TrimSpace(advisory),
			Severity:   mapSeverity(sev),
		},
	}
}

// mapSeverity maps GitHub's severities onto hubproto's ("medium" is GitHub's
// word for "moderate"). Unknown stays as-is and fails validation.
func mapSeverity(s string) hubproto.Severity {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "low":
		return hubproto.SeverityLow
	case "medium", "moderate":
		return hubproto.SeverityModerate
	case "high":
		return hubproto.SeverityHigh
	case "critical":
		return hubproto.SeverityCritical
	}
	return hubproto.Severity(s)
}

type ghPR struct {
	Number      int    `json:"number"`
	HeadRefName string `json:"headRefName"`
	Title       string `json:"title"`
	Author      struct {
		Login string `json:"login"`
		IsBot bool   `json:"is_bot"`
	} `json:"author"`
	StatusCheckRollup []struct {
		Name       string `json:"name"`
		Context    string `json:"context"`
		Status     string `json:"status"`
		Conclusion string `json:"conclusion"`
		State      string `json:"state"`
	} `json:"statusCheckRollup"`
}

func (c *Collector) redBumps(ctx context.Context, repo string) ([]hubproto.Item, int, error) {
	out, err := c.gh(ctx, "pr", "list", "--repo", repo, "--state", "open",
		"--limit", fmt.Sprint(maxPRList), "--json", "number,headRefName,author,statusCheckRollup,title")
	if err != nil {
		return nil, 0, err
	}
	var prs []ghPR
	if err := json.Unmarshal(out, &prs); err != nil {
		return nil, 0, errors.New("pull request JSON does not decode")
	}
	var items []hubproto.Item
	dropped := 0
	for _, pr := range prs {
		author := botAuthor(pr.Author.Login)
		if author == "" || !prFailing(pr) {
			continue
		}
		pkg := packageFromTitle(pr.Title)
		if pkg == "" {
			dropped++
			continue
		}
		items = append(items, hubproto.Item{
			Kind: hubproto.KindRedBumpPR,
			RedBumpPR: &hubproto.RedBumpPR{
				Repo: repo, PR: pr.Number, Branch: pr.HeadRefName, Package: pkg, Author: author,
			},
		})
	}
	return items, dropped, nil
}

// botAuthor maps gh's renderings of the two bump bots ("app/dependabot",
// "dependabot", "dependabot[bot]") onto hubproto's allowlisted logins; any
// other author is "".
func botAuthor(login string) string {
	l := strings.ToLower(strings.TrimSuffix(strings.TrimPrefix(login, "app/"), "[bot]"))
	switch l {
	case "dependabot":
		return hubproto.AuthorDependabot
	case "renovate":
		return hubproto.AuthorRenovate
	}
	return ""
}

// prFailing reports whether any check of the rollup failed. lola's own
// commit statuses (lola/…) are a review verdict, not CI, and are ignored.
func prFailing(pr ghPR) bool {
	for _, e := range pr.StatusCheckRollup {
		if strings.HasPrefix(e.Context, "lola/") || strings.HasPrefix(e.Name, "lola/") {
			continue
		}
		s := e.State // StatusContext
		if s == "" {
			if e.Status != "" && e.Status != "COMPLETED" {
				continue // still running
			}
			s = e.Conclusion
		}
		switch strings.ToUpper(s) {
		case "FAILURE", "ERROR", "TIMED_OUT", "CANCELLED", "ACTION_REQUIRED", "STARTUP_FAILURE":
			return true
		}
	}
	return false
}

// Title shapes the two bots write, matched conservatively. A title that fits
// none of them (a grouped update, a lockfile refresh) yields "" and the PR is
// skipped: guessing a package would put a wrong name in front of an agent.
var titlePackageRes = []*regexp.Regexp{
	// Dependabot: "Bump lodash from 4.17.20 to 4.17.21", "build(deps): bump @types/node from 1 to 2 in /web"
	regexp.MustCompile(`(?i)(?:^|[\s:])bump (\S+) from \S+ to \S+`),
	// Renovate: "Update dependency lodash to v4.17.21", "chore(deps): update rust crate serde to v1"
	regexp.MustCompile(`(?i)(?:^|[\s:])update (?:dependency|module|rust crate|crate|gem|package|plugin) (\S+) to \S+`),
	// Renovate: "Update actions/checkout action to v4"
	regexp.MustCompile(`(?i)(?:^|[\s:])update (\S+) action to \S+`),
}

func packageFromTitle(title string) string {
	if len(title) > maxTitleLen || strings.ContainsAny(title, "\n\r") {
		return ""
	}
	for _, re := range titlePackageRes {
		if m := re.FindStringSubmatch(title); m != nil {
			return strings.Trim(m[1], "`'\"")
		}
	}
	return ""
}

// Chunk splits items into batches of at most hubproto.MaxFindingsItems, the
// most one ReportFindings call may carry.
func Chunk(items []hubproto.Item) [][]hubproto.Item {
	var out [][]hubproto.Item
	for len(items) > 0 {
		n := min(len(items), hubproto.MaxFindingsItems)
		out = append(out, items[:n])
		items = items[n:]
	}
	return out
}
