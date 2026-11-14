package main

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/sushidev-team/lola/hub/internal/console"
)

type lockedBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (l *lockedBuffer) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

func (l *lockedBuffer) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.String()
}

// serve's stderr is the server log (docker logs, journald): it names the
// admin token's file but never prints the token itself.
func TestServeNeverLogsTheAdminToken(t *testing.T) {
	dir := t.TempDir()
	data := filepath.Join(dir, "hub.json")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var errb lockedBuffer
	c := &cli{stdout: io.Discard, stderr: &errb, getenv: func(string) string { return "" }, ctx: ctx}
	done := make(chan int, 1)
	go func() {
		done <- c.run([]string{"serve", "--data", data, "--addr", "127.0.0.1:0", "--admin-addr", "127.0.0.1:0"})
	}()
	deadline := time.After(10 * time.Second)
	for !strings.Contains(errb.String(), "admin token:") {
		select {
		case code := <-done:
			t.Fatalf("serve exited %d: %s", code, errb.String())
		case <-deadline:
			t.Fatalf("serve never announced the console: %s", errb.String())
		case <-time.After(20 * time.Millisecond):
		}
	}
	cancel()
	<-done
	raw, err := os.ReadFile(filepath.Join(dir, console.TokenFile))
	if err != nil {
		t.Fatal(err)
	}
	tok := strings.TrimSpace(string(raw))
	got := errb.String()
	if len(tok) != 64 || strings.Contains(got, tok) {
		t.Fatalf("admin token leaked to stderr: %q", got)
	}
	if !strings.Contains(got, "admin token: created in "+filepath.Join(dir, console.TokenFile)) {
		t.Fatalf("stderr does not name the token file: %q", got)
	}
}
