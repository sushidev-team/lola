package reviewagent

import (
	"context"
	"strings"
	"testing"

	"github.com/sushidev-team/lola/internal/agent"
	"github.com/sushidev-team/lola/internal/reviewprofile"
)

func TestReviewProfileKeepsUntrustedInputsOffArgv(t *testing.T) {
	stubGitDiff(t, "diff --git a/a b/a\n+changed", nil)
	cap := stubClaude(t, `{"complete":true,"summary":"Verified","findings":[],"evidence":[{"criterion":"Atomicity","observation":"Mutex surrounds the save"}]}`, nil)
	p := reviewprofile.Profile{ID: "backend", Kind: "backend", Provider: "codex-session", Version: "1"}
	in := reviewprofile.Inputs{Repo: "owner/repo", Head: "head", Base: "base", Contract: "untrusted $(touch /tmp/should-never-run)"}
	r, err := (&Client{Agent: agent.Codex}).ReviewProfile(context.Background(), "/fake", p, in)
	if err != nil || !r.Complete {
		t.Fatalf("profile: %+v %v", r, err)
	}
	if strings.Contains(strings.Join(cap.args, " "), "should-never-run") || !strings.Contains(cap.stdin, "should-never-run") {
		t.Fatal("untrusted acceptance contract escaped stdin")
	}
	if !strings.Contains(strings.Join(cap.args, " "), "read-only") {
		t.Fatal("profile lost read-only posture")
	}
	cap = stubClaude(t, "", nil)
	if _, err := (&Client{}).ReviewProfile(context.Background(), "/fake", p, in); err == nil {
		t.Fatal("empty specialist answer treated as clean")
	}
	if cap.calls != 1 {
		t.Fatal("unexpected provider invocation count")
	}
}
