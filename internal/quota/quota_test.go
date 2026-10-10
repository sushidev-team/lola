package quota

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestParseClaudeStatusLine(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	var in StatusLineInput
	raw := `{"cwd":"/a","workspace":{"current_dir":"/w"},"rate_limits":{"five_hour":{"used_percentage":42.5,"resets_at":1800003600},"seven_day":{"used_percentage":-3,"resets_at":1800500000}}}`
	if err := json.Unmarshal([]byte(raw), &in); err != nil {
		t.Fatal(err)
	}
	if in.Dir() != "/w" {
		t.Errorf("dir = %q", in.Dir())
	}
	s, ok := ParseClaude(in, now)
	if !ok || len(s.Windows) != 2 || s.Windows[0].Label != "5h" || s.Windows[0].UsedPercent != 42.5 || s.Windows[1].UsedPercent != 0 {
		t.Fatalf("snapshot = %+v (negative must clamp to 0)", s)
	}
	if !s.Windows[0].ResetsAt.Equal(time.Unix(1_800_003_600, 0)) {
		t.Errorf("reset = %v", s.Windows[0].ResetsAt)
	}
	if _, ok := ParseClaude(StatusLineInput{}, now); ok {
		t.Error("no rate_limits (API-key user) must report nothing")
	}
	// A window whose reset passed is dropped.
	live, ok := s.Live(time.Unix(1_800_004_000, 0))
	if !ok || len(live.Windows) != 1 || live.Windows[0].Label != "7d" {
		t.Errorf("live = %+v", live)
	}
}

func TestRecordClaudeRoundTripAndSkipsUnchanged(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state", "q.json")
	now := time.Unix(1_800_000_000, 0)
	s := Snapshot{At: now, Windows: []Window{{Label: "5h", UsedPercent: 10, ResetsAt: now.Add(time.Hour)}}}
	if err := RecordClaude(path, s); err != nil {
		t.Fatal(err)
	}
	st1, _ := os.Stat(path)
	s.At = now.Add(10 * time.Second)
	if err := RecordClaude(path, s); err != nil {
		t.Fatal(err)
	}
	st2, _ := os.Stat(path)
	if !os.SameFile(st1, st2) {
		t.Error("an unchanged snapshot within a minute must not rewrite the file")
	}
	back, err := ReadClaude(path)
	if err != nil || len(back.Windows) != 1 || back.Windows[0].UsedPercent != 10 || !back.At.Equal(now) {
		t.Fatalf("back = %+v err %v", back, err)
	}
}

func TestLatestCodexReadsNewestRateLimits(t *testing.T) {
	root := t.TempDir()
	day := filepath.Join(root, "sessions", "2026", "10", "10")
	if err := os.MkdirAll(day, 0o700); err != nil {
		t.Fatal(err)
	}
	old := `{"timestamp":"2026-10-10T08:00:00Z","type":"event_msg","payload":{"type":"token_count","rate_limits":{"primary":{"used_percent":3.0,"window_minutes":10080,"resets_at":1792096176},"secondary":null,"plan_type":"pro"}}}`
	cur := `{"timestamp":"2026-10-10T08:14:34.244Z","type":"event_msg","payload":{"type":"token_count","rate_limits":{"primary":{"used_percent":4.0,"window_minutes":10080,"resets_at":1792096176},"secondary":{"used_percent":12,"window_minutes":300,"resets_at":1792000000},"plan_type":"pro"}}}`
	body := old + "\n" + `{"type":"response_item","payload":{"text":"\"rate_limits\":{ in prose"}}` + "\n" + cur + "\n"
	if err := os.WriteFile(filepath.Join(day, "rollout-a.jsonl"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	s, ok := LatestCodex(root)
	if !ok || s.Plan != "pro" || len(s.Windows) != 2 {
		t.Fatalf("codex = %+v ok %v", s, ok)
	}
	if s.Windows[0].Label != "7d" || s.Windows[0].UsedPercent != 4 || s.Windows[1].Label != "5h" {
		t.Errorf("windows = %+v", s.Windows)
	}
	if _, ok := LatestCodex(t.TempDir()); ok {
		t.Error("no logs must report nothing")
	}
}

func TestReadRealCodexLogs(t *testing.T) {
	if s, ok := LatestCodex(CodexHome()); ok {
		t.Logf("this machine's codex limits: %+v", s)
	}
}
