package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"
	"github.com/sushidev-team/lola/internal/hook"
	"github.com/sushidev-team/lola/internal/protocol"
)

// fakeAgent answers agent-side commands from a script and records what was asked.
type fakeAgent struct {
	calls   []string
	args    []json.RawMessage
	replies map[string][]any
	err     error
}

func (f *fakeAgent) send(cmd string, args any) (*protocol.Response, error) {
	f.calls = append(f.calls, cmd)
	raw, _ := json.Marshal(args)
	f.args = append(f.args, raw)
	if f.err != nil {
		return nil, f.err
	}
	q := f.replies[cmd]
	if len(q) == 0 {
		return &protocol.Response{OK: false, Error: "no reply for " + cmd}, nil
	}
	r := q[0]
	if len(q) > 1 {
		f.replies[cmd] = q[1:]
	}
	if resp, ok := r.(*protocol.Response); ok {
		return resp, nil
	}
	data, _ := json.Marshal(r)
	return &protocol.Response{OK: true, Data: data}, nil
}

func runSSH(t *testing.T, f *fakeAgent, argv ...string) (code int, stdout, stderr string) {
	t.Helper()
	orig := sshAgentSend
	origStart, origMax := sshPollStart, sshPollMax
	sshAgentSend = f.send
	sshPollStart, sshPollMax = time.Millisecond, 2*time.Millisecond
	t.Cleanup(func() {
		sshAgentSend = orig
		sshPollStart, sshPollMax = origStart, origMax
	})
	t.Setenv("LOLA_SESSION", "s1")
	cmd := sshCmd()
	var o, e bytes.Buffer
	cmd.SetOut(&o)
	cmd.SetErr(&e)
	cmd.SetArgs(argv)
	cmd.SilenceErrors, cmd.SilenceUsage = true, true
	err := cmd.Execute()
	var ex sshExitError
	switch {
	case err == nil:
	case errors.As(err, &ex):
		code = ex.code
	default:
		t.Fatalf("not an exit code: %v", err)
	}
	return code, o.String(), e.String()
}

func exitPtr(n int) *int { return &n }

func TestSSHRunStreamsAndExitsWithTheRemoteCode(t *testing.T) {
	f := &fakeAgent{replies: map[string][]any{
		protocol.CmdSSHAgentRun: {protocol.SSHAgentRunData{Block: &protocol.SSHBlock{ID: "b1", Status: protocol.SSHBlockPendingApproval}}},
		protocol.CmdSSHAgentPoll: {
			protocol.SSHAgentPollData{Block: protocol.SSHBlock{ID: "b1", Status: protocol.SSHBlockPendingApproval, Host: "h", Tier: "production"}},
			protocol.SSHAgentPollData{Block: protocol.SSHBlock{ID: "b1", Status: protocol.SSHBlockRunning}, Stdout: "one\n", StdoutOffset: 4},
			protocol.SSHAgentPollData{Block: protocol.SSHBlock{ID: "b1", Status: protocol.SSHBlockFailed, ExitCode: exitPtr(7)}, Stderr: "boom\n", StderrOffset: 5, StdoutOffset: 4, Done: true},
		},
	}}
	code, out, errOut := runSSH(t, f, "run", "h", "--why", "w", "--cwd", "/srv", "--", "ls", "-la", "/x")
	if code != 7 {
		t.Fatalf("exit %d", code)
	}
	if out != "one\n" || !strings.Contains(errOut, "boom\n") || !strings.Contains(errOut, "waiting for a human's approval") {
		t.Fatalf("out=%q err=%q", out, errOut)
	}
	var a protocol.SSHAgentRunArgs
	_ = json.Unmarshal(f.args[0], &a)
	if a.Command != "ls -la /x" || a.Why != "w" || a.Cwd != "/srv" || a.Host != "h" {
		t.Fatalf("%+v", a)
	}
	// Offsets move on, so nothing prints twice.
	var p protocol.SSHAgentPollArgs
	_ = json.Unmarshal(f.args[3], &p)
	if p.StdoutOffset != 4 {
		t.Fatalf("%+v", p)
	}
}

