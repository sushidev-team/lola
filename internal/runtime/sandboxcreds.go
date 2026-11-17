package runtime

import (
	"context"
	"os/exec"
	"strings"
	"time"

	"github.com/sushidev-team/lola/internal/secrets"
)

// gitIdentityTimeout bounds each `git config` on the spawn path.
const gitIdentityTimeout = 5 * time.Second

// HostSandboxCredential is the production Native.SandboxCredential
// (secrets.SandboxCredential without the source).
func HostSandboxCredential(name string) (string, bool) {
	v, _ := secrets.SandboxCredential(name)
	return v, v != ""
}

// HostGitIdentity is the production Native.GitIdentity: user.name and
// user.email as git resolves them for the checkout at dir (its repository
// config, then the user's global one), so a sandboxed agent commits as the
// same person an unsandboxed one would. "" for either when unset or on error.
func HostGitIdentity(dir string) (string, string) {
	get := func(key string) string {
		ctx, cancel := context.WithTimeout(context.Background(), gitIdentityTimeout)
		defer cancel()
		args := []string{"config", "--get", key}
		if dir != "" {
			args = append([]string{"-C", dir}, args...)
		}
		out, err := exec.CommandContext(ctx, "git", args...).Output()
		if err != nil {
			return ""
		}
		v := strings.TrimSpace(string(out))
		if strings.ContainsAny(v, "\n\r") {
			return ""
		}
		return v
	}
	return get("user.name"), get("user.email")
}
