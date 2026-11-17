package daemon

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/sushidev-team/lola/internal/protocol"
)

func TestSSHGlanceStaysOffThePairedDeviceSessionList(t *testing.T) {
	r := newSSHRig(t, hostDev)
	r.session("s1", "")
	if _, err := r.d.handleSSHAgentRequest("s1", protocol.SSHAgentRequestArgs{Host: "dev-box", Why: "secret-reason"}); err != nil {
		t.Fatal(err)
	}

	glance := func(handle func(context.Context, protocol.Request) protocol.Response) (*protocol.SSHSessionInfo, string) {
		t.Helper()
		resp := handle(context.Background(), protocol.Request{Cmd: "sessions"})
		if !resp.OK {
			t.Fatalf("sessions: %s", resp.Error)
		}
		var data protocol.SessionsData
		if err := json.Unmarshal(resp.Data, &data); err != nil {
			t.Fatal(err)
		}
		if len(data.Sessions) != 1 {
			t.Fatalf("sessions = %d, want 1", len(data.Sessions))
		}
		return data.Sessions[0].SSH, string(resp.Data)
	}

	if got, _ := glance(r.d.handle); got == nil {
		t.Fatal("the local list lost its ssh glance")
	}
	got, raw := glance(r.d.handleRemote)
	if got != nil || strings.Contains(raw, "secret-reason") {
		t.Fatalf("the remote list carries ssh details: %s", raw)
	}
}
