package secrets

import (
	"strings"
	"testing"
)

func TestLinearAPIKeyEnvFallback(t *testing.T) {
	const envVar = "LOLA_TEST_LINEAR_KEY"
	t.Setenv(envVar, "lin_api_secret")

	key, err := LinearAPIKey("", envVar)
	if err != nil {
		t.Fatalf("LinearAPIKey: %v", err)
	}
	if key != "lin_api_secret" {
		t.Fatalf("got %q, want env value", key)
	}
}

func TestLinearAPIKeyMissing(t *testing.T) {
	const envVar = "LOLA_TEST_LINEAR_KEY_UNSET"
	t.Setenv(envVar, "")

	_, err := LinearAPIKey("", envVar)
	if err == nil {
		t.Fatal("want error when env var is empty")
	}
	if !strings.Contains(err.Error(), envVar) {
		t.Fatalf("error should name the env var tried: %v", err)
	}
}

func TestLinearAPIKeyNoSources(t *testing.T) {
	_, err := LinearAPIKey("", "")
	if err == nil {
		t.Fatal("want error when no sources are configured")
	}
	if !strings.Contains(err.Error(), "no keychain service or env var configured") {
		t.Fatalf("unexpected error text: %v", err)
	}
}

// TestLinearAPIKeyErrorOmitsSecretValues verifies the failure error names
// only the sources tried, never any value present in the environment.
func TestLinearAPIKeyErrorOmitsSecretValues(t *testing.T) {
	const secret = "lin_api_supersecret_do_not_leak"
	t.Setenv("LOLA_TEST_PRESENT_KEY", secret)

	// Resolution fails (different, unset var) while a secret sits in the
	// environment; the error must not surface it.
	_, err := LinearAPIKey("", "LOLA_TEST_ABSENT_KEY")
	if err == nil {
		t.Fatal("want error for unset env var")
	}
	msg := err.Error()
	if strings.Contains(msg, secret) {
		t.Fatal("error text contains a secret value")
	}
	if !strings.Contains(msg, "LOLA_TEST_ABSENT_KEY") {
		t.Fatalf("error should name the env var tried: %v", err)
	}
}

func TestLinearAPIKeyPrefersNonEmptyEnvValue(t *testing.T) {
	const envVar = "LOLA_TEST_LINEAR_KEY_WS"
	// Whitespace-only is still a value: LinearAPIKey does not trim env vars.
	t.Setenv(envVar, " padded ")
	key, err := LinearAPIKey("", envVar)
	if err != nil {
		t.Fatalf("LinearAPIKey: %v", err)
	}
	if key != " padded " {
		t.Fatalf("got %q, want env value passed through verbatim", key)
	}
}
