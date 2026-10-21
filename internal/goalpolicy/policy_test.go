package goalpolicy

import (
	"encoding/json"
	"math"
	"strings"
	"testing"
)

func validPolicy() Policy {
	return Policy{Members: []Member{{ID: "api", Project: "api", Source: "/private/api", Repo: "owner/api", Target: "main"}}, Checks: []Check{{ID: "tests", Command: "make test", Image: "sha256:" + strings.Repeat("a", 64), TimeoutSeconds: 60}}, Limits: Limits{Concurrency: 2, MaxTasks: 10, AttemptsPerTask: 2, ReviewRounds: 2, AgentRuns: 20, RuntimeMinutes: 60, AttemptMinutes: 15, Tokens: 100000}}
}

func TestPolicyRequiresFiniteLimitsAndExplicitScope(t *testing.T) {
	p := validPolicy()
	digest, err := p.Digest()
	if err != nil || digest == "" {
		t.Fatal(err)
	}
	for _, mutate := range []func(*Policy){
		func(p *Policy) { p.Limits.TargetRefreshes = -1 }, func(p *Policy) { p.Limits.TargetRefreshes = 4 },
		func(p *Policy) { p.Limits.Concurrency = 0 }, func(p *Policy) { p.Limits.Tokens = 0 }, func(p *Policy) { p.Limits.SpendUSD = math.NaN() }, func(p *Policy) { p.Limits.AttemptMinutes = 1000 }, func(p *Policy) { p.Members[0].Source = "relative" }, func(p *Policy) { p.Members[0].Paths = []string{"../other"} }, func(p *Policy) { p.Checks[0].Image = "toolchain:latest" }, func(p *Policy) { p.Checks[0].Member = "outside" }, func(p *Policy) { p.PublishPR = true; p.Members[0].Repo = "/missing" },
	} {
		p := validPolicy()
		mutate(&p)
		if p.Validate() == nil {
			t.Fatalf("invalid policy accepted: %+v", p)
		}
	}
	p.Limits.AgentRuns++
	changed, err := p.Digest()
	if err != nil || changed == digest {
		t.Fatal("policy limit change did not invalidate digest")
	}
}

func TestTargetRefreshAuthorizationPreservesLegacyPolicyDigests(t *testing.T) {
	p := validPolicy()
	before, _ := p.Digest()
	b, _ := json.Marshal(p)
	if strings.Contains(string(b), "target_refreshes") {
		t.Fatal("disabled field changes legacy accepted policy encoding")
	}
	p.Limits.TargetRefreshes = 1
	after, err := p.Digest()
	if err != nil || after == before {
		t.Fatal("refresh authorization is not bound to accepted policy")
	}
	p.Limits.TargetRefreshes = 0
	back, _ := p.Digest()
	if back != before {
		t.Fatal("disabled refresh did not preserve original policy digest")
	}
}
