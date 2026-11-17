package escalate

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/sushidev-team/lola/hub/internal/secret"
	"github.com/sushidev-team/lola/hub/internal/store"
	"github.com/sushidev-team/lola/hubproto"
)

// EnvDigest names the environment variable an OLDER hub read its one digest
// webhook from. It is only adopted into a single-org hub at startup now
// (admin.AdoptLegacyEnv); every org holds its own webhook, sealed.
const EnvDigest = "LOLA_HUB_DIGEST_WEBHOOK"

// Digest thresholds.
const (
	DigestInterval    = 24 * time.Hour
	DigestInReviewAge = 3 * 24 * time.Hour
	DigestClaimedAge  = 12 * time.Hour
	// DigestMaxLines bounds the item lines per section.
	DigestMaxLines = 10
)

// DigestReport is what a digest counts. Every line is built from validated
// item fields only.
type DigestReport struct {
	Org             string
	NewCritical     []string
	InReviewLong    []string
	ClaimedLong     []string
	Failed          []string
	EscalationsWait int
}

// Empty reports whether there is nothing to say.
func (r DigestReport) Empty() bool {
	return len(r.NewCritical)+len(r.InReviewLong)+len(r.ClaimedLong)+len(r.Failed) == 0 && r.EscalationsWait == 0
}

// digestLine is one item, from validated fields; "" for an item that does
// not validate (it is counted, never shown).
func digestLine(w *store.WorkItem) string {
	it := w.Item
	it.ID = ""
	if hubproto.Validate(it) != nil {
		return ""
	}
	s := itemTitle(it)
	if u, _ := lastPR(w); u != "" {
		s += " " + u
	}
	return s
}

// inReviewSince is when the item entered in_review: the newest in_review
// history entry, else the owner's last progress call.
func inReviewSince(w *store.WorkItem) time.Time {
	for i := len(w.History) - 1; i >= 0; i-- {
		if w.History[i].Action == hubproto.AttemptInReview {
			return w.History[i].At
		}
	}
	return w.ProgressAt
}

// BuildDigest counts org's items at now over the last DigestInterval.
func BuildDigest(d *store.Data, org string, now time.Time) DigestReport {
	r := DigestReport{Org: org}
	var items []*store.WorkItem
	for _, w := range d.Items {
		if w.Org == org {
			items = append(items, w)
		}
	}
	sort.Slice(items, func(i, j int) bool {
		if !items[i].FirstSeen.Equal(items[j].FirstSeen) {
			return items[i].FirstSeen.Before(items[j].FirstSeen)
		}
		return items[i].ID < items[j].ID
	})
	for _, w := range items {
		line := digestLine(w)
		switch {
		case w.Item.Severity() == hubproto.SeverityCritical && now.Sub(w.FirstSeen) < DigestInterval:
			r.NewCritical = append(r.NewCritical, line)
		}
		switch w.State {
		case hubproto.StateInReview:
			if since := inReviewSince(w); !since.IsZero() && now.Sub(since) >= DigestInReviewAge {
				r.InReviewLong = append(r.InReviewLong, line)
			}
		case hubproto.StateClaimed:
			at := w.ClaimedAt
			if at.IsZero() && w.Lease != nil {
				at = w.Lease.ClaimedAt
			}
			if !at.IsZero() && now.Sub(at) >= DigestClaimedAge {
				r.ClaimedLong = append(r.ClaimedLong, line)
			}
		case hubproto.StateFailed:
			if !w.ClosedAt.IsZero() && now.Sub(w.ClosedAt) < DigestInterval {
				r.Failed = append(r.Failed, line)
			}
		}
		if w.Escalation != nil && w.Escalation.Pending {
			r.EscalationsWait++
		}
	}
	return r
}

// slackEscape escapes what Slack's mrkdwn treats as markup. Validated
// fields may still carry '<' (a version range), which must stay text.
func slackEscape(s string) string {
	return strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;").Replace(s)
}

// Text renders the digest as Slack mrkdwn.
func (r DigestReport) Text() string {
	var b strings.Builder
	fmt.Fprintf(&b, "*lola-hub daily digest: %s*\n", slackEscape(r.Org))
	section := func(title string, lines []string) {
		if len(lines) == 0 {
			return
		}
		fmt.Fprintf(&b, "\n*%s: %d*\n", title, len(lines))
		shown := 0
		for _, l := range lines {
			if l == "" || shown == DigestMaxLines {
				continue
			}
			fmt.Fprintf(&b, "• %s\n", slackEscape(l))
			shown++
		}
		if rest := len(lines) - shown; rest > 0 {
			fmt.Fprintf(&b, "• and %d more\n", rest)
		}
	}
	section("New critical", r.NewCritical)
	section("In review for more than 3 days", r.InReviewLong)
	section("Claimed for more than 12 hours", r.ClaimedLong)
	section("Failed in the last day", r.Failed)
	if r.EscalationsWait > 0 {
		fmt.Fprintf(&b, "\n*Escalations waiting: %d*\n", r.EscalationsWait)
	}
	if r.Empty() {
		b.WriteString("\nNothing needs attention.\n")
	}
	return b.String()
}

