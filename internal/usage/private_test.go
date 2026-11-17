package usage

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestPrivateClaudeUsageCountsSubagentsAndDeduplicates(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()
	logs := filepath.Join(dir, "workspace", ".agent", "claude", "projects", "private")
	record := line("m1", "claude-sonnet-4", now.Format(time.RFC3339), 100, 20, 0, 0)
	writeLines(t, filepath.Join(logs, "a.jsonl"), record, record)
	writeLines(t, filepath.Join(logs, "a", "subagents", "b.jsonl"), line("m2", "claude-sonnet-4", now.Format(time.RFC3339), 30, 4, 0, 0))
	out := Private(context.Background(), dir, "claude", now)
	if out.Status != "partial" || Sum(out.Days).Tokens() != 154 || out.CostStatus != "estimate" || Sum(out.Days).CostUSD <= 0 {
		t.Fatalf("wrong private usage: %+v", out)
	}
}

func TestPrivateCodexUsageUsesAttemptCwdAndCumulativeCounts(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()
	workspace := filepath.Join(dir, "workspace")
	path := filepath.Join(workspace, ".agent", "codex", "sessions", now.Format("2006/01/02"), "run.jsonl")
	meta, _ := json.Marshal(map[string]any{"type": "session_meta", "payload": map[string]string{"cwd": workspace}})
	event := `{"timestamp":"` + now.Format(time.RFC3339) + `","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":100,"cached_input_tokens":30,"output_tokens":20}}}}`
	writeLines(t, path, string(meta), event, event)
	other, _ := json.Marshal(map[string]any{"type": "session_meta", "payload": map[string]string{"cwd": "/unrelated"}})
	writeLines(t, filepath.Join(filepath.Dir(path), "other.jsonl"), string(other), event)
	out := Private(context.Background(), dir, "codex", now)
	if out.Status != "partial" || Sum(out.Days).Tokens() != 120 || out.CostStatus != "unavailable" {
		t.Fatalf("wrong codex accounting: %+v", out)
	}
}

func TestPrivateUsageRejectsEscapesAndOversizedInput(t *testing.T) {
	for _, mode := range []string{"agent-link", "log-link", "fifo", "oversize", "negative", "canceled", "absent"} {
		t.Run(mode, func(t *testing.T) {
			dir := t.TempDir()
			now := time.Now()
			logs := filepath.Join(dir, "workspace", ".agent", "claude", "projects", "p")
			if err := os.MkdirAll(logs, 0700); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			switch mode {
			case "agent-link":
				original := filepath.Join(dir, "workspace", ".agent")
				if err := os.Rename(original, original+"-saved"); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(t.TempDir(), original); err != nil {
					t.Fatal(err)
				}
			case "log-link":
				if err := os.Symlink("/etc/passwd", filepath.Join(logs, "x.jsonl")); err != nil {
					t.Fatal(err)
				}
			case "fifo":
				if err := exec.Command("mkfifo", filepath.Join(logs, "x.jsonl")).Run(); err != nil {
					t.Fatal(err)
				}
			case "oversize":
				f, err := os.Create(filepath.Join(logs, "x.jsonl"))
				if err != nil {
					t.Fatal(err)
				}
				err = f.Truncate(65 << 20)
				f.Close()
				if err != nil {
					t.Fatal(err)
				}
			case "negative":
				writeLines(t, filepath.Join(logs, "x.jsonl"), line("m1", "claude-sonnet-4", now.Format(time.RFC3339), -10, 0, 0, 0), line("m2", "claude-sonnet-4", now.Format(time.RFC3339), 100, 0, 0, 0))
			case "canceled":
				cancel()
			}
			out := Private(ctx, dir, "claude", now)
			if out.Status != "missing" || Sum(out.Days).Tokens() != 0 {
				t.Fatalf("unsafe input treated as usage: %+v", out)
			}
		})
	}
}

func TestPrivateOpenCodeReadsOnlyCopiedDatabase(t *testing.T) {
	if _, err := exec.LookPath("sqlite3"); err != nil {
		t.Skip("sqlite3 unavailable")
	}
	dir := t.TempDir()
	now := time.Now()
	workspace := filepath.Join(dir, "workspace")
	db := filepath.Join(workspace, ".agent", "data", "opencode", "opencode.db")
	if err := os.MkdirAll(filepath.Dir(db), 0700); err != nil {
		t.Fatal(err)
	}
	data := `{"role":"assistant","tokens":{"input":100,"output":20,"reasoning":3,"cache":{"read":5,"write":0}},"cost":0.25}`
	query := `CREATE TABLE session(id TEXT,directory TEXT,time_updated INTEGER);CREATE TABLE message(id TEXT,session_id TEXT,time_created INTEGER,time_updated INTEGER,data TEXT);INSERT INTO session VALUES('s',` + sqlString(filepath.Join(workspace, "api")) + `,` + itoa(now.UnixMilli()) + `);INSERT INTO message VALUES('m','s',` + itoa(now.UnixMilli()) + `,` + itoa(now.UnixMilli()) + `,` + sqlString(data) + `);`
	cmd := exec.Command("sqlite3", db)
	cmd.Stdin = strings.NewReader(query)
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("fixture: %v %s", err, b)
	}
	out := Private(context.Background(), dir, "opencode", now)
	if out.Status != "partial" || Sum(out.Days).Tokens() != 128 || Sum(out.Days).CostUSD != 0.25 {
		t.Fatalf("private OpenCode usage: %+v", out)
	}
}

func TestPrivateUnknownModelDoesNotClaimKnownZeroCost(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()
	path := filepath.Join(dir, "workspace", ".agent", "claude", "projects", "p", "x.jsonl")
	writeLines(t, path, line("m1", "unknown-model", now.Format(time.RFC3339), 100, 10, 0, 0))
	out := Private(context.Background(), dir, "claude", now)
	if out.Status != "partial" || Sum(out.Days).Tokens() != 110 || out.CostStatus != "unavailable" {
		t.Fatalf("unknown cost presented as known: %+v", out)
	}
}
