package goalpolicy

import (
	"encoding/json"
	"strings"
	"testing"
)

func stackPolicy() Policy {
	p := validPolicy()
	p.PublishPR = true
	p.Members = append(p.Members, Member{ID: "web", Project: "web", Source: "/private/web", Repo: "owner/web", Target: "main"})
	return p
}

func TestRolloutRequiresExplicitNewAuthorizationAndPreservesLegacyDigest(t *testing.T) {
	p := stackPolicy()
	before, err := p.Digest()
	if err != nil {
		t.Fatal("legacy stack rejected", err)
	}
	b, _ := json.Marshal(p)
	if strings.Contains(string(b), "rollout") || p.ValidateAuthorization() == nil {
		t.Fatal("missing new authorization requirement or changed legacy bytes")
	}
	p.Rollout = &Rollout{Compatibility: []string{"API must support the previous web client until the web merge."}, MergeOrder: []string{"api", "web"}}
	after, err := p.Digest()
	if err != nil || p.ValidateAuthorization() != nil || after == before {
		t.Fatal("rollout not bound to accepted policy", err)
	}
	p.Rollout.MergeOrder = []string{"web", "api"}
	changed, _ := p.Digest()
	if changed == after {
		t.Fatal("changed human merge order retained evidence identity")
	}
	p.Rollout.Compatibility[0] = "Web must tolerate the previous API until the API merge."
	changedAgain, _ := p.Digest()
	if changedAgain == changed {
		t.Fatal("changed compatibility retained evidence identity")
	}
	p.Rollout = nil
	restored, _ := p.Digest()
	if restored != before {
		t.Fatal("legacy digest not preserved")
	}
}

func TestRolloutRejectsIncompleteUnboundedAndForeignInputs(t *testing.T) {
	for _, kind := range []string{"missing", "blank", "long", "many", "control", "newline", "utf8", "partial", "duplicate", "foreign", "extra", "no-publication"} {
		t.Run(kind, func(t *testing.T) {
			p := stackPolicy()
			p.Rollout = &Rollout{Compatibility: []string{"Keep the old API usable during rollout."}, MergeOrder: []string{"api", "web"}}
			switch kind {
			case "missing":
				p.Rollout.Compatibility = nil
			case "blank":
				p.Rollout.Compatibility[0] = "  "
			case "long":
				p.Rollout.Compatibility[0] = strings.Repeat("x", 501)
			case "many":
				p.Rollout.Compatibility = make([]string, 21)
			case "control":
				p.Rollout.Compatibility[0] = "bad\x1btext"
			case "newline":
				p.Rollout.Compatibility[0] = "one\ntwo"
			case "utf8":
				p.Rollout.Compatibility[0] = string([]byte{255})
			case "partial":
				p.Rollout.MergeOrder = []string{"api"}
			case "duplicate":
				p.Rollout.MergeOrder = []string{"api", "api"}
			case "foreign":
				p.Rollout.MergeOrder = []string{"api", "elsewhere"}
			case "extra":
				p.Rollout.MergeOrder = []string{"api", "web", "api"}
			case "no-publication":
				p.PublishPR = false
			}
			if p.Validate() == nil || p.ValidateAuthorization() == nil {
				t.Fatal("invalid rollout accepted")
			}
		})
	}
}