func TestSSHRunRefusalExitCodes(t *testing.T) {
	for code, want := range map[string]int{
		protocol.SSHRefuseHumanControl:  protocol.SSHExitHumanControl,
		protocol.SSHRefuseNoConnection:  protocol.SSHExitNotConnected,
		protocol.SSHRefuseNotConnected:  protocol.SSHExitNotConnected,
		protocol.SSHRefuseAwaitingGo:    protocol.SSHExitNotConnected,
		protocol.SSHRefuseConnDenied:    protocol.SSHExitDenied,
		protocol.SSHRefuseUnsandboxed:   protocol.SSHExitPolicyRefused,
		protocol.SSHRefuseUnknownHost:   protocol.SSHExitPolicyRefused,
		protocol.SSHRefuseRateLimited:   protocol.SSHExitPolicyRefused,
		protocol.SSHRefuseTooManyQueued: protocol.SSHExitPolicyRefused,
		protocol.SSHRefuseBadCommand:    protocol.SSHExitPolicyRefused,
		protocol.SSHRefuseStorage:       protocol.SSHExitPolicyRefused,
	} {
		f := &fakeAgent{replies: map[string][]any{
			protocol.CmdSSHAgentRun: {protocol.SSHAgentRunData{Refusal: &protocol.SSHRefusal{Code: code, Message: "said why"}}},
		}}
		got, _, errOut := runSSH(t, f, "run", "h", "--why", "w", "--", "ls")
		if got != want || !strings.Contains(errOut, "lola ssh: said why") {
			t.Errorf("%s: exit %d, stderr %q", code, got, errOut)
		}
	}
}

func TestSSHRunOutcomeLinesAndCodes(t *testing.T) {
	for _, tc := range []struct {
		b    protocol.SSHBlock
		code int
		word string
	}{
		{protocol.SSHBlock{ID: "b1", Status: protocol.SSHBlockDenied, Approval: protocol.SSHApprovalInfo{Reason: "too risky"}}, protocol.SSHExitDenied, "too risky"},
		{protocol.SSHBlock{ID: "b1", Status: protocol.SSHBlockExpired}, protocol.SSHExitDenied, "expired"},
		{protocol.SSHBlock{ID: "b1", Status: protocol.SSHBlockRefused, RefusalCode: protocol.SSHRefuseHumanControl}, protocol.SSHExitHumanControl, "wheel"},
		{protocol.SSHBlock{ID: "b1", Status: protocol.SSHBlockTimedOut}, protocol.SSHExitTimedOut, "timed out"},
		{protocol.SSHBlock{ID: "b1", Status: protocol.SSHBlockStopped}, protocol.SSHExitStopped, "may keep running"},
		{protocol.SSHBlock{ID: "b1", Status: protocol.SSHBlockLost}, protocol.SSHExitLost, "unknown"},
	} {
		f := &fakeAgent{replies: map[string][]any{
			protocol.CmdSSHAgentRun:  {protocol.SSHAgentRunData{Block: &protocol.SSHBlock{ID: "b1", Status: protocol.SSHBlockQueued}}},
			protocol.CmdSSHAgentPoll: {protocol.SSHAgentPollData{Block: tc.b, Done: true}},
		}}
		got, _, errOut := runSSH(t, f, "run", "h", "--why", "w", "--", "ls")
		if got != tc.code || !strings.Contains(errOut, tc.word) {
			t.Errorf("%s: exit %d, stderr %q", tc.b.Status, got, errOut)
		}
	}
}

