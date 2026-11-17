// Package goalpublish is the narrow upstream writer for verified goals. Its
// only mutations are non-forced pushes to goal/<id>, draft PR creation, and
// append-only verification comments on a goal's existing PR.
// Neither callers nor workers can supply a push ref, remote URL or Git command.
package goalpublish

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/sushidev-team/lola/internal/goalpolicy"
	"github.com/sushidev-team/lola/internal/goalwork"
)

var repositoryName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]*/[A-Za-z0-9_.-]+$`)

type Request struct {
	Goal              string            `json:"goal"`
	Operation         string            `json:"operation"`
	PreviousOperation string            `json:"previous_operation,omitempty"`
	Policy            goalpolicy.Policy `json:"policy"`
	Manifest          map[string]string `json:"manifest"`
	Targets           map[string]string `json:"targets,omitempty"`
	Title             string            `json:"title"`
	Body              string            `json:"body"`
}

type Member struct {
	Repo            string `json:"repo"`
	Target          string `json:"target"`
	Head            string `json:"head"`
	State           string `json:"state"` // pending, unchanged, published, merged, closed
	Pushed          bool   `json:"pushed"`
	Number          int    `json:"number,omitempty"`
	URL             string `json:"url,omitempty"`
	Draft           bool   `json:"draft"`
	MarkerOperation string `json:"marker_operation,omitempty"`
	EvidenceComment int64  `json:"evidence_comment,omitempty"`
}

type Record struct {
	Version   int               `json:"version"`
	Request   Request           `json:"request"`
	Members   map[string]Member `json:"members"`
	Complete  bool              `json:"complete"` // all changed members have PRs, not necessarily merged
	CreatedAt time.Time         `json:"created_at"`
	UpdatedAt time.Time         `json:"updated_at"`
}

type Publisher struct {
	Workspaces    *goalwork.Manager
	GitBin, GhBin string
	Run           func(context.Context, string, string, []string, []byte) ([]byte, error)
	// Guard checks current authority immediately before each remote mutation.
	// Its release serializes that mutation with pause/cancel on the daemon.
	Guard func(context.Context) (release func(), err error)
	mu    sync.Mutex
}

func Marker(goal, member, operation string) string {
	return "<!-- lola-goal-pr:" + goal + ":" + member + ":" + operation + " -->"
}

func (p *Publisher) Publish(ctx context.Context, want Request) (Record, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.Workspaces == nil || p.Guard == nil {
		return Record{}, errors.New("publication requires owned workspaces and a live authorization guard")
	}
	digest, err := want.Policy.Digest()
	if err != nil {
		return Record{}, err
	}
	if !want.Policy.PublishPR {
		return Record{}, errors.New("goal policy does not authorize publication")
	}
	if !validDescription(want) {
		return Record{}, errors.New("invalid bounded goal PR description")
	}
	branch, err := goalwork.GoalBranch(want.Goal)
	if err != nil {
		return Record{}, err
	}
	var checks []string
	for _, check := range want.Policy.Checks {
		checks = append(checks, check.ID)
	}
	if _, err := p.Workspaces.VerifyFinalAt(ctx, want.Goal, want.Operation, digest, want.Manifest, want.Targets, checks, want.Policy.Profiles); err != nil {
		return Record{}, err
	}
	previous, err := p.predecessor(ctx, want, digest)
	if err != nil {
		return Record{}, err
	}
	dir, err := p.Workspaces.DeliveryDirectory(want.Goal, want.Operation)
	if err != nil {
		return Record{}, err
	}
	path := filepath.Join(dir, "publication.json")
	record := Record{Version: 1, Request: want, Members: map[string]Member{}, CreatedAt: time.Now().UTC()}
	loaded := false
	if b, err := os.ReadFile(path); err == nil {
		if err := json.Unmarshal(b, &record); err != nil {
			return record, err
		}
		// Presentation may change between binary versions. The persisted prose
		// is authoritative once publication is reserved, including after a lost
		// response. Reuse it only when ALL execution inputs still match; never
		// rewrite an existing PR or revision comment to the new template.
		reserved := want
		reserved.Title, reserved.Body = record.Request.Title, record.Request.Body
		if record.Version != 1 || record.CreatedAt.IsZero() || !validDescription(reserved) || !sameRequest(record.Request, reserved) || record.Members == nil || len(record.Members) != len(want.Policy.Members) {
			return record, errors.New("publication identity reused with changed inputs")
		}
		want = reserved
		loaded = true
	} else if !errors.Is(err, os.ErrNotExist) {
		return record, err
	}
	// Validate all destinations before saving an intent or writing upstream.
	repositories := map[string]goalwork.Repository{}
	targets := map[string]string{}
	seen := map[string]bool{}
	for _, member := range want.Policy.Members {
		_, name, _ := strings.Cut(member.Repo, "/")
		if len(member.Repo) > 255 || !repositoryName.MatchString(member.Repo) || name == "." || name == ".." || seen[strings.ToLower(member.Repo)] || member.Target == branch {
			return record, errors.New("publication requires distinct GitHub repositories and a goal branch different from every delivery target")
		}
		seen[strings.ToLower(member.Repo)] = true
		r, err := p.Workspaces.Repository(ctx, want.Goal, member.ID)
		if err != nil {
			return record, err
		}
		if r.Source != member.Source || r.Target != member.Target || r.Branch != branch {
			return record, errors.New("publication placement differs from the verified repository")
		}
		repositories[member.ID] = r
		target := r.Start
		if len(want.Targets) > 0 {
			target = want.Targets[member.ID]
		}
		targets[member.ID] = target
		unchanged, err := p.Workspaces.TreesEqual(ctx, want.Goal, member.ID, want.Manifest[member.ID], target)
		if err != nil {
			return record, err
		}
		markerOperation := ""
		prior := previous.Members[member.ID]
		if want.PreviousOperation != "" {
			markerOperation = prior.MarkerOperation
			if markerOperation == "" {
				markerOperation = want.PreviousOperation
			}
		}
		if old, ok := record.Members[member.ID]; ok {
			if old.Repo != member.Repo || old.Target != member.Target || old.Head != want.Manifest[member.ID] || old.MarkerOperation != markerOperation || old.EvidenceComment < 0 || (old.State != "unchanged" && prior.Number != 0 && old.Number != prior.Number) {
				return record, errors.New("publication member journal mismatch")
			}
			if !slices.Contains([]string{"pending", "unchanged", "published", "merged", "closed"}, old.State) || (old.State == "unchanged" && (!unchanged || old.Number != 0)) || (slices.Contains([]string{"published", "merged", "closed"}, old.State) && (!old.Pushed || old.Number < 1 || old.URL == "")) || (record.Complete && old.State == "pending") {
				return record, errors.New("publication member journal is incomplete or inconsistent")
			}
		} else {
			if loaded {
				return record, errors.New("publication journal lost a member reservation")
			}
			state := "pending"
			if unchanged {
				state = "unchanged"
			}
			m := Member{Repo: member.Repo, Target: member.Target, Head: want.Manifest[member.ID], State: state, MarkerOperation: markerOperation}
			if state != "unchanged" {
				m.Number, m.URL, m.Draft = prior.Number, prior.URL, prior.Draft
			}
			record.Members[member.ID] = m
		}
	}
	if len(record.Members) != len(want.Manifest) {
		return record, errors.New("publication journal contains unexpected repositories")
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		return record, err
	}
	if err := p.effect(ctx, func() error { return save(path, &record) }); err != nil {
		return record, err
	} // intent precedes every external effect
	for _, id := range slices.Sorted(maps.Keys(record.Members)) {
		member := record.Members[id]
		prior := previous.Members[id]
		markerOperation := member.MarkerOperation
		if markerOperation == "" {
			markerOperation = want.Operation
		}
		marker := Marker(want.Goal, id, markerOperation)
		refs, err := p.refs(ctx, member.Repo, branch, member.Target)
		if err != nil {
			return record, err
		}
		if refs.Default == branch {
			return record, errors.New("refusing to push a repository's default branch")
		}
		if member.State == "unchanged" {
			if refs.Base != targets[id] {
				return record, ErrTargetChanged
			}
			continue
		}
		if refs.Head != "" && refs.Head != member.Head && (prior.Head == "" || refs.Head != prior.Head) {
			return record, errors.New("remote goal branch contains an unverified revision; preserving it")
		}
		prs, err := p.pullRequests(ctx, member.Repo, branch)
		if err != nil {
			return record, err
		}
		observed := member
		if refs.Head != "" && refs.Head == prior.Head {
			observed.Head = prior.Head
		}
		pr, err := matchPR(prs, observed, branch, marker)
		if err != nil {
			return record, err
		}
		if pr != nil && pr.State == "open" && refs.Head == "" {
			return record, errors.New("open goal PR lost its head branch; preserving remote state")
		}
		if member.Number != 0 && (pr == nil || pr.Number != member.Number) {
			return record, errors.New("recorded goal PR disappeared or changed identity")
		}
		if pr != nil && pr.State != "open" && pr.Head.SHA != member.Head {
			return record, errors.New("previous goal PR is closed; preserving human delivery decision")
		}
		if (pr == nil && refs.Base != targets[id]) || (pr != nil && pr.State == "open" && (pr.Base.SHA != targets[id] || refs.Base != targets[id])) {
			return record, ErrTargetChanged
		}
		if pr != nil {
			member.Number, member.URL, member.Draft = pr.Number, pr.URL, pr.Draft
			record.Members[id] = member
			if err := save(path, &record); err != nil {
				return record, err
			}
		}
		if (pr == nil || pr.State == "open") && refs.Head != member.Head {
			if member.Pushed || (refs.Head == "" && prior.Pushed) {
				return record, errors.New("published goal branch was deleted; refusing to recreate it")
			}
			if err := p.effect(ctx, func() error { return p.push(ctx, repositories[id].GitDir, member.Repo, member.Head, branch) }); err != nil {
				return record, err
			}
			// A push response alone is not the acknowledgment. Recovery performs
			// the same read if the process dies or the response is lost.
			refs, err = p.refs(ctx, member.Repo, branch, member.Target)
			if err != nil {
				return record, err
			}
			if refs.Head != member.Head {
				return record, errors.New("remote goal branch did not acknowledge the verified revision")
			}
			if refs.Base != targets[id] {
				return record, ErrTargetChanged
			}
			if pr != nil {
				prs, err = p.pullRequests(ctx, member.Repo, branch)
				if err != nil {
					return record, err
				}
				pr, err = matchPR(prs, member, branch, marker)
				if err != nil {
					return record, err
				}
				if pr == nil || pr.Number != member.Number {
					return record, errors.New("goal PR identity changed during refreshed push")
				}
			}
		}
		if pr == nil {
			if refs.Base != targets[id] {
				return record, ErrTargetChanged
			}
			member.Pushed = true
			record.Members[id] = member
			if err := save(path, &record); err != nil {
				return record, err
			}
			body := want.Body + "\n\n" + marker
			if want.PreviousOperation != "" {
				body += "\n\n" + revisionMarker(want.Goal, id, want.Operation)
			}
			if err := p.effect(ctx, func() error {
				_, err := p.createPR(ctx, member.Repo, branch, member.Target, want.Title, body)
				return err
			}); err != nil {
				return record, err
			}
			prs, err = p.pullRequests(ctx, member.Repo, branch)
			if err != nil {
				return record, err
			}
			pr, err = matchPR(prs, member, branch, marker)
			if err != nil {
				return record, err
			}
			if pr == nil {
				return record, errors.New("PR creation outcome not yet observable; retaining publication intent")
			}
		}
		if pr.State == "open" && pr.Base.SHA != targets[id] {
			return record, ErrTargetChanged
		}
		member.Number, member.URL, member.Draft = pr.Number, pr.URL, pr.Draft
		member.Pushed = true
		member.State = "published"
		if pr.Merged {
			member.State = "merged"
		} else if pr.State == "closed" {
			member.State = "closed"
		}
		record.Members[id] = member
		if err := save(path, &record); err != nil {
			return record, err
		}
		if want.PreviousOperation != "" && pr.State == "open" {
			comment, err := p.revisionEvidence(ctx, want, id, member, *pr)
			if err != nil {
				return record, err
			}
			member.EvidenceComment = comment
			record.Members[id] = member
			if err := save(path, &record); err != nil {
				return record, err
			}
		}
	}
	record.Complete = true
	return record, save(path, &record)
}

func sameRequest(a, b Request) bool {
	left, err := json.Marshal(a)
	if err != nil {
		return false
	}
	right, err := json.Marshal(b)
	return err == nil && bytes.Equal(left, right)
}

func validDescription(r Request) bool {
	return strings.HasPrefix(r.Title, "feat(goal): ") && len(r.Title) <= 280 && !strings.ContainsAny(r.Title, "\r\n") && len(r.Body) <= 60000 && r.Body != ""
}

func (p *Publisher) effect(ctx context.Context, fn func() error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	release, err := p.Guard(ctx)
	if err != nil {
		return err
	}
	if release == nil {
		return errors.New("publication guard did not acquire authority")
	}
	defer release()
	return fn()
}

func save(path string, r *Record) error {
	r.UpdatedAt = time.Now().UTC()
	b, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".publication-")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err = f.Write(append(b, '\n')); err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	if err := os.Rename(f.Name(), path); err != nil {
		return err
	}
	d, err := os.Open(filepath.Dir(path))
	if err != nil {
		return err
	}
	defer d.Close()
	return d.Sync()
}

func matchPR(prs []pullRequest, m Member, branch, marker string) (*pullRequest, error) {
	if len(prs) > 1 {
		return nil, errors.New("multiple PRs exist for the reserved goal branch; reconcile before continuing")
	}
	if len(prs) == 0 {
		return nil, nil
	}
	p := prs[0]
	if p.Number < 1 || p.Head.Ref != branch || p.Head.SHA != m.Head || p.Base.Ref != m.Target || !strings.EqualFold(p.Head.Repo.FullName, m.Repo) || !strings.EqualFold(p.Base.Repo.FullName, m.Repo) || !strings.Contains(p.Body, marker) || p.URL != fmt.Sprintf("https://github.com/%s/pull/%d", p.Base.Repo.FullName, p.Number) {
		return nil, errors.New("goal PR ownership, repository, target or verified head changed")
	}
	if p.State != "open" && p.State != "closed" {
		return nil, errors.New("unknown goal PR state")
	}
	return &p, nil
}
