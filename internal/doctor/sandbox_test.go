package doctor

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/sushidev-team/lola/internal/config"
	"github.com/sushidev-team/lola/internal/secrets"
)

func stubSandbox(t *testing.T, docker func(args ...string) error, cred func(string) (string, string)) {
	t.Helper()
	d, c := dockerRun, sandboxCredential
	t.Cleanup(func() { dockerRun, sandboxCredential = d, c })
	dockerRun = func(_ context.Context, _ string, args ...string) error { return docker(args...) }
	sandboxCredential = cred
}

func sandboxedConfig() *config.Config {
	return &config.Config{Projects: []config.Project{{Name: "web", Path: "/src/web", Sandbox: config.SandboxDocker, SandboxImage: "acme/web:1"}}}
}

func TestSandboxChecksOnlyWhenSandboxed(t *testing.T) {
	stubSandbox(t, func(...string) error { t.Fatal("docker probed for an unsandboxed config"); return nil },
		func(string) (string, string) { return "", "" })
	if got := sandboxResults(context.Background(), &config.Config{Projects: []config.Project{{Name: "web"}}}); got != nil {
		t.Fatalf("%+v", got)
	}
}

func TestSandboxChecksReportSourcesNeverValues(t *testing.T) {
	stubSandbox(t, func(args ...string) error {
		if args[0] == "image" && args[len(args)-1] == "acme/web:1" {
			return errors.New("no such image")
		}
		return nil
	}, func(name string) (string, string) {
		switch name {
		case "CLAUDE_CODE_OAUTH_TOKEN":
			return "sk-ant-oat-SECRET", secrets.SourceKeychain
		case "GH_TOKEN":
			return "gho_SECRET", secrets.SourceGH
		}
		return "", ""
	})
	res := sandboxResults(context.Background(), sandboxedConfig())
	all := ""
	for _, r := range res {
		all += r.Name + ": " + r.Detail + "\n"
		if !r.OK || r.Critical {
			t.Errorf("%s: %+v", r.Name, r)
		}
	}
	if strings.Contains(all, "SECRET") {
		t.Fatalf("a credential reached the report:\n%s", all)
	}
	for _, must := range []string{"acme/web:1 not pulled yet", "found in keychain", "from gh auth token"} {
		if !strings.Contains(all, must) {
			t.Errorf("missing %q:\n%s", must, all)
		}
	}
	// The default image is checked too: the hooks' linux lola comes from it.
	if st := CheckSandbox(context.Background(), sandboxedConfig()); len(st.Images) != 2 || st.Images[0] != config.DefaultSandboxImage() {
		t.Errorf("images = %v", st.Images)
	}
}

func TestSandboxChecksWithoutDocker(t *testing.T) {
	stubSandbox(t, func(...string) error { return errors.New("exec: not found") }, func(string) (string, string) { return "", "" })
	res := sandboxResults(context.Background(), sandboxedConfig())
	for _, r := range res {
		if r.OK {
			t.Errorf("%s ok without docker or credentials: %+v", r.Name, r)
		}
	}
	if !strings.Contains(res[2].Detail, "lola sandbox token") {
		t.Errorf("no way out named: %q", res[2].Detail)
	}
}
