package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sushidev-team/lola/hubproto"
	"github.com/sushidev-team/lola/internal/doctor"
	"github.com/sushidev-team/lola/internal/hub"
	"github.com/sushidev-team/lola/internal/protocol"
)

func noTTY() (uintptr, bool) { return 0, false }

func TestHubReadInvite(t *testing.T) {
	const tok = "tok_0123456789abcdef"
	var errb bytes.Buffer
	inv, inter, err := hubReadInvite("", strings.NewReader(tok+"\n"), &errb, noTTY)
	if err != nil || inv.Token != tok || inv.URL != "" || inter || errb.Len() != 0 {
		t.Fatalf("stdin: %+v %v %v %q", inv, inter, err, errb.String())
	}

	f := filepath.Join(t.TempDir(), "tok")
	if err := os.WriteFile(f, []byte("  lola-hub:https://hub.example.com#"+tok+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if inv, _, err := hubReadInvite(f, strings.NewReader("ignored"), &errb, noTTY); err != nil || inv.URL != "https://hub.example.com" || inv.Token != tok {
		t.Fatalf("file: %+v %v", inv, err)
	}
	if inv, _, err := hubReadInvite("-", strings.NewReader(tok), &errb, noTTY); err != nil || inv.Token != tok {
		t.Fatalf("dash: %+v %v", inv, err)
	}
	// A terminal: read without echo, and the answer is interactive.
	old := readSecret
	t.Cleanup(func() { readSecret = old })
	readSecret = func(uintptr) ([]byte, error) { return []byte(tok), nil }
	if inv, inter, err := hubReadInvite("", strings.NewReader(""), &errb, func() (uintptr, bool) { return 0, true }); err != nil || !inter || inv.Token != tok {
		t.Fatalf("tty: %+v %v %v", inv, inter, err)
	}

	for name, in := range map[string]string{"empty": "", "spaces": "a b", "two lines": "a\nb"} {
		if _, _, err := hubReadInvite("", strings.NewReader(in), &errb, noTTY); err == nil {
			t.Errorf("%s: want an error", name)
		}
	}
}

// The token has no argv path: a second argument is refused by cobra before
// anything is read.
func TestHubJoinHasNoArgvToken(t *testing.T) {
	cmd := hubCmd()
	cmd.SetArgs([]string{"join", "https://hub.example.com", "tok_0123456789abcdef"})
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetIn(strings.NewReader(""))
	if err := cmd.Execute(); err == nil {
		t.Fatal("a token on the command line must be refused")
	}
	if strings.Contains(out.String(), "tok_0123456789abcdef") {
		t.Error("the refused token was echoed")
	}
}

func TestHubJoinURL(t *testing.T) {
	inv := hub.Invite{URL: "https://hub.example.com", Token: "t"}
	if u, err := hubJoinURL(nil, inv); err != nil || u != inv.URL {
		t.Fatalf("invite url: %q %v", u, err)
	}
	if u, err := hubJoinURL([]string{"https://hub.example.com/"}, inv); err != nil || u != inv.URL {
		t.Fatalf("same url: %q %v", u, err)
	}
	if _, err := hubJoinURL([]string{"https://other.example.com"}, inv); err == nil {
		t.Fatal("a url that disagrees with the invite must be refused")
	}
	if _, err := hubJoinURL(nil, hub.Invite{Token: "t"}); err == nil {
		t.Fatal("a bare token without a url must be refused")
	}
	if u, err := hubJoinURL([]string{"https://h.example.com"}, hub.Invite{Token: "t"}); err != nil || u != "https://h.example.com" {
		t.Fatalf("bare token: %q %v", u, err)
	}
}

func TestHubConfirmKey(t *testing.T) {
	pub, _, _ := hubproto.GenerateKey()
	inv := hub.Invite{URL: "https://hub.example.com", Token: "t", KeyID: "k1", PublicKey: hubproto.EncodePublicKey(pub)}
	fp := hub.KeyFingerprint(inv.PublicKey)
	var w bytes.Buffer
	if ok, err := hubConfirmKey(inv, true, false, strings.NewReader(""), &w); err != nil || !ok || !strings.Contains(w.String(), fp) {
		t.Fatalf("--trust-key: %v %v %q", ok, err, w.String())
	}
	w.Reset()
	if ok, _ := hubConfirmKey(inv, false, false, strings.NewReader("yes\n"), &w); ok || !strings.Contains(w.String(), "--trust-key") {
		t.Fatalf("piped input must not confirm: %v %q", ok, w.String())
	}
	if ok, _ := hubConfirmKey(inv, false, true, strings.NewReader("yes\n"), &w); !ok {
		t.Fatal("typing yes at the terminal confirms")
	}
	if ok, _ := hubConfirmKey(inv, false, true, strings.NewReader("y\n"), &w); ok {
		t.Fatal("only yes confirms")
	}
	if strings.Contains(w.String(), inv.PublicKey) {
		t.Error("the key itself is printed; only its id and fingerprint should be")
	}
}

func TestRenderHubStatusAndOrg(t *testing.T) {
	d := &protocol.HubStatusData{Enabled: true, LastError: "heartbeat: refused", ClaimsToday: 1, MaxClaimsPerDay: 4,
		Policy:   &protocol.HubPolicyInfo{Version: 3, ApplyNote: "the team removed its policy"},
		Sessions: []protocol.HubSessionInfo{{Session: "lola-web-1", Summary: "GHSA-x · lodash", InReview: true}}}
	out := renderHubDaemon(d, "")
	for _, want := range []string{"has not reached the hub yet", "last problem: heartbeat: refused", "1 of 4", "v3 does not apply", "in review"} {
		if !strings.Contains(out, want) {
			t.Errorf("status lacks %q:\n%s", want, out)
		}
	}
	if out := renderHubDaemon(nil, "not running"); !strings.Contains(out, "not running") {
		t.Errorf("no daemon: %q", out)
	}
	now := time.Date(2026, 11, 12, 10, 0, 0, 0, time.UTC)
	org := renderHubOrg(protocol.HubOrgData{Org: "acme", PolicyVersion: 2, Devices: []protocol.HubDeviceInfo{
		{Name: "mbp", Me: true, Member: "Ana", LastSeen: now.Add(-5 * time.Minute), Active: true, PolicyVersion: 2, Agents: []string{"claude"}},
		{Name: "old", Active: false},
	}}, now)
	for _, want := range []string{"team acme, policy v2", "mbp (this one)", "Ana", "5m0s ago", "never, inactive"} {
		if !strings.Contains(org, want) {
			t.Errorf("org lacks %q:\n%s", want, org)
		}
	}
	stats := renderHubStats(hubproto.StatsView{Days: []hubproto.StatsDay{
		{Date: "2026-11-10", Opened: 3},
		{Date: "2026-11-11", Opened: 1, Done: 2, Failed: 1, FixSecondsSum: 2 * 5400},
	}})
	if strings.Index(stats, "2026-11-11") > strings.Index(stats, "2026-11-10") || !strings.Contains(stats, "1h30m0s") {
		t.Errorf("stats (newest first, avg fix 1h30m):\n%s", stats)
	}
	if renderHubStats(hubproto.StatsView{}) != "no stats yet\n" {
		t.Error("empty stats")
	}
	js := hubStatusJSONOf(hub.Status{}, nil, "not running", []doctor.Result{{Name: "hub joined", Detail: "not joined", Fix: doctor.FixJoinHub}})
	if js.Membership != "not joined" || len(js.Readiness) != 1 || js.Readiness[0].Fix != "hub_join" {
		t.Errorf("json = %+v", js)
	}
}

func TestHubCLIUsesTheDaemon(t *testing.T) {
	var sent []protocol.Request
	reply := map[string]protocol.Response{}
	old := hubSend
	t.Cleanup(func() { hubSend = old })
	hubSend = func(req protocol.Request) (*protocol.Response, error) {
		sent = append(sent, req)
		r, ok := reply[req.Cmd]
		if !ok {
			return nil, errors.New("dial unix: no such file")
		}
		return &r, nil
	}
	data := func(v any) json.RawMessage { b, _ := json.Marshal(v); return b }

	reply[protocol.CmdHubClaim] = protocol.Response{OK: true, Data: data(protocol.HubClaimData{Session: "lola-web-x", Project: "web", ItemID: "it1"})}
	reply[protocol.CmdHubBoard] = protocol.Response{OK: true, Data: data(protocol.HubBoardData{Items: []protocol.HubBoardItem{
		{ID: "it1", State: "open", Label: "GHSA-x · lodash", Repo: "acme/web", Local: protocol.HubLocalInfo{Claimable: true, Projects: []string{"a", "b"}}},
	}})}
	reply[protocol.CmdHubRelease] = protocol.Response{OK: false, Error: "hub: not joined"}

	run := func(args ...string) (string, error) {
		cmd := hubCmd()
		var out bytes.Buffer
		cmd.SetOut(&out)
		cmd.SetErr(&out)
		cmd.SetArgs(args)
		err := cmd.Execute()
		return out.String(), err
	}
	if out, err := run("claim", "it1", "--project", "web"); err != nil || !strings.Contains(out, "session lola-web-x") {
		t.Fatalf("claim: %q %v", out, err)
	}
	var ca protocol.HubClaimArgs
	if err := json.Unmarshal(sent[len(sent)-1].Args, &ca); err != nil || ca.ItemID != "it1" || ca.Project != "web" {
		t.Fatalf("claim args = %+v %v", ca, err)
	}
	if _, err := run("claim", "it1", "it2", "--minor", "--note", "keep the lockfile"); err != nil {
		t.Fatalf("claim several: %v", err)
	}
	ca = protocol.HubClaimArgs{}
	if err := json.Unmarshal(sent[len(sent)-1].Args, &ca); err != nil || len(ca.ItemIDs) != 2 || ca.Scope == nil || !ca.Scope.Minor || ca.Scope.Patch || ca.Scope.Note != "keep the lockfile" {
		t.Fatalf("claim scope args = %+v %v", ca, err)
	}
	reply[protocol.CmdHubOrg] = protocol.Response{OK: true, Data: data(protocol.HubOrgData{Org: "acme", Devices: []protocol.HubDeviceInfo{{Name: "mbp", Me: true}}})}
	if out, err := run("org"); err != nil || !strings.Contains(out, "mbp (this one)") {
		t.Fatalf("org: %q %v", out, err)
	}
	if out, err := run("board"); err != nil || !strings.Contains(out, "--project a|b") {
		t.Fatalf("board: %q %v", out, err)
	}
	if _, err := run("release", "it1"); err == nil || !strings.Contains(err.Error(), "not joined") {
		t.Fatalf("release: %v", err)
	}
	if _, err := run("sync"); err == nil || !strings.Contains(err.Error(), "not running") {
		t.Fatalf("sync without a daemon: %v", err)
	}
}
