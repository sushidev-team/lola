// Package goalcheck runs explicitly authorized acceptance commands in disposable
// containers. Its receipts, not a worker's claimed test results, are evidence
// for goal integration. It never executes repository code on the host.
package goalcheck

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/sushidev-team/lola/internal/goalpolicy"
	"github.com/sushidev-team/lola/internal/goalwork"
)

var identifier = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._-]{0,127}$`)

// Spec and ArtifactSpec are static accepted-policy types shared with goals.
type Spec = goalpolicy.Check
type ArtifactSpec = goalpolicy.ArtifactSpec

type Artifact struct {
	ID     string `json:"id"`
	Kind   string `json:"kind"`
	Path   string `json:"path"` // daemon-owned captured file
	SHA256 string `json:"sha256"`
	Bytes  int64  `json:"bytes"`
}

type Receipt struct {
	ID         string                       `json:"id"`
	Goal       string                       `json:"goal"`
	Operation  string                       `json:"operation"`
	Policy     string                       `json:"policy"`
	Spec       Spec                         `json:"spec"`
	Manifest   map[string]goalwork.Revision `json:"manifest"`
	State      string                       `json:"state"` // running, pass, fail, error, interrupted
	Stopped    bool                         `json:"stopped"`
	Error      string                       `json:"error,omitempty"`
	Output     string                       `json:"output,omitempty"`
	Artifacts  []Artifact                   `json:"artifacts,omitempty"`
	StartedAt  time.Time                    `json:"started_at"`
	FinishedAt time.Time                    `json:"finished_at,omitempty"`
}

// Runner serializes checks so the same identity cannot launch twice inside a
// daemon. Callers reserve IDs and budget before invoking it. The running receipt
// is written before Docker; a restart holds it for recovery, never reruns it.
type Runner struct {
	Workspaces *goalwork.Manager
	Docker     string
	Run        func(context.Context, string, []string) (string, error)
	mu         sync.Mutex
}

func (r *Runner) Execute(ctx context.Context, goal, operation, id, policy string, spec Spec, manifest map[string]goalwork.Revision) (Receipt, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.Workspaces == nil {
		return Receipt{}, errors.New("validation workspace manager unavailable")
	}
	if err := spec.Validate(); err != nil {
		return Receipt{}, err
	}
	if !identifier.MatchString(operation) || policy == "" {
		return Receipt{}, errors.New("check requires integration identity and policy revision")
	}
	if spec.Member != "" {
		if _, ok := manifest[spec.Member]; !ok {
			return Receipt{}, errors.New("check member absent from verified combination")
		}
	}
	dir, err := r.Workspaces.ValidationDirectory(goal, id)
	if err != nil {
		return Receipt{}, err
	}
	path := filepath.Join(dir, "check.json")
	want := Receipt{ID: id, Goal: goal, Operation: operation, Policy: policy, Spec: spec, Manifest: manifest}
	if old, err := readReceipt(path); err == nil {
		if !sameInputs(old, want) {
			return Receipt{}, errors.New("check identity reused with different inputs")
		}
		if old.State == "running" || !old.Stopped {
			return old, errors.New("check was interrupted; reconcile its container before retrying with a new identity")
		}
		if old.State != "pass" {
			return old, fmt.Errorf("check %s: %s", old.State, old.Error)
		}
		if err := r.validateEvidence(dir, old); err != nil {
			return old, err
		}
		return old, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return Receipt{}, err
	}
	w, err := r.Workspaces.PrepareValidation(ctx, goal, id, manifest)
	if err != nil {
		return Receipt{}, err
	}
	args, err := containerArgs(w, operation, spec)
	if err != nil {
		return Receipt{}, err
	}
	want.State = "running"
	want.StartedAt = time.Now().UTC()
	if err := saveReceipt(path, want); err != nil {
		return Receipt{}, err
	}
	runCtx, cancel := context.WithTimeout(ctx, time.Duration(spec.TimeoutSeconds)*time.Second)
	want.Output, err = r.command(runCtx, args)
	runCanceled := runCtx.Err() != nil
	cancel()
	// Killing docker run's client is insufficient: terminate only this owned
	// container, using a fresh context, before reading mutable output files.
	stopCtx, stopCancel := context.WithTimeout(context.Background(), 20*time.Second)
	_, stopErr := r.command(stopCtx, []string{"rm", "-f", containerName(goal, id)})
	stopCancel()
	if stopErr != nil && strings.Contains(strings.ToLower(stopErr.Error()), "no such container") {
		stopErr = nil
	}
	want.Stopped = stopErr == nil
	if stopErr != nil {
		err = errors.Join(err, fmt.Errorf("could not confirm validation container stopped: %w", stopErr))
	}
	want.FinishedAt = time.Now().UTC()
	want.State = "pass"
	if err != nil {
		want.State = "error"
		var exit *exec.ExitError
		if !runCanceled && stopErr == nil && errors.As(err, &exit) && exit.ExitCode() > 0 && exit.ExitCode() < 125 {
			want.State = "fail" // the accepted command ran and rejected the candidate
		}
		want.Error = err.Error()
	} else {
		want.Artifacts, err = captureArtifacts(w.Directory, dir, spec.Artifacts)
		if err != nil {
			want.State = "error"
			want.Error = err.Error()
		}
	}
	if saveErr := saveReceipt(path, want); saveErr != nil {
		return want, saveErr
	}
	return want, err
}

// Recover stops only the container for an already journaled check. It retains
// its workspace and marks it interrupted; an unknown exit outcome never passes.
func (r *Runner) Recover(ctx context.Context, goal, id string) (Receipt, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.Workspaces == nil {
		return Receipt{}, errors.New("validation workspace manager unavailable")
	}
	dir, err := r.Workspaces.ValidationDirectory(goal, id)
	if err != nil {
		return Receipt{}, err
	}
	path := filepath.Join(dir, "check.json")
	old, err := readReceipt(path)
	if err != nil {
		return Receipt{}, err
	}
	if old.Goal != goal || old.ID != id {
		return Receipt{}, errors.New("check ownership mismatch")
	}
	if old.State != "running" && old.Stopped {
		return old, nil
	}
	stopCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	if _, err := r.command(stopCtx, []string{"rm", "-f", containerName(goal, id)}); err != nil && !strings.Contains(strings.ToLower(err.Error()), "no such container") {
		return old, err
	}
	old.Stopped = true
	if old.State == "running" {
		old.State = "interrupted"
		old.Error = "daemon restarted before receiving a conclusive check result"
	}
	old.FinishedAt = time.Now().UTC()
	return old, saveReceipt(path, old)
}

func sameInputs(a, b Receipt) bool {
	return a.ID == b.ID && a.Goal == b.Goal && a.Operation == b.Operation && a.Policy == b.Policy && reflect.DeepEqual(a.Spec, b.Spec) && reflect.DeepEqual(a.Manifest, b.Manifest)
}

func containerName(goal, id string) string {
	sum := sha256.Sum256([]byte(goal + "\x00" + id))
	return "lola-check-" + hex.EncodeToString(sum[:16])
}

func readReceipt(path string) (Receipt, error) {
	var r Receipt
	b, err := os.ReadFile(path)
	if err != nil {
		return r, err
	}
	err = json.Unmarshal(b, &r)
	return r, err
}

func saveReceipt(path string, r Receipt) error {
	b, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".check-")
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
