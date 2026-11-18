package daemon

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/sushidev-team/lola/internal/config"
	"github.com/sushidev-team/lola/internal/linear"
	"github.com/sushidev-team/lola/internal/protocol"
)

func bulkFixture(t *testing.T) (*Daemon, string) {
	t.Helper()
	fake := &linear.Fake{LabelsByTeam: map[string][]linear.Label{"team-1": {
		{ID: "11111111-1111-1111-1111-111111111111", Name: "agent-ready", Parent: &linear.Label{Name: "agent"}},
		{ID: "22222222-2222-2222-2222-222222222222", Name: "picked-up", Parent: &linear.Label{Name: "agent"}},
		{ID: "33333333-3333-3333-3333-333333333333", Name: "agent-ready", Parent: &linear.Label{Name: "ai"}},
	}}}
	cfg := testConfig(seenPoll("a"), seenPoll("b"))
	d := newTestDaemon(t, cfg, fake, &fakeNative{})
	t.Cleanup(d.stopAllWorkers)
	path, err := config.DefaultPath()
	if err != nil {
		t.Fatal(err)
	}
	if err := cfg.Save(path); err != nil {
		t.Fatal(err)
	}
	return d, path
}

func bulk(t *testing.T, d *Daemon, a protocol.BulkProjectsArgs) (protocol.BulkProjectsData, error) {
	t.Helper()
	raw, _ := json.Marshal(a)
	return d.handleBulkProjects(context.Background(), raw)
}

func ptr[T any](v T) *T { return &v }

// By default a bulk edit stores ids; with KeepNames the references are checked
// against each team but stored as written, globs included.
func TestBulkProjectsKeepNames(t *testing.T) {
	d, path := bulkFixture(t)

	// ids: a glob matching two labels is refused (one id per reference)
	if _, err := bulk(t, d, protocol.BulkProjectsArgs{MatchLabels: ptr([]string{"*/agent-ready"})}); err == nil {
		t.Fatal("glob matching several labels must be refused when resolving to ids")
	}

	data, err := bulk(t, d, protocol.BulkProjectsArgs{
		MatchLabels: ptr([]string{"*/agent-ready"}), OnSentSetLabel: ptr("agent/picked-up"), KeepNames: true,
	})
	if err != nil || !data.Applied {
		t.Fatalf("data=%+v err=%v", data, err)
	}
	fc, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range []string{"a", "b"} {
		p := fc.ProjectByName(n)
		if len(p.MatchLabels) != 1 || p.MatchLabels[0] != "*/agent-ready" || p.OnSentSetLabel != "agent/picked-up" {
			t.Fatalf("%s = %v %q", n, p.MatchLabels, p.OnSentSetLabel)
		}
	}

	// a single-label key must name exactly one label, and a typo is refused
	if _, err := bulk(t, d, protocol.BulkProjectsArgs{OnSentSetLabel: ptr("*/agent-ready"), KeepNames: true}); err == nil {
		t.Fatal("on-sent glob matching several labels must be refused")
	}
	if _, err := bulk(t, d, protocol.BulkProjectsArgs{MatchLabels: ptr([]string{"nope/nothing"}), KeepNames: true}); err == nil {
		t.Fatal("a name matching nothing must be refused")
	}
}