func TestSSHRunGivesUpWithABlockIDAndDoesNotCancel(t *testing.T) {
	f := &fakeAgent{replies: map[string][]any{
		protocol.CmdSSHAgentRun:  {protocol.SSHAgentRunData{Block: &protocol.SSHBlock{ID: "b9", Status: protocol.SSHBlockQueued}}},
		protocol.CmdSSHAgentPoll: {protocol.SSHAgentPollData{Block: protocol.SSHBlock{ID: "b9", Status: protocol.SSHBlockRunning}}},
	}}
	code, _, errOut := runSSH(t, f, "run", "h", "--why", "w", "--wait", "1ms", "--", "sleep", "9")
	if code != protocol.SSHExitStillWaiting || !strings.Contains(errOut, "lola ssh wait b9") {
		t.Fatalf("%d %q", code, errOut)
	}
	for _, c := range f.calls {
		if c == protocol.CmdSSHAgentCancel {
			t.Fatal("giving up stopped the command")
		}
	}
}

func TestSSHRunDetach(t *testing.T) {
	f := &fakeAgent{replies: map[string][]any{
		protocol.CmdSSHAgentRun: {protocol.SSHAgentRunData{Block: &protocol.SSHBlock{ID: "b3", Status: protocol.SSHBlockPendingApproval}}},
	}}
	code, _, errOut := runSSH(t, f, "run", "h", "--why", "w", "--detach", "--", "ls")
	if code != protocol.SSHExitStillWaiting || !strings.Contains(errOut, "b3") || len(f.calls) != 1 {
		t.Fatalf("%d %q %v", code, errOut, f.calls)
	}
}

func TestSSHDaemonUnreachableIs248(t *testing.T) {
	f := &fakeAgent{err: fmt.Errorf("%w: gone", hook.ErrNoDaemon)}
	code, _, errOut := runSSH(t, f, "run", "h", "--why", "w", "--", "ls")
	if code != protocol.SSHExitDaemon || !strings.Contains(errOut, "cannot reach") {
		t.Fatalf("%d %q", code, errOut)
	}
	code, _, _ = runSSH(t, f, "post", "narration", "x")
	if code != protocol.SSHExitDaemon {
		t.Fatalf("post: %d", code)
	}
}

func TestSSHBadRequestsAre246(t *testing.T) {
	f := &fakeAgent{}
	if code, _, _ := runSSH(t, f, "run", "h"); code != protocol.SSHExitPolicyRefused {
		t.Fatalf("no command: %d", code)
	}
	if code, _, _ := runSSH(t, f, "run", "h", "--nope", "--", "ls"); code != protocol.SSHExitPolicyRefused {
		t.Fatalf("bad flag: %d", code)
	}
	if code, _, _ := runSSH(t, f, "request"); code != protocol.SSHExitPolicyRefused {
		t.Fatalf("no host: %d", code)
	}
	f = &fakeAgent{replies: map[string][]any{protocol.CmdSSHAgentPost: {&protocol.Response{OK: false, Error: "lola ssh post: unknown kind"}}}}
	code, _, errOut := runSSH(t, f, "post", "nope", "x")
	if code != protocol.SSHExitPolicyRefused || !strings.Contains(errOut, "unknown kind") {
		t.Fatalf("%d %q", code, errOut)
	}
}

func TestSSHPostPassesArgvThroughAndPrintsInbox(t *testing.T) {
	f := &fakeAgent{replies: map[string][]any{
		protocol.CmdSSHAgentPost: {protocol.SSHAgentPostData{ID: "e4", Revision: 1, Inbox: []string{"[lola] The human says:\nhurry"}}},
	}}
	code, out, errOut := runSSH(t, f, "post", "finding", "disk full", "--severity", "high", "--evidence", "b1")
	if code != 0 || strings.TrimSpace(out) != "e4" || !strings.Contains(errOut, "hurry") {
		t.Fatalf("%d %q %q", code, out, errOut)
	}
	var a protocol.SSHAgentPostArgs
	_ = json.Unmarshal(f.args[0], &a)
	if strings.Join(a.Argv, "|") != "finding|disk full|--severity|high|--evidence|b1" {
		t.Fatalf("%v", a.Argv)
	}
	_, help, _ := runSSH(t, &fakeAgent{}, "post", "--help")
	if !strings.Contains(help, "lola ssh post <kind>") {
		t.Fatalf("%q", help)
	}
}

