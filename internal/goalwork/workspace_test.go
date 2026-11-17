package goalwork

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func fixture(t *testing.T) (*Manager, Repository) {
	t.Helper()
	dir := t.TempDir()
	m := &Manager{Root: filepath.Join(dir, "goals")}
	source := filepath.Join(dir, "source")
	if err := os.Mkdir(source, 0700); err != nil {
		t.Fatal(err)
	}
	gitOK(t, m, source, "init", "-b", "main")
	put(t, filepath.Join(source, "app.txt"), "initial\n")
	gitOK(t, m, source, "add", "app.txt")
	gitOK(t, m, source, "commit", "-m", "initial")
	r, err := m.Initialize(context.Background(), "goal1", "api", source, "main")
	if err != nil {
		t.Fatal(err)
	}
	return m, r
}

func gitOK(t *testing.T, m *Manager, dir string, args ...string) string {
	t.Helper()
	out, err := m.git(context.Background(), dir, args...)
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(out)
}

func put(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
}

func worker(t *testing.T, m *Manager, r Repository, task, attempt, base string) Workspace {
	t.Helper()
	w, err := m.PrepareWorker(context.Background(), r.Goal, r.Member, task, attempt, base)
	if err != nil {
		t.Fatal(err)
	}
	return w
}

// Only the fixture uses host git on a worker. Production asks the container to
// export this bundle and never executes host git against its mutable metadata.
func submit(t *testing.T, m *Manager, w Workspace, file, content string) string {
	t.Helper()
	put(t, filepath.Join(w.Directory, file), content)
	gitOK(t, m, w.Directory, "add", "--", file)
	gitOK(t, m, w.Directory, "commit", "-m", "worker output")
	head := gitOK(t, m, w.Directory, "rev-parse", "HEAD")
	gitOK(t, m, w.Directory, "bundle", "create", filepath.Join(w.Directory, ".lola", "submission.bundle"), "HEAD", "^"+w.Base)
	return head
}

