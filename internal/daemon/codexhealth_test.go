package daemon

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sushidev-team/lola/internal/linear"
)

func TestCodexCapabilityCacheInvalidatesOnReplacement(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "codex")
	calls := filepath.Join(dir, "calls")
	t.Setenv("LOLA_CODEX_PROBE_CALLS", calls)
	write := func(path, body string) {
		t.Helper()
		if err := os.WriteFile(path, []byte("#!/bin/sh\n"+body+"\n"), 0755); err != nil {
			t.Fatal(err)
		}
	}
	write(path, `echo probe >> "$LOLA_CODEX_PROBE_CALLS"; echo --approve-for-me`)
	for i := 0; i < 2; i++ {
		if err := checkCodexCapability(path); err != nil {
			t.Fatal(err)
		}
	}
	out, err := os.ReadFile(calls)
	if err != nil || string(out) != "probe\n" {
		t.Fatalf("expected one probe for unchanged binary: %q, %v", out, err)
	}
	replacement := filepath.Join(dir, "replacement")
	write(replacement, "echo --full-auto")
	if err := os.Rename(replacement, path); err != nil {
		t.Fatal(err)
	}
	if err := checkCodexCapability(path); err == nil || !strings.Contains(err.Error(), "upgrade Codex CLI") {
		t.Fatalf("unsupported replacement must invalidate cache: %v", err)
	}
	write(replacement, "echo --approve-for-me")
	if err := os.Rename(replacement, path); err != nil {
		t.Fatal(err)
	}
	if err := checkCodexCapability(path); err != nil {
		t.Fatalf("upgraded installation must recover: %v", err)
	}
}

func TestTickUnsupportedCodexDoesNotMutateState(t *testing.T) {
	dir := t.TempDir()
	for _, bin := range []string{"tmux", "git", "codex"} {
		if err := os.WriteFile(filepath.Join(dir, bin), []byte("#!/bin/sh\necho --full-auto\n"), 0755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", dir)
	is := testIssue("FE-1", 1, "2024-01-01T00:00:00Z")
	fake := &linear.Fake{Issues: []linear.Issue{is}}
	nat := &fakeNative{}
	p := labelPoll("p1")
	p.Agent = "codex"
	d := newTestDaemon(t, testConfig(p), fake, nat)
	d.runtimeHealth = checkRuntimeHealth
	if _, err := d.tick(context.Background(), "p1", false); err == nil || !strings.Contains(err.Error(), "upgrade Codex CLI") {
		t.Fatalf("unsupported Codex must fail dispatch: %v", err)
	}
	if len(fake.CallNames()) != 0 || len(nat.spawnCalls()) != 0 || d.inflight.Has(is.ID) {
		t.Fatalf("unsupported Codex must not call Linear, spawn, or mark in-flight")
	}
	if _, err := os.Stat(seenPath(d, "p1")); !os.IsNotExist(err) {
		t.Fatalf("unsupported Codex must not write seen state: %v", err)
	}
}
