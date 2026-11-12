package runtime

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sushidev-team/lola/internal/agent"
	"github.com/sushidev-team/lola/internal/config"
)

// fakeDocker scripts the docker CLI for SandboxLola: image IDs by image, which
// images a pull makes appear, and failures by subcommand.
type fakeDocker struct {
	ids   map[string]string // image -> id ("" = not present)
	pulls map[string]string // image -> id it has after a pull
	fail  map[string]error  // subcommand -> error
	calls []string
}

func (f *fakeDocker) run(_ context.Context, docker string, args ...string) ([]byte, error) {
	f.calls = append(f.calls, strings.Join(args, " "))
	if err := f.fail[args[0]]; err != nil {
		return nil, err
	}
	switch args[0] {
	case "image":
		img := args[len(args)-1]
		if id := f.ids[img]; id != "" {
			return []byte("sha256:" + id + "\n"), nil
		}
		return nil, errors.New("exit status 1: No such image")
	case "pull":
		f.ids[args[1]] = f.pulls[args[1]]
		return nil, nil
	case "create":
		return []byte("cid123\n"), nil
	case "cp":
		if args[1] != "cid123:/usr/local/bin/lola" {
			return nil, errors.New("wrong source " + args[1])
		}
		return nil, os.WriteFile(args[2], []byte("ELF"), 0o600)
	case "rm":
		return nil, nil
	}
	return nil, errors.New("unexpected docker " + args[0])
}

const testImageID = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

func TestSandboxLolaExtractsOncePerImageID(t *testing.T) {
	home := t.TempDir()
	fd := &fakeDocker{ids: map[string]string{"img:1": testImageID}}
	r := &SandboxLola{Home: home, Run: fd.run}

	got, err := r.Resolve(context.Background(), "docker", "img:1")
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(home, "cache", "sandbox", "lola-0123456789ab")
	if got != want || r.Last() != want {
		t.Fatalf("path = %q (last %q), want %q", got, r.Last(), want)
	}
	if fi, err := os.Stat(got); err != nil || fi.Mode().Perm() != 0o755 {
		t.Fatalf("cached lola: %v %v", fi, err)
	}
	if strings.Join(fd.calls, "|") != "image inspect --format {{.Id}} img:1|create img:1|cp cid123:/usr/local/bin/lola "+mustTmp(t, fd.calls[2])+"|rm -f cid123" {
		t.Fatalf("calls: %q", fd.calls)
	}

	// Cache hit: one inspect, nothing copied.
	fd.calls = nil
	if again, err := r.Resolve(context.Background(), "docker", "img:1"); err != nil || again != want {
		t.Fatalf("again = %q, %v", again, err)
	}
	if len(fd.calls) != 1 {
		t.Fatalf("cache hit ran %q", fd.calls)
	}

	// A new image ID (an updated tag) is extracted afresh.
	fd.ids["img:1"] = strings.Repeat("f", 64)
	fresh, err := r.Resolve(context.Background(), "docker", "img:1")
	if err != nil || fresh == want || filepath.Base(fresh) != "lola-ffffffffffff" {
		t.Fatalf("fresh = %q, %v", fresh, err)
	}
	if _, err := os.Stat(want); err != nil {
		t.Fatal("a recent older binary must stay: a live container may mount it")
	}
	if left, _ := filepath.Glob(filepath.Join(home, "cache", "sandbox", "*.tmp-*")); len(left) != 0 {
		t.Fatalf("temp files left: %v", left)
	}
}

func mustTmp(t *testing.T, call string) string {
	t.Helper()
	f := strings.Fields(call)
	if len(f) != 3 || !strings.Contains(f[2], ".tmp-") {
		t.Fatalf("cp target is not a temp file: %q", call)
	}
	return f[2]
}

func TestSandboxLolaPullsAMissingImage(t *testing.T) {
	fd := &fakeDocker{ids: map[string]string{}, pulls: map[string]string{"img:1": testImageID}}
	r := &SandboxLola{Home: t.TempDir(), Run: fd.run}
	if _, err := r.Resolve(context.Background(), "docker", "img:1"); err != nil {
		t.Fatal(err)
	}
	if fd.calls[1] != "pull img:1" {
		t.Fatalf("calls: %q", fd.calls)
	}
}

func TestSandboxLolaFailsClearlyWithoutDocker(t *testing.T) {
	for _, tc := range []struct {
		name string
		fail map[string]error
		want string
	}{
		{"pull fails", map[string]error{"pull": errors.New("exit status 1: denied")}, "pulling img:1 failed"},
		{"copy fails", map[string]error{"cp": errors.New("exit status 1: no such file")}, "copying lola out of img:1"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ids := map[string]string{"img:1": testImageID}
			if tc.name == "pull fails" {
				ids = map[string]string{}
			}
			fd := &fakeDocker{ids: ids, fail: tc.fail}
			home := t.TempDir()
			r := &SandboxLola{Home: home, Run: fd.run}
			_, err := r.Resolve(context.Background(), "docker", "img:1")
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want %q", err, tc.want)
			}
			if r.Last() != "" {
				t.Fatal("a failure must not leave a path to mount")
			}
			if left, _ := filepath.Glob(filepath.Join(home, "cache", "sandbox", "*")); len(left) != 0 {
				t.Fatalf("left behind: %v", left)
			}
		})
	}
}

