package goalreview

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/sushidev-team/lola/internal/agent"
	"github.com/sushidev-team/lola/internal/goalwork"
	"github.com/sushidev-team/lola/internal/reviewprofile"
	"github.com/sushidev-team/lola/internal/usage"
)

const testImage = "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

func fixture(t *testing.T) (Runner, goalwork.Candidate, reviewprofile.Profile, reviewprofile.Inputs) {
	t.Helper()
	source := t.TempDir()
	git := func(args ...string) {
		cmd := exec.Command("git", append([]string{"-C", source, "-c", "user.name=Test", "-c", "user.email=test@localhost", "-c", "commit.gpgsign=false", "-c", "core.hooksPath=/dev/null"}, args...)...)
		cmd.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null")
		if b, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git: %v %s", err, b)
		}
	}
	git("init", "-b", "main")
	if err := os.WriteFile(filepath.Join(source, "app.txt"), []byte("accepted behavior\n"), 0600); err != nil {
		t.Fatal(err)
	}
	git("add", "app.txt")
	git("commit", "-m", "initial")
	m := &goalwork.Manager{Root: filepath.Join(t.TempDir(), "goals")}
	repo, err := m.Initialize(context.Background(), "goal1", "api", source, "main")
	if err != nil {
		t.Fatal(err)
	}
	c, err := m.FinalCandidate(context.Background(), "goal1", "final1", "api", repo.Start)
	if err != nil {
		t.Fatal(err)
	}
	dir, err := m.ValidationDirectory(c.Goal, "check1")
	if err != nil {
		t.Fatal(err)
	}
	dir = filepath.Join(dir, "artifacts")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "result")
	body := []byte("independent acceptance evidence")
	if err := os.WriteFile(path, body, 0600); err != nil {
		t.Fatal(err)
	}
	in := reviewprofile.Inputs{ContextID: c.Operation, Repo: "org/api", Head: c.Commit, Base: c.Base, Contract: "Preserve accepted behavior", Artifacts: []reviewprofile.Artifact{{ID: "checks/result", Kind: "acceptance", Path: path, SHA256: fmt.Sprintf("%x", sha256.Sum256(body))}}}
	p := reviewprofile.Profile{ID: "backend", Kind: "backend", Provider: "codex-session", Version: "1", Enabled: true, TimeoutSeconds: 30}
	r := Runner{Workspaces: m, Image: "local/reviewer:1", Agent: "codex", Run: func(_ context.Context, _ string, args []string, _ []byte, _ []string) ([]byte, error) {
		switch args[0] {
		case "image":
			return []byte(testImage), nil
		case "run":
			return fixtureAgentEvents(args, resultBody()), nil
		case "rm":
			return nil, nil
		default:
			t.Fatalf("unexpected docker call %v", args)
			return nil, nil
		}
	}}
	return r, c, p, in
}

func resultBody() []byte {
	b, _ := json.Marshal(reviewprofile.Result{Complete: true, Evidence: []reviewprofile.Evidence{{Criterion: "accepted behavior", Observation: "Source and captured check meet the contract", ArtifactID: "checks/result"}}})
	return b
}

