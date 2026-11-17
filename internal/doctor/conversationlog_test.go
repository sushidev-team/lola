package doctor

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sushidev-team/lola/internal/usage"
)

const (
	logGood = `{"type":"user","message":{"content":"hello"},"uuid":"u","timestamp":"2026-11-06T10:00:00Z"}`
	logBad  = `{"type":"assistant","message":{"content":[{"type":"brand_new_block"}]},"uuid":"a"}`
)

func conversationRig(t *testing.T) (dir string) {
	t.Helper()
	home := t.TempDir()
	root := t.TempDir()
	t.Setenv("LOLA_HOME", home)
	t.Setenv("CLAUDE_CONFIG_DIR", root)
	dir = usage.SlugDir(filepath.Join(root, "projects"), filepath.Join(home, "worktrees", "p", "s1"))
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	return dir
}

func writeTranscript(t *testing.T, dir, name string, good, bad int) {
	t.Helper()
	var b strings.Builder
	for range good {
		b.WriteString(logGood + "\n")
	}
	for range bad {
		b.WriteString(logBad + "\n")
	}
	if err := os.WriteFile(filepath.Join(dir, name), []byte(b.String()), 0o600); err != nil {
		t.Fatal(err)
	}
}

const logName = "aaaaaaaa-0000-4000-8000-000000000001.jsonl"

func TestConversationLogHealthy(t *testing.T) {
	writeTranscript(t, conversationRig(t), logName, 19, 1)
	res, ok := conversationLogResult()
	if !ok || !res.OK || res.Critical || !strings.Contains(res.Detail, "19 of 20") {
		t.Errorf("result: %+v ok=%v", res, ok)
	}
}

func TestConversationLogWarnsBelowTheFloor(t *testing.T) {
	writeTranscript(t, conversationRig(t), logName, 10, 10)
	res, ok := conversationLogResult()
	if !ok || res.OK || res.Critical || !strings.Contains(res.Detail, "10 of 20") {
		t.Errorf("result: %+v ok=%v", res, ok)
	}
}

func TestConversationLogHasNoRowWithoutATranscript(t *testing.T) {
	conversationRig(t)
	if res, ok := conversationLogResult(); ok {
		t.Errorf("a row with nothing to read: %+v", res)
	}
}

// A person's own claude session, in a directory outside lola's worktrees, is not read.
func TestConversationLogIgnoresOtherProjects(t *testing.T) {
	dir := conversationRig(t)
	other := filepath.Join(filepath.Dir(dir), "-Users-someone-private-project")
	if err := os.MkdirAll(other, 0o700); err != nil {
		t.Fatal(err)
	}
	writeTranscript(t, other, logName, 0, 20)
	if res, ok := conversationLogResult(); ok {
		t.Errorf("read a transcript outside lola's worktrees: %+v", res)
	}
}

func TestConversationLogReadsTheNewest(t *testing.T) {
	dir := conversationRig(t)
	const older = "bbbbbbbb-0000-4000-8000-000000000002.jsonl"
	writeTranscript(t, dir, older, 0, 30)
	writeTranscript(t, dir, logName, 12, 0)
	if err := os.Chtimes(filepath.Join(dir, older), time.Now().Add(-time.Hour), time.Now().Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	res, ok := conversationLogResult()
	if !ok || !res.OK || !strings.Contains(res.Detail, fmt.Sprintf("%d of %d", 12, 12)) {
		t.Errorf("result: %+v ok=%v", res, ok)
	}
}

func TestCheckCarriesTheConversationLogRow(t *testing.T) {
	writeTranscript(t, conversationRig(t), logName, 12, 0)
	var found bool
	for _, r := range Check(context.Background(), nil).Results {
		found = found || r.Name == checkConversationLog
	}
	if !found {
		t.Error("Check has no conversation log row")
	}
}
