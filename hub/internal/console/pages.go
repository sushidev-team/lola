package console

import (
	"bytes"
	"encoding/json"
	"fmt"
	"html/template"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/sushidev-team/lola/hub/internal/admin"
	"github.com/sushidev-team/lola/hub/internal/api"
	"github.com/sushidev-team/lola/hub/internal/escalate"
	"github.com/sushidev-team/lola/hub/internal/store"
	"github.com/sushidev-team/lola/hubproto"
)

// States in display order.
var states = []hubproto.State{hubproto.StateOpen, hubproto.StateClaimed, hubproto.StateInReview, hubproto.StateEscalated, hubproto.StateDone, hubproto.StateFailed}

var funcs = template.FuncMap{
	"ago": func(t time.Time) string {
		if t.IsZero() {
			return "never"
		}
		d := time.Since(t)
		switch {
		case d < 0:
			return "in " + roundDur(-d)
		case d < time.Minute:
			return "just now"
		}
		return roundDur(d) + " ago"
	},
	"stamp": func(t time.Time) string {
		if t.IsZero() {
			return "-"
		}
		return t.UTC().Format("2006-01-02 15:04 UTC")
	},
	"short": func(s string) string {
		if len(s) > 10 {
			return s[:10] + "…"
		}
		return s
	},
	// acts bundles what the resolve/reopen buttons need; ret is the item id
	// to come back to ("" = the org page).
	"acts": func(org, id, csrf string, filter any, ret string) actArgs {
		return actArgs{Org: org, ID: id, CSRF: csrf, Filter: fmt.Sprint(filter), Return: ret}
	},
	"dur": func(secs int64) string {
		if secs <= 0 {
			return "-"
		}
		return roundDur(time.Duration(secs) * time.Second)
	},
}

type actArgs struct{ Org, ID, CSRF, Filter, Return string }

func roundDur(d time.Duration) string {
	switch {
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 48*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	}
	return fmt.Sprintf("%dd", int(d.Hours()/24))
}

type pageData struct {
	Title string
	CSRF  string
	Flash *flash
	Body  any
}

