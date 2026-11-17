package hubproto

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/url"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"
)

func boardItem(id string) Item {
	return Item{ID: id, Kind: KindRedBumpPR, RedBumpPR: &RedBumpPR{
		Repo: "acme/web", PR: 7, Branch: "dependabot/npm_and_yarn/lodash-4.17.21",
		Package: "lodash", Author: AuthorDependabot,
	}}
}

func goodView(id string) ItemView {
	t0 := time.Date(2026, 11, 1, 9, 0, 0, 0, time.UTC)
	return ItemView{
		Item: boardItem(id), State: StateClaimed, Attempts: 1, Reporters: 2,
		FirstSeen: t0, LastSeen: t0.Add(time.Hour),
		Lease:      &LeaseView{Mine: true, DeviceName: "Martin's MacBook", Expires: t0.Add(2 * time.Hour)},
		Outcome:    &OutcomeView{PRURL: "https://github.com/acme/web/pull/8", Delivery: DeliveryCIPending},
		Escalation: &EscalationView{Identifier: "ENG-123", URL: "https://linear.app/acme/issue/ENG-123/fix-lodash"},
	}
}

func TestItemViewValidate(t *testing.T) {
	if err := goodView("a1").Validate(); err == nil {
		// Martin's has an apostrophe: not a valid device name.
		t.Fatal("apostrophe device name accepted")
	}
	v := goodView("a1")
	v.Lease.DeviceName = "martin-mbp"
	if err := v.Validate(); err != nil {
		t.Fatal(err)
	}
	pending := v
	pending.Escalation = &EscalationView{Pending: true}
	if err := pending.Validate(); err != nil {
		t.Fatalf("pending escalation: %v", err)
	}

	bad := map[string]func(*ItemView){
		"no id":            func(v *ItemView) { v.Item.ID = "" },
		"bad item":         func(v *ItemView) { v.Item.RedBumpPR.Author = "mallory" },
		"bad state":        func(v *ItemView) { v.State = "running" },
		"neg attempts":     func(v *ItemView) { v.Attempts = -1 },
		"huge attempts":    func(v *ItemView) { v.Attempts = MaxItemAttempts + 1 },
		"huge reporters":   func(v *ItemView) { v.Reporters = MaxItemReporters + 1 },
		"zero first seen":  func(v *ItemView) { v.FirstSeen = time.Time{} },
		"last before":      func(v *ItemView) { v.LastSeen = v.FirstSeen.Add(-time.Second) },
		"lease when open":  func(v *ItemView) { v.State = StateOpen },
		"lease bad name":   func(v *ItemView) { v.Lease.DeviceName = "x\x1b[31m" },
		"lease empty name": func(v *ItemView) { v.Lease.DeviceName = "" },
		"lease no expiry":  func(v *ItemView) { v.Lease.Expires = time.Time{} },
		"pr javascript":    func(v *ItemView) { v.Outcome.PRURL = "javascript:alert(1)" },
		"pr other host":    func(v *ItemView) { v.Outcome.PRURL = "https://evil.com/acme/web/pull/8" },
		"bad delivery":     func(v *ItemView) { v.Outcome.Delivery = "shipped" },
		"bad identifier":   func(v *ItemView) { v.Escalation.Identifier = "eng-123" },
		"prose identifier": func(v *ItemView) { v.Escalation.Identifier = "ENG-123 click here" },
		"url scheme":       func(v *ItemView) { v.Escalation.URL = "javascript://linear.app/x" },
		"url http":         func(v *ItemView) { v.Escalation.URL = "http://linear.app/acme/issue/ENG-1" },
		"url file":         func(v *ItemView) { v.Escalation.URL = "file:///etc/passwd" },
		"url host suffix":  func(v *ItemView) { v.Escalation.URL = "https://linear.app.evil.com/x" },
		"url userinfo":     func(v *ItemView) { v.Escalation.URL = "https://linear.app@evil.com/x" },
		"url port":         func(v *ItemView) { v.Escalation.URL = "https://linear.app:8443/x" },
		"url query":        func(v *ItemView) { v.Escalation.URL = "https://linear.app/x?r=https://evil.com" },
		"url fragment":     func(v *ItemView) { v.Escalation.URL = "https://linear.app/x#y" },
		"url dotdot":       func(v *ItemView) { v.Escalation.URL = "https://linear.app/a/../b" },
		"url double slash": func(v *ItemView) { v.Escalation.URL = "https://linear.app//evil.com/x" },
		"url bare host":    func(v *ItemView) { v.Escalation.URL = "https://linear.app/" },
		"url too long":     func(v *ItemView) { v.Escalation.URL = "https://linear.app/" + strings.Repeat("a", MaxLinearURLLen) },
	}
	for name, mut := range bad {
		w := goodView("a1")
		w.Lease.DeviceName = "martin-mbp"
		mut(&w)
		if err := w.Validate(); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: got %v, want ErrInvalid", name, err)
		}
	}
}

