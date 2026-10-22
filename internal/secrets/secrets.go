// Package secrets resolves the Linear API key from the macOS Keychain or an
// environment variable. Key values must never appear in logs or errors.
package secrets

import (
	"errors"
	"fmt"
	"os"
	"strings"
)

// errKeychainNotFound is returned by keychainLookup when no matching item
// exists; LinearAPIKey then falls through to the env var. Any other keychain
// error is fatal (a locked/broken keychain should surface, not be masked).
var errKeychainNotFound = errors.New("keychain item not found")

// LinearAPIKey resolves the Linear API key, trying the keychain service
// first (if keychainService is non-empty), then the environment variable
// (if envVar is non-empty). The error names the sources tried but never
// includes any looked-up value.
func LinearAPIKey(keychainService, envVar string) (string, error) {
	return Resolve("linear API key", keychainService, envVar)
}

// Resolve is LinearAPIKey for any named secret (the Linear agent's client
// secret, OAuth token and webhook secret use it): keychain service first, then
// the env var. what names the secret in errors; the value never appears.
func Resolve(what, keychainService, envVar string) (string, error) {
	var tried []string
	if keychainService != "" {
		key, err := keychainLookup(keychainService)
		switch {
		case err == nil && key != "":
			return key, nil
		case err != nil && !errors.Is(err, errKeychainNotFound):
			return "", err
		}
		tried = append(tried, fmt.Sprintf("keychain service %q", keychainService))
	}
	if envVar != "" {
		if key := os.Getenv(envVar); key != "" {
			return key, nil
		}
		tried = append(tried, fmt.Sprintf("env var %s", envVar))
	}
	if len(tried) == 0 {
		return "", fmt.Errorf("%s: no keychain service or env var configured", what)
	}
	return "", fmt.Errorf("%s not found (tried %s)", what, strings.Join(tried, ", "))
}

// Store writes value to the login keychain under service (macOS only), via the
// same stdin-fed security(1) path as StoreLinearAPIKey: never in argv, never in
// an error.
func Store(service, value string) error { return StoreLinearAPIKey(service, value) }
