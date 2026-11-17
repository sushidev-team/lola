package agentfeed

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sushidev-team/lola/internal/protocol"
)

// oneRecord reads a single user or assistant record through the real reader.
func oneRecord(t *testing.T, kind string, content any) []Item {
	t.Helper()
	root, slug, dir := slugRig(t)
	line, err := json.Marshal(map[string]any{"type": kind, "uuid": "r-1", "timestamp": "2026-11-06T10:00:00Z", "message": map[string]any{"content": content}})
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(dir, segA), append(line, '\n'))
	f := newClaudeFeed(root, slug, segA)
	drain(t, f)
	return f.All()
}

func toolSummaryOf(t *testing.T, name string, input map[string]any) *protocol.AgentFeedTool {
	t.Helper()
	items := oneRecord(t, "assistant", []map[string]any{{"type": "tool_use", "id": "t-1", "name": name, "input": input}})
	if len(items) != 1 || items[0].Tool == nil {
		t.Fatalf("items = %+v", items)
	}
	return items[0].Tool
}

// Another agent's message is neither a person nor readable as sent: it is markup around JSON.
func TestTeammateMessageIsOneLineWithItsOwnOrigin(t *testing.T) {
	text := "Another Claude session sent a message:\n<teammate-message teammate_id=\"accept-job\" color=\"yellow\" summary=\"Lock done, 32 tests green\">\n{\"type\":\"idle_notification\"}\n</teammate-message>\n\nThis came from another Claude session."
	items := oneRecord(t, "user", text)
	if len(items) != 1 || items[0].Origin != "teammate" {
		t.Fatalf("items = %+v", items)
	}
	if got := items[0].Text; got != "Message from accept-job · Lock done, 32 tests green" {
		t.Errorf("text = %q", got)
	}
}

// Someone quoting the tag mid-message is still someone typing.
func TestQuotedTeammateTagStaysAUserMessage(t *testing.T) {
	items := oneRecord(t, "user", "why does <teammate-message> show up raw?")
	if len(items) != 1 || items[0].Origin == "teammate" || !strings.Contains(items[0].Text, "teammate-message") {
		t.Fatalf("items = %+v", items)
	}
}

func TestRunSummaryNamesWhatRan(t *testing.T) {
	cases := []struct{ command, description, summary, detail string }{
		{"cd /Users/me/worktree && go test ./...", "", "go test ./...", "cd /Users/me/worktree && go test ./..."},
		{`cd "/a b" && make`, "", "make", `cd "/a b" && make`},
		{"go test ./...", "", "go test ./...", ""},
		{"python3 - <<'EOF'\nprint(1)\nEOF", "Print a number", "Print a number", "python3 - <<'EOF'"},
		{"cat > x <<EOF\nhi\nEOF", "", "cat > x <<EOF …", "cat > x <<EOF"},
		{"git commit -m \"first\nsecond\"", "", "git commit -m \"first …", "git commit -m \"first"},
	}
	for _, c := range cases {
		tool := toolSummaryOf(t, "Bash", map[string]any{"command": c.command, "description": c.description})
		if tool.Summary != c.summary || tool.Detail != c.detail {
			t.Errorf("%q: summary %q detail %q, want %q and %q", c.command, tool.Summary, tool.Detail, c.summary, c.detail)
		}
	}
}

func TestOtherToolsNameSomething(t *testing.T) {
	cases := []struct {
		name  string
		input map[string]any
		want  string
	}{
		{"Skill", map[string]any{"skill": "github-pr", "args": "x"}, "github-pr"},
		{"SendMessage", map[string]any{"to": "reviewer", "message": "long"}, "reviewer"},
		{"AskUserQuestion", map[string]any{"questions": []map[string]any{{"question": "Which tenant?"}}}, "Which tenant?"},
		{"TaskStop", map[string]any{"task_id": "abc123"}, "abc123"},
		{"mcp__x__thing", map[string]any{"count": 3, "name": "first string"}, "first string"},
		{"mcp__x__write", map[string]any{"content": "never this"}, ""},
	}
	for _, c := range cases {
		if got := toolSummaryOf(t, c.name, c.input).Summary; got != c.want {
			t.Errorf("%s: summary %q, want %q", c.name, got, c.want)
		}
	}
}

func TestImageIsAnnouncedOnce(t *testing.T) {
	both := oneRecord(t, "user", []map[string]any{{"type": "text", "text": "[Image #2] look at this"}, {"type": "image"}})
	if len(both) != 1 || both[0].Text != "[Image #2] look at this" {
		t.Errorf("token and block: %+v", both)
	}
	alone := oneRecord(t, "user", []map[string]any{{"type": "text", "text": "look"}, {"type": "image"}})
	if len(alone) != 1 || !strings.Contains(alone[0].Text, "[Image]") {
		t.Errorf("block alone: %+v", alone)
	}
}

func TestDisplayPasteSeparatesWhatWasSeparate(t *testing.T) {
	full := strings.Repeat("x", pasteCutBytes)
	wrap := func(id, body string) string {
		return "\n\n<pasted_content id=\"" + id + "\">\n" + body + "\n</pasted_content id=\"" + id + "\">\n"
	}
	cases := map[string]struct{ in, want string }{
		"typed then paste":        {"look at this" + wrap("a", "body"), "look at this\nbody"},
		"paste then typed":        {wrap("a", "body") + "what now", "body\nwhat now"},
		"two pastes":              {wrap("a", "one") + wrap("b", "two"), "one\ntwo"},
		"fragments of one paste":  {wrap("a", "hal") + wrap("a", "f"), "half"},
		"full fragment runs into": {wrap("a", full) + "tail", full + "tail"},
		"no wrapper":              {"plain text", "plain text"},
	}
	for name, c := range cases {
		if got := displayPaste(c.in); got != c.want {
			t.Errorf("%s: %q, want %q", name, got, c.want)
		}
	}
	// Hashing must not depend on any of this.
	if Normalize("look at this"+wrap("a", "body")) != "look at thisbody" {
		t.Errorf("Normalize changed: %q", Normalize("look at this"+wrap("a", "body")))
	}
}

func TestRunSummarySkipsSemicolonSetup(t *testing.T) {
	command := `cd "/repo name"; SP=src/Board.tsx; rg -n Button "$SP"`
	tool := toolSummaryOf(t, "Bash", map[string]any{"command": command})
	if tool.Summary != `rg -n Button "$SP"` || tool.Detail != command {
		t.Fatalf("summary %q, detail %q", tool.Summary, tool.Detail)
	}
}

func TestStructuredPatchTextPreservesRecordedChange(t *testing.T) {
	raw := json.RawMessage(`{"structuredPatch":[{"oldStart":12,"oldLines":1,"newStart":12,"newLines":1,"lines":["-const x = 1;","+const x = 2;"]}]}`)
	want := "@@ -12,1 +12,1 @@\n-const x = 1;\n+const x = 2;\n"
	if got := structuredPatchText(raw); got != want {
		t.Fatalf("patch %q", got)
	}
}
