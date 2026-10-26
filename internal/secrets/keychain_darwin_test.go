//go:build darwin

package secrets

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"github.com/creack/pty"
	"io"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// TestStoreLinearAPIKeyPassesServiceAndKeyToSeam verifies the public function
// forwards service+key to the exec seam unchanged.
func TestStoreLinearAPIKeyPassesServiceAndKeyToSeam(t *testing.T) {
	orig := runSecurityAdd
	t.Cleanup(func() { runSecurityAdd = orig })

	var gotService, gotKey string
	runSecurityAdd = func(service, key string) error {
		gotService, gotKey = service, key
		return nil
	}
	if err := StoreLinearAPIKey("lola-linear", "lin_api_secret"); err != nil {
		t.Fatalf("StoreLinearAPIKey: %v", err)
	}
	if gotService != "lola-linear" {
		t.Fatalf("seam got service %q, want lola-linear", gotService)
	}
	if gotKey != "lin_api_secret" {
		t.Fatal("seam received the wrong key")
	}
}

// TestStoreLinearAPIKeyRejectsEmptyInputs verifies validation happens before
// the seam runs, with clean messages and no key involved.
func TestStoreLinearAPIKeyRejectsEmptyInputs(t *testing.T) {
	orig := runSecurityAdd
	t.Cleanup(func() { runSecurityAdd = orig })
	runSecurityAdd = func(string, string) error {
		t.Fatal("seam must not run for invalid input")
		return nil
	}
	if err := StoreLinearAPIKey("", "k"); err == nil {
		t.Fatal("want error for empty service")
	}
	if err := StoreLinearAPIKey("svc", ""); err == nil {
		t.Fatal("want error for empty key")
	}
}

// TestSecurityAddCmdKeepsKeyOffArgv verifies the key is delivered on stdin and
// never placed in argv, where ps(1) could read it.
func TestSecurityAddCmdKeepsKeyOffArgv(t *testing.T) {
	const secret = "lin_api_supersecret_do_not_leak"
	cmd := securityAddCmd(context.Background(), "alice", "lola-linear", secret)

	for _, arg := range cmd.Args {
		if strings.Contains(arg, secret) {
			t.Fatalf("argv leaks the key: %q", arg)
		}
	}
	want := []string{
		"/usr/bin/security", "add-generic-password",
		"-a", "alice", "-s", "lola-linear", "-U", "-w",
	}
	if len(cmd.Args) != len(want) {
		t.Fatalf("argv = %v, want %v", cmd.Args, want)
	}
	for i := range want {
		if cmd.Args[i] != want[i] {
			t.Fatalf("argv[%d] = %q, want %q", i, cmd.Args[i], want[i])
		}
	}
	if cmd.Stdin == nil {
		t.Fatal("key must be provided on stdin")
	}
	got, err := io.ReadAll(cmd.Stdin)
	if err != nil {
		t.Fatalf("read stdin: %v", err)
	}
	// `-w` with no value prompts for the password and a confirmation retype,
	// so the key must be piped twice (still off argv).
	if string(got) != secret+"\n"+secret+"\n" {
		t.Fatalf("stdin = %q, want key piped twice (password + retype)", got)
	}
}

// TestStoreLinearAPIKeyErrorOmitsKey ensures a failing store surfaces an error
// that never contains the key value.
func TestStoreLinearAPIKeyErrorOmitsKey(t *testing.T) {
	orig := runSecurityAdd
	t.Cleanup(func() { runSecurityAdd = orig })
	runSecurityAdd = func(service, key string) error {
		return errors.New("exit status 1")
	}
	const secret = "lin_api_do_not_leak"
	err := StoreLinearAPIKey("lola-linear", secret)
	if err == nil {
		t.Fatal("want error when the store fails")
	}
	if strings.Contains(err.Error(), secret) {
		t.Fatal("error text contains the key")
	}
}

// Exercise a real controlling terminal, not just the command's fields. The
// fake security child must lose /dev/tty and consume its pipe. No keychain item
// or user terminal is touched.
func TestKeychainWriteDoesNotInheritControllingTerminal(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestKeychainCommandHelper$")
	cmd.Env = append(os.Environ(), "LOLA_TEST_KEYCHAIN_HELPER=terminal")
	terminal, err := pty.Start(cmd)
	if err != nil {
		t.Fatal(err)
	}
	defer terminal.Close()
	// Drain the PTY while the child exits, and close it on cancellation so
	// terminal teardown is bounded as well as the helper process itself.
	go func() { _, _ = io.Copy(io.Discard, terminal) }()
	go func() { <-ctx.Done(); _ = terminal.Close() }()
	if err := cmd.Wait(); err != nil {
		t.Fatalf("keychain helper inherited a controlling terminal or lost its input: %v", err)
	}
}

func TestKeychainCommandCancellationStopsHungChild(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd := fakeSecurityCommand(ctx, "wait")
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	cmd.Stderr = io.Discard
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	ready, err := bufio.NewReader(stdout).ReadString('\n')
	if err != nil || ready != "ready\n" {
		cancel()
		_ = cmd.Wait()
		t.Fatal("helper did not start")
	}
	started := time.Now()
	cancel()
	if err := cmd.Wait(); err == nil || cmd.ProcessState == nil {
		t.Fatal("cancel did not stop the helper")
	}
	if time.Since(started) > 3*time.Second {
		t.Fatal("cancel did not bound the command")
	}
}

func fakeSecurityCommand(ctx context.Context, mode string) *exec.Cmd {
	cmd := securityAddCmd(ctx, "test-user", "test-service", "synthetic-key")
	cmd.Path = os.Args[0]
	cmd.Args = []string{os.Args[0], "-test.run=^TestKeychainCommandHelper$"}
	cmd.Env = append(os.Environ(), "LOLA_TEST_KEYCHAIN_HELPER="+mode)
	return cmd
}

func TestKeychainCommandHelper(t *testing.T) {
	switch os.Getenv("LOLA_TEST_KEYCHAIN_HELPER") {
	case "terminal":
		terminal, err := os.OpenFile("/dev/tty", os.O_RDWR, 0)
		if err != nil {
			os.Exit(10)
		}
		_ = terminal.Close()
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if err := fakeSecurityCommand(ctx, "detached").Run(); err != nil {
			os.Exit(11)
		}
	case "detached":
		terminal, err := os.OpenFile("/dev/tty", os.O_RDWR, 0)
		if err == nil {
			_ = terminal.Close()
			os.Exit(12)
		}
		input, err := io.ReadAll(os.Stdin)
		if err != nil || string(input) != "synthetic-key\nsynthetic-key\n" {
			os.Exit(13)
		}
	case "wait":
		_, _ = fmt.Fprintln(os.Stdout, "ready")
		for {
			time.Sleep(time.Hour)
		}
	}
}
