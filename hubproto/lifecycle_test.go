package hubproto

import (
	"encoding/json"
	"errors"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestInReviewState(t *testing.T) {
	if !StateInReview.Valid() || string(StateInReview) != "in_review" {
		t.Fatal("in_review")
	}
	seen := map[State]bool{}
	for _, s := range AllStates() {
		if !s.Valid() || seen[s] {
			t.Fatalf("AllStates: %q", s)
		}
		seen[s] = true
	}
	if len(seen) != 6 {
		t.Fatalf("AllStates has %d states", len(seen))
	}
	if BoardPath(StateInReview, 0) != "/v1/board?state=in_review" {
		t.Fatal(BoardPath(StateInReview, 0))
	}
}

func TestProgressPath(t *testing.T) {
	if got := ProgressPath("abc"); got != "/v1/items/abc/progress" {
		t.Fatal(got)
	}
	if ProgressPath("../x") != "" {
		t.Fatal("bad id built a path")
	}
	id, act, ok := ParseItemPath("/v1/items/abc/progress")
	if !ok || id != "abc" || act != ActionProgress {
		t.Fatalf("parse: %q %q %v", id, act, ok)
	}
}

func TestProgressValidate(t *testing.T) {
	ok := ProgressRequest{PRURL: "https://github.com/a/b/pull/3", Delivery: DeliveryReviewPending}
	if err := ok.Validate(); err != nil {
		t.Fatal(err)
	}
	for name, r := range map[string]ProgressRequest{
		"no url":       {Delivery: DeliveryCIPending},
		"other host":   {PRURL: "https://evil.com/a/b/pull/3", Delivery: DeliveryCIPending},
		"bad delivery": {PRURL: ok.PRURL, Delivery: "shipped"},
	} {
		if err := r.Validate(); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: %v", name, err)
		}
	}
	var back ProgressRequest
	if err := DecodeStrict(strings.NewReader(`{"pr_url":"https://github.com/a/b/pull/3","delivery":"draft","note":"x"}`), &back); err == nil {
		t.Fatal("unknown field accepted")
	}
}

func TestFailRequestGroupAndPR(t *testing.T) {
	good := FailRequest{Reason: FailCI, PRURL: "https://github.com/a/b/pull/9", Group: "a1b2c3d4"}
	if err := good.Validate(); err != nil {
		t.Fatal(err)
	}
	// Both are optional, and omitted on the wire when empty: a fail without
	// them is byte-identical to the old frame.
	b, _ := json.Marshal(FailRequest{Reason: FailCI})
	if string(b) != `{"reason":"ci_failed"}` {
		t.Fatalf("got %s", b)
	}
	for name, mut := range map[string]func(*FailRequest){
		"group short":   func(r *FailRequest) { r.Group = "abc1234" },
		"group long":    func(r *FailRequest) { r.Group = strings.Repeat("a", 33) },
		"group upper":   func(r *FailRequest) { r.Group = "ABCDEFGH" },
		"group dash":    func(r *FailRequest) { r.Group = "abcd-efgh" },
		"group newline": func(r *FailRequest) { r.Group = "abcdefgh\n" },
		"pr other host": func(r *FailRequest) { r.PRURL = "https://evil.com/a/b/pull/9" },
		"pr js":         func(r *FailRequest) { r.PRURL = "javascript:alert(1)" },
	} {
		r := good
		mut(&r)
		if err := r.Validate(); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: %v", name, err)
		}
	}
	if !ValidFailGroup(strings.Repeat("9", 32)) || !ValidFailGroup("abcdefgh") {
		t.Fatal("good group refused")
	}
}

