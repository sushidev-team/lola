package main

import (
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"github.com/sushidev-team/lola/internal/protocol"
)

func ok(t *testing.T, v any) protocol.Response {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return protocol.Response{OK: true, Data: raw}
}

// Every method is one socket round trip with the right command and arguments,
// and decodes the typed reply.
func TestPhonesServiceRoundTrips(t *testing.T) {
	dev := protocol.DeviceInfo{ID: "d1", Name: "Pixel", Tiers: []string{"read", "answer"}}
	f := startFakeDaemon(t, func(req protocol.Request) protocol.Response {
		switch req.Cmd {
		case protocol.CmdPairBegin:
			return ok(t, protocol.PairBeginData{Pairing: "0123456789abcdef", Payload: "lola2.x", Relay: true})
		case protocol.CmdPairStatus, protocol.CmdPairConfirm, protocol.CmdPairReject:
			return ok(t, protocol.PairStatusData{Pairing: "0123456789abcdef", State: protocol.PairStateConfirming, Via: "relay"})
		case protocol.CmdDevices:
			return ok(t, protocol.DevicesData{Devices: []protocol.DeviceInfo{dev}})
		case protocol.CmdRevokeDevice:
			return ok(t, protocol.RevokeDeviceData{ID: "d1", Closed: 2})
		case protocol.CmdSetDeviceTiers:
			return ok(t, dev)
		}
		return protocol.Response{Error: "unknown cmd " + req.Cmd}
	})
	s := &PhonesService{}

	b, err := s.Begin()
	if err != nil || b.Payload != "lola2.x" || !b.Relay {
		t.Fatalf("Begin = %+v, %v", b, err)
	}
	st, err := s.Status(protocol.PairStatusArgs{Pairing: "p", Since: "waiting", WaitMs: 1})
	if err != nil || st.State != protocol.PairStateConfirming || st.Via != "relay" {
		t.Fatalf("Status = %+v, %v", st, err)
	}
	if _, err := s.Confirm(protocol.PairConfirmArgs{Pairing: "p", Code: "123456", Tiers: []string{"read"}}); err != nil {
		t.Fatalf("Confirm: %v", err)
	}
	if _, err := s.Reject("p"); err != nil {
		t.Fatalf("Reject: %v", err)
	}
	dd, err := s.Devices()
	if err != nil || len(dd.Devices) != 1 || dd.Devices[0].Name != "Pixel" {
		t.Fatalf("Devices = %+v, %v", dd, err)
	}
	rv, err := s.Revoke("d1")
	if err != nil || rv.Closed != 2 {
		t.Fatalf("Revoke = %+v, %v", rv, err)
	}
	di, err := s.SetTiers("d1", []string{"read", "answer"})
	if err != nil || di.ID != "d1" {
		t.Fatalf("SetTiers = %+v, %v", di, err)
	}

	reqs := f.requests()
	want := []string{protocol.CmdPairBegin, protocol.CmdPairStatus, protocol.CmdPairConfirm,
		protocol.CmdPairReject, protocol.CmdDevices, protocol.CmdRevokeDevice, protocol.CmdSetDeviceTiers}
	if len(reqs) != len(want) {
		t.Fatalf("got %d requests, want %d", len(reqs), len(want))
	}
	for i, r := range reqs {
		if r.Cmd != want[i] {
			t.Errorf("request %d = %q, want %q", i, r.Cmd, want[i])
		}
	}
	if len(reqs[0].Args) != 0 {
		t.Errorf("pairBegin carried args %s", reqs[0].Args)
	}
	var ca protocol.PairConfirmArgs
	if err := json.Unmarshal(reqs[2].Args, &ca); err != nil || ca.Code != "123456" || !reflect.DeepEqual(ca.Tiers, []string{"read"}) {
		t.Errorf("confirm args = %+v, %v", ca, err)
	}
	var ra protocol.PairRejectArgs
	if err := json.Unmarshal(reqs[3].Args, &ra); err != nil || ra.Pairing != "p" {
		t.Errorf("reject args = %+v, %v", ra, err)
	}
	var ta protocol.SetDeviceTiersArgs
	if err := json.Unmarshal(reqs[6].Args, &ta); err != nil || ta.ID != "d1" || len(ta.Tiers) != 2 {
		t.Errorf("setDeviceTiers args = %+v, %v", ta, err)
	}
}

// A Problem is a state the tab renders, not an error; a daemon refusal reaches
// the caller verbatim; no daemon is an error.
func TestPhonesServiceFailures(t *testing.T) {
	startFakeDaemon(t, func(req protocol.Request) protocol.Response {
		if req.Cmd == protocol.CmdPairBegin {
			return ok(t, protocol.PairBeginData{Problem: "The phone listener is off."})
		}
		return protocol.Response{Error: "does not match"}
	})
	s := &PhonesService{}
	b, err := s.Begin()
	if err != nil || b.Problem == "" || b.Payload != "" {
		t.Fatalf("Begin = %+v, %v", b, err)
	}
	if _, err := s.Confirm(protocol.PairConfirmArgs{Pairing: "p", Code: "1"}); err == nil || err.Error() != "does not match" {
		t.Fatalf("Confirm error = %v", err)
	}

	t.Setenv("LOLA_HOME", t.TempDir())
	if _, err := s.Devices(); err == nil {
		t.Fatal("expected a failure with no daemon")
	}
}

func TestPhonesServiceStatusTimeoutCoversWait(t *testing.T) {
	// Not observable through the socket without a slow fake; pin the constant
	// relationship the long poll depends on.
	if pairWaitSlack < time.Second {
		t.Fatal("slack must outlive the daemon's own wait")
	}
}

// TestBeginOutlivesTheDaemonsRelayWait: the daemon may wait PairBeginRelayWait
// for the hub before answering with a LAN-only QR, so Begin's socket budget
// must be longer, or an unhealthy hub fails every pairing.
func TestBeginOutlivesTheDaemonsRelayWait(t *testing.T) {
	if pairBeginTimeout <= protocol.PairBeginRelayWait+time.Second {
		t.Fatalf("pairBeginTimeout %v leaves no slack over the daemon's %v", pairBeginTimeout, protocol.PairBeginRelayWait)
	}
}
