package reviewverify

import (
	"context"
	"encoding/csv"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sushidev-team/lola/internal/goalpolicy"
	"github.com/sushidev-team/lola/internal/reviewpack"
)

func fixture(t *testing.T) (Runner, reviewpack.Package, string, goalpolicy.Check) {
	t.Helper()
	home, source := t.TempDir(), t.TempDir()
	git := func(args ...string) string {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", source, "-c", "core.hooksPath=/dev/null", "-c", "commit.gpgsign=false", "-c", "user.name=Test", "-c", "user.email=test@localhost"}, args...)...)
		cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1")
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("%v %s", err, out)
		}
		return strings.TrimSpace(string(out))
	}
	git("init", "-b", "main")
	os.WriteFile(filepath.Join(source, "source.txt"), []byte("committed"), 0600)
	git("add", "source.txt")
	git("commit", "-m", "initial")
	sha := git("rev-parse", "HEAD")
	p, err := reviewpack.Publish(home, "worker", source, "acme/repo", 1, reviewpack.Manifest{Version: 1, Title: "Review", Body: "Review", Base: sha, Head: sha})
	if err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(source, "source.txt"), []byte("dirty worker output"), 0600)
	return Runner{Home: home}, p, source, goalpolicy.Check{ID: "unit", Command: "./test.sh", Image: "sha256:" + strings.Repeat("a", 64), TimeoutSeconds: 10}
}

func TestExactCommitIsolationAndDurableRequest(t *testing.T) {
	r, p, source, spec := fixture(t)
	v, fresh, err := r.Reserve(p, strings.Repeat("1", 32), spec)
	if err != nil || !fresh {
		t.Fatal(err)
	}
	runs := 0
	r.Run = func(_ context.Context, _ string, args []string) (string, error) {
		if args[0] == "rm" {
			return "", nil
		}
		runs++
		joined := strings.Join(args, " ")
		for _, guard := range []string{"--network none", "--read-only", "--cap-drop ALL", "--pull never", "--memory 2048m", "--workdir /workspace/repo"} {
			if !strings.Contains(joined, guard) {
				t.Fatalf("missing isolation %s", guard)
			}
		}
		for i, a := range args {
			if a != "--mount" {
				continue
			}
			fields, _ := csv.NewReader(strings.NewReader(args[i+1])).Read()
			for _, field := range fields {
				if !strings.HasPrefix(field, "source=") {
					continue
				}
				root := strings.TrimPrefix(field, "source=")
				data, err := os.ReadFile(filepath.Join(root, "repo", "source.txt"))
				if err != nil || string(data) != "committed" {
					t.Fatalf("dirty input copied: %s %v", data, err)
				}
				os.WriteFile(filepath.Join(root, "repo", "source.txt"), []byte("check mutation"), 0600)
			}
		}
		return "independent check passed", nil
	}
	if err := r.Execute(context.Background(), v, source); err != nil {
		t.Fatal(err)
	}
	all, err := (Runner{Home: r.Home}).List(p)
	if err != nil || len(all) != 1 || all[0].State != "pass" || !all[0].Stopped || all[0].Head != p.Head {
		t.Fatalf("receipt: %+v %v", all, err)
	}
	if _, fresh, err := r.Reserve(p, v.ID, spec); err != nil || fresh || runs != 1 {
		t.Fatalf("repeat: %v %v %d", fresh, err, runs)
	}
	spec.Command = "different"
	if _, _, err := r.Reserve(p, v.ID, spec); err == nil {
		t.Fatal("rebound identity")
	}
	data, _ := os.ReadFile(filepath.Join(source, "source.txt"))
	if string(data) != "dirty worker output" {
		t.Fatal("source changed")
	}
	p.Head = strings.Repeat("d", 40)
	if _, err := r.List(p); err == nil {
		t.Fatal("receipt moved to another revision")
	}
}