func TestReadOnlyBoundaryAllAgents(t *testing.T) {
	for _, kind := range []string{"claude", "codex", "opencode"} {
		t.Run(kind, func(t *testing.T) {
			r, c, p, in := fixture(t)
			r.Agent = kind
			r.PassEnv = []string{"OPENAI_API_KEY", "GH_TOKEN", "LINEAR_API_KEY", "SSH_AUTH_SOCK"}
			t.Setenv("OPENAI_API_KEY", "private-model-key")
			t.Setenv("GH_TOKEN", "private-github-key")
			t.Setenv("LINEAR_API_KEY", "private-linear-key")
			t.Setenv("SSH_AUTH_SOCK", "/private/socket")
			base := r.Run
			calls := 0
			r.Run = func(ctx context.Context, bin string, args []string, input []byte, env []string) ([]byte, error) {
				if args[0] == "run" {
					calls++
					receipt, _, err := r.load(c.Goal, "review1")
					if err != nil || receipt.State != "running" || receipt.Stopped {
						t.Fatalf("no durable launch intent: %+v %v", receipt, err)
					}
					joined := strings.Join(args, " ")
					for _, want := range []string{"--read-only", "--cap-drop ALL", "--security-opt no-new-privileges", "--pull never", "--init", "--user ", "--pids-limit 128", "--memory 1g", "--cpus 2", "--no-healthcheck", "--entrypoint sh " + testImage, "exec env -i", "timeout -s TERM -k 10 30", quote(agent.Parse(kind).Binary())} {
						if !strings.Contains(joined, want) {
							t.Errorf("missing isolation setting %q", want)
						}
					}
					mounts := 0
					for i, a := range args {
						if a == "--mount" {
							mounts++
							value := args[i+1]
							if !strings.HasSuffix(value, ",readonly") || (!strings.Contains(value, "source="+c.Directory+",") && !strings.Contains(value, "reviews/review1/evidence,")) {
								t.Errorf("unexpected mount %s", value)
							}
						}
					}
					if mounts != 2 {
						t.Fatalf("unexpected mounts: %d", mounts)
					}
					for _, secret := range []string{"private-model-key", "private-github-key", "private-linear-key", "SSH_AUTH_SOCK", "GH_TOKEN", "LINEAR_API_KEY"} {
						if strings.Contains(joined, secret) || strings.Contains(string(input), secret) {
							t.Errorf("secret in input/argv %s", secret)
						}
					}
					if !slices.Contains(env, "OPENAI_API_KEY=private-model-key") {
						t.Fatal("explicit model credential missing")
					}
					for _, v := range env {
						if strings.HasPrefix(v, "GH_TOKEN=") || strings.HasPrefix(v, "LINEAR_API_KEY=") || strings.HasPrefix(v, "SSH_AUTH_SOCK=") {
							t.Fatalf("upstream credential passed: %s", strings.SplitN(v, "=", 2)[0])
						}
					}
					if strings.Contains(string(input), in.Artifacts[0].Path) || !strings.Contains(string(input), "/evidence/000") {
						t.Fatal("artifact path not remapped")
					}
					dir, _ := r.Workspaces.ReviewDirectory(c.Goal, "review1")
					b, err := os.ReadFile(filepath.Join(dir, "evidence", "000"))
					if err != nil || string(b) != "independent acceptance evidence" {
						t.Fatal("captured evidence unavailable")
					}
				}
				return base(ctx, bin, args, input, env)
			}
			got, err := r.Review(context.Background(), "review1", c, p, in)
			if err != nil {
				t.Fatal(err)
			}
			if err := r.Evidence(c.Goal, "review1", p.Key(in), got); err != nil {
				t.Fatal(err)
			}
			if _, err := r.Review(context.Background(), "review1", c, p, in); err == nil || calls != 1 {
				t.Fatal("replayed a paid review")
			}
			altered := got
			altered.Evidence = slices.Clone(got.Evidence)
			altered.Evidence[0].Observation = "changed"
			if r.Evidence(c.Goal, "review1", p.Key(in), altered) == nil {
				t.Fatal("changed result accepted")
			}
			r.Model = "different-model"
			if r.Evidence(c.Goal, "review1", p.Key(in), got) == nil {
				t.Fatal("changed provider accepted")
			}
		})
	}
}

func TestFailedReviewCannotPassOrRelaunch(t *testing.T) {
	for _, mode := range []string{"incomplete", "trailing", "oversize", "lost-response", "cleanup", "canceled", "modified-candidate"} {
		t.Run(mode, func(t *testing.T) {
			r, c, p, in := fixture(t)
			base := r.Run
			calls := 0
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			r.Run = func(ctx context.Context, bin string, args []string, input []byte, env []string) ([]byte, error) {
				if args[0] == "run" {
					calls++
					switch mode {
					case "incomplete":
						return []byte(`{"complete":false}`), nil
					case "trailing":
						return append(resultBody(), []byte(" extra")...), nil
					case "oversize":
						return []byte(strings.Repeat("x", 33000)), nil
					case "lost-response":
						return nil, errors.New("connection interrupted")
					case "canceled":
						cancel()
					case "modified-candidate":
						if err := os.WriteFile(filepath.Join(c.Directory, "app.txt"), []byte("changed"), 0600); err != nil {
							t.Fatal(err)
						}
					}
				}
				if mode == "cleanup" && args[0] == "rm" {
					return nil, errors.New("unavailable")
				}
				if mode == "cleanup" && args[0] == "ps" {
					return []byte("still-running"), nil
				}
				return base(ctx, bin, args, input, env)
			}
			got, err := r.Review(ctx, "review1", c, p, in)
			if err == nil {
				t.Fatal("failure passed")
			}
			if r.Evidence(c.Goal, "review1", p.Key(in), got) == nil {
				t.Fatal("failed run counted as evidence")
			}
			if mode == "cleanup" {
				observed, err := r.Usage(c.Goal, "review1")
				if err != nil || observed == nil || usage.Sum(observed.Days).Tokens() != 120 {
					t.Fatal("cleanup failure erased paid usage")
				}
			}
			if _, err := r.Review(context.Background(), "review1", c, p, in); err == nil || calls != 1 {
				t.Fatal("failed run automatically relaunched")
			}
			r.Run = base
			if err := r.Recover(context.Background(), c.Goal, "review1"); err != nil {
				t.Fatal(err)
			}
			receipt, _, err := r.load(c.Goal, "review1")
			if err != nil || !receipt.Stopped || receipt.State == "complete" {
				t.Fatal("recovery promoted failed result")
			}
		})
	}
}

