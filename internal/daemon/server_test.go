package daemon

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/sushidev-team/lola/internal/config"
	"github.com/sushidev-team/lola/internal/linear"
	"github.com/sushidev-team/lola/internal/protocol"
	"github.com/sushidev-team/lola/internal/runtime"
	"github.com/sushidev-team/lola/internal/state"
)

// newEnableTestDaemon builds a daemon whose config (poll p1 + [[project]]
// proj1) is saved to <home>/config.toml, ready for handleEnable/handleReload
// tests (both persist via config.DefaultPath under LOLA_HOME).
func newEnableTestDaemon(t *testing.T, poll config.Project) *Daemon {
	t.Helper()
	cfg := testConfig(poll)
	d := newTestDaemon(t, cfg, &linear.Fake{}, &fakeNative{})
	path, err := config.DefaultPath()
	if err != nil {
		t.Fatal(err)
	}
	if err := cfg.Save(path); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(d.stopAllWorkers) // a successful enable starts a worker
	return d
}

// Enabling validates the whole config (which resolves the poll's [[project]]),
// flips the flag, and persists.
func TestEnableValidatesConfigAndPersists(t *testing.T) {
	p := labelPoll("p1")
	p.Enabled = false
	d := newEnableTestDaemon(t, p)

	if err := d.handleEnable(context.Background(), "p1", true); err != nil {
		t.Fatalf("handleEnable: %v", err)
	}
	if !d.cfg.PollByName("p1").Enabled {
		t.Error("poll not enabled")
	}
	if _, err := os.Stat(filepath.Join(d.home, "config.toml")); err != nil {
		t.Errorf("config not saved: %v", err)
	}
}

// The enable-time validation runs Validate: a project with an invalid polling
// config (here a bad match_mode) is rejected and its enabled flag rolled back.
func TestEnableRejectsInvalidPollingConfig(t *testing.T) {
	p := labelPoll("p1")
	p.Enabled = false
	p.MatchMode = "bogus" // fails Validate
	d := newEnableTestDaemon(t, p)

	err := d.handleEnable(context.Background(), "p1", true)
	if err == nil {
		t.Fatal("handleEnable must reject an invalid polling config")
	}
	if d.cfg.PollByName("p1").Enabled {
		t.Error("polling enabled despite validation failure")
	}
}

func TestDisableStopsPoll(t *testing.T) {
	d := newEnableTestDaemon(t, labelPoll("p1")) // enabled

	if err := d.handleEnable(context.Background(), "p1", false); err != nil {
		t.Fatalf("handleEnable(disable): %v", err)
	}
	if d.cfg.PollByName("p1").Enabled {
		t.Error("poll still enabled")
	}
}

func TestEnableUnknownPollErrors(t *testing.T) {
	d := newEnableTestDaemon(t, labelPoll("p1"))
	if err := d.handleEnable(context.Background(), "ghost", true); err == nil {
		t.Fatal("handleEnable must error for an unknown poll")
	}
}

// Reload rejects an invalid on-disk config and keeps the previous one live.
func TestReloadRejectsInvalidConfigKeepsPrevious(t *testing.T) {
	d := newEnableTestDaemon(t, labelPoll("p1"))
	path, err := config.DefaultPath()
	if err != nil {
		t.Fatal(err)
	}
	bad := testConfig(labelPoll("p1"))
	bad.Defaults.GlobalCap = 0 // invalid
	if err := bad.Save(path); err != nil {
		t.Fatal(err)
	}

	if err := d.handleReload(context.Background()); err == nil {
		t.Fatal("reload must reject an invalid config")
	}
	if d.cfg.Defaults.GlobalCap != 10 {
		t.Errorf("reload must keep the previous config, global_cap = %d", d.cfg.Defaults.GlobalCap)
	}
}

// Finding 4: a reload that changes [tmux].socket_name (but not [[project]]) must
// rebuild the native runtime so its Alive/Adopt/Kill/Spawn land on the SAME
// server as d.tmuxClient's live send-keys/capture. Without the rebuild the
// observer would read the OLD server while keys go to the NEW one.
func TestReloadRebuildsNativeOnSocketChange(t *testing.T) {
	d := newEnableTestDaemon(t, labelPoll("p1"))
	// Stand in a real native runtime on the default "lola" socket and mark it
	// owned so the realNative-gated rebuild path is exercised.
	d.native = newNativeRuntime(d.cfg, d.home, d.lolaBin, d.linearKey, d.nativeLogf)
	d.realNative = true
	if got := d.native.(*runtime.Native).Tmux.SocketName; got != config.DefaultTmuxSocketName {
		t.Fatalf("precondition: native socket = %q, want %q", got, config.DefaultTmuxSocketName)
	}

	// Persist a config identical except for the tmux socket name.
	path, err := config.DefaultPath()
	if err != nil {
		t.Fatal(err)
	}
	nc := testConfig(labelPoll("p1"))
	nc.Tmux = config.TmuxConfig{SocketName: "team-lola"}
	if err := nc.Save(path); err != nil {
		t.Fatal(err)
	}

	if err := d.handleReload(context.Background()); err != nil {
		t.Fatalf("handleReload: %v", err)
	}

	nat, ok := d.native.(*runtime.Native)
	if !ok {
		t.Fatalf("native is %T, want *runtime.Native", d.native)
	}
	if got := nat.Tmux.SocketName; got != "team-lola" {
		t.Fatalf("native tmux socket = %q, want team-lola (runtime must be rebuilt on socket change)", got)
	}
	if got := d.tmuxClient().SocketName; got != "team-lola" {
		t.Fatalf("tmuxClient socket = %q, want team-lola (both must target the same server)", got)
	}
}

