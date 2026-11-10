package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func noTTY() (uintptr, bool) { return 0, false }

func TestHubJoinTokenSources(t *testing.T) {
	var errb bytes.Buffer
	tok, err := hubJoinToken([]string{"https://hub"}, "", strings.NewReader("tok-123\n"), &errb, noTTY)
	if err != nil || tok != "tok-123" || errb.Len() != 0 {
		t.Fatalf("stdin: %q %v %q", tok, err, errb.String())
	}

	f := filepath.Join(t.TempDir(), "tok")
	if err := os.WriteFile(f, []byte("  tok-file\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if tok, err := hubJoinToken([]string{"https://hub"}, f, strings.NewReader("ignored"), &errb, noTTY); err != nil || tok != "tok-file" {
		t.Fatalf("file: %q %v", tok, err)
	}
	if tok, err := hubJoinToken([]string{"https://hub"}, "-", strings.NewReader("tok-dash"), &errb, noTTY); err != nil || tok != "tok-dash" {
		t.Fatalf("dash: %q %v", tok, err)
	}

	errb.Reset()
	tok, err = hubJoinToken([]string{"https://hub", "tok-argv"}, "", strings.NewReader(""), &errb, noTTY)
	if err != nil || tok != "tok-argv" || !strings.Contains(errb.String(), "visible in ps") {
		t.Fatalf("argv: %q %v %q", tok, err, errb.String())
	}

	for name, in := range map[string]string{"empty": "", "spaces": "a b", "two lines": "a\nb"} {
		if _, err := hubJoinToken([]string{"https://hub"}, "", strings.NewReader(in), &errb, noTTY); err == nil {
			t.Errorf("%s: want an error", name)
		}
	}
	if _, err := hubJoinToken([]string{"https://hub", "x"}, f, strings.NewReader(""), &errb, noTTY); err == nil {
		t.Error("argument plus --token-file must be refused")
	}
}
