package worktree

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/sushidev-team/lola/internal/config"
)

func TestTailBufferKeepsTheLastBytes(t *testing.T) {
	b := &tailBuffer{max: 8}
	for _, s := range []string{"abc", "defgh", "ij"} {
		if n, err := b.Write([]byte(s)); n != len(s) || err != nil {
			t.Fatalf("Write(%q) = %d, %v", s, n, err)
		}
	}
	if got := b.String(); got != "cdefghij" {
		t.Errorf("tail = %q", got)
	}
	b.Write([]byte("0123456789xyz"))
	if got := b.String(); got != "56789xyz" {
		t.Errorf("oversized write: tail = %q", got)
	}
}

// A job a hook backgrounds must not outlive Teardown: Kill removes the
// worktree right after it.
func TestTeardownKillsWhatAHookBackgrounded(t *testing.T) {
	dir := t.TempDir()
	pidFile := filepath.Join(dir, "pid")
	p := config.Project{PreRemove: []string{"sleep 30 >/dev/null 2>&1 & echo $! > " + pidFile}}
	if errs := (&Manager{}).Teardown(context.Background(), p, dir); len(errs) != 0 {
		t.Fatalf("Teardown: %v", errs)
	}
	raw, err := os.ReadFile(pidFile)
	if err != nil {
		t.Fatal(err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(raw)))
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for {
		if err := syscall.Kill(pid, 0); errors.Is(err, syscall.ESRCH) {
			return
		}
		if time.Now().After(deadline) {
			syscall.Kill(pid, syscall.SIGKILL)
			t.Fatalf("backgrounded pid %d still running after Teardown", pid)
		}
		time.Sleep(20 * time.Millisecond)
	}
}
