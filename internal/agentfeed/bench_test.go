package agentfeed

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// syntheticTranscript writes about size bytes in the proportions the survey of real
// transcripts measured: 42% user records (tool results), 36% request bookkeeping, 10%
// attachments and about 8.5% assistant records, at the worst file's 13 KB average line.
func syntheticTranscript(t testing.TB, path string, size int64) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var b bytes.Buffer
	body := strings.Repeat("output of some tool with \"quotes\" and a path /a/b/c.go\n", 170)
	result, _ := json.Marshal(body)
	bookkeeping := `{"type":"api-request-blob","text":"` + strings.Repeat("x", 18000) + `"}` + "\n"
	attachment := `{"type":"attachment","attachment":{"text":"` + strings.Repeat("y", 5000) + `"}}` + "\n"
	prose := strings.Repeat("A sentence of prose about what happens next. ", 40)
	var written int64
	for i := 0; written < size; i++ {
		b.Reset()
		fmt.Fprintf(&b, `{"parentUuid":"p","type":"assistant","message":{"id":"m%d","content":[{"type":"text","text":"%s"}]},"uuid":"a-%d","timestamp":"2026-11-06T10:00:00Z"}`+"\n", i, prose, i)
		fmt.Fprintf(&b, `{"parentUuid":"p","type":"assistant","message":{"id":"m%d","content":[{"type":"tool_use","id":"t-%d","name":"Read","input":{"file_path":"/w/s/internal/daemon/file%d.go"}}]},"uuid":"b-%d","timestamp":"2026-11-06T10:00:00Z"}`+"\n", i, i, i, i)
		fmt.Fprintf(&b, `{"parentUuid":"p","type":"user","message":{"role":"user","content":[{"tool_use_id":"t-%d","type":"tool_result","content":%s}]},"uuid":"c-%d","timestamp":"2026-11-06T10:00:01Z","toolUseResult":{"stdout":%s}}`+"\n", i, result, i, result)
		b.WriteString(bookkeeping)
		b.WriteString(attachment)
		n, err := f.Write(b.Bytes())
		if err != nil {
			t.Fatal(err)
		}
		written += int64(n)
	}
}

const benchSize = 123 << 20

func firstScan(t testing.TB, root, slug string) time.Duration {
	f := newClaudeFeed(root, slug, segA)
	start := time.Now()
	for f.Scanning() {
		f.Advance(context.Background(), 8<<20)
	}
	return time.Since(start)
}

// BenchmarkClaudeFirstScan measures a first read of a 123 MB transcript, page-cached. Run
// with: go test ./internal/agentfeed -bench FirstScan -run '^$' -benchtime 3x
func BenchmarkClaudeFirstScan(b *testing.B) {
	root := b.TempDir()
	dir := filepath.Join(root, "-w-s")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		b.Fatal(err)
	}
	syntheticTranscript(b, filepath.Join(dir, segA), benchSize)
	firstScan(b, root, "-w-s") // warm the page cache
	b.SetBytes(benchSize)
	b.ResetTimer()
	for b.Loop() {
		firstScan(b, root, "-w-s")
	}
}

// The budget from the plan: a first scan of the worst real file under a second, cached.
// It writes 123 MB, so it runs only when asked for.
func TestFirstScanUnderASecond(t *testing.T) {
	if os.Getenv("LOLA_FEED_BENCH") == "" {
		t.Skip("set LOLA_FEED_BENCH=1 to write a 123 MB file and time a scan of it")
	}
	root := t.TempDir()
	dir := filepath.Join(root, "-w-s")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	syntheticTranscript(t, filepath.Join(dir, segA), benchSize)
	firstScan(t, root, "-w-s")
	if d := firstScan(t, root, "-w-s"); d > time.Second {
		t.Errorf("first scan took %v", d)
	} else {
		t.Logf("first scan of 123 MB took %v", d)
	}
}
