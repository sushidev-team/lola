package runtime

import (
	"strings"
	"testing"
)

// Every agent launch's prompt.md teaches `lola report`, naming the SAME binary
// the hooks call (a PATH lookup in the pane may find a different lola, or none).
func TestWithReportBriefing(t *testing.T) {
	n := &Native{LolaBin: "/opt/my tools/lola"}
	got := string(n.withReportBriefing([]byte("# task")))
	if !strings.HasPrefix(got, "# task\n\n## Progress reporting") {
		t.Fatalf("briefing must follow the task body:\n%s", got)
	}
	if !strings.Contains(got, `'/opt/my tools/lola' report todo set`) {
		t.Fatalf("briefing must name the quoted lola binary:\n%s", got)
	}
	if got := string((&Native{}).withReportBriefing(nil)); !strings.Contains(got, "`lola report`") {
		t.Fatalf("no LolaBin falls back to PATH lookup:\n%s", got)
	}
}