func stage(t *testing.T, m *Manager, w Workspace, operation, base, head string) Candidate {
	t.Helper()
	c, err := m.Stage(context.Background(), w, operation, base, head, nil)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func proof(c Candidate) Verification {
	return Verification{ID: "v1", Goal: c.Goal, Operation: c.Operation, Manifest: map[string]string{c.Member: c.Commit}, Policy: "policy1", Checks: []Check{{ID: "tests", State: "pass"}}, Reviews: []Check{{ID: "security", State: "pass"}}, At: time.Now().UTC()}
}

func advance(t *testing.T, m *Manager, c Candidate) Receipt {
	t.Helper()
	r, err := m.Advance(context.Background(), c, proof(c), "policy1", []string{"tests"}, []string{"security"})
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestPrivateGoalIntegrationLeavesMainAndCheckoutUntouched(t *testing.T) {
	m, r := fixture(t)
	put(t, filepath.Join(r.Source, "uncommitted.txt"), "user work\n")
	before := gitOK(t, m, r.Source, "show-ref")
	w := worker(t, m, r, "task1", "attempt1", r.Start)
	for _, dir := range []string{r.GitDir, w.Directory} {
		if got := gitOK(t, m, dir, "remote"); got != "" {
			t.Fatalf("upstream credential route remains: %q", got)
		}
	}
	if _, err := os.Stat(filepath.Join(w.GitDir, "objects", "info", "alternates")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("object store shares alternates: %v", err)
	}
	// Packed objects must be independent inodes, including when clone runs on
	// a filesystem that supports hardlinks.
	a, _ := filepath.Glob(filepath.Join(r.GitDir, "objects", "pack", "*.pack"))
	b, _ := filepath.Glob(filepath.Join(w.GitDir, "objects", "pack", "*.pack"))
	for _, x := range a {
		for _, y := range b {
			fi, _ := os.Stat(x)
			fj, _ := os.Stat(y)
			if os.SameFile(fi, fj) {
				t.Fatal("shared Git object inode")
			}
		}
	}
	head := submit(t, m, w, "app.txt", "implemented\n")
	c := stage(t, m, w, "op1", r.Start, head)
	if c.Commit == head {
		t.Fatal("expected an explicit integration commit")
	}
	v := proof(c)
	receipt, err := m.Advance(context.Background(), c, v, "policy1", []string{"tests"}, []string{"security"})
	if err != nil || !receipt.Committed {
		t.Fatalf("advance: %+v %v", receipt, err)
	}
	if got := gitOK(t, m, r.Source, "show-ref"); got != before {
		t.Fatal("source refs changed")
	}
	if got := gitOK(t, m, r.GitDir, "rev-parse", "refs/heads/main"); got != r.Start {
		t.Fatal("private main advanced")
	}
	if got := gitOK(t, m, r.Source, "status", "--porcelain"); got != "?? uncommitted.txt" {
		t.Fatalf("user work changed: %s", got)
	}
	if tip, _ := m.Tip(context.Background(), r.Goal, r.Member); tip != c.Commit {
		t.Fatal("goal tip not advanced")
	}
	if again, err := m.Advance(context.Background(), c, v, "policy1", []string{"tests"}, []string{"security"}); err != nil || !again.Committed {
		t.Fatalf("idempotent retry: %+v %v", again, err)
	}
	if again, err := m.Initialize(context.Background(), r.Goal, r.Member, r.Source, r.Target); err != nil || again != r {
		t.Fatalf("initialize recovery: %+v %v", again, err)
	}
}

func TestWorkerMetadataNeverExecutesOnHost(t *testing.T) {
	m, r := fixture(t)
	w := worker(t, m, r, "task1", "attempt1", r.Start)
	head := submit(t, m, w, "app.txt", "safe output\n")
	// A worker can rewrite all its private metadata. Neither preparation retry
	// nor submission ingestion is allowed to consult it on the host.
	put(t, filepath.Join(w.Directory, ".git"), "gitdir: /does/not/exist\n")
	put(t, filepath.Join(w.GitDir, "config"), "not even valid Git configuration\n")
	again, err := m.PrepareWorker(context.Background(), r.Goal, r.Member, w.Task, w.Attempt, w.Base)
	if err != nil || again != w {
		t.Fatalf("resumed worker read Git metadata: %v", err)
	}
	c := stage(t, m, w, "op1", r.Start, head)
	advance(t, m, c)
}

func TestStageRejectsWrongHeadAndScope(t *testing.T) {
	for _, tc := range []string{"head", "scope", "symlink", "oversize", "reserved"} {
		t.Run(tc, func(t *testing.T) {
			m, r := fixture(t)
			w := worker(t, m, r, "task1", "attempt1", r.Start)
			head := submit(t, m, w, "app.txt", "change\n")
			var allowed []string
			switch tc {
			case "head":
				head = r.Start
			case "scope":
				allowed = []string{"frontend"}
			case "symlink":
				bundle := filepath.Join(w.Directory, ".lola", "submission.bundle")
				if err := os.Rename(bundle, bundle+".real"); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(bundle+".real", bundle); err != nil {
					t.Fatal(err)
				}
			case "oversize":
				m.MaxBundleBytes = 1
			case "reserved":
				put(t, filepath.Join(w.Directory, ".lola", "payload"), "tracked control file")
				gitOK(t, m, w.Directory, "add", "-f", ".lola/payload")
				gitOK(t, m, w.Directory, "commit", "-m", "control files")
				head = gitOK(t, m, w.Directory, "rev-parse", "HEAD")
				gitOK(t, m, w.Directory, "bundle", "create", filepath.Join(w.Directory, ".lola", "submission.bundle"), "HEAD", "^"+w.Base)
			}
			if _, err := m.Stage(context.Background(), w, "op1", r.Start, head, allowed); err == nil {
				t.Fatal("unsafe output accepted")
			}
			if tip, _ := m.Tip(context.Background(), r.Goal, r.Member); tip != r.Start {
				t.Fatal("rejected bundle changed goal")
			}
		})
	}
}

func TestIntegrationStaleBaseAndConflictPreserveWork(t *testing.T) {
	m, r := fixture(t)
	a := worker(t, m, r, "a", "attempt1", r.Start)
	b := worker(t, m, r, "b", "attempt1", r.Start)
	ah := submit(t, m, a, "app.txt", "a\n")
	bh := submit(t, m, b, "app.txt", "b\n")
	ac := stage(t, m, a, "opa", r.Start, ah)
	bc := stage(t, m, b, "opb", r.Start, bh)
	advance(t, m, ac)
	if _, err := m.Advance(context.Background(), bc, proof(bc), "policy1", []string{"tests"}, []string{"security"}); !errors.Is(err, ErrStale) {
		t.Fatalf("stale verification accepted: %v", err)
	}
	if _, err := m.Stage(context.Background(), b, "opb2", ac.Commit, bh, nil); !errors.Is(err, ErrConflict) {
		t.Fatalf("conflict not surfaced: %v", err)
	}
	conflict := filepath.Join(m.Root, r.Goal, "candidates", "opb2", r.Member, "tree", "app.txt")
	content, err := os.ReadFile(conflict)
	if err != nil || !strings.Contains(string(content), "<<<<<<<") {
		t.Fatalf("conflict evidence lost: %s %v", content, err)
	}
	if content, err := os.ReadFile(filepath.Join(b.Directory, "app.txt")); err != nil || string(content) != "b\n" {
		t.Fatal("worker evidence changed")
	}
}

func TestIntegrationRecoversCASBeforeReceiptCommit(t *testing.T) {
	m, r := fixture(t)
	w := worker(t, m, r, "task1", "attempt1", r.Start)
	c := stage(t, m, w, "op1", r.Start, submit(t, m, w, "app.txt", "change\n"))
	v := proof(c)
	journal := filepath.Join(filepath.Dir(c.Directory), "integration.json")
	if err := writeJSON(journal, Receipt{Candidate: c, Verification: v, At: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	gitOK(t, m, r.GitDir, "fetch", c.Directory, c.Commit)
	gitOK(t, m, r.GitDir, "update-ref", "refs/heads/"+r.Branch, c.Commit, c.Base)
	restarted := &Manager{Root: m.Root}
	got, err := restarted.Advance(context.Background(), c, v, "policy1", []string{"tests"}, []string{"security"})
	if err != nil || !got.Committed {
		t.Fatalf("recovery failed: %+v %v", got, err)
	}
	v.Manifest = map[string]string{c.Member: c.Commit, "other": r.Start}
	if _, err := restarted.Advance(context.Background(), c, v, "policy1", []string{"tests"}, []string{"security"}); err == nil {
		t.Fatal("changed verification silently accepted")
	}
}

func TestVerificationRequiresExactUnambiguousEvidence(t *testing.T) {
	c := Candidate{Goal: "g", Operation: "op", Member: "api", Commit: strings.Repeat("a", 40)}
	for _, tc := range []string{"policy", "head", "operation", "failed", "missing_review", "duplicate", "no_checks"} {
		t.Run(tc, func(t *testing.T) {
			v := proof(c)
			checks := []string{"tests"}
			switch tc {
			case "policy":
				v.Policy = "other"
			case "head":
				v.Manifest[c.Member] = strings.Repeat("b", 40)
			case "operation":
				v.Operation = "other"
			case "failed":
				v.Checks[0].State = "fail"
			case "missing_review":
				v.Reviews = nil
			case "duplicate":
				v.Checks = append(v.Checks, Check{ID: "tests", State: "fail"})
			case "no_checks":
				checks = nil
			}
			if err := v.Validate(c, "policy1", checks, []string{"security"}); err == nil {
				t.Fatal("invalid proof passed")
			}
		})
	}
}

func TestPathsAndReceiptOwnership(t *testing.T) {
	m, r := fixture(t)
	for _, id := range []string{"../escape", ".", "..", "a/b", "-option"} {
		if _, err := m.path(id); err == nil {
			t.Fatalf("accepted %q", id)
		}
	}
	if _, err := m.Initialize(context.Background(), r.Goal, r.Member, r.Source, "other"); err == nil {
		t.Fatal("repository repurposed")
	}
	w := worker(t, m, r, "t", "a", r.Start)
	if !reflect.DeepEqual(w.Base, r.Start) {
		t.Fatal("wrong initial revision")
	}
	link := filepath.Join(m.Root, r.Goal, "attempts", "symlink")
	if err := os.Symlink(t.TempDir(), link); err != nil {
		t.Fatal(err)
	}
	if _, err := m.path(r.Goal, "attempts", "symlink", "a"); err == nil {
		t.Fatal("managed symlink accepted")
	}
}

func TestTaskScopeChecksBothSidesOfRename(t *testing.T) {
	m, r := fixture(t)
	w := worker(t, m, r, "task-rename", "attempt-rename", r.Start)
	if err := os.Mkdir(filepath.Join(w.Directory, "allowed"), 0700); err != nil {
		t.Fatal(err)
	}
	gitOK(t, m, w.Directory, "mv", "app.txt", "allowed/app.txt")
	gitOK(t, m, w.Directory, "commit", "-m", "rename")
	head := gitOK(t, m, w.Directory, "rev-parse", "HEAD")
	gitOK(t, m, w.Directory, "bundle", "create", filepath.Join(w.Directory, ".lola", "submission.bundle"), "HEAD", "^"+w.Base)
	if _, err := m.Stage(context.Background(), w, "rename-check", r.Start, head, []string{"allowed"}); !errors.Is(err, ErrScope) {
		t.Fatalf("rename deleted out-of-scope file: %v", err)
	}
	if tip, err := m.Tip(context.Background(), r.Goal, r.Member); err != nil || tip != r.Start {
		t.Fatal("rejected rename advanced goal", err)
	}
	// When both endpoints belong to the task, the same bundle is admissible.
	if _, err := m.Stage(context.Background(), w, "rename-both", r.Start, head, []string{"allowed", "app.txt"}); err != nil {
		t.Fatal(err)
	}
}
