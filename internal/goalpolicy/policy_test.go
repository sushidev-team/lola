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

func TestCheckMemoryIsBoundedAndBoundToPolicy(t *testing.T) {
	p := validPolicy()
	legacy, _ := p.Digest()
	b, _ := json.Marshal(p)
	if strings.Contains(string(b), "memory_mib") || p.Checks[0].MemoryLimitBytes() != 2<<30 {
		t.Fatal("default changed legacy authorization")
	}
	for _, memory := range []int{-1, 1, 255, 8193} {
		p.Checks[0].MemoryMiB = memory
		if p.Validate() == nil {
			t.Fatalf("accepted invalid memory limit %d", memory)
		}
	}
	for _, memory := range []int{256, 4096, 8192} {
		p.Checks[0].MemoryMiB = memory
		digest, err := p.Digest()
		if err != nil || digest == legacy || p.Checks[0].MemoryLimitBytes() != int64(memory)<<20 {
			t.Fatalf("memory authorization not enforced: %d %v", memory, err)
		}
	}
	p.Checks[0].MemoryMiB = 0
	if digest, _ := p.Digest(); digest != legacy {
		t.Fatal("legacy digest changed")
	}
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

func TestHumanAcceptancePreservesLegacyEncodingAndRequiresDelivery(t *testing.T) {
	p := validPolicy()
	b, _ := json.Marshal(p)
	before, _ := p.Digest()
	if strings.Contains(string(b), "require_human_acceptance") {
		t.Fatal("default changed legacy policy encoding")
	}
	p.RequireHumanAcceptance = true
	if p.Validate() == nil {
		t.Fatal("acceptance allowed without PR delivery")
	}
	p.PublishPR = true
	after, err := p.Digest()
	if err != nil || after == before {
		t.Fatal("new requirement not part of authorization digest", err)
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
