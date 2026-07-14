//go:build darwin

package secrets

import (
	"errors"
	"io"
	"strings"
	"testing"
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
	cmd := securityAddCmd("alice", "lola-linear", secret)

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