func TestInterruptedVerificationNeverRetriesOrPasses(t *testing.T) {
	r, p, _, spec := fixture(t)
	v, _, err := r.Reserve(p, strings.Repeat("2", 32), spec)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := r.Reserve(p, strings.Repeat("3", 32), spec); err == nil {
		t.Fatal("overlapped unsettled check")
	}
	v, err = r.Recover(context.Background(), v)
	if err != nil || v.State != "interrupted" || !v.Stopped {
		t.Fatalf("recover: %+v %v", v, err)
	}
	if _, fresh, err := r.Reserve(p, strings.Repeat("3", 32), spec); err != nil || !fresh {
		t.Fatal(err)
	}
}

func TestCheckFailureAndUnavailableCleanup(t *testing.T) {
	for _, cleanupFails := range []bool{false, true} {
		t.Run(map[bool]string{false: "failure", true: "unknown"}[cleanupFails], func(t *testing.T) {
			r, p, source, spec := fixture(t)
			v, _, err := r.Reserve(p, strings.Repeat("4", 32), spec)
			if err != nil {
				t.Fatal(err)
			}
			r.Run = func(_ context.Context, _ string, args []string) (string, error) {
				if args[0] == "rm" {
					if cleanupFails {
						return "", errors.New("Docker unavailable")
					}
					return "", nil
				}
				return "rejected", exec.Command("sh", "-c", "exit 1").Run()
			}
			if err := r.Execute(context.Background(), v, source); err != nil {
				t.Fatal(err)
			}
			all, _ := r.List(p)
			want := "fail"
			if cleanupFails {
				want = "error"
			}
			if all[0].State != want || all[0].Stopped == cleanupFails {
				t.Fatalf("%+v", all[0])
			}
		})
	}
}

func TestRejectsUnboundedOrAgentSuppliedPolicy(t *testing.T) {
	r, p, _, spec := fixture(t)
	for _, change := range []func(*goalpolicy.Check){func(s *goalpolicy.Check) { s.Image = "latest" }, func(s *goalpolicy.Check) { s.TimeoutSeconds = 0 }, func(s *goalpolicy.Check) { s.MemoryMiB = 9000 }, func(s *goalpolicy.Check) { s.Member = "foreign" }, func(s *goalpolicy.Check) {
		s.Artifacts = []goalpolicy.ArtifactSpec{{ID: "a", Kind: "test", Path: "out"}}
	}} {
		bad := spec
		change(&bad)
		if _, _, err := r.Reserve(p, strings.Repeat("5", 32), bad); err == nil {
			t.Fatalf("accepted %+v", bad)
		}
	}
}

func TestReviewVerificationDockerBoundary(t *testing.T) {
	image := os.Getenv("LOLA_REVIEW_CHECK_IMAGE")
	if image == "" {
		t.Skip("set LOLA_REVIEW_CHECK_IMAGE to a locally available digest-pinned image with sh")
	}
	r, p, source, spec := fixture(t)
	spec.Image = image
	spec.Command = `test "$(cat source.txt)" = committed && test -z "$GITHUB_TOKEN" && test ! -e /var/run/docker.sock && test ! -e /workspace/repository.json && ! touch /lola-host-write && echo disposable > source.txt`
	v, _, err := r.Reserve(p, strings.Repeat("8", 32), spec)
	if err != nil {
		t.Fatal(err)
	}
	if err := r.Execute(context.Background(), v, source); err != nil {
		t.Fatal(err)
	}
	all, err := r.List(p)
	if err != nil || len(all) != 1 || all[0].State != "pass" || !all[0].Stopped {
		t.Fatalf("Docker result: %+v %v", all, err)
	}
	data, _ := os.ReadFile(filepath.Join(source, "source.txt"))
	if string(data) != "dirty worker output" {
		t.Fatal("original checkout modified")
	}
	// A deadline must stop the owned container rather than lose only the CLI.
	spec.Command = "sleep 30"
	spec.TimeoutSeconds = 1
	v, _, err = r.Reserve(p, strings.Repeat("9", 32), spec)
	if err != nil {
		t.Fatal(err)
	}
	if err := r.Execute(context.Background(), v, source); err != nil {
		t.Fatal(err)
	}
	all, err = r.List(p)
	if err != nil || all[0].State != "error" || !all[0].Stopped {
		t.Fatalf("timeout result: %+v %v", all, err)
	}
}
