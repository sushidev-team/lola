// Package reviewverify retains independently executed checks of review revisions.
// It reuses the goal runner's isolation, but grants no goal or merge authority.
package reviewverify

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/sushidev-team/lola/internal/goalcheck"
	"github.com/sushidev-team/lola/internal/goalpolicy"
	"github.com/sushidev-team/lola/internal/goalwork"
	"github.com/sushidev-team/lola/internal/reviewpack"
)

var identity = regexp.MustCompile(`^[a-f0-9]{32}$`)
var digest = regexp.MustCompile(`^[a-f0-9]{64}$`)

type Record struct {
	ID         string           `json:"id"`
	Package    string           `json:"package"`
	Session    string           `json:"session"`
	Base       string           `json:"base"`
	Head       string           `json:"head"`
	Spec       goalpolicy.Check `json:"spec"`
	State      string           `json:"state"`
	Stopped    bool             `json:"stopped"`
	Output     string           `json:"output,omitempty"`
	Error      string           `json:"error,omitempty"`
	StartedAt  time.Time        `json:"startedAt"`
	FinishedAt time.Time        `json:"finishedAt,omitempty"`
}

type Runner struct {
	Home   string
	Docker string
	Run    func(context.Context, string, []string) (string, error)
}

func (r Runner) directory(pkg string) (string, error) {
	if !digest.MatchString(pkg) {
		return "", errors.New("invalid package identity")
	}
	return filepath.Join(r.Home, "review-verifications", pkg), nil
}

// Reserve is serialized by the daemon and precedes every effect. Retrying the
// same request returns its record, including failed/uncertain execution.
func (r Runner) Reserve(p reviewpack.Package, id string, spec goalpolicy.Check) (Record, bool, error) {
	if !identity.MatchString(id) {
		return Record{}, false, errors.New("verification requires a 32-character lowercase hex request ID")
	}
	if err := spec.Validate(); err != nil {
		return Record{}, false, err
	}
	if spec.Member != "" || len(spec.Artifacts) != 0 {
		return Record{}, false, errors.New("review checks run in the selected repository without artifact exports")
	}
	all, err := r.List(p)
	if err != nil {
		return Record{}, false, err
	}
	for _, old := range all {
		if old.ID == id {
			if !reflect.DeepEqual(old.Spec, spec) {
				return Record{}, false, errors.New("verification request ID reused with different inputs")
			}
			return old, false, nil
		}
	}
	for _, old := range all {
		if !old.Stopped {
			return Record{}, false, errors.New("a check is running or interrupted; refresh or recover it before starting another")
		}
	}
	if len(all) >= 32 {
		return Record{}, false, errors.New("verification limit reached (32 checks per package)")
	}
	v := Record{ID: id, Package: p.ID, Session: p.Session, Base: p.Base, Head: p.Head, Spec: spec, State: "running", StartedAt: time.Now().UTC()}
	return v, true, r.Save(v)
}

func (r Runner) Save(v Record) error {
	if !identity.MatchString(v.ID) {
		return errors.New("invalid verification identity")
	}
	dir, err := r.directory(v.Package)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, ".receipt-")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err = f.Write(b); err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	return os.Rename(f.Name(), filepath.Join(dir, v.ID+".json"))
}

func (r Runner) List(p reviewpack.Package) ([]Record, error) {
	dir, err := r.directory(p.ID)
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return []Record{}, nil
	}
	if err != nil {
		return nil, err
	}
	all := []Record{}
	for _, e := range entries {
		if filepath.Ext(e.Name()) != ".json" {
			continue
		}
		b, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			return nil, err
		}
		var v Record
		if err := json.Unmarshal(b, &v); err != nil {
			return nil, err
		}
		if !identity.MatchString(v.ID) || e.Name() != v.ID+".json" || v.Package != p.ID || v.Session != p.Session || v.Base != p.Base || v.Head != p.Head {
			return nil, errors.New("verification receipt identity mismatch")
		}
		all = append(all, v)
	}
	sort.Slice(all, func(i, j int) bool { return all[i].StartedAt.After(all[j].StartedAt) })
	return all, nil
}

func (r Runner) checker() (*goalwork.Manager, *goalcheck.Runner) {
	m := &goalwork.Manager{Root: filepath.Join(r.Home, "review-check-workspaces")}
	return m, &goalcheck.Runner{Workspaces: m, Docker: r.Docker, Run: r.Run}
}

func (r Runner) Execute(ctx context.Context, v Record, source string) error {
	m, c := r.checker()
	// The outer bound also covers repository preparation, not only the command.
	ctx, cancel := context.WithTimeout(ctx, time.Duration(v.Spec.TimeoutSeconds)*time.Second+2*time.Minute)
	defer cancel()
	_, err := m.InitializeRevision(ctx, v.Package, "repo", source, v.Head)
	if err != nil {
		v.State, v.Error, v.Stopped = "error", err.Error(), true
	} else {
		spec := v.Spec
		spec.Member = "repo"
		receipt, runErr := c.Execute(ctx, v.Package, v.Package, v.ID, v.Package, spec, map[string]goalwork.Revision{"repo": {Commit: v.Head}})
		v.State, v.Stopped, v.Output, v.Error = receipt.State, receipt.Stopped, receipt.Output, receipt.Error
		if runErr != nil && v.Error == "" {
			v.Error = runErr.Error()
		}
		if v.State == "" {
			v.State = "error"
			v.Stopped = true
		}
	}
	v.FinishedAt = time.Now().UTC()
	if len(v.Output) > 16384 {
		v.Output = strings.ToValidUTF8(v.Output[:16384], "�") + "\n[Output preview truncated at 16 KiB]"
	}
	return r.Save(v)
}

// Recover only stops the container for this receipt. It never reruns a command
// or promotes an outcome that was lost across a restart to a pass.
func (r Runner) Recover(ctx context.Context, v Record) (Record, error) {
	_, c := r.checker()
	if v.Stopped {
		return v, nil
	}
	_, err := c.Recover(ctx, v.Package, v.ID)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return v, err
	}
	v.State, v.Stopped, v.Error = "interrupted", true, "Verification interrupted; start a new check to obtain a conclusive result."
	v.FinishedAt = time.Now().UTC()
	return v, r.Save(v)
}
