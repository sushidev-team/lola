package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/sushidev-team/lola/internal/protocol"
	"github.com/sushidev-team/lola/internal/scm"
)

func commentOutput(threads []scm.ReviewThread) protocol.PRReadinessData {
	out := protocol.PRReadinessData{}
	for _, thread := range threads {
		out.Threads = append(out.Threads, protocol.ReviewThread{ID: thread.ID, Preview: thread.Preview})
	}
	return out
}

func TestPRCommentsAreAsyncCachedAndContentBound(t *testing.T) {
	d := prsDaemon(t)
	d.cfg.Brain.Enabled = true
	started, release := make(chan struct{}), make(chan struct{})
	calls := 0
	d.commentSummarize = func(ctx context.Context, instruction, input string) (string, error) {
		calls++
		if _, ok := ctx.Deadline(); !ok {
			t.Error("helper has no deadline")
		}
		if !strings.Contains(instruction, "untrusted data") {
			t.Error("missing instruction boundary")
		}
		var items []commentSummaryInput
		if err := json.Unmarshal([]byte(input), &items); err != nil || !strings.Contains(items[0].Body, "actual concern") {
			t.Errorf("lost source: %s", input)
		}
		if calls == 1 {
			close(started)
			<-release
		}
		return `{"t":"Preserve decimal points when parsing thresholds."}`, nil
	}
	threads := []scm.ReviewThread{{ID: "t", Path: "a.ts", Preview: "Script executed", Body: strings.Repeat("evidence ", 100) + "actual concern: preserve decimals"}}
	read := func() protocol.PRReadinessData {
		out := commentOutput(threads)
		d.summarizePRComments(context.Background(), "acme/app", 42, threads, &out)
		return out
	}
	first := read()
	if !first.SummariesPending || first.Threads[0].Preview != "Script executed" {
		t.Fatalf("lost immediate fallback: %+v", first)
	}
	<-started
	if !read().SummariesPending {
		t.Fatal("in-flight summary disappeared")
	}
	close(release)
	d.wg.Wait()
	got := read()
	if calls != 1 || got.SummariesPending || got.Threads[0].Summary != "Preserve decimal points when parsing thresholds." {
		t.Fatalf("cache: %+v, calls=%d", got, calls)
	}
	threads[0].Body += " Changed beyond preview."
	if !read().SummariesPending {
		t.Fatal("edited comment reused stale summary")
	}
	d.wg.Wait()
	if calls != 2 {
		t.Fatalf("edit did not invalidate: %d", calls)
	}
	d.cfg.Brain.Enabled = false
	if got := read(); got.SummariesPending || got.Threads[0].Summary != "" {
		t.Fatal("disabled brain still supplies summaries")
	}
}

func TestPRCommentSummaryFailuresKeepOriginalAndBackOff(t *testing.T) {
	for name, result := range map[string]string{"invalid": "not JSON", "wrong id": `{"other":"wrong finding"}`, "overlong": `{"t":"` + strings.Repeat("a", 241) + `"}`, "partial": `{"t":"valid prefix", "other": 42}`, "error": "error", "panic": "panic"} {
		t.Run(name, func(t *testing.T) {
			d := prsDaemon(t)
			d.cfg.Brain.Enabled = true
			calls := 0
			d.commentSummarize = func(context.Context, string, string) (string, error) {
				calls++
				if result == "error" {
					return "", errors.New("unavailable")
				}
				if result == "panic" {
					panic("helper")
				}
				return result, nil
			}
			threads := []scm.ReviewThread{{ID: "t", Preview: "Original finding", Body: "Original finding"}}
			out := commentOutput(threads)
			d.summarizePRComments(context.Background(), "acme/app", 42, threads, &out)
			d.wg.Wait()
			out = commentOutput(threads)
			d.summarizePRComments(context.Background(), "acme/app", 42, threads, &out)
			if out.SummariesPending || out.Threads[0].Summary != "" || out.Threads[0].Preview != "Original finding" || calls != 1 {
				t.Fatalf("unsafe fallback: %+v calls=%d", out, calls)
			}
		})
	}
}

func TestPRCommentsBoundBatchesAndCancelOnShutdown(t *testing.T) {
	d := prsDaemon(t)
	d.cfg.Brain.Enabled = true
	root, cancel := context.WithCancel(context.Background())
	defer cancel()
	d.shutdownCtx = root
	started := make(chan struct{})
	d.commentSummarize = func(ctx context.Context, _ string, input string) (string, error) {
		var items []commentSummaryInput
		if len(input) > 11000 || !utf8.ValidString(input) || json.Unmarshal([]byte(input), &items) != nil || len(items) != 1 {
			t.Errorf("unbounded/broken batch: %d bytes", len(input))
		}
		if !strings.Contains(items[0].Body, "finding at end") {
			t.Error("lost tail")
		}
		close(started)
		<-ctx.Done()
		return "", ctx.Err()
	}
	threads := []scm.ReviewThread{{ID: "t", Body: strings.Repeat("界\"\n", 8000) + "finding at end"}, {ID: "other", Body: strings.Repeat("x", 10000)}}
	out := commentOutput(threads)
	d.summarizePRComments(context.Background(), "acme/app", 42, threads, &out)
	<-started
	cancel()
	d.wg.Wait()
	if d.commentSummaries.busy {
		t.Fatal("worker remained busy after shutdown")
	}
	for _, entry := range d.commentSummaries.entries {
		if entry.retry.Before(time.Now()) {
			t.Fatal("failed helper not backed off")
		}
	}
}
