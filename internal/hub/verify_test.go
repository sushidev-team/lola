package hub

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/sushidev-team/lola/hubproto"
)

const head40 = "0123456789abcdef0123456789abcdef01234567"

func verifyBump() hubproto.Item {
	return hubproto.Item{ID: "it1", Kind: hubproto.KindRedBumpPR, RedBumpPR: &hubproto.RedBumpPR{
		Repo: "acme/web", PR: 11, Branch: "dependabot/npm_and_yarn/axios-1.7.4", Package: "axios",
		Author: hubproto.AuthorDependabot,
	}}
}

func prView(mut func(m map[string]string)) string {
	m := map[string]string{
		"state": `"OPEN"`, "author": `{"login":"app/dependabot"}`,
		"headRefName": `"dependabot/npm_and_yarn/axios-1.7.4"`, "headRefOid": `"` + head40 + `"`,
		"isCrossRepository": `false`,
		"statusCheckRollup": `[{"name":"test","status":"COMPLETED","conclusion":"FAILURE"}]`,
	}
	if mut != nil {
		mut(m)
	}
	var parts []string
	for k, v := range m {
		if v != "" {
			parts = append(parts, `"`+k+`":`+v)
		}
	}
	return "{" + strings.Join(parts, ",") + "}"
}

func ghReturning(out string, err error, seen *[]string) GhFunc {
	return func(_ context.Context, args ...string) ([]byte, error) {
		if seen != nil {
			*seen = append(*seen, strings.Join(args, " "))
		}
		return []byte(out), err
	}
}

func TestVerifyRedBumpConfirms(t *testing.T) {
	var seen []string
	c := &Collector{Gh: ghReturning(prView(nil), nil, &seen)}
	v, err := c.Verify(context.Background(), verifyBump())
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if v.Head != head40 || v.Item.RedBumpPR.Branch != "dependabot/npm_and_yarn/axios-1.7.4" || v.Item.ID != "it1" {
		t.Errorf("verified = %+v / %+v", v, v.Item.RedBumpPR)
	}
	if len(seen) != 1 || !strings.HasPrefix(seen[0], "pr view 11 --repo acme/web --json ") {
		t.Errorf("gh calls = %q", seen)
	}
}

func TestVerifyRedBumpRefuses(t *testing.T) {
	cases := map[string]func(m map[string]string){
		"closed":        func(m map[string]string) { m["state"] = `"CLOSED"` },
		"merged":        func(m map[string]string) { m["state"] = `"MERGED"` },
		"human author":  func(m map[string]string) { m["author"] = `{"login":"mallory"}` },
		"other bot":     func(m map[string]string) { m["author"] = `{"login":"app/renovate"}` },
		"other branch":  func(m map[string]string) { m["headRefName"] = `"main"` },
		"cross repo":    func(m map[string]string) { m["isCrossRepository"] = `true` },
		"cross unknown": func(m map[string]string) { m["isCrossRepository"] = "" },
		"no head":       func(m map[string]string) { m["headRefOid"] = `""` },
		"checks green": func(m map[string]string) {
			m["statusCheckRollup"] = `[{"name":"test","status":"COMPLETED","conclusion":"SUCCESS"}]`
		},
		"checks none":      func(m map[string]string) { m["statusCheckRollup"] = `[]` },
		"checks only lola": func(m map[string]string) { m["statusCheckRollup"] = `[{"context":"lola/review/x","state":"FAILURE"}]` },
	}
	for name, mut := range cases {
		c := &Collector{Gh: ghReturning(prView(mut), nil, nil)}
		if _, err := c.Verify(context.Background(), verifyBump()); !errors.Is(err, ErrNotConfirmed) {
			t.Errorf("%s: err = %v, want ErrNotConfirmed", name, err)
		}
	}
	c := &Collector{Gh: ghReturning("", errors.New("boom"), nil)}
	if _, err := c.Verify(context.Background(), verifyBump()); err == nil {
		t.Error("a gh error must refuse")
	}
	c = &Collector{Gh: ghReturning("{not json", nil, nil)}
	if _, err := c.Verify(context.Background(), verifyBump()); err == nil {
		t.Error("bad JSON must refuse")
	}
}

func TestVerifyAdvisoryUsesGitHubsAlert(t *testing.T) {
	c := &Collector{Gh: ghReturning(alertsPage1, nil, nil)}
	// The hub names a range and fixed version of its own: GitHub's win.
	it := hubproto.Item{ID: "a1", Kind: hubproto.KindDepAdvisory, DepAdvisory: &hubproto.DepAdvisory{
		Repo: "acme/web", Ecosystem: "npm", Package: "lodash", Vulnerable: "< 99",
		Fixed: "99.0.0", Advisory: "GHSA-jf85-cpcp-j695", Severity: hubproto.SeverityLow,
	}}
	v, err := c.Verify(context.Background(), it)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	d := v.Item.DepAdvisory
	if v.Item.ID != "a1" || d.Fixed != "4.17.12" || d.Vulnerable != "< 4.17.12" || d.Severity != hubproto.SeverityCritical {
		t.Errorf("verified advisory = %+v", d)
	}
	brief, err := Briefing(v.Item)
	if err != nil || strings.Contains(brief, "99") {
		t.Errorf("briefing must use GitHub's versions: %v\n%s", err, brief)
	}
}

func TestVerifyAdvisoryRefuses(t *testing.T) {
	base := hubproto.DepAdvisory{
		Repo: "acme/web", Ecosystem: "npm", Package: "lodash", Vulnerable: "< 4.17.12",
		Fixed: "4.17.12", Advisory: "GHSA-jf85-cpcp-j695", Severity: hubproto.SeverityCritical,
	}
	muts := map[string]func(*hubproto.DepAdvisory){
		"other package":   func(d *hubproto.DepAdvisory) { d.Package = "lodash-es" },
		"other ecosystem": func(d *hubproto.DepAdvisory) { d.Ecosystem = "pip" },
		"other advisory":  func(d *hubproto.DepAdvisory) { d.Advisory = "GHSA-j8r2-6x86-q33q" },
	}
	for name, mut := range muts {
		d := base
		mut(&d)
		c := &Collector{Gh: ghReturning(alertsPage1, nil, nil)}
		_, err := c.Verify(context.Background(), hubproto.Item{ID: "a1", Kind: hubproto.KindDepAdvisory, DepAdvisory: &d})
		if !errors.Is(err, ErrNotConfirmed) {
			t.Errorf("%s: err = %v, want ErrNotConfirmed", name, err)
		}
	}
	d := base
	c := &Collector{Gh: ghReturning("", &GhError{Cmd: "api", HTTPStatus: 403, Err: errors.New("exit 1")}, nil)}
	if _, err := c.Verify(context.Background(), hubproto.Item{ID: "a1", Kind: hubproto.KindDepAdvisory, DepAdvisory: &d}); err == nil {
		t.Error("alerts unavailable must refuse")
	}
}
