package doctor

import (
	"context"
	"fmt"
	"os/exec"
	"slices"
	"strings"

	"github.com/sushidev-team/lola/internal/config"
	"github.com/sushidev-team/lola/internal/secrets"
)

// The container sandbox checks, reported only when some project (or a
// production ssh host) runs its agent in a container. None is critical: the
// daemon runs fine, only sandboxed spawns fail, and they say why.
const (
	checkSandboxDocker = "sandbox docker"
	checkSandboxImage  = "sandbox image"
	checkSandboxClaude = "sandbox claude login"
	checkSandboxGitHub = "sandbox github token"
)

// Seams, so tests never run docker, the keychain or gh.
var (
	dockerRun = func(ctx context.Context, docker string, args ...string) error {
		ctx, cancel := context.WithTimeout(ctx, execTimeout)
		defer cancel()
		return exec.CommandContext(ctx, docker, args...).Run()
	}
	sandboxCredential = secrets.SandboxCredential
)

// SandboxStatus is the sandbox's readiness as plain facts, for the doctor
// rows and the desktop settings line. It never carries a credential value.
type SandboxStatus struct {
	Docker       string   `json:"docker"`       // the CLI exec'd
	DockerOK     bool     `json:"dockerOk"`     // the CLI answered and its daemon is up
	DefaultImage string   `json:"defaultImage"` // lola's image for this build
	Images       []string `json:"images"`       // every image a sandboxed project runs
	Missing      []string `json:"missing"`      // of Images, the ones not pulled yet
	ClaudeSource string   `json:"claudeSource"` // secrets.Source*, "" = none
	GitHubSource string   `json:"githubSource"`
	UseAPIKey    bool     `json:"useApiKey"` // [sandbox].use_api_key
	APIKeySet    bool     `json:"apiKeySet"` // ANTHROPIC_API_KEY is in this environment
}

// CheckSandbox gathers SandboxStatus. Nothing is pulled: a missing image is
// pulled on the first spawn.
func CheckSandbox(ctx context.Context, cfg *config.Config) SandboxStatus {
	st := SandboxStatus{Docker: cfg.Sandbox.DockerBin(), DefaultImage: config.DefaultSandboxImage()}
	add := func(img string) {
		if img != "" && !slices.Contains(st.Images, img) {
			st.Images = append(st.Images, img)
		}
	}
	if cfg.Sandbox.LolaBin == "" {
		add(st.DefaultImage) // the hooks' linux lola comes out of it
	}
	for i := range cfg.Projects {
		if cfg.Projects[i].Sandboxed() {
			add(cfg.SandboxImageFor(&cfg.Projects[i]))
		}
	}
	if cfg.AnySSHSandboxed() {
		add(cfg.SandboxImageFor(nil))
	}
	st.DockerOK = dockerRun(ctx, st.Docker, "version", "--format", "{{.Server.Version}}") == nil
	if st.DockerOK {
		for _, img := range st.Images {
			if dockerRun(ctx, st.Docker, "image", "inspect", "--format", "{{.Id}}", img) != nil {
				st.Missing = append(st.Missing, img)
			}
		}
	}
	_, st.ClaudeSource = sandboxCredential("CLAUDE_CODE_OAUTH_TOKEN")
	// The API key counts only when the human switched it on: it bills per
	// token, and claude prefers it over the subscription token.
	st.UseAPIKey = cfg.Sandbox.UseAPIKey
	_, apiSrc := sandboxCredential(config.SandboxAPIKeyEnv)
	st.APIKeySet = apiSrc != secrets.SourceNone
	if st.UseAPIKey && st.APIKeySet {
		st.ClaudeSource = config.SandboxAPIKeyEnv + " (" + apiSrc + ", billed per token)"
	}
	_, st.GitHubSource = sandboxCredential("GH_TOKEN")
	return st
}

// sandboxResults renders CheckSandbox as doctor rows, nil when nothing is
// sandboxed.
func sandboxResults(ctx context.Context, cfg *config.Config) []Result {
	if !cfg.AnySandboxed() && !cfg.AnySSHSandboxed() {
		return nil
	}
	st := CheckSandbox(ctx, cfg)
	var out []Result
	if st.DockerOK {
		out = append(out, Result{Name: checkSandboxDocker, OK: true, Detail: st.Docker + " is running"})
	} else {
		out = append(out, Result{Name: checkSandboxDocker, Detail: fmt.Sprintf("%s does not answer: install and start Docker, OrbStack or Colima (or set [sandbox].docker); sandboxed sessions cannot start", st.Docker)})
	}
	switch {
	case !st.DockerOK:
		out = append(out, Result{Name: checkSandboxImage, Detail: "unknown until docker runs: " + strings.Join(st.Images, ", ")})
	case len(st.Missing) > 0:
		out = append(out, Result{Name: checkSandboxImage, OK: true, Detail: strings.Join(st.Missing, ", ") + " not pulled yet; pulled on the first sandboxed spawn"})
	default:
		out = append(out, Result{Name: checkSandboxImage, OK: true, Detail: strings.Join(st.Images, ", ") + " present"})
	}
	// The doctor runs in this process; the daemon's environment may differ,
	// but the keychain and gh are the same for both.
	if st.ClaudeSource != "" {
		out = append(out, Result{Name: checkSandboxClaude, OK: true, Detail: "found in " + st.ClaudeSource})
	} else {
		out = append(out, Result{Name: checkSandboxClaude, Detail: "no token for a sandboxed claude: run `claude setup-token`, then `lola sandbox token` (or export CLAUDE_CODE_OAUTH_TOKEN for the daemon)"})
	}
	if cfg.Sandbox.APIKeyFiltered() {
		out = append(out, Result{Name: checkSandboxClaude, OK: true, Detail: "[sandbox].pass_env lists ANTHROPIC_API_KEY but use_api_key is off, so the key is not passed"})
	}
	if st.GitHubSource != "" {
		out = append(out, Result{Name: checkSandboxGitHub, OK: true, Detail: "from " + st.GitHubSource + " (the sandboxed agent can push and open PRs as you)"})
	} else {
		out = append(out, Result{Name: checkSandboxGitHub, Detail: "no GH_TOKEN and `gh auth token` gave none: sandboxed agents cannot push or open PRs (run `gh auth login`)"})
	}
	return out
}
