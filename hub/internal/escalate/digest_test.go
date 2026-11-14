package escalate

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/sushidev-team/lola/hub/internal/secret"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/sushidev-team/lola/hub/internal/lease"
	"github.com/sushidev-team/lola/hub/internal/store"
	"github.com/sushidev-team/lola/hubproto"
)

func digestStore(t *testing.T) *store.File {
	t.Helper()
	s, _ := store.Open("")
	now := t0
	_ = s.Update(func(d *store.Data) error {
		d.Orgs["acme"] = &store.Org{Name: "acme"}
		d.Items["ic"] = lease.NewItem("ic", "acme", critical(), "dev", now.Add(-time.Hour)) // new critical, escalation pending
		rv := lease.NewItem("ir", "acme", advisory("review-me", hubproto.SeverityHigh), "dev", now.Add(-10*24*time.Hour))
		rv.State = hubproto.StateInReview
		rv.History = []store.Attempt{{Action: hubproto.AttemptInReview, PRURL: "https://github.com/acme/web/pull/3", At: now.Add(-4 * 24 * time.Hour)}}
		d.Items["ir"] = rv
		cl := lease.NewItem("il", "acme", advisory("claimed-long", hubproto.SeverityLow), "dev", now.Add(-2*24*time.Hour))
		cl.State, cl.ClaimedAt = hubproto.StateClaimed, now.Add(-13*time.Hour)
		d.Items["il"] = cl
		fresh := lease.NewItem("if", "acme", advisory("claimed-fresh", hubproto.SeverityLow), "dev", now.Add(-2*24*time.Hour))
		fresh.State, fresh.ClaimedAt = hubproto.StateClaimed, now.Add(-time.Hour)
		d.Items["if"] = fresh
		f := lease.NewItem("ix", "acme", advisory("failed-one", hubproto.SeverityModerate), "dev", now.Add(-3*24*time.Hour))
		f.State, f.ClosedAt = hubproto.StateFailed, now.Add(-2*time.Hour)
		d.Items["ix"] = f
		bad := lease.NewItem("ib", "acme", advisory("x", hubproto.SeverityModerate), "dev", now.Add(-3*24*time.Hour))
		bad.Item.DepAdvisory.Package = "evil`\n<!channel>"
		bad.State, bad.ClosedAt = hubproto.StateFailed, now.Add(-time.Hour)
		d.Items["ib"] = bad
		return nil
	})
	return s
}

func TestBuildDigestCountsFromValidatedFields(t *testing.T) {
	s := digestStore(t)
	var r DigestReport
	_ = s.View(func(d *store.Data) error { r = BuildDigest(d, "acme", t0); return nil })
	if len(r.NewCritical) != 1 || len(r.InReviewLong) != 1 || len(r.ClaimedLong) != 1 || len(r.Failed) != 2 || r.EscalationsWait != 1 {
		t.Fatalf("%+v", r)
	}
	txt := r.Text()
	for _, want := range []string{"New critical: 1", "claimed-long", "review-me", "https://github.com/acme/web/pull/3", "Failed in the last day: 2", "Escalations waiting: 1"} {
		if !strings.Contains(txt, want) {
			t.Fatalf("missing %q in\n%s", want, txt)
		}
	}
	if strings.Contains(txt, "evil") || strings.Contains(txt, "<!channel>") || strings.Contains(txt, "claimed-fresh") {
		t.Fatalf("unvalidated or unqualified item in digest:\n%s", txt)
	}
}

// reroute sends every request to srv, whatever host it names, and records
// the URL that was asked for.
type reroute struct {
	srv  *httptest.Server
	seen []string
	fail bool
}

func (r *reroute) RoundTrip(req *http.Request) (*http.Response, error) {
	r.seen = append(r.seen, req.URL.String())
	if r.fail {
		return nil, errors.New("dial " + req.URL.String())
	}
	out, _ := http.NewRequest(req.Method, r.srv.URL, req.Body)
	out.Header = req.Header
	return http.DefaultTransport.RoundTrip(out)
}

