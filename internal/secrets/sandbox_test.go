package secrets

import (
	"context"
	"errors"
	"os"
	"testing"
)

func stubSandboxSources(t *testing.T, keychain func(string) (string, bool, error), gh func(context.Context) (string, error)) {
	t.Helper()
	k, g := sandboxKeychainLookup, ghAuthToken
	t.Cleanup(func() { sandboxKeychainLookup, ghAuthToken = k, g })
	sandboxKeychainLookup, ghAuthToken = keychain, gh
}

func TestSandboxCredentialSources(t *testing.T) {
	for _, k := range []string{"CLAUDE_CODE_OAUTH_TOKEN", "GH_TOKEN"} {
		t.Setenv(k, "")
		os.Unsetenv(k)
	}
	var service string
	stubSandboxSources(t,
		func(s string) (string, bool, error) { service = s; return "sk-ant-oat-kc", true, nil },
		func(context.Context) (string, error) { return "gho_host\n", nil })

	if v, src := SandboxCredential("CLAUDE_CODE_OAUTH_TOKEN"); v != "sk-ant-oat-kc" || src != SourceKeychain || service != SandboxClaudeService {
		t.Fatalf("claude: %q %q (service %q)", v, src, service)
	}
	if v, src := SandboxCredential("GH_TOKEN"); v != "gho_host" || src != SourceGH {
		t.Fatalf("gh: %q %q", v, src)
	}
	// Only the two known names have a fallback.
	if v, src := SandboxCredential("ANTHROPIC_API_KEY"); v != "" || src != SourceNone {
		t.Fatalf("api key: %q %q", v, src)
	}
	// The environment wins.
	t.Setenv("GH_TOKEN", "ghp_env")
	if v, src := SandboxCredential("GH_TOKEN"); v != "ghp_env" || src != SourceEnv {
		t.Fatalf("env: %q %q", v, src)
	}
}

func TestSandboxCredentialFailsQuietly(t *testing.T) {
	for _, k := range []string{"CLAUDE_CODE_OAUTH_TOKEN", "GH_TOKEN"} {
		t.Setenv(k, "")
		os.Unsetenv(k)
	}
	stubSandboxSources(t,
		func(string) (string, bool, error) { return "", false, errors.New("locked") },
		func(context.Context) (string, error) { return "multi\nline", nil })
	if v, src := SandboxCredential("CLAUDE_CODE_OAUTH_TOKEN"); v != "" || src != SourceNone {
		t.Fatalf("claude: %q %q", v, src)
	}
	if v, src := SandboxCredential("GH_TOKEN"); v != "" || src != SourceNone {
		t.Fatalf("a malformed gh answer must not be passed on: %q %q", v, src)
	}
}

func TestLookupStoreDeleteValidate(t *testing.T) {
	if _, _, err := Lookup(""); err == nil {
		t.Fatal("empty service")
	}
	if err := Store("svc", ""); err == nil {
		t.Fatal("empty value")
	}
	if err := Store("", "v"); err == nil {
		t.Fatal("empty service")
	}
	if err := Delete(""); err == nil {
		t.Fatal("empty service")
	}
}