// With no [sandbox].lola_bin the launch mounts the binary taken out of the
// default image, and a docker that cannot provide it refuses the spawn.
func TestSandboxedSpawnUsesTheImagesLola(t *testing.T) {
	f := sandboxFixture(t)
	f.n.Cfg.Sandbox.LolaBin = ""
	f.n.Cfg.Sandbox.Image = ""
	img := config.DefaultSandboxImage()
	fd := &fakeDocker{ids: map[string]string{img: testImageID}}
	f.n.SandboxLola = &SandboxLola{Home: t.TempDir(), Run: fd.run}
	f.n.RemoveContainer = func(context.Context, string) error { return nil }

	s, err := f.n.Spawn(context.Background(), f.p, issueENG42(), "")
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	cached := f.n.SandboxLola.Last()
	line := f.n.launchLine(f.p.Name, s.ID, agent.Claude, defaultLaunchPrompt(s.ID), "", false)
	for _, must := range []string{
		shQuote(cached + ":/usr/local/bin/lola:ro"),
		shQuote(img),
	} {
		if !strings.Contains(line, strings.ReplaceAll(must, "'", `'\''`)) && !strings.Contains(line, must) {
			t.Errorf("launch line missing %q:\n%s", must, line)
		}
	}

	f2 := sandboxFixture(t)
	f2.n.Cfg.Sandbox.LolaBin = ""
	f2.n.SandboxLola = &SandboxLola{Home: t.TempDir(), Run: (&fakeDocker{ids: map[string]string{}, fail: map[string]error{"pull": errors.New("exit status 1: daemon down")}}).run}
	if _, err := f2.n.Spawn(context.Background(), f2.p, issueENG42(), ""); err == nil || !strings.Contains(err.Error(), "pulling") {
		t.Fatalf("spawn without docker: %v", err)
	}
	if _, err := os.Stat(filepath.Join(f2.root, "nori")); err == nil {
		if entries, _ := os.ReadDir(filepath.Join(f2.root, "nori")); len(entries) != 0 {
			t.Fatalf("a refused spawn left a worktree: %v", entries)
		}
	}
}

// A launch line built while no linux lola is known refuses to start the agent
// rather than run one the daemon cannot hear.
func TestSandboxLineWithoutLolaRefusesToStart(t *testing.T) {
	f := sandboxFixture(t)
	f.n.Cfg.Sandbox.LolaBin = ""
	if line := f.n.sandboxLine(&f.p, "lola-nori-eng-42", agent.Claude, "exec claude"); line != sandboxHooklessGuard {
		t.Fatalf("line = %s", line)
	}
	if line := f.n.sandboxScratchLine("lola-local-x", t.TempDir(), agent.Claude, "exec claude"); line != sandboxHooklessGuard {
		t.Fatalf("scratch line = %s", line)
	}
}

func TestSandboxEnvCredentialsAndIdentity(t *testing.T) {
	f := sandboxFixture(t)
	f.n.Cfg.Sandbox.PassEnv = nil // the defaults
	for _, k := range config.DefaultSandboxPassEnv {
		t.Setenv(k, "")
		os.Unsetenv(k)
	}
	t.Setenv("ANTHROPIC_API_KEY", "sk-env")
	var asked []string
	f.n.SandboxCredential = func(name string) (string, bool) {
		asked = append(asked, name)
		switch name {
		case "CLAUDE_CODE_OAUTH_TOKEN":
			return "sk-ant-oat-keychain", true
		case "ANTHROPIC_API_KEY":
			return "sk-should-not-win", true
		}
		return "", false
	}
	f.n.GitIdentity = func(dir string) (string, string) {
		if dir != f.p.Path {
			t.Errorf("identity asked of %q, want the main checkout", dir)
		}
		return "Ada Lovelace", "ada@example.com"
	}
	id := "lola-nori-eng-42"
	env := string(f.n.envFile(f.p, id, filepath.Join(f.root, "nori", id), agent.Claude))
	for _, must := range []string{
		"IS_SANDBOX=1\n",
		"CLAUDE_CODE_OAUTH_TOKEN=sk-ant-oat-keychain\n",
		"ANTHROPIC_API_KEY=sk-env\n",
		"GIT_AUTHOR_NAME='Ada Lovelace'\n", "GIT_AUTHOR_EMAIL=ada@example.com\n",
		"GIT_COMMITTER_NAME='Ada Lovelace'\n", "GIT_COMMITTER_EMAIL=ada@example.com\n",
	} {
		if !strings.Contains(env, must) {
			t.Errorf("env missing %q:\n%s", must, env)
		}
	}
	if strings.Contains(env, "GH_TOKEN") || strings.Contains(env, "sk-should-not-win") {
		t.Errorf("env:\n%s", env)
	}
	if strings.Join(asked, ",") != "CLAUDE_CODE_OAUTH_TOKEN,GH_TOKEN" {
		t.Errorf("fallback asked for %v; the environment answers first", asked)
	}

	// An explicit pass_env is the whole list: nothing else is copied.
	f.n.Cfg.Sandbox.PassEnv = []string{"ANTHROPIC_API_KEY"}
	if env := string(f.n.envFile(f.p, id, filepath.Join(f.root, "nori", id), agent.Claude)); strings.Contains(env, "CLAUDE_CODE_OAUTH_TOKEN") {
		t.Errorf("explicit pass_env widened:\n%s", env)
	}
}