func TestGroupKeyAndEscalationID(t *testing.T) {
	if GroupKey("a1b2c3d4") != "group|a1b2c3d4" || GroupKey("BAD") != "" {
		t.Fatal("GroupKey")
	}
	key := DedupKey(boardItem("x"))
	id := EscalationIssueID("acme", key)
	uuidRe := regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-8[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)
	if !uuidRe.MatchString(id) {
		t.Fatalf("not a v8 UUID: %q", id)
	}
	// Stable: the same finding under another item id maps to the same issue.
	if EscalationIssueID("acme", DedupKey(boardItem("y"))) != id {
		t.Fatal("depends on the item id")
	}
	if EscalationIssueID("other", key) == id || EscalationIssueID("acme", key+"x") == id {
		t.Fatal("collision across org or key")
	}
	if EscalationIssueID("Acme Corp", key) != "" || EscalationIssueID("acme", "") != "" {
		t.Fatal("bad input derived an id")
	}
}

func TestRedBumpPRGroupedPackage(t *testing.T) {
	b := goodBump()
	b.Package = ""
	if err := b.Validate(); err != nil {
		t.Fatalf("grouped PR without package: %v", err)
	}
	withPkg := goodBump()
	if DedupKey(Item{Kind: KindRedBumpPR, RedBumpPR: &b}) != DedupKey(Item{Kind: KindRedBumpPR, RedBumpPR: &withPkg}) {
		t.Fatal("dedup key depends on package")
	}
	b.Package = "x\ny"
	if b.Validate() == nil {
		t.Fatal("bad non-empty package accepted")
	}
}

func TestLeaseClaimedAt(t *testing.T) {
	pub, _ := mustKey(t)
	t0 := time.Date(2026, 11, 1, 9, 0, 0, 0, time.UTC)
	l := Lease{ItemID: "i1", Device: DeviceID(pub), Expires: t0.Add(time.Hour)}
	b, _ := json.Marshal(l)
	if strings.Contains(string(b), "claimed_at") {
		t.Fatalf("zero claimed_at on the wire: %s", b)
	}
	l.ClaimedAt = t0
	if err := l.Validate(); err != nil {
		t.Fatal(err)
	}
	l.ClaimedAt = l.Expires.Add(time.Second)
	if err := l.Validate(); !errors.Is(err, ErrInvalid) {
		t.Fatalf("claimed after expiry: %v", err)
	}
	if LeaseGraceFactor != 2 || DefaultMaxLeaseAge != 24*time.Hour || DeviceCooldown != 6*time.Hour ||
		InReviewIdleReopen != 14*24*time.Hour || DefaultStaleAfter != 48*time.Hour {
		t.Fatal("lifecycle timings drifted from the contract")
	}
}

func TestMemberLabelAndUniqueName(t *testing.T) {
	for _, s := range []string{"Martin", "ops team", "a", "x.y_z-1", strings.Repeat("a", MaxMemberLabelLen)} {
		if !ValidMemberLabel(s) {
			t.Errorf("%q refused", s)
		}
	}
	for _, s := range []string{"", " lead", "-x", "a\nb", "<b>", "Martin's", strings.Repeat("a", MaxMemberLabelLen+1)} {
		if ValidMemberLabel(s) {
			t.Errorf("%q accepted", s)
		}
	}

	taken := map[string]bool{"mac": true, "mac-2": true}
	in := func(s string) bool { return taken[s] }
	if got := UniqueDeviceName("mbp", in); got != "mbp" {
		t.Fatal(got)
	}
	if got := UniqueDeviceName("mac", in); got != "mac-3" {
		t.Fatal(got)
	}
	long := strings.Repeat("m", maxDeviceNameLen)
	taken[long] = true
	if got := UniqueDeviceName(long, in); len(got) != maxDeviceNameLen || !strings.HasSuffix(got, "-2") || !ValidDeviceName(got) {
		t.Fatalf("long: %q", got)
	}
	if UniqueDeviceName("bad\nname", in) != "" {
		t.Fatal("invalid name accepted")
	}
	if UniqueDeviceName("x", func(string) bool { return true }) != "" {
		t.Fatal("exhausted suffixes returned a name")
	}
}