// --- The notification split (THE FLAP FIX) ----------------------------------

// Claude Code's idle nudge ("Claude is waiting for your input", ~60s after a
// turn ends) means the turn ENDED and nobody looked — not that the agent asked
// anything. It must therefore leave the agent axis on IDLE, drop only the
// display-only Nudged breadcrumb, and OPEN the send-keys gate (the agent is
// provably parked at its own composer, exactly as after "stop").
//
// This is the ~90% case: 412 of 458 needs_input transitions in the measured
// 20MB daemon.log came from this one signal, and because nothing demotes
// waiting_input except a new turn, every unread finished turn stuck on
// "Needs You" and flapped against the delivery word on the nudge's 60s period.
func TestHookNotificationIdleNudgeParksIdleWithOpenGate(t *testing.T) {
	for _, c := range []struct{ name, message, reason string }{
		{"message", "Claude is waiting for your input", ""},
		{"reasonField", "", "idle_timeout"},
		{"bare", "", ""}, // an unclassifiable notification is the nudge by default
	} {
		t.Run(c.name, func(t *testing.T) {
			d := newTestDaemon(t, nativeTestConfig(nativePoll("p1")), &linear.Fake{}, &fakeNative{})
			s := nativeSess("FE-1", "working")
			d.sessions.Upsert(s)

			resp := d.handle(context.Background(), protocol.Request{
				Cmd: "hookEvent", Session: s.ID, Event: "notification",
				Hook: &protocol.HookPayload{Message: c.message, Reason: c.reason},
			})
			if !resp.OK {
				t.Fatalf("hookEvent must always be OK, got %+v", resp)
			}

			got, ok := d.sessions.Get(s.ID)
			if !ok {
				t.Fatal("session vanished")
			}
			if got.AgentState != state.AgentIdle {
				t.Errorf("AgentState = %q, want idle — the nudge is 'nobody looked', not 'the agent is blocked'", got.AgentState)
			}
			if got.InputReason != "" {
				t.Errorf("InputReason = %q, want empty: the axis is not waiting_input at all", got.InputReason)
			}
			if !got.Nudged {
				t.Error("Nudged must be set — it is the display-only breadcrumb that replaces the old needs_input")
			}
			if !got.AtPrompt || !got.AtPromptVerified {
				t.Errorf("AtPrompt/verified = %v/%v, want true/true: the nudge proves the agent is at its composer", got.AtPrompt, got.AtPromptVerified)
			}
			if c.message != "" && got.LastNotification != c.message {
				t.Errorf("LastNotification = %q, want %q recorded for display even on the idle branch", got.LastNotification, c.message)
			}
		})
	}
}

// The other 10%: a real permission prompt is a genuine block and keeps the old
// behavior exactly — waiting_input, InputPermission, and the send-keys gate
// CLOSED, because typed prose would answer a y/n approval with the wrong text.
func TestHookNotificationPermissionParksWaitingInput(t *testing.T) {
	for _, c := range []struct{ name, message, reason string }{
		{"message", "Claude needs your permission to use Bash", ""},
		{"codexNotifyType", "", "approval-requested"},
	} {
		t.Run(c.name, func(t *testing.T) {
			d := newTestDaemon(t, nativeTestConfig(nativePoll("p1")), &linear.Fake{}, &fakeNative{})
			s := nativeSess("FE-1", "working")
			d.sessions.Upsert(s)

			d.handleHookEvent(protocol.Request{
				Cmd: "hookEvent", Session: s.ID, Event: "notification",
				Hook: &protocol.HookPayload{Message: c.message, Reason: c.reason},
			})

			got, _ := d.sessions.Get(s.ID)
			if got.AgentState != state.AgentWaitingInput || got.InputReason != state.InputPermission {
				t.Errorf("axis = %q/%q, want waiting_input/permission_prompt", got.AgentState, got.InputReason)
			}
			if got.Status != "needs_input" {
				t.Errorf("legacy status = %q, want needs_input (mobile still reads this field)", got.Status)
			}
			if got.AtPrompt {
				t.Error("a permission prompt must keep the send-keys gate CLOSED")
			}
			if got.Nudged {
				t.Error("Nudged is the idle-nudge breadcrumb only; a real block must not set it")
			}
			if c.message != "" && got.LastNotification != c.message {
				t.Errorf("LastNotification = %q, want %q", got.LastNotification, c.message)
			}
		})
	}
}

// The breadcrumb must not outlive the idle it describes: once the agent takes a
// new turn (user_prompt), Nudged is cleared by SetAgentState — otherwise a
// working session keeps claiming somebody is being waited on.
func TestHookNudgeBreadcrumbClearsWhenTheAgentResumes(t *testing.T) {
	d := newTestDaemon(t, nativeTestConfig(nativePoll("p1")), &linear.Fake{}, &fakeNative{})
	s := nativeSess("FE-1", "working")
	d.sessions.Upsert(s)

	d.handleHookEvent(protocol.Request{
		Cmd: "hookEvent", Session: s.ID, Event: "notification",
		Hook: &protocol.HookPayload{Message: "Claude is waiting for your input"},
	})
	if got, _ := d.sessions.Get(s.ID); !got.Nudged {
		t.Fatal("precondition: the nudge must set Nudged")
	}

	d.handleHookEvent(protocol.Request{Cmd: "hookEvent", Session: s.ID, Event: "user_prompt"})

	got, _ := d.sessions.Get(s.ID)
	if got.AgentState != state.AgentWorking {
		t.Fatalf("AgentState = %q, want working after a turn start", got.AgentState)
	}
	if got.Nudged {
		t.Error("Nudged must be cleared when the axis leaves idle")
	}
}
