package goal

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/sushidev-team/lola/internal/goalpolicy"
)

func ownershipGoal(t *testing.T) Goal {
	t.Helper()
	g := executable(t)
	g.Tasks[1].BlockedBy = nil
	g.Tasks[1].Relations = nil
	for i := range g.Tasks {
		g.Tasks[i].Apps = []string{"api"}
	}
	g.Tasks[0].Paths = map[string][]string{"api": {"src/api"}}
	g.Tasks[1].Paths = map[string][]string{"api": {"src/web"}}
	return g
}

func TestOwnershipAllowsOnlyDisjointParallelAttempts(t *testing.T) {
	for _, mode := range []string{"disjoint", "overlap", "prefix", "case", "legacy", "read-only"} {
		t.Run(mode, func(t *testing.T) {
			g := ownershipGoal(t)
			switch mode {
			case "overlap":
				g.Tasks[1].Paths["api"] = []string{"src/api"}
			case "prefix":
				g.Tasks[1].Paths["api"] = []string{"src"}
			case "case":
				g.Tasks[1].Paths["api"] = []string{"SRC/API"}
			case "legacy":
				g.Tasks[0].Paths = nil
			case "read-only":
				g.Tasks[1].Paths["api"] = []string{}
				g.Tasks[1].Apps = []string{"api", "web"}
				g.Tasks[1].Paths["web"] = []string{"src"}
				g.Execution.Policy.Members = append(g.Execution.Policy.Members, goalpolicy.Member{ID: "web", Project: "web", Source: "/private/web", Target: "main"})
				g.Execution.Manifest["web"] = g.Execution.Manifest["api"]
				g.Execution.PolicyDigest, _ = g.Execution.Policy.Digest()
			}
			a, err := g.ReserveAttempt(g.Tasks[0].ID, "one", "one", time.Now())
			if err != nil {
				t.Fatal(err)
			}
			if a.Scopes == nil {
				t.Fatal("ownership not reserved")
			}
			_, err = g.ReserveAttempt(g.Tasks[1].ID, "two", "two", time.Now())
			want := mode == "disjoint" || mode == "read-only"
			if (err == nil) != want {
				t.Fatalf("parallel eligibility: %v", err)
			}
			if !want && len(g.Execution.Runs) != 1 {
				t.Fatal("held task consumed agent budget")
			}
		})
	}
}

func TestOwnershipCannotExpandPolicyOrSilentlyChangeAfterAuthorization(t *testing.T) {
	g := ownershipGoal(t)
	p := g.Execution.Policy
	p.Members = append([]goalpolicy.Member(nil), p.Members...)
	p.Members[0].Paths = []string{"src/api"}
	if _, err := TaskScopes(p, g.Tasks[1]); err == nil {
		t.Fatal("scope expanded policy")
	}
	g.Tasks[1].Paths = map[string][]string{"api": {}}
	if _, err := TaskScopes(p, g.Tasks[1]); err == nil {
		t.Fatal("worker without a writable deliverable accepted")
	}
	g.Tasks[1].Paths = map[string][]string{"api": {"src/web"}}
	a, err := g.ReserveAttempt(g.Tasks[0].ID, "a", "s", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	store, _ := Open(filepath.Join(t.TempDir(), "goals.json"))
	g, err = store.Save(g)
	if err != nil {
		t.Fatal(err)
	}
	a = g.Execution.Attempts[a.ID]
	a.Scopes["api"] = goalpolicy.Scope{All: true}
	g.Execution.Attempts[a.ID] = a
	if _, err := store.Save(g); err == nil {
		t.Fatal("attempt ownership expanded")
	}
	g, _ = store.Get(g.ID)
	g.Tasks[0].Paths["api"] = []string{"src"}
	if _, err := store.Save(g); err == nil {
		t.Fatal("accepted task ownership changed")
	}
	g, _ = store.Get(g.ID)
	g.Tasks[0].Acceptance = []string{"weaker criterion"}
	if _, err := store.Save(g); err == nil {
		t.Fatal("accepted task criteria changed")
	}
	// Old journals with no attempt ownership field resolve conservatively to
	// their immutable accepted contract, and restart does not release the lease.
	g, _ = store.Get(g.ID)
	a = g.Execution.Attempts[a.ID]
	a.Scopes = nil
	g.Execution.Attempts[a.ID] = a
	g, err = store.Save(g)
	if err != nil {
		t.Fatal(err)
	}
	store, err = Open(store.path)
	if err != nil {
		t.Fatal(err)
	}
	g, _ = store.Get(g.ID)
	if scopes, err := g.AttemptScopes(g.Execution.Attempts[a.ID]); err != nil || !scopes["api"].Contains("src/api/file.go") || scopes["api"].Contains("src/web/file.go") {
		t.Fatal("legacy ownership changed", err)
	}
}

func TestTaskOwnershipMustCoverExactlyRoutedRepositories(t *testing.T) {
	d := testDraft()
	d.Tasks[0].Apps = []string{"api", "shared"}
	for _, paths := range []map[string][]string{{"api": {"src"}}, {"api": {"src"}, "unknown": {}}, {"api": {"../escape"}, "shared": {}}} {
		d.Tasks[0].Paths = paths
		if err := ValidateDraft(d); err == nil {
			t.Fatal("invalid ownership accepted")
		}
	}
	d.Tasks[0].Paths = map[string][]string{"api": {"src"}, "shared": {}}
	if err := ValidateDraft(d); err != nil {
		t.Fatal(err)
	}
}
