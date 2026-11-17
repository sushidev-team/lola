package runtime

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sushidev-team/lola/internal/config"
	"github.com/sushidev-team/lola/internal/session"
)

func TestBriefingAlwaysHasTalkSection(t *testing.T) {
	n := &Native{Cfg: &config.Config{}}
	got := string(n.withReportBriefing([]byte("task")))
	if !strings.Contains(got, "## Talking and widgets") {
		t.Fatalf("every agent launch gets the section:\n%s", got)
	}
	for _, want := range []string{" post narration ", " post widget "} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q:\n%s", want, got)
		}
	}
	section := n.talkBriefing()
	if lines := strings.Count(strings.TrimSpace(section), "\n") + 1; lines > 7 { // heading, blank, 5 lines
		t.Fatalf("the briefing is read on every launch, so it stays at 5 lines: %d\n%s", lines, section)
	}
}

func TestBriefingOmitsSSHWithoutHosts(t *testing.T) {
	n := &Native{Cfg: &config.Config{}}
	if got := string(n.withReportBriefing([]byte("task"))); strings.Contains(got, "## Remote hosts over ssh") {
		t.Fatalf("no hosts, no ssh section:\n%s", got)
	}
	n = &Native{Cfg: &config.Config{SSHHosts: []config.SSHHost{{Alias: "box"}}}}
	got := string(n.withReportBriefing([]byte("task")))
	if !strings.Contains(got, "## Remote hosts over ssh") || !strings.Contains(got, "## Talking and widgets") {
		t.Fatalf("with hosts both sections:\n%s", got)
	}
}

func TestOpenDirectoryPassesPromptToAgent(t *testing.T) {
	f := newFixture(t, "", "*\"has-session\"*) exit 1 ;;")
	id := "lola-local-prompted"
	s, err := f.n.OpenDirectoryPrompt(context.Background(), f.p, id, f.repo, "main", "claude", true, "fix the login's redirect")
	if err != nil {
		t.Fatal(err)
	}
	if s.Kind != session.KindLocal {
		t.Fatalf("kind %s", s.Kind)
	}
	art, err := localArtifacts(id)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(art, "launch"))
	if err != nil {
		t.Fatal(err)
	}
	log := string(raw)
	if !strings.Contains(log, "fix the login") || !strings.Contains(log, "redirect") {
		t.Fatalf("the prompt never reached the launch line:\n%s", log)
	}
	if strings.Contains(log, "Wait for the user's task") {
		t.Fatalf("an agent with a task must not be told to wait:\n%s", log)
	}
	if !IsLaunchPrompt(id, f.n.LocalFirstTurn(id, "fix the login's redirect")) {
		t.Fatal("lola's own first turn should not read as someone typing in the terminal")
	}
}

func TestLocalReviveDoesNotRepeatThePrompt(t *testing.T) {
	f := newFixture(t, "", "*\"has-session\"*) exit 1 ;;")
	id := "lola-local-again"
	s, err := f.n.OpenDirectoryPrompt(context.Background(), f.p, id, f.repo, "main", "claude", true, "UNIQUE-TASK-WORDS")
	if err != nil {
		t.Fatal(err)
	}
	before := strings.Count(loggedArgs(t, f.tmuxLog), "UNIQUE-TASK-WORDS")
	if _, err := f.n.Revive(context.Background(), s); err != nil {
		t.Fatal(err)
	}
	if after := strings.Count(loggedArgs(t, f.tmuxLog), "UNIQUE-TASK-WORDS"); after != before {
		t.Fatalf("a reopened agent must not redo a task: %d -> %d", before, after)
	}
}

func TestIsLaunchPromptIsPerSession(t *testing.T) {
	n := &Native{Cfg: &config.Config{}}
	text := n.LocalFirstTurn("lola-local-a", "")
	if !IsLaunchPrompt("lola-local-a", text) {
		t.Fatal("own first turn")
	}
	if IsLaunchPrompt("lola-local-b", text) {
		t.Fatal("a forged line can only hide itself")
	}
	if !strings.HasSuffix(text, "Wait for the user's task.") {
		t.Fatalf("with no task the agent is told to wait:\n%s", text)
	}
}