func testBox(t *testing.T) *secret.Box {
	t.Helper()
	k, _ := secret.NewKey()
	b, err := secret.New(k)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func setHook(t *testing.T, s store.Store, box *secret.Box, org, hook string) {
	t.Helper()
	sealed, err := box.Seal(org, secret.FieldDigestWebhook, hook)
	if err != nil {
		t.Fatal(err)
	}
	_ = s.Update(func(d *store.Data) error { d.Orgs[org].DigestWebhook = sealed; return nil })
}

func digestErr(s store.Store, org string) (class string) {
	_ = s.View(func(d *store.Data) error { class = d.Orgs[org].DigestErr; return nil })
	return
}

const slackHook = "https://hooks.slack.com/services/T0001/B0001/SECRETsecret"

func TestDigesterPostsSlackTextAndFailsOpen(t *testing.T) {
	s := digestStore(t)
	box := testBox(t)
	setHook(t, s, box, "acme", slackHook)
	var got map[string]string
	status := http.StatusOK
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&got)
		w.WriteHeader(status)
	}))
	defer srv.Close()
	rt := &reroute{srv: srv}
	var logs []string
	g := &Digester{Store: s, Box: box, Client: &http.Client{Transport: rt}, Now: func() time.Time { return t0 },
		Logf: func(f string, a ...any) { logs = append(logs, fmt.Sprintf(f, a...)) }}
	if sent, failed := g.SendOnce(context.Background()); sent != 1 || failed != 0 {
		t.Fatalf("sent %d failed %d", sent, failed)
	}
	if len(got) != 1 || !strings.Contains(got["text"], "lola-hub daily digest: acme") || rt.seen[0] != slackHook {
		t.Fatalf("payload %v to %v", got, rt.seen)
	}
	status = http.StatusInternalServerError
	if sent, failed := g.SendOnce(context.Background()); sent != 0 || failed != 1 {
		t.Fatalf("sent %d failed %d", sent, failed)
	}
	if c := digestErr(s, "acme"); c != ErrDigestPost.Error() {
		t.Fatalf("status class %q", c)
	}
	rt.fail = true
	g.SendOnce(context.Background())
	if strings.Contains(strings.Join(logs, "\n")+digestErr(s, "acme"), "SECRET") {
		t.Fatalf("the webhook leaked: %v", logs)
	}
	rt.fail, status = false, http.StatusOK
	g.SendOnce(context.Background())
	if c := digestErr(s, "acme"); c != "" {
		t.Fatalf("status class not cleared: %q", c)
	}
}

func TestSlackEscape(t *testing.T) {
	if got := slackEscape("< 1.2 & <!here>"); got != "&lt; 1.2 &amp; &lt;!here&gt;" {
		t.Fatal(got)
	}
}

// Each org's digest goes to its own webhook; an org without one gets none.
func TestDigesterIsPerOrg(t *testing.T) {
	s := digestStore(t)
	box := testBox(t)
	_ = s.Update(func(d *store.Data) error {
		d.Orgs["beta"] = &store.Org{Name: "beta"}
		d.Orgs["gamma"] = &store.Org{Name: "gamma"}
		return nil
	})
	const betaHook = "https://hooks.slack.com/services/T0002/B0002/betabeta"
	setHook(t, s, box, "acme", slackHook)
	setHook(t, s, box, "beta", betaHook)
	texts := map[string]string{}
	var mu sync.Mutex
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var got map[string]string
		_ = json.NewDecoder(r.Body).Decode(&got)
		mu.Lock()
		texts[r.Header.Get("X-Asked")] = got["text"]
		mu.Unlock()
	}))
	defer srv.Close()
	rt := &reroute{srv: srv}
	g := &Digester{Store: s, Box: box, Client: &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		req.Header.Set("X-Asked", req.URL.String())
		return rt.RoundTrip(req)
	})}, Now: func() time.Time { return t0 }}
	if sent, failed := g.SendOnce(context.Background()); sent != 2 || failed != 0 {
		t.Fatalf("sent %d failed %d", sent, failed)
	}
	if !strings.Contains(texts[slackHook], "digest: acme") || !strings.Contains(texts[betaHook], "digest: beta") || len(texts) != 2 {
		t.Fatalf("digests went to the wrong webhooks: %v", texts)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// A webhook that was moved between orgs in the store, or that no longer
// names Slack, is never posted to.
func TestDigesterRefusesWebhookItCannotTrust(t *testing.T) {
	s := digestStore(t)
	box := testBox(t)
	_ = s.Update(func(d *store.Data) error { d.Orgs["beta"] = &store.Org{Name: "beta"}; return nil })
	setHook(t, s, box, "acme", "https://169.254.169.254/latest/meta-data")
	sealed, _ := box.Seal("acme", secret.FieldDigestWebhook, slackHook)
	_ = s.Update(func(d *store.Data) error { d.Orgs["beta"].DigestWebhook = sealed; return nil })
	rt := &reroute{fail: true}
	g := &Digester{Store: s, Box: box, Client: &http.Client{Transport: rt}}
	if sent, failed := g.SendOnce(context.Background()); sent != 0 || failed != 2 || len(rt.seen) != 0 {
		t.Fatalf("sent %d failed %d, requests %v", sent, failed, rt.seen)
	}
	if c := digestErr(s, "beta"); c != ErrDigestSealed.Error() {
		t.Fatalf("class %q", c)
	}
}
