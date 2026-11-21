package hubproto

import (
	"encoding/json"
	"testing"
)

func TestAgentClaimRequestValidate(t *testing.T) {
	ok := AgentClaimRequest{Session: "0b8d6a5e-1c2d-4e3f-9a8b-7c6d5e4f3a2b", Owner: "11111111-2222-3333-4444-555555555555"}
	if err := ok.Validate(); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []AgentClaimRequest{
		{Session: ""},
		{Session: "short"},
		{Session: "has spaces in it ok?"},
		{Session: ok.Session, Owner: "../etc"},
	} {
		if bad.Validate() == nil {
			t.Errorf("accepted %+v", bad)
		}
	}
}

func TestAgentSettleAndAuthorizeValidate(t *testing.T) {
	id := "0b8d6a5e-1c2d-4e3f-9a8b-7c6d5e4f3a2b"
	if (AgentAuthorizeRequest{Session: id, Epoch: 0}).Validate() == nil {
		t.Error("epoch 0 accepted")
	}
	if (AgentSettleRequest{Session: id, Epoch: 1, Outcome: "merged"}).Validate() == nil {
		t.Error("unknown outcome accepted")
	}
	if err := (AgentSettleRequest{Session: id, Epoch: 1, Outcome: SettleBound}).Validate(); err != nil {
		t.Error(err)
	}
}

// An older frame stays byte-identical: no linear_user_id key unless set.
func TestHeartbeatLinearUserIDIsOptionalOnTheWire(t *testing.T) {
	b, _ := json.Marshal(HeartbeatRequest{Agents: []string{}, Repos: []string{}, LiveLeases: []string{}})
	if string(b) != `{"free_slots":0,"agents":[],"repos":[],"live_leases":[]}` {
		t.Fatalf("heartbeat changed on the wire: %s", b)
	}
	if (HeartbeatRequest{LinearUserID: "bad id!"}).Validate() == nil {
		t.Error("bad linear user id accepted")
	}
}