func TestBoardResponseValidate(t *testing.T) {
	mk := func(n int) BoardResponse {
		r := BoardResponse{}
		for i := 0; i < n; i++ {
			v := goodView("i" + strconv.Itoa(i))
			v.Lease.DeviceName = "dev-1"
			r.Items = append(r.Items, v)
		}
		return r
	}
	if err := mk(3).Validate(); err != nil {
		t.Fatal(err)
	}
	if err := mk(MaxBoardItems + 1).Validate(); !errors.Is(err, ErrInvalid) {
		t.Fatalf("oversized: %v", err)
	}
	dup := mk(2)
	dup.Items[1].Item.ID = dup.Items[0].Item.ID
	if err := dup.Validate(); !errors.Is(err, ErrInvalid) {
		t.Fatalf("duplicate: %v", err)
	}
	// One bad row fails the whole response.
	one := mk(3)
	one.Items[2].Escalation.URL = "https://evil.com"
	if err := one.Validate(); !errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), "items[2]") {
		t.Fatalf("bad row: %v", err)
	}

	// A full board fits MaxViewBody and decodes strictly.
	full := mk(MaxBoardItems)
	b, _ := json.Marshal(full)
	if len(b) > MaxViewBody {
		t.Fatalf("full board is %d bytes, over MaxViewBody", len(b))
	}
	var back BoardResponse
	if err := DecodeStrictLimit(bytes.NewReader(b), MaxViewBody, &back); err != nil {
		t.Fatal(err)
	}
	if err := back.Validate(); err != nil {
		t.Fatal(err)
	}
	if err := DecodeStrictLimit(bytes.NewReader(b), 100, &back); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("limit: %v", err)
	}
	unknown := `{"items":[],"script":"x"}`
	if err := DecodeStrictLimit(strings.NewReader(unknown), MaxViewBody, &back); err == nil {
		t.Fatal("unknown field accepted")
	}
}

func goodOrg() OrgView {
	return OrgView{Org: "acme", PolicyVersion: 3, Devices: []DeviceView{
		{Name: "martin-mbp", Me: true, LastSeen: time.Now(), FreeSlots: 2, Agents: []string{"claude", "codex"}, Active: true},
		{Name: "Office Mac mini", FreeSlots: 0},
	}}
}