func TestUntrustedEvidenceNeverReachesModel(t *testing.T) {
	for _, mode := range []string{"hash", "escape", "symlink", "directory", "head", "image"} {
		t.Run(mode, func(t *testing.T) {
			r, c, p, in := fixture(t)
			base := r.Run
			calls := 0
			switch mode {
			case "hash":
				in.Artifacts[0].SHA256 = strings.Repeat("0", 64)
			case "escape":
				in.Artifacts[0].Path = filepath.Join(t.TempDir(), "secret")
			case "symlink":
				path := in.Artifacts[0].Path
				if err := os.Rename(path, path+"-original"); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(path+"-original", path); err != nil {
					t.Fatal(err)
				}
			case "directory":
				in.Artifacts[0].Path = filepath.Dir(in.Artifacts[0].Path)
			case "head":
				in.Head = strings.Repeat("0", 40)
			}
			r.Run = func(ctx context.Context, bin string, args []string, input []byte, env []string) ([]byte, error) {
				if args[0] == "run" {
					calls++
				}
				if mode == "image" && args[0] == "image" {
					return []byte("unresolved-tag"), nil
				}
				return base(ctx, bin, args, input, env)
			}
			if _, err := r.Review(context.Background(), "review1", c, p, in); err == nil || calls != 0 {
				t.Fatal("untrusted input reached model")
			}
		})
	}
}

func TestRecoveryStopsOnlyOwnedContainer(t *testing.T) {
	r, c, p, in := fixture(t)
	got, err := r.Review(context.Background(), "review1", c, p, in)
	if err != nil {
		t.Fatal(err)
	}
	old, path, err := r.load(c.Goal, "review1")
	if err != nil {
		t.Fatal(err)
	}
	old.Stopped = false
	old.State = "running"
	if err := save(path, old); err != nil {
		t.Fatal(err)
	}
	calls := 0
	r.Run = func(_ context.Context, _ string, args []string, _ []byte, _ []string) ([]byte, error) {
		calls++
		if !slices.Equal(args, []string{"rm", "--force", ContainerName(c.Goal, "review1")}) {
			t.Fatalf("unexpected recovery command %v", args)
		}
		return nil, nil
	}
	if err := r.Recover(context.Background(), c.Goal, "review1"); err != nil {
		t.Fatal(err)
	}
	if err := r.Recover(context.Background(), c.Goal, "review1"); err != nil || calls != 1 {
		t.Fatal("recovery did not persist cleanup")
	}
	if r.Evidence(c.Goal, "review1", p.Key(in), got) == nil {
		t.Fatal("interrupted result promoted")
	}
}

func fixtureAgentEvents(args []string, body []byte) []byte {
	kind := "codex"
	inner := args[len(args)-1]
	if strings.Contains(inner, "'claude'") {
		kind = "claude"
	} else if strings.Contains(inner, "'opencode'") {
		kind = "opencode"
	}
	var events []any
	switch kind {
	case "claude":
		events = []any{map[string]any{"type": "result", "subtype": "success", "is_error": false, "result": string(body), "usage": map[string]int{"input_tokens": 100, "output_tokens": 20}, "total_cost_usd": 0.001}}
	case "codex":
		events = []any{map[string]any{"type": "item.completed", "item": map[string]string{"type": "agent_message", "text": string(body)}}, map[string]any{"type": "turn.completed", "usage": map[string]int{"input_tokens": 100, "cached_input_tokens": 20, "output_tokens": 20}}}
	case "opencode":
		events = []any{map[string]any{"type": "text", "part": map[string]string{"type": "text", "id": "t1", "messageID": "m1", "text": string(body)}}, map[string]any{"type": "step_finish", "part": map[string]any{"type": "step-finish", "messageID": "m1", "id": "s1", "reason": "stop", "cost": 0.001, "tokens": map[string]int{"input": 100, "output": 20}}}}
	}
	var out []byte
	for _, e := range events {
		b, _ := json.Marshal(e)
		out = append(out, append(b, '\n')...)
	}
	return out
}
