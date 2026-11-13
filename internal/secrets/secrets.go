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
		return "", errors.New("linear API key: no keychain service or env var configured")
	}
	return "", fmt.Errorf("linear API key not found (tried %s)", strings.Join(tried, ", "))
}

// SandboxClaudeService is the keychain service holding the Claude Code token a
// sandboxed claude authenticates with (`claude setup-token`, stored by
// `lola sandbox token`). The container cannot read the keychain the host
// claude logs in through, so lola reads this item and passes the value in the
// session's 0600 env file.
const SandboxClaudeService = "lola-sandbox-claude"

// Lookup reads the keychain item for service. ok is false when there is none
// (or off macOS). The error never carries the value.
func Lookup(service string) (value string, ok bool, err error) {
	if service == "" {
		return "", false, errors.New("keychain service name is required")
	}
	v, err := keychainLookup(service)
	switch {
	case errors.Is(err, errKeychainNotFound):
		return "", false, nil
	case err != nil:
		return "", false, err
	}
	return v, v != "", nil
}

// Store writes value to the login keychain under service, replacing any
// existing item. The value goes to security(1) on stdin, never argv.
func Store(service, value string) error {
	if service == "" {
		return errors.New("keychain service name is required")
	}
	if value == "" {
		return errors.New("cannot store an empty secret")
	}
	return keychainStore(service, value)
}

// Delete removes service's keychain item. A missing item is not an error.
func Delete(service string) error {
	if service == "" {
		return errors.New("keychain service name is required")
	}
	return keychainDelete(service)
}
