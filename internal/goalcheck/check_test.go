package goalcheck

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/sushidev-team/lola/internal/goalwork"
)

func setup(t *testing.T) (*Runner, Spec, map[string]goalwork.Revision) {
	t.Helper()
	dir := t.TempDir()
	source := filepath.Join(dir, "source")
	if err := os.Mkdir(source, 0700); err != nil {
		t.Fatal(err)
	}
	git := func(args ...string) string {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-c", "core.hooksPath=/dev/null", "-c", "commit.gpgsign=false", "-c", "user.name=Test", "-c", "user.email=test@localhost", "-C", source}, args...)...)
		cmd.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null")
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("fixture git: %v %s", err, out)
		}
		return strings.TrimSpace(string(out))
	}
	git("init", "-b", "main")
	if err := os.WriteFile(filepath.Join(source, "app.txt"), []byte("initial\n"), 0600); err != nil {
		t.Fatal(err)
	}
	git("add", "app.txt")
	git("commit", "-m", "initial")
	m := &goalwork.Manager{Root: filepath.Join(dir, "goals")}
	repo, err := m.Initialize(context.Background(), "goal1", "api", source, "main")
	if err != nil {
		t.Fatal(err)
	}
	spec := Spec{ID: "unit", Command: "./test.sh", Member: "api", Image: "sha256:" + strings.Repeat("a", 64), TimeoutSeconds: 10, Artifacts: []ArtifactSpec{{ID: "test-report", Kind: "integration-check", Path: "api/report.json"}}}
	return &Runner{Workspaces: m}, spec, map[string]goalwork.Revision{"api": {Commit: repo.Start}}
}

func fakeRun(t *testing.T, r *Runner, goal, id string, body func(string) error) *int {
	t.Helper()
	calls := new(int)
	r.Run = func(ctx context.Context, bin string, args []string) (string, error) {
		if args[0] == "rm" {
			return "", nil
		}
		*calls++
		dir, err := r.Workspaces.ValidationDirectory(goal, id)
		if err != nil {
			t.Fatal(err)
		}
		receipt, err := readReceipt(filepath.Join(dir, "check.json"))
		if err != nil || receipt.State != "running" {
			t.Fatalf("no durable intent before check: %+v %v", receipt, err)
		}
		return "checks executed", body(filepath.Join(dir, "workspace"))
	}
	return calls
}