// Digester posts each org's daily digest to that org's OWN Slack webhook
// (store.Org.DigestWebhook, sealed; opened with Box only for the post). An
// org without a webhook gets none, and one org's chores never reach another
// org's channel. It fails open: a failed post is recorded as a class on the
// org and retried on the next tick; nothing waits on it.
type Digester struct {
	Store  store.Store
	Box    *secret.Box
	Client *http.Client
	Now    func() time.Time
	Logf   func(format string, args ...any)
}

func (g *Digester) now() time.Time {
	if g.Now != nil {
		return g.Now()
	}
	return time.Now()
}

func (g *Digester) logf(format string, args ...any) {
	if g.Logf != nil {
		g.Logf(format, args...)
	}
}

// Digest error classes: never the webhook URL or a response body.
var (
	ErrDigestPost   = errors.New("digest: webhook post failed")
	ErrDigestSealed = errors.New("digest: webhook cannot be opened")
)

// Enabled reports whether a digest can be posted at all (a secret key).
func (g *Digester) Enabled() bool { return g.Box != nil }

// SendOnce builds and posts one digest per org that has a webhook, and
// returns how many were posted and how many failed.
func (g *Digester) SendOnce(ctx context.Context) (sent, failed int) {
	type post struct {
		org, hook string
		err       error
		text      string
	}
	var posts []post
	_ = g.Store.View(func(d *store.Data) error {
		for name, o := range d.Orgs {
			if o.DigestWebhook == "" {
				continue
			}
			p := post{org: name}
			hook, err := g.Box.Open(name, secret.FieldDigestWebhook, o.DigestWebhook)
			// Re-validated on every use: the hub posts to this URL by
			// itself, so a store edit must not aim it elsewhere.
			if err != nil || !hubproto.ValidDigestWebhook(hook) {
				p.err = ErrDigestSealed
			} else {
				p.hook, p.text = hook, BuildDigest(d, name, g.now()).Text()
			}
			posts = append(posts, p)
		}
		return nil
	})
	sort.Slice(posts, func(i, j int) bool { return posts[i].org < posts[j].org })
	for i := range posts {
		p := &posts[i]
		if p.err == nil {
			p.err = g.post(ctx, p.hook, p.text)
		}
		if p.err != nil {
			failed++
			g.logf("digest: org %s: %s", p.org, digestClass(p.err))
		} else {
			sent++
		}
	}
	if len(posts) == 0 {
		return
	}
	err := g.Store.Update(func(d *store.Data) error {
		changed := false
		for _, p := range posts {
			o := d.Orgs[p.org]
			if o == nil {
				continue
			}
			class := ""
			if p.err != nil {
				class = digestClass(p.err)
			}
			if o.DigestErr != class {
				o.DigestErr, changed = class, true
			}
		}
		if !changed {
			return errSkip
		}
		return nil
	})
	if err != nil && !errors.Is(err, errSkip) {
		g.logf("digest: record status: %v", err)
	}
	return
}

func digestClass(err error) string {
	if errors.Is(err, ErrDigestSealed) {
		return ErrDigestSealed.Error()
	}
	return ErrDigestPost.Error()
}

func (g *Digester) post(ctx context.Context, hook, text string) error {
	body, err := json.Marshal(map[string]string{"text": text})
	if err != nil {
		return ErrDigestPost
	}
	cctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(cctx, http.MethodPost, hook, bytes.NewReader(body))
	if err != nil {
		return ErrDigestPost
	}
	req.Header.Set("Content-Type", "application/json")
	c := g.Client
	if c == nil {
		// Never follow a redirect: the URL was validated, a Location was not.
		c = &http.Client{Timeout: 20 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		}}
	}
	resp, err := c.Do(req)
	if err != nil {
		return ErrDigestPost // *url.Error would carry the webhook URL
	}
	resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return ErrDigestPost
	}
	return nil
}

// Run posts a digest every interval (0 = DigestInterval) until ctx ends,
// starting one interval after start. It never panics out.
func (g *Digester) Run(ctx context.Context, interval time.Duration) {
	if !g.Enabled() {
		return
	}
	if interval <= 0 {
		interval = DigestInterval
	}
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		func() {
			defer func() {
				if r := recover(); r != nil {
					g.logf("digest: panic recovered")
				}
			}()
			g.SendOnce(ctx)
		}()
	}
}
