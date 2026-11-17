package goal

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/sushidev-team/lola/internal/goalpolicy"
	"github.com/sushidev-team/lola/internal/usage"
)

func rolloutAuthorization(t *testing.T) (Goal, goalpolicy.Policy) {
	t.Helper()
	g := executable(t)
	p := g.Execution.Policy
	p.PublishPR = true
	p.Members[0].Repo = "owner/api"
	p.Members = append(p.Members, goalpolicy.Member{ID: "web", Project: "web", Source: "/private/web", Repo: "owner/web", Target: "main"})
	g.Execution, g.State = nil, "planned"
	for i := range g.Tasks {
		g.Tasks[i].Apps = []string{"api", "web"}
	}
	return g, p
}

func TestStackAuthorizationRequiresAndFreezesRollout(t *testing.T) {
	g, p := rolloutAuthorization(t)
	before := copyGoal(g)
	if err := g.BeginPreparation(p, time.Now().UTC()); err == nil || !reflect.DeepEqual(g, before) {
		t.Fatal("missing rollout authorized or changed goal")
	}
	p.Rollout = &goalpolicy.Rollout{Compatibility: []string{"Retain old client support until web merges."}, MergeOrder: []string{"api", "web"}}
	if err := g.BeginPreparation(p, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	p.Rollout.MergeOrder[0] = "changed"
	p.Rollout.Compatibility[0] = "changed"
	if g.Execution.Policy.Rollout.MergeOrder[0] != "api" || g.Execution.Policy.Rollout.Compatibility[0] == "changed" {
		t.Fatal("authorization aliases mutable rollout inputs")
	}
}

func TestRolloutMigrationPreservesLegacyAndFailsAtomically(t *testing.T) {
	for _, version := range []int{1, 2} {
		t.Run(string(rune('0'+version)), func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "goals.json")
			s, _ := Open(path)
			legacy := executable(t)
			now := time.Now().UTC()
			if err := legacy.ReserveRun("prior-review", "reviewer", "backend", now); err != nil {
				t.Fatal(err)
			}
			if err := legacy.ObserveUsage("prior-review", usage.Observation{Agent: "codex", Status: "partial", CostStatus: "estimate", At: now, Days: map[string]usage.Totals{now.Format(usage.DayFormat): {Input: 123, CostUSD: 0.25}}}); err != nil {
				t.Fatal(err)
			}
			if version == 2 {
				legacy.Execution.Policy.PublishPR = true
				legacy.Execution.Policy.RequireHumanAcceptance = true
				legacy.Execution.Policy.Members[0].Repo = "owner/api"
				legacy.Execution.PolicyDigest, _ = legacy.Execution.Policy.Digest()
			}
			legacy, err := s.Save(legacy)
			if err != nil || s.data.Version != version {
				t.Fatal(err)
			}
			before, _ := os.ReadFile(path)
			s, err = Open(path)
			if err != nil {
				t.Fatal(err)
			}
			readOnly, _ := os.ReadFile(path)
			if !bytes.Equal(before, readOnly) {
				t.Fatal("open rewrote legacy store")
			}
			g, p := rolloutAuthorization(t)
			p.Rollout = &goalpolicy.Rollout{Compatibility: []string{"Keep old clients working."}, MergeOrder: []string{"api", "web"}}
			if err := g.BeginPreparation(p, time.Now().UTC()); err != nil {
				t.Fatal(err)
			}
			s.write = func(string, []byte) error { return errors.New("disk full") }
			if _, err := s.Save(g); err == nil || s.data.Version != version || len(s.List()) != 1 {
				t.Fatal("failed migration became visible")
			}
			after, _ := os.ReadFile(path)
			if !bytes.Equal(before, after) {
				t.Fatal("failed migration changed disk")
			}
			s.write = atomicWrite
			g, err = s.Save(g)
			if err != nil || s.data.Version != 3 {
				t.Fatal("missing format upgrade", err)
			}
			s, err = Open(path)
			if err != nil {
				t.Fatal(err)
			}
			got, _ := s.Get(g.ID)
			gotLegacy, _ := s.Get(legacy.ID)
			if !reflect.DeepEqual(got, g) || !reflect.DeepEqual(gotLegacy, legacy) {
				t.Fatal("restart changed goal or legacy contract")
			}
			// A later old-format goal write cannot downgrade the shared store.
			if _, err := s.Save(gotLegacy); err != nil || s.data.Version != 3 {
				t.Fatal("later write downgraded format", err)
			}
			got.Execution.Policy.Rollout.MergeOrder = []string{"web", "api"}
			got.Execution.PolicyDigest, _ = got.Execution.Policy.Digest()
			if _, err := s.Save(got); err == nil {
				t.Fatal("accepted rollout replaced")
			}
			b, _ := os.ReadFile(path)
			var old snapshot
			_ = json.Unmarshal(b, &old)
			old.Version = version
			if err := validateSnapshot(old); err == nil || !strings.Contains(err.Error(), "version 3") {
				t.Fatal("rollout accepted under old format", err)
			}
		})
	}
}
