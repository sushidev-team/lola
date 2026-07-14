//go:build darwin

package secrets

import (
	"errors"
	"fmt"
	"os/exec"
	"strings"
)

// security(1) exits with 44 (errSecItemNotFound) when no item matches.
const securityExitNotFound = 44

func keychainLookup(service string) (string, error) {
	// -w prints only the password, so command output must never be
	// included in any error message.
	out, err := exec.Command("/usr/bin/security", "find-generic-password", "-s", service, "-w").Output()
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) && exitErr.ExitCode() == securityExitNotFound {
			return "", errKeychainNotFound
		}
		return "", fmt.Errorf("keychain lookup for service %q failed: %w", service, err)
	}
	return strings.TrimRight(string(out), "\r\n"), nil
}
