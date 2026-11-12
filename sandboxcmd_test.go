package main

import (
	"bytes"
	"errors"
	"strings"
	"testing"
)

func TestReadSandboxTokenFromSetupTokenOutput(t *testing.T) {
	out := "✓ Long-lived authentication token created successfully!\n\nYour OAuth token (valid for 1 year):\n\nsk-ant-oat01-AbC_d-9\n\nStore this token securely.\n"
	got, err := readSandboxToken(strings.NewReader(out), &bytes.Buffer{}, noTTY)
	if err != nil || got != "sk-ant-oat01-AbC_d-9" {
		t.Fatalf("%q %v", got, err)
	}
	if got, err := readSandboxToken(strings.NewReader("  plain-token \n"), &bytes.Buffer{}, noTTY); err != nil || got != "plain-token" {
		t.Fatalf("%q %v", got, err)
	}
	for _, bad := range []string{"", "two words", "sk-ant-a sk-ant-b"} {
		if _, err := readSandboxToken(strings.NewReader(bad), &bytes.Buffer{}, noTTY); err == nil {
			t.Errorf("accepted %q", bad)
		} else if strings.Contains(err.Error(), "sk-ant") || (bad != "" && strings.Contains(err.Error(), bad)) {
			t.Errorf("error echoes the input: %v", err)
		}
	}
}

func TestSandboxTokenCommandStoresAndClears(t *testing.T) {
	st, del := sandboxStoreSecret, sandboxDeleteSecret
	t.Cleanup(func() { sandboxStoreSecret, sandboxDeleteSecret = st, del })
	var stored, deleted string
	sandboxStoreSecret = func(service, v string) error { stored = service + "=" + v; return nil }
	sandboxDeleteSecret = func(service string) error { deleted = service; return nil }

	cmd := sandboxCmd()
	cmd.SetArgs([]string{"token"})
	cmd.SetIn(strings.NewReader("sk-ant-oat01-x\n"))
	var out bytes.Buffer
	cmd.SetOut(&out)
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if stored != "lola-sandbox-claude=sk-ant-oat01-x" || strings.Contains(out.String(), "sk-ant") {
		t.Fatalf("stored %q, said %q", stored, out.String())
	}
	cmd = sandboxCmd()
	cmd.SetArgs([]string{"token", "--clear"})
	cmd.SetOut(&out)
	if err := cmd.Execute(); err != nil || deleted != "lola-sandbox-claude" {
		t.Fatalf("clear: %v %q", err, deleted)
	}
	// Never as an argument.
	cmd = sandboxCmd()
	cmd.SetArgs([]string{"token", "sk-ant-oat01-x"})
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	if err := cmd.Execute(); err == nil {
		t.Fatal("accepted the token on argv")
	}
	sandboxStoreSecret = func(string, string) error { return errors.New("keychain locked") }
	cmd = sandboxCmd()
	cmd.SetArgs([]string{"token"})
	cmd.SetIn(strings.NewReader("sk-ant-oat01-x"))
	if err := cmd.Execute(); err == nil || strings.Contains(err.Error(), "sk-ant") {
		t.Fatalf("store failure: %v", err)
	}
}
