package protocol

import (
	"encoding/json"
	"reflect"
	"slices"
	"strings"
	"testing"
)

func TestTalkAndSSHListsAreDisjoint(t *testing.T) {
	for _, cmd := range TalkCommands {
		if slices.Contains(SSHAgentCommands, cmd) {
			t.Errorf("%s is on TalkCommands and SSHAgentCommands", cmd)
		}
		if slices.Contains(SSHHumanCommands, cmd) {
			t.Errorf("%s is on TalkCommands and SSHHumanCommands", cmd)
		}
	}
	for _, cmd := range SSHAgentCommands {
		if slices.Contains(SSHHumanCommands, cmd) {
			t.Errorf("%s is on both ssh lists", cmd)
		}
	}
	for _, want := range []string{CmdSSHRecall, CmdSSHAnswer} {
		if !slices.Contains(TalkCommands, want) {
			t.Errorf("%s missing from TalkCommands", want)
		}
	}
	if n := len(SSHHumanCommands); n != 24 {
		t.Errorf("SSHHumanCommands has %d entries, want 24", n)
	}
}

func TestLocalOnlyCommandsNamed(t *testing.T) {
	want := []string{"pairBegin", "pairStatus", "pairConfirm", "pairReject", "devices", "revokeDevice", "setDeviceTiers"}
	if !slices.Equal(LocalOnlyCommands, want) {
		t.Errorf("LocalOnlyCommands = %v, want %v", LocalOnlyCommands, want)
	}
	if slices.Contains(LocalOnlyCommands, CmdRemotePair) || slices.Contains(LocalOnlyCommands, CmdRelayCred) {
		t.Error("remote.pair and relayCred are reachable by a phone and must not be local-only")
	}
	if CmdRemotePair != "remote.pair" || CmdRelayCred != "relayCred" || CmdSessionTimeline != "sessionTimeline" {
		t.Error("a wire command name changed")
	}
}

func TestTiersAreClosed(t *testing.T) {
	if slices.Contains(Tiers, TierNever) {
		t.Error("never is a classification, not a grantable tier")
	}
	if !slices.Equal(DefaultTiers, Tiers) {
		t.Errorf("DefaultTiers = %v, want every tier", DefaultTiers)
	}
}

// The daemon never shows the pairing code: a displayed code next to a confirm
// button proves nothing. No field of the begin/status replies may carry one.
func TestPairStatusHasNoCodeField(t *testing.T) {
	for _, typ := range []reflect.Type{reflect.TypeOf(PairStatusData{}), reflect.TypeOf(PairBeginData{})} {
		for i := range typ.NumField() {
			f := typ.Field(i)
			tag := strings.Split(f.Tag.Get("json"), ",")[0]
			if strings.EqualFold(f.Name, "code") || strings.EqualFold(tag, "code") {
				t.Errorf("%s.%s exposes a code", typ.Name(), f.Name)
			}
		}
	}
}

// hubStatus is a read a phone may make: the relay block names no rendezvous id,
// bearer, hash or connection id.
func TestHubStatusRelayCarriesNoSecrets(t *testing.T) {
	raw, err := json.Marshal(HubStatusData{Relay: &HubRelayInfo{State: HubRelayStateConnected, ActiveConns: 1, Rendezvous: 2}})
	if err != nil {
		t.Fatal(err)
	}
	var top map[string]json.RawMessage
	if err := json.Unmarshal(raw, &top); err != nil {
		t.Fatal(err)
	}
	var relay map[string]json.RawMessage
	if err := json.Unmarshal(top["relay"], &relay); err != nil {
		t.Fatal(err)
	}
	allowed := []string{"state", "reason", "since", "lastError", "lastErrorAt", "activeConns", "rendezvous"}
	for k := range relay {
		if !slices.Contains(allowed, k) {
			t.Errorf("hubStatus.relay carries %q", k)
		}
	}
	typ := reflect.TypeOf(HubRelayInfo{})
	for i := range typ.NumField() {
		tag := strings.ToLower(typ.Field(i).Tag.Get("json"))
		for _, bad := range []string{"rv", "bearer", "hash", "token", "tok", "conn"} {
			if tag == bad || strings.HasPrefix(tag, bad+",") {
				t.Errorf("HubRelayInfo field %s looks like a secret", typ.Field(i).Name)
			}
		}
	}
	// Omitted entirely when [remote] is off.
	raw, _ = json.Marshal(HubStatusData{})
	if strings.Contains(string(raw), `"relay"`) {
		t.Error("relay must be omitted when nil")
	}
}
