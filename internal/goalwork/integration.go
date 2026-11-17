package goalwork

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"syscall"
	"time"

	"github.com/sushidev-team/lola/internal/goalpolicy"
)

var ErrScope = errors.New("worker output exceeds accepted write scope")

// Candidate is daemon-owned. Checks run on a COPY, never in Directory: tests
// and worker code must not be able to rewrite the object that will be accepted.
type Candidate struct {
	Goal         string   `json:"goal"`
	Operation    string   `json:"operation"`
	Member       string   `json:"member"`
	Base         string   `json:"base"`
	WorkerBase   string   `json:"worker_base"`
	WorkerHead   string   `json:"worker_head"`
	TargetSHA    string   `json:"target_sha,omitempty"` // captured target of a refresh candidate
	PriorTarget  string   `json:"prior_target,omitempty"`
	Commit       string   `json:"commit"`
	Directory    string   `json:"directory"`
	BundleSHA256 string   `json:"bundle_sha256"`
	Paths        []string `json:"paths"`
}

// Stage consumes a bundle produced INSIDE the worker sandbox. Host git reads
// only the copied bundle, using daemon-owned configuration. Worker config,
// hooks, .git pointers, alternates and upload-pack settings are never consulted.
func (m *Manager) Stage(ctx context.Context, w Workspace, operation, base, head string, allowedPaths []string) (Candidate, error) {
	if err := validSHA(base); err != nil {
		return Candidate{}, err
	}
	if err := validSHA(head); err != nil {
		return Candidate{}, err
	}
	dir, err := m.path(w.Goal, "candidates", operation, w.Member)
	if err != nil {
		return Candidate{}, err
	}
	wdir, err := m.path(w.Goal, "attempts", w.Task, w.Attempt, w.Member)
	if err != nil {
		return Candidate{}, err
	}
	known, err := readJSON[Workspace](filepath.Join(wdir, "workspace.json"))
	if err != nil {
		return Candidate{}, err
	}
	if known != w || w.Directory != filepath.Join(wdir, "tree") || w.GitDir != filepath.Join(wdir, "store.git") {
		return Candidate{}, errors.New("worker workspace identity mismatch")
	}
	if old, err := readJSON[Candidate](filepath.Join(dir, "candidate.json")); err == nil {
		if old.Goal != w.Goal || old.Member != w.Member || old.Operation != operation || old.Base != base || old.WorkerBase != w.Base || old.WorkerHead != head || old.Directory != filepath.Join(dir, "tree") {
			return old, errors.New("candidate operation reused for different inputs")
		}
		if err := checkPaths(old.Paths, allowedPaths); err != nil {
			return old, err
		}
		return old, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return Candidate{}, err
	}
	if _, err := os.Lstat(dir); err == nil {
		return Candidate{}, errors.New("partial candidate retained; reserve a new integration operation")
	} else if !errors.Is(err, os.ErrNotExist) {
		return Candidate{}, err
	}
	tip, err := m.Tip(ctx, w.Goal, w.Member)
	if err != nil {
		return Candidate{}, err
	}
	if tip != base {
		return Candidate{}, ErrStale
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return Candidate{}, err
	}
	bundle := filepath.Join(dir, "submission.bundle")
	digest, err := m.copySubmission(w.Directory, bundle)
	if err != nil {
		return Candidate{}, err
	}
	r, err := m.Repository(ctx, w.Goal, w.Member)
	if err != nil {
		return Candidate{}, err
	}
	tree := filepath.Join(dir, "tree")
	if _, err := m.git(ctx, dir, "clone", "--no-local", "--no-hardlinks", "--no-checkout", "--template=", r.GitDir, tree); err != nil {
		return Candidate{}, err
	}
	if err := m.noRemote(ctx, tree); err != nil {
		return Candidate{}, err
	}
	if _, err := m.git(ctx, tree, "checkout", "--detach", base); err != nil {
		return Candidate{}, err
	}
	if _, err := m.git(ctx, tree, "bundle", "verify", bundle); err != nil {
		return Candidate{}, err
	}
	if _, err := m.git(ctx, tree, "fetch", "--no-tags", "--no-recurse-submodules", bundle, "HEAD"); err != nil {
		return Candidate{}, err
	}
	imported, err := m.resolve(ctx, tree, "FETCH_HEAD")
	if err != nil {
		return Candidate{}, err
	}
	if imported != head {
		return Candidate{}, errors.New("submitted bundle HEAD does not match declared output")
	}
	if _, err := m.git(ctx, tree, "merge-base", "--is-ancestor", w.Base, head); err != nil {
		return Candidate{}, fmt.Errorf("worker output does not contain its assigned starting revision: %w", err)
	}
	changed, err := m.changedPaths(ctx, tree, w.Base, head)
	if err != nil {
		return Candidate{}, err
	}
	if err := checkPaths(changed, allowedPaths); err != nil {
		return Candidate{}, err
	}
	// The source repository is a private clone with hooks/config disabled.
	// Conflicts are kept as evidence; no automatic reset or destructive cleanup.
	if _, err := m.git(ctx, tree, "merge", "--no-ff", "--no-commit", "--no-edit", head); err != nil {
		return Candidate{}, fmt.Errorf("%w: %v", ErrConflict, err)
	}
	mergeHead := filepath.Join(tree, ".git", "MERGE_HEAD")
	if _, err := os.Stat(mergeHead); err == nil {
		if _, err := m.git(ctx, tree, "commit", "--no-gpg-sign", "-m", "Integrate verified goal task "+w.Task); err != nil {
			return Candidate{}, err
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return Candidate{}, err
	}
	commit, err := m.resolve(ctx, tree, "HEAD")
	if err != nil {
		return Candidate{}, err
	}
	changed, err = m.changedPaths(ctx, tree, base, commit)
	if err != nil {
		return Candidate{}, err
	}
	if err := checkPaths(changed, allowedPaths); err != nil {
		return Candidate{}, err
	}
	c := Candidate{Goal: w.Goal, Operation: operation, Member: w.Member, Base: base, WorkerBase: w.Base, WorkerHead: head, Commit: commit, Directory: tree, BundleSHA256: digest, Paths: changed}
	if err := writeJSON(filepath.Join(dir, "candidate.json"), c); err != nil {
		return Candidate{}, err
	}
	return c, nil
}

func (m *Manager) changedPaths(ctx context.Context, dir, base, head string) ([]string, error) {
	// Rename detection can hide the deleted source path in --name-only output.
	// Ownership must cover BOTH sides, so treat renames as delete + add.
	out, err := m.git(ctx, dir, "diff", "--no-ext-diff", "--no-renames", "--name-only", "-z", base, head, "--")
	if err != nil {
		return nil, err
	}
	if out == "" {
		return []string{}, nil
	}
	return strings.Split(strings.TrimSuffix(out, "\x00"), "\x00"), nil
}

func checkPaths(paths, allowed []string) error {
	if err := goalpolicy.ValidatePaths(allowed); err != nil {
		return err
	}
	for _, p := range paths {
		if err := goalpolicy.ValidatePaths([]string{p}); err != nil {
			return fmt.Errorf("%w: reserved or escaping path %q", ErrScope, p)
		}
		if len(allowed) > 0 && !slices.ContainsFunc(allowed, func(prefix string) bool { return p == prefix || strings.HasPrefix(p, prefix+"/") }) {
			return fmt.Errorf("%w: out-of-scope path %q", ErrScope, p)
		}
	}
	return nil
}

func (m *Manager) copySubmission(worktree, destination string) (string, error) {
	root, err := os.OpenRoot(worktree)
	if err != nil {
		return "", err
	}
	defer root.Close()
	for _, p := range []string{".lola", ".lola/submission.bundle"} {
		fi, err := root.Lstat(p)
		if err != nil {
			return "", err
		}
		if fi.Mode()&os.ModeSymlink != 0 || (p != ".lola" && !fi.Mode().IsRegular()) {
			return "", errors.New("submission must be a regular bundle inside the worker worktree")
		}
	}
	in, err := root.OpenFile(".lola/submission.bundle", os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return "", err
	}
	defer in.Close()
	fi, err := in.Stat()
	if err != nil {
		return "", err
	}
	if !fi.Mode().IsRegular() {
		return "", errors.New("submission is not a regular file")
	}
	limit := m.MaxBundleBytes
	if limit <= 0 {
		limit = 256 << 20
	}
	if fi.Size() > limit {
		return "", errors.New("submission bundle exceeds configured size limit")
	}
	out, err := os.OpenFile(destination, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return "", err
	}
	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(out, h), io.LimitReader(in, limit+1))
	if err == nil {
		err = out.Sync()
	}
	closeErr := out.Close()
	if err != nil {
		return "", err
	}
	if closeErr != nil {
		return "", closeErr
	}
	if n > limit {
		return "", errors.New("submission bundle exceeds configured size limit")
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

type Check struct {
	ID       string `json:"id"`
	State    string `json:"state"` // only "pass" is acceptance evidence
	Artifact string `json:"artifact,omitempty"`
}

// Verification is produced by the daemon's validation runner, never decoded
// from a worker report. Manifest binds cross-repository checks to their actual
// commit combination; required IDs are supplied by the accepted goal policy.
type Verification struct {
	ID        string            `json:"id"`
	Goal      string            `json:"goal"`
	Operation string            `json:"operation"`
	Manifest  map[string]string `json:"manifest"`
	Targets   map[string]string `json:"targets,omitempty"`
	Policy    string            `json:"policy"`
	Checks    []Check           `json:"checks"`
	Reviews   []Check           `json:"reviews"`
	At        time.Time         `json:"at"`
}

func (v Verification) Validate(c Candidate, policy string, checks, profiles []string) error {
	if v.ID == "" || v.Goal != c.Goal || v.Operation != c.Operation || v.Manifest[c.Member] != c.Commit || v.Policy == "" || v.Policy != policy || v.At.IsZero() {
		return errors.New("verification does not describe this candidate and accepted policy")
	}
	if c.TargetSHA != "" && v.Targets[c.Member] != c.TargetSHA {
		return errors.New("refresh verification does not bind the captured target")
	}
	if len(v.Targets) > 0 {
		if len(v.Targets) != len(v.Manifest) {
			return errors.New("verification requires the complete target manifest")
		}
		for member, target := range v.Targets {
			if _, ok := v.Manifest[member]; !ok {
				return errors.New("verification contains an unexpected delivery target")
			}
			if err := validSHA(target); err != nil {
				return err
			}
		}
	}
	if len(checks) == 0 {
		return errors.New("goal integration requires explicit acceptance checks")
	}
	for _, required := range []struct {
		ids     []string
		results []Check
	}{{checks, v.Checks}, {profiles, v.Reviews}} {
		seen := map[string]bool{}
		for _, result := range required.results {
			if result.ID == "" || seen[result.ID] {
				return errors.New("acceptance evidence contains duplicate or empty identities")
			}
			seen[result.ID] = true
		}
		for _, id := range required.ids {
			if id == "" || !slices.ContainsFunc(required.results, func(r Check) bool { return r.ID == id && r.State == "pass" }) {
				return fmt.Errorf("required acceptance evidence %q has not passed", id)
			}
		}
	}
	return nil
}

type Receipt struct {
	Candidate    Candidate    `json:"candidate"`
	Verification Verification `json:"verification"`
	Committed    bool         `json:"committed"`
	At           time.Time    `json:"at"`
}

// Advance moves only refs/heads/goal/<id>, with a compare-and-swap against the
// verified base. The intent is durable before the ref update. If a process dies
// after that update, the matching intent proves how to finish the receipt.
func (m *Manager) Advance(ctx context.Context, c Candidate, v Verification, policy string, checks, profiles []string) (Receipt, error) {
	if err := v.Validate(c, policy, checks, profiles); err != nil {
		return Receipt{}, err
	}
	dir, err := m.path(c.Goal, "candidates", c.Operation, c.Member)
	if err != nil {
		return Receipt{}, err
	}
	known, err := readJSON[Candidate](filepath.Join(dir, "candidate.json"))
	if err != nil {
		return Receipt{}, err
	}
	if known.Directory != filepath.Join(dir, "tree") || !reflect.DeepEqual(known, c) {
		return Receipt{}, errors.New("candidate receipt changed")
	}
	if actual, err := m.resolve(ctx, known.Directory, "HEAD"); err != nil || actual != c.Commit {
		return Receipt{}, errors.New("candidate commit changed after verification")
	}
	r, err := m.Repository(ctx, c.Goal, c.Member)
	if err != nil {
		return Receipt{}, err
	}
	tip, err := m.Tip(ctx, c.Goal, c.Member)
	if err != nil {
		return Receipt{}, err
	}
	journal := filepath.Join(dir, "integration.json")
	intent, readErr := readJSON[Receipt](journal)
	if readErr == nil {
		if intent.Candidate.Base != c.Base || intent.Candidate.Commit != c.Commit || !reflect.DeepEqual(intent.Verification, v) {
			return Receipt{}, errors.New("integration operation already has a different intent")
		}
		if intent.Committed {
			if _, err := m.git(ctx, r.GitDir, "merge-base", "--is-ancestor", c.Commit, tip); err != nil {
				return Receipt{}, ErrStale
			}
			return intent, nil
		}
	} else if !errors.Is(readErr, os.ErrNotExist) {
		return Receipt{}, readErr
	}
	if tip != c.Base && tip != c.Commit {
		return Receipt{}, ErrStale
	}
	if tip == c.Commit && c.Commit != c.Base && readErr != nil {
		return Receipt{}, errors.New("goal ref advanced without a matching integration intent")
	}
	if readErr != nil {
		intent = Receipt{Candidate: known, Verification: v, At: time.Now().UTC()}
		if err := writeJSON(journal, intent); err != nil {
			return Receipt{}, err
		}
	}
	if tip != c.Commit {
		if _, err := m.git(ctx, r.GitDir, "fetch", "--no-tags", "--no-recurse-submodules", known.Directory, c.Commit); err != nil {
			return Receipt{}, err
		}
		if _, err := m.git(ctx, r.GitDir, "update-ref", "refs/heads/"+r.Branch, c.Commit, c.Base); err != nil {
			return Receipt{}, fmt.Errorf("%w: %v", ErrStale, err)
		}
	}
	intent.Committed = true
	if err := writeJSON(journal, intent); err != nil {
		return Receipt{}, err
	}
	return intent, nil
}