func TestSSHRequestWaitsForTheHuman(t *testing.T) {
	hostRow := func(state, wheel, reason string) protocol.SSHAgentHostsData {
		return protocol.SSHAgentHostsData{Hosts: []protocol.SSHAgentHost{{Name: "h", State: state, Wheel: wheel, Reason: reason}}}
	}
	req := protocol.SSHAgentRequestData{Conn: "c1", State: protocol.SSHConnRequested}
	f := &fakeAgent{replies: map[string][]any{
		protocol.CmdSSHAgentRequest: {req},
		protocol.CmdSSHAgentHosts:   {hostRow("requested", "agent", ""), hostRow("authenticating", "agent", ""), hostRow("connected", "agent", "")},
	}}
	code, out, errOut := runSSH(t, f, "request", "h", "--why", "w")
	if code != 0 || !strings.Contains(out, "connected") || !strings.Contains(errOut, "waiting for the human's go") || !strings.Contains(errOut, "signing in") {
		t.Fatalf("%d %q %q", code, out, errOut)
	}
	f = &fakeAgent{replies: map[string][]any{
		protocol.CmdSSHAgentRequest: {req},
		protocol.CmdSSHAgentHosts:   {hostRow("denied", "agent", "not on a Friday")},
	}}
	code, _, errOut = runSSH(t, f, "request", "h", "--why", "w")
	if code != protocol.SSHExitDenied || !strings.Contains(errOut, "not on a Friday") {
		t.Fatalf("%d %q", code, errOut)
	}
	f = &fakeAgent{replies: map[string][]any{
		protocol.CmdSSHAgentRequest: {req},
		protocol.CmdSSHAgentHosts:   {hostRow("failed", "agent", "")},
	}}
	if code, _, _ = runSSH(t, f, "request", "h", "--why", "w"); code != protocol.SSHExitNotConnected {
		t.Fatalf("%d", code)
	}
	f = &fakeAgent{replies: map[string][]any{
		protocol.CmdSSHAgentRequest: {req},
		protocol.CmdSSHAgentHosts:   {hostRow("requested", "agent", "")},
	}}
	if code, _, _ = runSSH(t, f, "request", "h", "--why", "w", "--wait", "1ms"); code != protocol.SSHExitStillWaiting {
		t.Fatalf("%d", code)
	}
	f = &fakeAgent{replies: map[string][]any{
		protocol.CmdSSHAgentRequest: {req},
		protocol.CmdSSHAgentHosts:   {hostRow("connected", "human", "")},
	}}
	if code, _, _ = runSSH(t, f, "request", "h", "--why", "w"); code != protocol.SSHExitHumanControl {
		t.Fatalf("%d", code)
	}
}

func TestSSHOutputStopsAtTheEnd(t *testing.T) {
	f := &fakeAgent{replies: map[string][]any{
		protocol.CmdSSHAgentPoll: {
			protocol.SSHAgentPollData{Stdout: "a", StdoutOffset: 1, Block: protocol.SSHBlock{Status: protocol.SSHBlockRunning}},
			protocol.SSHAgentPollData{StdoutOffset: 1, Block: protocol.SSHBlock{Status: protocol.SSHBlockRunning}},
		},
	}}
	code, out, _ := runSSH(t, f, "output", "b1")
	if code != 0 || out != "a" {
		t.Fatalf("%d %q", code, out)
	}
}

func TestSSHEveryVerbIsRegistered(t *testing.T) {
	root := sshCmd()
	names := map[string]*cobra.Command{}
	for _, c := range root.Commands() {
		names[c.Name()] = c
	}
	for _, want := range []string{"hosts", "request", "run", "wait", "output", "post", "open", "ls", "go", "deny", "approve", "reject", "take", "handback", "stop", "close", "terminal"} {
		if names[want] == nil {
			t.Errorf("no verb %s", want)
		}
	}
}
