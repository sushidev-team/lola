package daemon

import (
	"os"
	"strings"
	"testing"
)

// cmd=status names the MACHINE, because a phone reaches the same Mac on a
// different address at home and at the office — an address is a poor name for
// it and a stale one is worse. It travels on an authenticated answer, never in
// the mDNS advertisement, which deliberately carries no hostname at all.
func TestStatusNamesTheMachine(t *testing.T) {
	want, err := os.Hostname()
	if err != nil {
		t.Skip("this machine has no hostname to compare against")
	}
	want = strings.TrimSuffix(strings.TrimSpace(want), ".local")
	if got := machineName(); got != want {
		t.Errorf("machineName() = %q, want %q", got, want)
	}
	if strings.HasSuffix(machineName(), ".local") {
		t.Error("the mDNS suffix is plumbing, not part of the name")
	}
}
