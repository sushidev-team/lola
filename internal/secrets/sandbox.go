package secrets

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"time"
)

// Zero-setup credentials for a SANDBOXED agent. A container cannot read the
// macOS keychain the host claude and gh log in through, so the daemon fills the
// gap from host-side sources and writes the values only into the session's
// 0600 env file. Values never go into config, argv, logs or errors; callers
// that only report (the doctor, the UIs) read the source and drop the value.

// credentialTimeout bounds each host-side lookup on the spawn path.
const credentialTimeout = 5 * time.Second

// Where SandboxCredential found a value. Names only, never values.
const (
	SourceEnv      = "environment"
	SourceKeychain = "keychain"
	SourceGH       = "gh auth token"
	SourceNone     = ""
)

// Exec seams for SandboxCredential.
var (
	sandboxKeychainLookup = Lookup
	ghAuthToken           = func(ctx context.Context) (string, error) {
		cmd := exec.CommandContext(ctx, "gh", "auth", "token")
		cmd.WaitDelay = time.Second
		out, err := cmd.Output() // stderr is dropped, so nothing reaches a log
		return strings.TrimSpace(string(out)), err
	}
)

// SandboxCredential resolves the credential variable name for a sandboxed
// agent: the process environment first; then, for CLAUDE_CODE_OAUTH_TOKEN,
// the keychain item SandboxClaudeService; for GH_TOKEN, the host's
// `gh auth token` (the container agent then holds the user's GitHub token,
// the same power an unsandboxed agent has through gh). source says where the
// value came from, SourceNone when nowhere.
func SandboxCredential(name string) (value, source string) {
	if v := os.Getenv(name); v != "" {
		return v, SourceEnv
	}
	switch name {
	case "CLAUDE_CODE_OAUTH_TOKEN":
		if v, ok, err := sandboxKeychainLookup(SandboxClaudeService); err == nil && ok {
			return v, SourceKeychain
		}
	case "GH_TOKEN":
		ctx, cancel := context.WithTimeout(context.Background(), credentialTimeout)
		defer cancel()
		v, err := ghAuthToken(ctx)
		if v = strings.TrimSpace(v); err == nil && v != "" && !strings.ContainsAny(v, " \t\r\n") {
			return v, SourceGH
		}
	}
	return "", SourceNone
}