func (c *Console) render(w http.ResponseWriter, status int, page string, s *session, d pageData) {
	if s != nil {
		d.CSRF = s.csrf
		if d.Flash == nil {
			d.Flash = c.takeFlash(s)
		}
	}
	var buf bytes.Buffer
	if err := c.pages[page].Execute(&buf, d); err != nil {
		c.logf("console: render %s: %v", page, err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	_, _ = buf.WriteTo(w)
}

// back redirects to the org page (keeping a valid state filter), or to the
// item page an action was taken from.
func back(w http.ResponseWriter, r *http.Request, org string) {
	u := "/orgs/" + url.PathEscape(org)
	if id := r.PostFormValue("return_item"); id != "" && hubproto.ValidItemID(id) {
		u += "/items/" + url.PathEscape(id)
	} else if st := hubproto.State(r.PostFormValue("return_state")); st.Valid() {
		u += "?state=" + url.QueryEscape(string(st))
	}
	http.Redirect(w, r, u, http.StatusSeeOther)
}

func (c *Console) done(w http.ResponseWriter, r *http.Request, s *session, org string, err error, okFormat string, args ...any) {
	if err != nil {
		c.setFlash(s, true, "%s", err.Error())
	} else {
		c.setFlash(s, false, okFormat, args...)
	}
	back(w, r, org)
}

// ---- overview -----------------------------------------------------------------

type orgRow struct {
	Name    string
	Devices int
	Counts  []stateCount
	Policy  int
	Team    string
	Key     bool // a Linear key is set
	Digest  bool // a digest webhook is set
}

type stateCount struct {
	State hubproto.State
	N     int
}

type eventRow struct {
	At     time.Time
	Org    string
	Kind   string
	Item   string
	Device string
	Detail string
}

// linearBody is the escalation status shown on top of a page: whether the
// hub can open secrets at all, whether the org has a key and a team, and how
// many escalations wait.
type linearBody struct {
	NoBox   bool // the hub runs without a secret key: no org's key is usable
	Enabled bool // the org has a Linear key the hub can open (overview: true)
	Waiting int
	NoTeam  bool // an org page only: the org has no team, its escalations stay pending
}

func (c *Console) linear(d *store.Data, org string) linearBody {
	return linearBody{NoBox: c.cfg.Box == nil, Enabled: escalate.KeyReady(d, org, c.cfg.Box),
		Waiting: escalate.Waiting(d, org), NoTeam: escalate.TeamFor(d, org, c.cfg.LinearTeam) == ""}
}

type overviewBody struct {
	Orgs   []orgRow
	Events []eventRow
	Totals []stateCount
	Linear linearBody
	// Code is a create code minted by the request before this one, shown
	// once.
	Code       string
	CodeExpiry time.Time
}

func counts(d *store.Data, org string) []stateCount {
	m := map[hubproto.State]int{}
	for _, w := range d.Items {
		if org == "" || w.Org == org {
			m[w.State]++
		}
	}
	out := make([]stateCount, 0, len(states))
	for _, st := range states {
		out = append(out, stateCount{st, m[st]})
	}
	return out
}

func recentEvents(d *store.Data, org string, n int) []eventRow {
	var out []eventRow
	for i := len(d.Events) - 1; i >= 0 && len(out) < n; i-- {
		e := d.Events[i]
		if org != "" && e.Org != org {
			continue
		}
		out = append(out, eventRow{At: e.At, Org: e.Org, Kind: e.Kind, Item: e.Item, Device: e.Device, Detail: e.Detail})
	}
	return out
}

func (c *Console) overview(w http.ResponseWriter, r *http.Request, s *session) {
	var body overviewBody
	_ = c.cfg.Store.View(func(d *store.Data) error {
		names := make([]string, 0, len(d.Orgs))
		for n := range d.Orgs {
			names = append(names, n)
		}
		sort.Strings(names)
		for _, n := range names {
			o := d.Orgs[n]
			row := orgRow{Name: n, Counts: counts(d, n), Team: o.LinearTeam, Key: o.LinearKey != "", Digest: o.DigestWebhook != ""}
			if o.Policy != nil {
				row.Policy = o.Policy.Version
			}
			for _, dv := range d.Devices {
				if dv.Org == n && !dv.Revoked() {
					row.Devices++
				}
			}
			body.Orgs = append(body.Orgs, row)
		}
		body.Totals = counts(d, "")
		body.Events = recentEvents(d, "", 30)
		// Keys and teams are per org: the overview only says whether the
		// hub can use any of them, and how many escalations wait.
		body.Linear = linearBody{NoBox: c.cfg.Box == nil, Enabled: true, Waiting: escalate.Waiting(d, "")}
		return nil
	})
	if rv := c.takeReveal(s, ""); rv != nil {
		body.Code, body.CodeExpiry = rv.token, rv.expiry
	}
	c.render(w, http.StatusOK, "overview", s, pageData{Title: "Overview", Body: body})
}

func (c *Console) createOrg(w http.ResponseWriter, r *http.Request, s *session) {
	name := strings.TrimSpace(r.PostFormValue("name"))
	if err := admin.CreateOrg(c.cfg.Store, name, c.cfg.Now()); err != nil {
		c.setFlash(s, true, "%s", err.Error())
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}
	c.setFlash(s, false, "Created org %s.", name)
	http.Redirect(w, r, "/orgs/"+url.PathEscape(name), http.StatusSeeOther)
}

// ---- org page -------------------------------------------------------------------

type deviceRow struct {
	ID            string
	Name          string
	Member        string
	Revoked       bool
	LastSeen      time.Time
	Active        bool
	FreeSlots     int
	Agents        string
	Repos         int
	PolicyVersion int
	PolicyCurrent bool // applies the org's published version
}

type itemRow struct {
	ID         string
	State      hubproto.State
	Kind       hubproto.Kind
	Repo       string
	Subject    string
	Severity   string
	Attempts   int
	Reporters  int
	Holder     string
	Until      time.Time
	Owner      string
	PRURL      string
	Delivery   string
	Escalation string
	FirstSeen  time.Time
	LastSeen   time.Time
}

type keyRow struct {
	ID      string
	Trusted string
}

type statsRow struct {
	Date      string
	Opened    int
	Done      int
	Failed    int
	Escalated int
	AvgFix    int64 // seconds per done item; 0 = none
}

type orgBody struct {
	Org         string
	Team        string
	Linear      linearBody
	Settings    hubproto.OrgSettingsView
	Devices     []deviceRow
	Tokens      []admin.TokenInfo
	Items       []itemRow
	Filter      hubproto.State
	Counts      []stateCount
	All         int
	Keys        []keyRow
	Policy      *hubproto.Policy
	PolicyJSON  string
	PolicyKeys  []string
	Events      []eventRow
	Stats       []statsRow
	StatsTotal  statsRow
	Token       string
	TokenLabel  string
	TokenExpiry time.Time
	Kinds       []hubproto.Kind
	Severities  []hubproto.Severity
	Authors     []string
	States      []hubproto.State
}

// memberName passes a member label only when it validates: it is display
// data written by an enrolment, rendered as text either way.
func memberName(m string) string {
	if hubproto.ValidMemberLabel(m) {
		return m
	}
	return ""
}

// statsRows turns the org's daily counters (the C11 stats) into table rows,
// newest day first, inside the stats window.
func statsRows(d *store.Data, org string, now time.Time) ([]statsRow, statsRow) {
	oldest := store.StatsOldest(now)
	var rows []statsRow
	var tot statsRow
	var fix int64
	days := d.Stats[org]
	for i := len(days) - 1; i >= 0; i-- {
		s := days[i]
		if s.Date < oldest {
			continue
		}
		row := statsRow{Date: s.Date, Opened: s.Opened, Done: s.Done, Failed: s.Failed, Escalated: s.Escalated}
		if s.Done > 0 {
			row.AvgFix = s.FixSecondsSum / int64(s.Done)
		}
		rows = append(rows, row)
		tot.Opened += s.Opened
		tot.Done += s.Done
		tot.Failed += s.Failed
		tot.Escalated += s.Escalated
		fix += s.FixSecondsSum
	}
	if tot.Done > 0 {
		tot.AvgFix = fix / int64(tot.Done)
	}
	tot.Date = fmt.Sprintf("%d days", hubproto.StatsDays)
	return rows, tot
}

func (c *Console) orgBody(org string, filter hubproto.State) (orgBody, bool) {
	b := orgBody{Org: org, Filter: filter, States: states,
		Kinds:      []hubproto.Kind{hubproto.KindDepAdvisory, hubproto.KindRedBumpPR},
		Severities: []hubproto.Severity{hubproto.SeverityLow, hubproto.SeverityModerate, hubproto.SeverityHigh, hubproto.SeverityCritical},
		Authors:    []string{hubproto.AuthorDependabot, hubproto.AuthorRenovate},
	}
	found := false
	now := c.cfg.Now()
	_ = c.cfg.Store.View(func(d *store.Data) error {
		o := d.Orgs[org]
		if o == nil {
			return nil
		}
		found = true
		b.Team = o.LinearTeam
		b.Linear = c.linear(d, org)
		b.Settings = admin.Settings(d, org, c.cfg.Box)
		for id, enc := range o.AdminKeys {
			trusted := id + ":" + enc
			b.Keys = append(b.Keys, keyRow{ID: id, Trusted: trusted})
		}
		sort.Slice(b.Keys, func(i, j int) bool { return b.Keys[i].ID < b.Keys[j].ID })
		published := 0
		if o.Policy != nil {
			cp := *o.Policy
			b.Policy = &cp
			published = cp.Version
			if raw, err := json.MarshalIndent(cp.Settings, "", "  "); err == nil {
				b.PolicyJSON = string(raw)
			}
			b.PolicyKeys = cp.Settings.Keys()
		}
		for _, dv := range d.Devices {
			if dv.Org != org {
				continue
			}
			b.Devices = append(b.Devices, deviceRow{
				ID: dv.ID, Name: dv.Name, Member: memberName(dv.Member), Revoked: dv.Revoked(), LastSeen: dv.LastSeen,
				Active:    !dv.Revoked() && !dv.LastSeen.IsZero() && now.Sub(dv.LastSeen) < api.ActiveWindow,
				FreeSlots: dv.FreeSlots, Agents: strings.Join(dv.Agents, ", "), Repos: len(dv.Repos),
				PolicyVersion: dv.PolicyVersion, PolicyCurrent: published > 0 && dv.PolicyVersion == published,
			})
		}
		sort.Slice(b.Devices, func(i, j int) bool {
			if b.Devices[i].Revoked != b.Devices[j].Revoked {
				return !b.Devices[i].Revoked
			}
			return b.Devices[i].Name < b.Devices[j].Name
		})
		b.Counts = counts(d, org)
		for _, sc := range b.Counts {
			b.All += sc.N
		}
		for _, w := range d.Items {
			if w.Org != org || (filter != "" && w.State != filter) {
				continue
			}
			b.Items = append(b.Items, itemRowFor(d, w))
		}
		sort.Slice(b.Items, func(i, j int) bool {
			if !b.Items[i].LastSeen.Equal(b.Items[j].LastSeen) {
				return b.Items[i].LastSeen.After(b.Items[j].LastSeen)
			}
			return b.Items[i].ID < b.Items[j].ID
		})
		b.Events = recentEvents(d, org, 25)
		b.Stats, b.StatsTotal = statsRows(d, org, now)
		return nil
	})
	if found {
		b.Tokens, _ = admin.ListEnrollTokens(c.cfg.Store, org, now)
	}
	return b, found
}

func deviceName(d *store.Data, id string) string {
	if dv := d.Devices[id]; dv != nil {
		if m := memberName(dv.Member); m != "" {
			return dv.Name + " (" + m + ")"
		}
		return dv.Name
	}
	return id
}

func subjectOf(it hubproto.Item) string {
	switch {
	case it.DepAdvisory != nil:
		a := it.DepAdvisory
		s := a.Advisory + " · " + a.Ecosystem + "/" + a.Package + " " + a.Vulnerable
		if a.Fixed != "" {
			s += " → " + a.Fixed
		}
		return s
	case it.RedBumpPR != nil:
		p := it.RedBumpPR
		pkg := p.Package
		if pkg == "" {
			pkg = "grouped bump"
		}
		return fmt.Sprintf("#%d %s (%s)", p.PR, pkg, p.Author)
	}
	return ""
}

func escalationText(x *store.Escalation) string {
	if x == nil {
		return ""
	}
	switch {
	case x.LinearID != "" && x.Err == escalate.ClosedMark:
		return x.LinearID + " (closed)"
	case x.LinearID != "":
		return x.LinearID
	case x.Err != "":
		return "pending (" + x.Err + ")"
	case x.Pending:
		return "pending"
	}
	return ""
}

func itemRowFor(d *store.Data, w *store.WorkItem) itemRow {
	row := itemRow{ID: w.ID, State: w.State, Kind: w.Item.Kind, Repo: w.Item.Repo(),
		Severity: string(w.Item.Severity()), Attempts: w.Attempts, Reporters: w.Reporters,
		FirstSeen: w.FirstSeen, LastSeen: w.LastSeen, Subject: subjectOf(w.Item)}
	if w.State == hubproto.StateClaimed && w.Lease != nil {
		row.Holder = deviceName(d, w.Lease.Device)
		row.Until = w.Lease.Expires
	}
	if w.Owner != "" && w.State != hubproto.StateOpen && w.State != hubproto.StateClaimed {
		row.Owner = deviceName(d, w.Owner)
	}
	if o := w.Outcome; o != nil {
		// Only a github.com PR URL becomes a link.
		if hubproto.ValidatePRURL(o.PRURL) == nil {
			row.PRURL = o.PRURL
		}
		row.Delivery = string(o.Delivery)
	}
	row.Escalation = escalationText(w.Escalation)
	return row
}

func (c *Console) orgPage(w http.ResponseWriter, r *http.Request, s *session) {
	org := r.PathValue("org")
	filter := hubproto.State(r.URL.Query().Get("state"))
	if filter != "" && !filter.Valid() {
		filter = ""
	}
	b, ok := c.orgBody(org, filter)
	if !ok {
		http.NotFound(w, r)
		return
	}
	if rv := c.takeReveal(s, org); rv != nil {
		b.Token, b.TokenLabel, b.TokenExpiry = rv.token, rv.label, rv.expiry
	}
	c.render(w, http.StatusOK, "org", s, pageData{Title: org, Body: b})
}

// ---- item page ------------------------------------------------------------------

type historyRow struct {
	At     time.Time
	Action hubproto.AttemptAction
	Device string
	Member string
	Reason hubproto.FailReason
	PRURL  string // a valid github.com PR URL, else ""
}

type itemBody struct {
	Org        string
	Row        itemRow
	Item       hubproto.Item
	Reopened   int
	LastFail   hubproto.FailReason
	ClaimedAt  time.Time
	FailGroup  string
	Escalation *store.Escalation
	LinearURL  string // a valid linear.app URL, else ""
	History    []historyRow
	Events     []eventRow
	Linear     linearBody
}

func (c *Console) itemPage(w http.ResponseWriter, r *http.Request, s *session) {
	org, id := r.PathValue("org"), r.PathValue("id")
	var b itemBody
	found := false
	_ = c.cfg.Store.View(func(d *store.Data) error {
		it := d.Items[id]
		if it == nil || it.Org != org {
			return nil
		}
		found = true
		b = itemBody{Org: org, Row: itemRowFor(d, it), Item: it.Item, Reopened: it.Reopened,
			LastFail: it.LastFail, ClaimedAt: it.ClaimedAt, Escalation: it.Escalation,
			Linear: c.linear(d, org)}
		if hubproto.ValidFailGroup(it.FailGroup) {
			b.FailGroup = it.FailGroup
		}
		if x := it.Escalation; x != nil && x.URL != "" && hubproto.ValidateLinearURL(x.URL) == nil {
			b.LinearURL = x.URL
		}
		// Newest first on the page; the store keeps newest last.
		for i := len(it.History) - 1; i >= 0; i-- {
			a := it.History[i]
			h := historyRow{At: a.At, Action: a.Action, Device: a.DeviceName, Member: memberName(a.Member), Reason: a.Reason}
			if a.PRURL != "" && hubproto.ValidatePRURL(a.PRURL) == nil {
				h.PRURL = a.PRURL
			}
			b.History = append(b.History, h)
		}
		for i := len(d.Events) - 1; i >= 0 && len(b.Events) < 50; i-- {
			e := d.Events[i]
			if e.Org == org && e.Item == id {
				b.Events = append(b.Events, eventRow{At: e.At, Org: e.Org, Kind: e.Kind, Item: e.Item, Device: e.Device, Detail: e.Detail})
			}
		}
		return nil
	})
	if !found {
		http.NotFound(w, r)
		return
	}
	c.render(w, http.StatusOK, "item", s, pageData{Title: id, Body: b})
}

// ---- events page ----------------------------------------------------------------

// MaxEventsShown bounds the per-org event log page.
const MaxEventsShown = 500

type eventsBody struct {
	Org    string
	Events []eventRow
}

func (c *Console) eventsPage(w http.ResponseWriter, r *http.Request, s *session) {
	org := r.PathValue("org")
	found := false
	var b eventsBody
	_ = c.cfg.Store.View(func(d *store.Data) error {
		if d.Orgs[org] == nil {
			return nil
		}
		found = true
		b = eventsBody{Org: org, Events: recentEvents(d, org, MaxEventsShown)}
		return nil
	})
	if !found {
		http.NotFound(w, r)
		return
	}
	c.render(w, http.StatusOK, "events", s, pageData{Title: org + " events", Body: b})
}

// ---- actions --------------------------------------------------------------------

func (c *Console) createEnroll(w http.ResponseWriter, r *http.Request, s *session) {
	org := r.PathValue("org")
	ttl := admin.DefaultTokenTTL
	if v := r.PostFormValue("ttl"); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil {
			c.done(w, r, s, org, fmt.Errorf("lifetime must be a duration such as 24h"), "")
			return
		}
		ttl = d
	}
	label := strings.TrimSpace(r.PostFormValue("label"))
	tok, exp, err := admin.CreateEnrollToken(c.cfg.Store, org, label, ttl, c.cfg.Now())
	if err != nil {
		c.done(w, r, s, org, err, "")
		return
	}
	// Held in the server-side session and shown once by the GET the
	// redirect lands on (POST-redirect-GET): a reload or back/forward
	// cannot re-POST and mint another live token the operator never sees.
	// The token is stored hashed; it is never put in a flash or a URL.
	c.setReveal(s, &reveal{org: org, token: tok, label: label, expiry: exp})
	back(w, r, org)
}

func (c *Console) revokeEnroll(w http.ResponseWriter, r *http.Request, s *session) {
	org, id := r.PathValue("org"), r.PathValue("id")
	err := admin.RevokeEnrollToken(c.cfg.Store, org, id, c.cfg.Now())
	c.done(w, r, s, org, err, "Revoked enroll token %s.", id)
}

func (c *Console) revokeDevice(w http.ResponseWriter, r *http.Request, s *session) {
	org, id := r.PathValue("org"), r.PathValue("id")
	n, err := admin.RevokeDevice(c.cfg.Store, org, id, c.cfg.Now())
	c.done(w, r, s, org, err, "Revoked the device; reopened %d leased item(s).", n)
}

func (c *Console) purgeItem(w http.ResponseWriter, r *http.Request, s *session) {
	org, id := r.PathValue("org"), r.PathValue("id")
	err := admin.PurgeItem(c.cfg.Store, org, id, "", c.cfg.Now())
	// The item page is gone after a purge: never return to it.
	r.PostForm.Del("return_item")
	c.done(w, r, s, org, err, "Purged item %s.", id)
}

func (c *Console) resolveItem(w http.ResponseWriter, r *http.Request, s *session) {
	org, id := r.PathValue("org"), r.PathValue("id")
	err := admin.ResolveItem(c.cfg.Store, org, id, c.cfg.Now())
	c.done(w, r, s, org, err, "Resolved %s: done. Its Linear issue, if sent, is closed by the escalator.", id)
}

func (c *Console) reopenItem(w http.ResponseWriter, r *http.Request, s *session) {
	org, id := r.PathValue("org"), r.PathValue("id")
	err := admin.ReopenItem(c.cfg.Store, org, id, c.cfg.Now())
	c.done(w, r, s, org, err, "Reopened %s with its attempts reset.", id)
}

func (c *Console) purgeState(w http.ResponseWriter, r *http.Request, s *session) {
	org := r.PathValue("org")
	st := hubproto.State(r.PostFormValue("state"))
	n, err := admin.PurgeState(c.cfg.Store, org, st, c.cfg.Now())
	c.done(w, r, s, org, err, "Purged %d %s item(s).", n, st)
}

func (c *Console) addItem(w http.ResponseWriter, r *http.Request, s *session) {
	org := r.PathValue("org")
	f := admin.ItemFields{
		Kind: r.PostFormValue("kind"), Repo: strings.TrimSpace(r.PostFormValue("repo")),
		Ecosystem: strings.TrimSpace(r.PostFormValue("ecosystem")), Package: strings.TrimSpace(r.PostFormValue("package")),
		Vulnerable: strings.TrimSpace(r.PostFormValue("vulnerable")), Fixed: strings.TrimSpace(r.PostFormValue("fixed")),
		Advisory: strings.TrimSpace(r.PostFormValue("advisory")), Severity: r.PostFormValue("severity"),
		PR: r.PostFormValue("pr"), Branch: strings.TrimSpace(r.PostFormValue("branch")), Author: r.PostFormValue("author"),
	}
	it, err := admin.BuildItem(f)
	if err != nil {
		c.done(w, r, s, org, fmt.Errorf("not added: %w", err), "")
		return
	}
	id, created, err := admin.AddItem(c.cfg.Store, org, it, c.cfg.Now())
	if err == nil && !created {
		c.done(w, r, s, org, nil, "Already known as %s; recorded as a re-report.", id)
		return
	}
	c.done(w, r, s, org, err, "Added %s %s.", it.Kind, id)
}

func (c *Console) setLinear(w http.ResponseWriter, r *http.Request, s *session) {
	org := r.PathValue("org")
	team := strings.TrimSpace(r.PostFormValue("team"))
	if r.PostFormValue("clear") != "" {
		team = ""
	} else if team == "" {
		c.done(w, r, s, org, fmt.Errorf("enter a Linear team id, or use Clear"), "")
		return
	}
	err := admin.SetLinearTeam(c.cfg.Store, org, team, c.cfg.Now())
	if team == "" {
		c.done(w, r, s, org, err, "Cleared the Linear team.")
		return
	}
	c.done(w, r, s, org, err, "Escalations now go to Linear team %s.", team)
}

// setLinearKey stores (or clears) the org's Linear key. The value is written
// sealed and never shown again; it is not put in a flash, a log or a URL.
func (c *Console) setLinearKey(w http.ResponseWriter, r *http.Request, s *session) {
	org := r.PathValue("org")
	key := strings.TrimSpace(r.PostFormValue("key"))
	if r.PostFormValue("clear") != "" {
		key = ""
	} else if key == "" {
		c.done(w, r, s, org, fmt.Errorf("paste a Linear API key, or use Clear"), "")
		return
	}
	err := admin.SetLinearKey(c.cfg.Store, c.cfg.Box, org, key, c.cfg.Now())
	if key == "" {
		c.done(w, r, s, org, err, "Cleared the Linear key.")
		return
	}
	c.done(w, r, s, org, err, "Stored the Linear key (sealed). It is not shown again.")
}

// setDigest stores (or clears) the org's digest webhook, like setLinearKey.
func (c *Console) setDigest(w http.ResponseWriter, r *http.Request, s *session) {
	org := r.PathValue("org")
	hook := strings.TrimSpace(r.PostFormValue("webhook"))
	if r.PostFormValue("clear") != "" {
		hook = ""
	} else if hook == "" {
		c.done(w, r, s, org, fmt.Errorf("paste a Slack incoming webhook, or use Clear"), "")
		return
	}
	err := admin.SetDigestWebhook(c.cfg.Store, c.cfg.Box, org, hook, c.cfg.Now())
	if hook == "" {
		c.done(w, r, s, org, err, "Cleared the digest webhook.")
		return
	}
	c.done(w, r, s, org, err, "Stored the digest webhook (sealed). It is not shown again.")
}

func (c *Console) removeKey(w http.ResponseWriter, r *http.Request, s *session) {
	org, id := r.PathValue("org"), r.PathValue("id")
	err := admin.RemovePolicyKey(c.cfg.Store, org, id, c.cfg.Now())
	c.done(w, r, s, org, err, "Removed key %s. It can no longer publish a policy or change this org's settings.", id)
}

// createCode mints a create code, shown once on the overview.
func (c *Console) createCode(w http.ResponseWriter, r *http.Request, s *session) {
	code, exp, err := admin.CreateCreateCode(c.cfg.Store, admin.DefaultCreateCodeTTL, c.cfg.Now())
	if err != nil {
		c.setFlash(s, true, "%s", err.Error())
	} else {
		c.setReveal(s, &reveal{token: code, expiry: exp})
	}
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (c *Console) addKey(w http.ResponseWriter, r *http.Request, s *session) {
	org := r.PathValue("org")
	keyID := strings.TrimSpace(r.PostFormValue("key_id"))
	pub := strings.TrimSpace(r.PostFormValue("public_key"))
	// Accept the keygen's "trusted" form (key_id:public_key) pasted whole.
	if keyID == "" {
		if k, p, ok := strings.Cut(pub, ":"); ok {
			keyID, pub = k, p
		}
	}
	trusted, err := admin.AddPolicyKey(c.cfg.Store, org, keyID, pub, c.cfg.Now())
	c.done(w, r, s, org, err, "Registered key %s. Daemons pin it with trusted_policy_keys = [%q].", keyID, trusted)
}

func (c *Console) publish(w http.ResponseWriter, r *http.Request, s *session) {
	org := r.PathValue("org")
	raw := []byte(strings.TrimSpace(r.PostFormValue("policy")))
	if len(raw) == 0 {
		c.done(w, r, s, org, fmt.Errorf("paste a signed policy bundle"), "")
		return
	}
	// Optional: the signer's trusted line, registered in the same step. The
	// signature must still verify; the console never signs.
	register := strings.TrimSpace(r.PostFormValue("register_key"))
	p, err := admin.PublishPolicyWithKey(c.cfg.Store, org, raw, register, c.cfg.Now())
	c.done(w, r, s, org, err, "Published policy v%d (key %s).", p.Version, p.KeyID)
}
