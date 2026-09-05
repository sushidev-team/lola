package remote

import (
	"strings"
	"testing"

	"github.com/sushidev-team/lola/internal/protocol"
)

// TestCommandDeniedIsAClosedList. The entries are named individually because
// each one is a decision with its own reason, and a table that only asserted
// "some commands are denied" would pass with any of them missing.
func TestCommandDeniedIsAClosedList(t *testing.T) {
	denied := []string{
		"stop", "reload", "renameProject", "hookEvent",
		"pairBegin", "pairStatus", "pairConfirm", "devices", "revokeDevice",
		"", "remote.hello", "remote.", "remote.whatever",
	}
	for _, cmd := range denied {
		if !CommandDenied(cmd) {
			t.Errorf("CommandDenied(%q) = false; it must be refused for every remote peer", cmd)
		}
	}
	allowed := []string{
		"status", "sessions", "projects", "prs", "tickets", "pane",
		"answer", "resolveConflict", "kill", "revive", "review", "coderabbit",
		"dev", "devFreePort", "switchAgent", "enable", "disable", "pollOnce",
		"open", "openPr", "openManual", "openTicket", "openURL",
	}
	for _, cmd := range allowed {
		if CommandDenied(cmd) {
			t.Errorf("CommandDenied(%q) = true; the denial list must not swallow a reachable command", cmd)
		}
	}
}

// TestKillIsReachableButNeverForced states the split the denial list rests on:
// the COMMAND is allowed and the FIELD is not, because a dirty worktree is the
// one gate teardown has.
func TestKillIsReachableButNeverForced(t *testing.T) {
	if CommandDenied("kill") {
		t.Fatal("kill is denied outright; only Force is")
	}
	got := normalizeRequest("kill", protocol.Request{Cmd: "answer", Session: "lola-fe-42", Force: true, Text: "hi"})
	if got.Force {
		t.Error("Force survived normalizeRequest")
	}
	if got.Cmd != "kill" {
		t.Errorf("Cmd is %q; the envelope must win over the payload", got.Cmd)
	}
	if got.Session != "lola-fe-42" || got.Text != "hi" {
		t.Errorf("normalizeRequest dropped a field it should carry: %+v", got)
	}
}

// TestAuditLineNamesTheActorAndNeverThePayload. The exclusion is load-bearing:
// answer carries prose typed at a phone, which may contain a pasted token, and
// the daemon's log is append-only, unrotated and mirrored to stderr.
func TestAuditLineNamesTheActorAndNeverThePayload(t *testing.T) {
	secret := "ghp_thislookslikeapersonalaccesstoken"
	r := newRig(t, func(r *rig, _ *Options) {
		r.auth.peer = Peer{DeviceID: "phone-1", Insecure: true}
	})
	r.req("a1", "answer", protocol.Request{Session: "lola-fe-42", Text: secret})
	r.next()

	log := r.log.all()
	if strings.Contains(log, secret) {
		t.Fatalf("the audit line carried the payload:\n%s", log)
	}
	for _, want := range []string{"device=phone-1", "cmd=answer", "session=lola-fe-42", "ok=true"} {
		if !strings.Contains(log, want) {
			t.Errorf("the audit line is missing %q:\n%s", want, log)
		}
	}
	if !strings.Contains(log, "insecure=true") {
		t.Errorf("the audit line does not record the insecure M1 path:\n%s", log)
	}
}

// TestReadsAreNotAudited. A remote peer can drive writes into an unrotated log
// file, so only the commands that CHANGE something earn a line.
func TestReadsAreNotAudited(t *testing.T) {
	r := newRig(t)
	for _, cmd := range []string{"status", "sessions", "projects", "pane"} {
		r.req("x", cmd, protocol.Request{Session: "lola-fe-42"})
		r.next()
	}
	if log := r.log.all(); strings.Contains(log, "cmd=sessions") || strings.Contains(log, "cmd=status") {
		t.Errorf("a read command wrote an audit line:\n%s", log)
	}
}

// TestAuditFieldsCannotForgeALogLine. Session, Cmd and the device label are all
// peer-supplied, and a newline in one of them would write a second line that
// reads as the daemon's own.
func TestAuditFieldsCannotForgeALogLine(t *testing.T) {
	forged := "lola-fe-42\n2026-08-28 12:00:00 daemon stopped"
	r := newRig(t)
	r.req("a1", "kill", protocol.Request{Session: forged})
	r.next()

	log := r.log.all()
	if strings.Contains(log, "daemon stopped\n") || strings.Contains(log, "\n2026-08-28") {
		t.Fatalf("a session id forged a log line:\n%q", log)
	}
	if !strings.Contains(log, "cmd=kill") {
		t.Errorf("the kill was not audited at all:\n%s", log)
	}
}

func TestLogSafeClipsAndStrips(t *testing.T) {
	cases := map[string]string{
		"":                       "-",
		"lola-fe-42":             "lola-fe-42",
		"a\nb":                   "a?b",
		"a\tb\rc":                "a?b?c",
		"a\x00b":                 "a?b",
		strings.Repeat("x", 200): strings.Repeat("x", 64) + "...",
	}
	for in, want := range cases {
		if got := logSafe(in); got != want {
			t.Errorf("logSafe(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestAuthorizerSeesEveryFrameNotJustTheFirst. Authorization is per FRAME
// because that is what makes an M2 capability downgrade behave like a
// revocation: it takes effect on the next frame rather than the next connect.
func TestAuthorizerSeesEveryFrameNotJustTheFirst(t *testing.T) {
	r := newRig(t)
	r.req("a", "sessions", protocol.Request{})
	r.next()
	r.sub("s1", "lola-fe-42", 55, 34)
	r.wantResync("lola-fe-42")
	r.pty("lola-fe-42", protocol.PTYInputPayload{Action: protocol.PTYActionWrite, Data: []byte("x")})
	r.wantOpen()

	seen := r.auth.seen()
	want := map[string]bool{"req:sessions": false, "sub:": false, "pty:": false}
	for _, s := range seen {
		if _, ok := want[s]; ok {
			want[s] = true
		}
	}
	for k, ok := range want {
		if !ok {
			t.Errorf("the authorizer never saw %q; it saw %v", k, seen)
		}
	}
}

// TestAuthorizeFrameCarriesTheAuthenticatedPeer. The identity comes from the
// authorizer and from nowhere else — the envelope deliberately carries no
// device id, because a peer-asserted identity field would be one the server has
// to remember to ignore.
func TestAuthorizeFrameCarriesTheAuthenticatedPeer(t *testing.T) {
	r := newRig(t, func(r *rig, _ *Options) {
		r.auth.peer = Peer{DeviceID: "phone-9", Label: "Test phone"}
	})
	r.req("a", "sessions", protocol.Request{})
	r.next()

	peers := r.auth.peers()
	if len(peers) != 1 {
		t.Fatalf("the authorizer was consulted %d times, want 1", len(peers))
	}
	if peers[0].DeviceID != "phone-9" || peers[0].Label != "Test phone" {
		t.Errorf("got %+v, want the peer Authenticate returned", peers[0])
	}
	if peers[0].RemoteAddr == "" {
		t.Error("the peer's address was not filled in from the connection")
	}
	if peers[0].ConnectedAt.IsZero() {
		t.Error("the peer was not stamped from Options.Now")
	}
}