func TestCheckPreservesCanonicalRevisionAndCapturesEvidence(t *testing.T) {
	r, spec, manifest := setup(t)
	calls := fakeRun(t, r, "goal1", "check1", func(dir string) error {
		// Commands may mutate their disposable source and Git metadata. Neither
		// becomes part of the authoritative goal or a later check's input.
		if err := os.WriteFile(filepath.Join(dir, "api", "app.txt"), []byte("test mutation"), 0600); err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(dir, "api", ".git", "config"), []byte("corrupt"), 0600); err != nil {
			return err
		}
		return os.WriteFile(filepath.Join(dir, "api", "report.json"), []byte(`{"passed":true}`), 0600)
	})
	got, err := r.Execute(context.Background(), "goal1", "op1", "check1", "policy1", spec, manifest)
	if err != nil || got.State != "pass" || len(got.Artifacts) != 1 {
		t.Fatalf("check: %+v %v", got, err)
	}
	if got.Artifacts[0].SHA256 == "" || strings.Contains(got.Artifacts[0].Path, "/workspace/") {
		t.Fatal("artifact not captured outside test environment")
	}
	if evidence, err := r.Evidence("goal1", "op1", "policy1", manifest, []Spec{spec}, map[string]string{spec.ID: "check1"}); err != nil || len(evidence) != 1 {
		t.Fatalf("acceptance evidence: %+v %v", evidence, err)
	}
	if _, err := r.Evidence("goal1", "other-op", "policy1", manifest, []Spec{spec}, map[string]string{spec.ID: "check1"}); err == nil {
		t.Fatal("check passed for another operation")
	}
	otherSpec := spec
	otherSpec.Command = "true"
	if _, err := r.Evidence("goal1", "op1", "policy1", manifest, []Spec{otherSpec}, map[string]string{spec.ID: "check1"}); err == nil {
		t.Fatal("check passed for different acceptance command")
	}
	if tip, err := r.Workspaces.Tip(context.Background(), "goal1", "api"); err != nil || tip != manifest["api"].Commit {
		t.Fatal("check changed authoritative revision")
	}
	again, err := r.Execute(context.Background(), "goal1", "op1", "check1", "policy1", spec, manifest)
	if err != nil || *calls != 1 || !reflect.DeepEqual(again.Artifacts, got.Artifacts) {
		t.Fatalf("cached check reran or lost evidence: %d %v", *calls, err)
	}
	fresh, err := r.Workspaces.PrepareValidation(context.Background(), "goal1", "fresh", manifest)
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(fresh.Directory, "api", "app.txt"))
	if err != nil || string(data) != "initial\n" {
		t.Fatal("check mutation contaminated later check")
	}
	if err := os.WriteFile(got.Artifacts[0].Path, []byte("tampered"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Execute(context.Background(), "goal1", "op1", "check1", "policy1", spec, manifest); err == nil {
		t.Fatal("tampered evidence treated as valid")
	}
}

func TestCheckDistinguishesRejectedCodeFromUnknownExecution(t *testing.T) {
	for _, tc := range []struct {
		code string
		want string
	}{{"1", "fail"}, {"125", "error"}, {"126", "error"}, {"137", "error"}} {
		t.Run(tc.code, func(t *testing.T) {
			r, spec, manifest := setup(t)
			fakeRun(t, r, "goal1", "check1", func(string) error { return exec.Command("sh", "-c", "exit "+tc.code).Run() })
			got, err := r.Execute(context.Background(), "goal1", "op1", "check1", "policy1", spec, manifest)
			if err == nil || got.State != tc.want || !got.Stopped {
				t.Fatalf("exit %s: %+v %v", tc.code, got, err)
			}
		})
	}
}

func TestCheckRecoveryRequiresConfirmedContainerStop(t *testing.T) {
	r, spec, manifest := setup(t)
	spec.Artifacts = nil
	r.Run = func(_ context.Context, _ string, args []string) (string, error) {
		if args[0] == "rm" {
			return "", errors.New("Docker unavailable during cleanup")
		}
		return "pass", nil
	}
	got, err := r.Execute(context.Background(), "goal1", "op1", "check1", "policy1", spec, manifest)
	if err == nil || got.State != "error" || got.Stopped {
		t.Fatal("unconfirmed container stop passed")
	}
	if _, err := r.Recover(context.Background(), "goal1", "check1"); err == nil {
		t.Fatal("recovery claimed cleanup without stopping container")
	}
	r.Run = func(context.Context, string, []string) (string, error) { return "", nil }
	got, err = r.Recover(context.Background(), "goal1", "check1")
	if err != nil || !got.Stopped || got.State != "error" {
		t.Fatal("recovery erased the unknown outcome")
	}
	if _, err := r.Execute(context.Background(), "goal1", "op1", "check1", "policy1", spec, manifest); err == nil {
		t.Fatal("cleanup converted unknown check into pass")
	}
}

func TestCheckFailureOrMissingArtifactNeverPasses(t *testing.T) {
	for _, tc := range []string{"exit", "missing", "symlink", "empty", "stop"} {
		t.Run(tc, func(t *testing.T) {
			r, spec, manifest := setup(t)
			fakeRun(t, r, "goal1", "check1", func(dir string) error {
				switch tc {
				case "exit":
					return errors.New("test failed")
				case "missing":
					return nil
				case "symlink":
					return os.Symlink(filepath.Join(dir, "api", "app.txt"), filepath.Join(dir, "api", "report.json"))
				case "empty":
					return os.WriteFile(filepath.Join(dir, "api", "report.json"), nil, 0600)
				default:
					return os.WriteFile(filepath.Join(dir, "api", "report.json"), []byte("passed"), 0600)
				}
			})
			if tc == "stop" {
				prev := r.Run
				r.Run = func(ctx context.Context, bin string, args []string) (string, error) {
					if args[0] == "rm" {
						return "", errors.New("Docker unavailable")
					}
					return prev(ctx, bin, args)
				}
			}
			got, err := r.Execute(context.Background(), "goal1", "op1", "check1", "policy1", spec, manifest)
			if err == nil || got.State == "pass" {
				t.Fatalf("invalid evidence passed: %+v %v", got, err)
			}
			if _, err := r.Execute(context.Background(), "goal1", "op1", "check1", "policy1", spec, manifest); err == nil {
				t.Fatal("failed check passed on retry")
			}
		})
	}
}

func TestInterruptedCheckRequiresRecoveryAndFreshIdentity(t *testing.T) {
	r, spec, manifest := setup(t)
	w, err := r.Workspaces.PrepareValidation(context.Background(), "goal1", "check1", manifest)
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Dir(w.Directory)
	old := Receipt{ID: "check1", Goal: "goal1", Operation: "op1", Policy: "policy1", Spec: spec, Manifest: manifest, State: "running", StartedAt: time.Now().UTC()}
	if err := saveReceipt(filepath.Join(dir, "check.json"), old); err != nil {
		t.Fatal(err)
	}
	calls := 0
	r.Run = func(_ context.Context, _ string, args []string) (string, error) {
		calls++
		if args[0] != "rm" {
			t.Fatal("interrupted check relaunched")
		}
		return "", nil
	}
	if _, err := r.Execute(context.Background(), "goal1", "op1", "check1", "policy1", spec, manifest); err == nil || calls != 0 {
		t.Fatal("ambiguous check resumed automatically")
	}
	got, err := r.Recover(context.Background(), "goal1", "check1")
	if err != nil || got.State != "interrupted" || calls != 1 {
		t.Fatalf("recover: %+v %v", got, err)
	}
	if _, err := r.Execute(context.Background(), "goal1", "op1", "check1", "policy1", spec, manifest); err == nil || calls != 1 {
		t.Fatal("recovered check was rerun")
	}
}

func TestContainerBoundaryAndPolicyValidation(t *testing.T) {
	spec := Spec{ID: "test", Command: "printf '%s' '$TOKEN; literal'", Image: "example/toolchain@sha256:" + strings.Repeat("a", 64), TimeoutSeconds: 60}
	w := goalwork.ValidationWorkspace{Goal: "g", ID: "c", Directory: "/private/path, with spaces/workspace"}
	args, err := containerArgs(w, "op", spec)
	if err != nil {
		t.Fatal(err)
	}
	for _, pair := range [][2]string{{"--network", "none"}, {"--cap-drop", "ALL"}, {"--pull", "never"}, {"--security-opt", "no-new-privileges"}, {"--memory", "2g"}} {
		i := slices.Index(args, pair[0])
		if i < 0 || args[i+1] != pair[1] {
			t.Fatalf("missing isolation flag: %v", pair)
		}
	}
	mounts := 0
	for _, arg := range args {
		if arg == "--mount" {
			mounts++
		}
	}
	if mounts != 1 {
		t.Fatal("unexpected host mounts")
	}
	for _, arg := range args {
		if strings.Contains(arg, "docker.sock") || strings.Contains(arg, "GH_TOKEN") || strings.Contains(arg, "LINEAR_API_KEY") {
			t.Fatal("credentials or control socket exposed")
		}
	}
	if args[len(args)-1] != "mkdir -p /tmp/home && "+spec.Command {
		t.Fatal("command changed while quoting")
	}
	for _, mutation := range []func(*Spec){func(s *Spec) { s.Image = "image:latest" }, func(s *Spec) { s.Member = "../escape" }, func(s *Spec) { s.TimeoutSeconds = 0 }, func(s *Spec) { s.Artifacts = []ArtifactSpec{{ID: "x", Kind: "screenshot", Path: "../../secret"}} }} {
		x := spec
		mutation(&x)
		if x.Validate() == nil {
			t.Fatal("unsafe policy accepted")
		}
	}
}