func TestOrgViewValidate(t *testing.T) {
	if err := goodOrg().Validate(); err != nil {
		t.Fatal(err)
	}
	bad := map[string]func(*OrgView){
		"bad org":      func(o *OrgView) { o.Org = "Acme Corp" },
		"neg policy":   func(o *OrgView) { o.PolicyVersion = -1 },
		"empty name":   func(o *OrgView) { o.Devices[0].Name = "" },
		"control name": func(o *OrgView) { o.Devices[1].Name = "mac\x07" },
		"newline name": func(o *OrgView) { o.Devices[1].Name = "mac\nfake line" },
		"html name":    func(o *OrgView) { o.Devices[1].Name = "<img src=x>" },
		"long name":    func(o *OrgView) { o.Devices[1].Name = strings.Repeat("a", maxDeviceNameLen+1) },
		"neg slots":    func(o *OrgView) { o.Devices[0].FreeSlots = -1 },
		"huge slots":   func(o *OrgView) { o.Devices[0].FreeSlots = MaxFreeSlots + 1 },
		"bad agent":    func(o *OrgView) { o.Devices[0].Agents = []string{"rm -rf"} },
		"too many agts": func(o *OrgView) {
			o.Devices[0].Agents = strings.Split(strings.Repeat("a,", MaxHeartbeatAgents)+"a", ",")
		},
		"two me": func(o *OrgView) { o.Devices[1].Me = true },
		"too many devs": func(o *OrgView) {
			o.Devices = make([]DeviceView, MaxOrgDevices+1)
			for i := range o.Devices {
				o.Devices[i].Name = "d" + strconv.Itoa(i)
			}
		},
	}
	for name, mut := range bad {
		o := goodOrg()
		mut(&o)
		if err := o.Validate(); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: got %v, want ErrInvalid", name, err)
		}
	}
}

func TestBoardQuery(t *testing.T) {
	if BoardPath("", 0) != "/v1/board" {
		t.Fatal(BoardPath("", 0))
	}
	if got := BoardPath(StateEscalated, 50); got != "/v1/board?limit=50&state=escalated" {
		t.Fatal(got)
	}
	if BoardPath("x&admin=1", 0) != "" || BoardPath("", MaxBoardItems+1) != "" {
		t.Fatal("bad input built a path")
	}
	cases := []struct {
		q     string
		state State
		limit int
		ok    bool
	}{
		{"", "", DefaultBoardLimit, true},
		{"state=open", StateOpen, DefaultBoardLimit, true},
		{"state=claimed&limit=10", StateClaimed, 10, true},
		{"limit=500", "", 500, true},
		{"limit=501", "", 0, false},
		{"limit=0", "", 0, false},
		{"limit=-1", "", 0, false},
		{"limit=010", "", 0, false},
		{"limit=abc", "", 0, false},
		{"state=deleted", "", 0, false},
		{"state=open&state=done", "", 0, false},
		{"owner=me", "", 0, false},
	}
	for _, c := range cases {
		q, _ := url.ParseQuery(c.q)
		st, lim, err := ParseBoardQuery(q)
		if (err == nil) != c.ok || st != c.state || lim != c.limit {
			t.Errorf("%q: got %q %d %v", c.q, st, lim, err)
		}
	}
}

func TestSettingsKeys(t *testing.T) {
	if k := (Settings{}).Keys(); len(k) != 0 {
		t.Fatalf("empty settings lock %v", k)
	}
	off := false
	got := samplePolicy().Settings.Keys()
	want := []string{KeyReviewProviders, KeyBudgetDaily, KeyMergeQueue, KeyMaxConcurrency, KeyAcceptKinds, KeyEscalateAfter}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
	// A false bool is present (locked off); a zero int is absent.
	if got := (Settings{MergeQueueEnabled: &off}).Keys(); !reflect.DeepEqual(got, []string{KeyMergeQueue}) {
		t.Fatalf("locked-off merge queue: %v", got)
	}
	// Every key Keys can name is exactly a JSON key the struct writes.
	b, _ := json.Marshal(samplePolicy().Settings)
	var m map[string]any
	_ = json.Unmarshal(b, &m)
	if len(m) != len(want) {
		t.Fatalf("json has %d keys, Keys names %d", len(m), len(want))
	}
	for _, k := range want {
		if _, ok := m[k]; !ok {
			t.Errorf("Keys names %q, json does not", k)
		}
	}
}
