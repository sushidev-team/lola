package main

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// stubLoginShellPATH replaces the login-shell probe for the duration of a test,
// so ensurePATH is exercised without spawning the developer's own shell.
func stubLoginShellPATH(t *testing.T, v string) {
	t.Helper()
	orig := loginShellPATH
	loginShellPATH = func() string { return v }
	t.Cleanup(func() { loginShellPATH = orig })
}

func pathEntries() []string { return filepath.SplitList(os.Getenv("PATH")) }

// The reason the probe exists: version managers (mise, asdf, fnm, volta) put
// `claude` somewhere no static list can predict, and a Finder-launched .app
// inherits none of it.
func TestEnsurePATHAdoptsTheLoginShellPATH(t *testing.T) {
	t.Setenv("PATH", "/usr/bin:/bin")
	stubLoginShellPATH(t, "/Users/x/.local/share/mise/shims:/usr/bin:/bin")

	ensurePATH()
	got := pathEntries()
	if !slices.Contains(got, "/Users/x/.local/share/mise/shims") {
		t.Fatalf("login-shell entry missing from PATH: %v", got)
	}
}

// The login shell's own precedence is preserved, and it goes AHEAD of the
// minimal inherited PATH — a user who installed a newer git meant to use it.
func TestEnsurePATHKeepsLoginPrecedenceAndPrepends(t *testing.T) {
	t.Setenv("PATH", "/usr/bin:/bin")
	stubLoginShellPATH(t, "/opt/homebrew/bin:/first:/second")

	ensurePATH()
	got := pathEntries()
	iBrew := slices.Index(got, "/opt/homebrew/bin")
	iFirst := slices.Index(got, "/first")
	iSecond := slices.Index(got, "/second")
	iUsrBin := slices.Index(got, "/usr/bin")
	if iBrew < 0 || iFirst < 0 || iSecond < 0 || iUsrBin < 0 {
		t.Fatalf("expected every entry present, got %v", got)
	}
	if !(iBrew < iFirst && iFirst < iSecond) {
		t.Errorf("login-shell order not preserved: %v", got)
	}
	if iFirst > iUsrBin {
		t.Errorf("login-shell entries must precede the inherited PATH: %v", got)
	}
}

// A wedged profile, or no $SHELL, must still leave a usable PATH: the static
// list is the floor, and it is a superset of the two Homebrew directories the
// previous implementation added.
func TestEnsurePATHFallsBackToTheStaticList(t *testing.T) {
	t.Setenv("PATH", "/usr/bin:/bin")
	stubLoginShellPATH(t, "")

	ensurePATH()
	got := pathEntries()
	for _, want := range []string{"/opt/homebrew/bin", "/usr/local/bin"} {
		if !slices.Contains(got, want) {
			t.Errorf("static fallback %q missing: %v", want, got)
		}
	}
	// ~/go/bin is where a `go install`ed lola lands — the single most common way
	// to own a CLI the app could not see.
	home, err := os.UserHomeDir()
	if err == nil && !slices.Contains(got, filepath.Join(home, "go", "bin")) {
		t.Errorf("~/go/bin must be expanded and present: %v", got)
	}
}

// A directory must appear once. Duplicates make PATH grow on every launch and
// make the effective precedence hard to reason about.
func TestEnsurePATHDeduplicates(t *testing.T) {
	t.Setenv("PATH", "/usr/bin:/bin:/opt/homebrew/bin")
	stubLoginShellPATH(t, "/opt/homebrew/bin:/usr/bin")

	ensurePATH()
	got := pathEntries()
	seen := map[string]int{}
	for _, p := range got {
		seen[p]++
	}
	for p, n := range seen {
		if n > 1 {
			t.Errorf("%q appears %d times in %v", p, n, got)
		}
	}
}

// ensurePATH runs at startup on every launch, including one where it already
// ran — it must converge, not accumulate.
func TestEnsurePATHIsIdempotent(t *testing.T) {
	t.Setenv("PATH", "/usr/bin:/bin")
	stubLoginShellPATH(t, "/opt/homebrew/bin")

	ensurePATH()
	first := os.Getenv("PATH")
	ensurePATH()
	if second := os.Getenv("PATH"); second != first {
		t.Fatalf("second run changed PATH:\n first  = %s\n second = %s", first, second)
	}
}

// The probe reads its answer from a SENTINEL, not from "the output": login rc
// files print banners, and a PATH assembled from someone's shell greeting would
// be handed straight to exec.
// widenPathProbe lifts the probe's startup budget for a test that spawns a real
// shell and asserts on what it answered.
//
// The production bound is 3 seconds because it runs at app startup and a wedged
// profile must not hold the window. Under `go test ./...` this process is one
// of many spawning children at once, and a probe that misses the deadline
// reports "" — indistinguishable from a shell that printed no sentinel, so the
// test fails claiming the sentinel was not found. The bound is not what is
// under test here; the parsing is.
func widenPathProbe(t *testing.T) {
	t.Helper()
	prev := pathProbeTimeout
	pathProbeTimeout = 60 * time.Second
	t.Cleanup(func() { pathProbeTimeout = prev })
}

func TestLoginShellPATHIgnoresBanners(t *testing.T) {
	sh := filepath.Join(t.TempDir(), "noisyshell")
	script := "#!/bin/sh\n" +
		"echo 'Welcome to your shell!'\n" +
		"echo 'You have mail.'\n" +
		"printf '\\n" + pathSentinel + "%s\\n' \"/from/profile:/usr/bin\"\n"
	if err := os.WriteFile(sh, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SHELL", sh)
	widenPathProbe(t)

	got := loginShellPATH()
	if got != "/from/profile:/usr/bin" {
		t.Fatalf("loginShellPATH() = %q, want the sentinel line only", got)
	}
	if strings.Contains(got, "Welcome") {
		t.Error("banner text leaked into PATH")
	}
}

// A shell that fails, or prints no sentinel, reports nothing — never a partial
// guess the caller would treat as the user's PATH.
func TestLoginShellPATHEmptyOnFailure(t *testing.T) {
	sh := filepath.Join(t.TempDir(), "brokenshell")
	if err := os.WriteFile(sh, []byte("#!/bin/sh\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SHELL", sh)
	if got := loginShellPATH(); got != "" {
		t.Fatalf("loginShellPATH() = %q, want empty", got)
	}

	t.Setenv("SHELL", "")
	if got := loginShellPATH(); got != "" {
		t.Fatalf("loginShellPATH() with no $SHELL = %q, want empty", got)
	}
}