func TestEnrollResponseValidate(t *testing.T) {
	pub, _ := mustKey(t)
	good := EnrollResponse{DeviceID: DeviceID(pub), Org: "acme", Member: "Martin", DeviceName: "mac-2"}
	if err := good.Validate(); err != nil {
		t.Fatal(err)
	}
	old := EnrollResponse{DeviceID: good.DeviceID, Org: "acme"}
	if err := old.Validate(); err != nil {
		t.Fatalf("pre-member response: %v", err)
	}
	b, _ := json.Marshal(old)
	if strings.Contains(string(b), "member") || strings.Contains(string(b), "device_name") {
		t.Fatalf("empty new fields on the wire: %s", b)
	}
	for name, mut := range map[string]func(*EnrollResponse){
		"bad device": func(r *EnrollResponse) { r.DeviceID = "nope" },
		"bad org":    func(r *EnrollResponse) { r.Org = "Acme Corp" },
		"bad member": func(r *EnrollResponse) { r.Member = "x\x1b[31m" },
		"bad name":   func(r *EnrollResponse) { r.DeviceName = "`id`" },
	} {
		r := good
		mut(&r)
		if err := r.Validate(); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func TestHeartbeatPolicyVersion(t *testing.T) {
	b, _ := json.Marshal(HeartbeatRequest{})
	if strings.Contains(string(b), "policy_version") {
		t.Fatalf("zero policy_version on the wire: %s", b)
	}
	if err := (HeartbeatRequest{PolicyVersion: 7}).Validate(); err != nil {
		t.Fatal(err)
	}
	if err := (HeartbeatRequest{PolicyVersion: -1}).Validate(); !errors.Is(err, ErrInvalid) {
		t.Fatalf("negative: %v", err)
	}
}

func goodAttempt(action AttemptAction, at time.Time) Attempt {
	return Attempt{DeviceName: "martin-mbp", Member: "Martin", Action: action, At: at}
}

func reviewView() ItemView {
	t0 := time.Date(2026, 11, 1, 9, 0, 0, 0, time.UTC)
	v := goodView("r1")
	v.State = StateInReview
	v.Lease = nil
	v.ClaimedAt = t0
	v.Owner = &OwnerView{Mine: true, DeviceName: "martin-mbp", Member: "Martin"}
	v.LastFail = FailCI
	v.Reopened = 1
	failed := goodAttempt(AttemptFailed, t0.Add(time.Minute))
	failed.Reason, failed.PRURL = FailCI, "https://github.com/acme/web/pull/8"
	v.History = []Attempt{
		goodAttempt(AttemptClaimed, t0),
		failed,
		{Action: AttemptReopened, At: t0.Add(2 * time.Minute)},
		goodAttempt(AttemptClaimed, t0.Add(3*time.Minute)),
		{DeviceName: "martin-mbp", Action: AttemptInReview, PRURL: "https://github.com/acme/web/pull/9", At: t0.Add(4 * time.Minute)},
	}
	return v
}

func TestItemViewLifecycleFields(t *testing.T) {
	if err := reviewView().Validate(); err != nil {
		t.Fatal(err)
	}
	// A pre-history row still validates, and the new fields stay off the wire.
	plain := goodView("p1")
	plain.Lease.DeviceName = "dev-1"
	if err := plain.Validate(); err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(plain)
	for _, k := range []string{"history", "last_fail", "reopened", "claimed_at", "owner"} {
		if strings.Contains(string(b), `"`+k+`"`) {
			t.Errorf("%s on the wire when empty", k)
		}
	}
	claimed := plain
	claimed.ClaimedAt = plain.FirstSeen
	claimed.Lease = &LeaseView{DeviceName: "dev-1", Member: "ops team", Expires: plain.LastSeen}
	if err := claimed.Validate(); err != nil {
		t.Fatalf("claimed with claimed_at and member: %v", err)
	}

	bad := map[string]func(*ItemView){
		"owner on open":        func(v *ItemView) { v.State = StateOpen },
		"owner on claimed":     func(v *ItemView) { v.State = StateClaimed },
		"lease in review":      func(v *ItemView) { v.Lease = &LeaseView{DeviceName: "x", Expires: v.LastSeen} },
		"owner bad name":       func(v *ItemView) { v.Owner.DeviceName = "x\x1b[31m" },
		"owner empty name":     func(v *ItemView) { v.Owner.DeviceName = "" },
		"owner bad member":     func(v *ItemView) { v.Owner.Member = "<img>" },
		"claimed_at when done": func(v *ItemView) { v.State = StateDone },
		"bad last_fail":        func(v *ItemView) { v.LastFail = "because" },
		"neg reopened":         func(v *ItemView) { v.Reopened = -1 },
		"huge reopened":        func(v *ItemView) { v.Reopened = MaxItemAttempts + 1 },
		"history too long": func(v *ItemView) {
			for len(v.History) <= MaxItemHistory {
				v.History = append(v.History, goodAttempt(AttemptClaimed, v.History[len(v.History)-1].At))
			}
		},
		"history out of order": func(v *ItemView) { v.History[1].At = v.History[0].At.Add(-time.Second) },
		"history bad action":   func(v *ItemView) { v.History[0].Action = "deleted" },
		"history no device":    func(v *ItemView) { v.History[0].DeviceName = "" },
		"history bad device":   func(v *ItemView) { v.History[0].DeviceName = "a\nb" },
		"history bad member":   func(v *ItemView) { v.History[0].Member = "a\nb" },
		"history member alone": func(v *ItemView) { v.History[2].Member = "Martin" },
		"history bad reason":   func(v *ItemView) { v.History[1].Reason = "free text" },
		"reason on claim":      func(v *ItemView) { v.History[0].Reason = FailCI },
		"history bad pr":       func(v *ItemView) { v.History[4].PRURL = "https://evil.com/a/b/pull/1" },
		"history zero at":      func(v *ItemView) { v.History[0].At = time.Time{} },
	}
	for name, mut := range bad {
		v := reviewView()
		mut(&v)
		if err := v.Validate(); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: got %v, want ErrInvalid", name, err)
		}
	}

	// Every action round-trips and hub actions need no device.
	for _, a := range AllAttemptActions() {
		if !a.Valid() {
			t.Fatal(a)
		}
	}
	for _, a := range []AttemptAction{AttemptReopened, AttemptClosedStale} {
		if err := (Attempt{Action: a, At: time.Now()}).Validate(); err != nil {
			t.Errorf("%s without device: %v", a, err)
		}
	}
	stale := Attempt{Action: AttemptClosedStale, Reason: FailObsolete, At: time.Now()}
	expired := Attempt{DeviceName: "d", Action: AttemptExpired, Reason: FailTimeout, At: time.Now()}
	if stale.Validate() != nil || expired.Validate() != nil {
		t.Fatal("reason on stale/expired refused")
	}
}

func TestBoardWithHistoryFitsViewBody(t *testing.T) {
	// A full board where every row carries a full history still fits.
	r := BoardResponse{}
	for i := 0; i < MaxBoardItems; i++ {
		v := reviewView()
		v.Item.ID = "i" + strconv.Itoa(i)
		for len(v.History) < MaxItemHistory {
			last := v.History[len(v.History)-1]
			v.History = append(v.History, Attempt{DeviceName: strings.Repeat("d", maxDeviceNameLen), Member: strings.Repeat("m", MaxMemberLabelLen),
				Action: AttemptFailed, Reason: FailSandbox, PRURL: "https://github.com/acme/web/pull/1234567", At: last.At})
		}
		r.Items = append(r.Items, v)
	}
	if err := r.Validate(); err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(r)
	if len(b) > MaxViewBody {
		t.Fatalf("full board with full histories is %d bytes, over MaxViewBody %d", len(b), MaxViewBody)
	}
}

func TestOrgViewMemberAndPolicyVersion(t *testing.T) {
	o := goodOrg()
	o.Devices[0].Member = "Martin"
	o.Devices[0].PolicyVersion = 3
	if err := o.Validate(); err != nil {
		t.Fatal(err)
	}
	for name, mut := range map[string]func(*OrgView){
		"bad member":     func(o *OrgView) { o.Devices[1].Member = "a\nb" },
		"neg policy ver": func(o *OrgView) { o.Devices[1].PolicyVersion = -1 },
	} {
		c := goodOrg()
		mut(&c)
		if err := c.Validate(); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: %v", name, err)
		}
	}
}
