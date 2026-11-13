//go:build darwin

package secrets

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/user"
	"strings"
	"syscall"
	"time"
)

// security(1) exits with 44 (errSecItemNotFound) when no item matches.
const securityExitNotFound = 44

const keychainTimeout = 15 * time.Second

// A dev app or TUI can inherit a controlling terminal even when the command's
// stdin is a pipe. getpass prefers /dev/tty, and reading a background terminal
// sends SIGTTIN to the entire process group, stopping Lola too. A fresh session
// has no controlling terminal, so security consumes only the supplied stdin.
func securityCommand(ctx context.Context, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, "/usr/bin/security", args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	cmd.WaitDelay = time.Second
	return cmd
}

func keychainLookup(service string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), keychainTimeout)
	defer cancel()
	// -w prints only the password, so command output must never be
	// included in any error message.
	out, err := securityCommand(ctx, "find-generic-password", "-s", service, "-w").Output()
	if err != nil {
		if ctx.Err() != nil {
			return "", fmt.Errorf("keychain lookup for service %q timed out: %w", service, ctx.Err())
		}
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) && exitErr.ExitCode() == securityExitNotFound {
			return "", errKeychainNotFound
		}
		return "", fmt.Errorf("keychain lookup for service %q failed: %w", service, err)
	}
	return strings.TrimRight(string(out), "\r\n"), nil
}

// StoreLinearAPIKey writes key to the login keychain under service, replacing
// any existing item for that service (-U). The key is handed to security(1) on
// stdin so it never appears in argv (visible via ps) nor in any error returned
// here.
func StoreLinearAPIKey(service, key string) error {
	if service == "" {
		return errors.New("keychain service name is required")
	}
	if key == "" {
		return errors.New("cannot store an empty api key")
	}
	return runSecurityAdd(service, key)
}

// runSecurityAdd is the exec seam: tests override it to assert the service/key
// it receives without invoking security(1). It must never place the key in
// argv or in the error it returns.
var runSecurityAdd = func(service, key string) error {
	ctx, cancel := context.WithTimeout(context.Background(), keychainTimeout)
	defer cancel()
	cmd := securityAddCmd(ctx, currentUsername(), service, key)
	// Discard security's own output so nothing it prints can reach a log.
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return fmt.Errorf("keychain store timed out; unlock the login keychain and try again: %w", ctx.Err())
		}
		// err is "exit status N"; it carries neither argv nor the key.
		return fmt.Errorf("keychain store for service %q failed: %w", service, err)
	}
	return nil
}

// securityAddCmd builds the add-generic-password invocation. The password is
// supplied on stdin: `-w` given no value prompts for the password AND a
// confirmation retype — even when stdin is not a tty — so the key must be
// written twice or security(1) hits EOF on the retype and silently stores an
// empty item while exiting 0. Feeding it on stdin keeps the key out of argv.
func securityAddCmd(ctx context.Context, username, service, key string) *exec.Cmd {
	cmd := securityCommand(ctx, "add-generic-password",
		"-a", username, "-s", service, "-U", "-w")
	cmd.Stdin = strings.NewReader(key + "\n" + key + "\n")
	return cmd
}

// currentUsername resolves the account name for the item's -a field, falling
// back to $USER when the user database is unavailable.
func currentUsername() string {
	if u, err := user.Current(); err == nil && u.Username != "" {
		return u.Username
	}
	return os.Getenv("USER")
}
