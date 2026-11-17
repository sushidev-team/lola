package goalcheck

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// Explicit opt-in: no image pulls, network access, or existing containers are
// involved. Only this test's generated repositories and owned containers change.
func TestDockerValidationBoundary(t *testing.T) {
	image := os.Getenv("LOLA_GOAL_CHECK_IMAGE")
	if image == "" {
		t.Skip("set LOLA_GOAL_CHECK_IMAGE to a local digest-pinned image containing sh")
	}
	t.Setenv("GH_TOKEN", "do-not-pass-to-validation")
	t.Setenv("LINEAR_API_KEY", "do-not-pass-to-validation")
	r, spec, manifest := setup(t)
	spec.Image = image
	spec.TimeoutSeconds = 20
	spec.Command = `set -eu
test "$(cat app.txt)" = initial
test -z "${GH_TOKEN:-}"
test -z "${LINEAR_API_KEY:-}"
test ! -S /var/run/docker.sock
test ! -e /sys/class/net/eth0
if touch /should-not-write-root 2>/dev/null; then exit 31; fi
printf 'modified only the check copy' > app.txt
printf 'invalid Git config' > .git/config
printf '{"checks":"passed"}\n' > report.json`
	got, err := r.Execute(context.Background(), "goal1", "op1", "docker-check", "policy1", spec, manifest)
	if err != nil || got.State != "pass" {
		t.Fatalf("container check: %+v %v", got, err)
	}
	if len(got.Artifacts) != 1 {
		t.Fatal("missing runtime evidence")
	}
	if tip, err := r.Workspaces.Tip(context.Background(), "goal1", "api"); err != nil || tip != manifest["api"].Commit {
		t.Fatal("container changed authoritative goal ref")
	}
	assertContainerGone(t, "goal1", "docker-check")

	spec.Command = "sleep 60"
	spec.TimeoutSeconds = 1
	spec.Artifacts = nil
	start := time.Now()
	got, err = r.Execute(context.Background(), "goal1", "op1", "timeout-check", "policy1", spec, manifest)
	if err == nil || got.State == "pass" {
		t.Fatalf("timeout passed: %+v %v", got, err)
	}
	if time.Since(start) > 25*time.Second {
		t.Fatal("container exceeded bounded timeout and cleanup")
	}
	assertContainerGone(t, "goal1", "timeout-check")
}

func assertContainerGone(t *testing.T, goal, id string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "docker", "container", "inspect", containerName(goal, id)).CombinedOutput()
	if err == nil || !strings.Contains(strings.ToLower(string(out)), "no such") {
		t.Fatalf("validation container still exists or cannot be inspected: %v %s", err, out)
	}
}
