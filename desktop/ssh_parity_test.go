package main

// Anti-drift pin for the agent-assisted SSH vocabulary. lib/ssh.ts mirrors
// internal/protocol/ssh.go with no compiler in common, so a text read of the real
// file is the only thing that can notice them diverging. It matters more here than
// for the status words: these constants include the sentences a human reads at the
// moment trust is spent, and the UX spec says they are never retyped.

import (
	"os"
	"regexp"
	"strconv"
	"testing"

	"github.com/sushidev-team/lola/internal/protocol"
)

const sshTS = "frontend/src/lib/ssh.ts"

func readSSHTS(t *testing.T) string {
	t.Helper()
	src, err := os.ReadFile(sshTS)
	if err != nil {
		t.Fatalf("read %s: %v", sshTS, err)
	}
	return string(src)
}

func tsStringArray(t *testing.T, src, name string) []string {
	t.Helper()
	m := regexp.MustCompile(`(?s)export const ` + name + `: string\[\] = \[(.*?)\];`).FindStringSubmatch(src)
	if m == nil {
		t.Fatalf("%s: %s not found, so this test stopped guarding anything", sshTS, name)
	}
	var out []string
	for _, q := range regexp.MustCompile(`"([a-z_]+)"`).FindAllStringSubmatch(m[1], -1) {
		out = append(out, q[1])
	}
	return out
}

func tsString(t *testing.T, src, name string) string {
	t.Helper()
	m := regexp.MustCompile(`(?s)export const ` + name + ` =\s*"(.*?)";`).FindStringSubmatch(src)
	if m == nil {
		t.Fatalf("%s: %s not found", sshTS, name)
	}
	return m[1]
}

func TestSSHVocabularyParity(t *testing.T) {
	src := readSSHTS(t)
	for _, c := range []struct {
		name string
		want []string
	}{
		{"SSH_ELEMENT_KINDS", protocol.SSHElementKinds},
		{"SSH_SEVERITIES", protocol.SSHSeverities},
		{"SSH_STEP_STATES", protocol.SSHStepStates},
		{"SSH_CONN_STATES", []string{
			protocol.SSHConnRequested, protocol.SSHConnDenied, protocol.SSHConnConnecting, protocol.SSHConnAuthenticating,
			protocol.SSHConnConnected, protocol.SSHConnDisconnected, protocol.SSHConnFailed, protocol.SSHConnClosed,
		}},
		{"SSH_BLOCK_STATUSES", []string{
			protocol.SSHBlockPendingApproval, protocol.SSHBlockQueued, protocol.SSHBlockRunning, protocol.SSHBlockDenied,
			protocol.SSHBlockExpired, protocol.SSHBlockRefused, protocol.SSHBlockSucceeded, protocol.SSHBlockFailed,
			protocol.SSHBlockTimedOut, protocol.SSHBlockStopped, protocol.SSHBlockLost,
		}},
		{"SSH_EVENT_KINDS", []string{
			protocol.SSHEventRequested, protocol.SSHEventGo, protocol.SSHEventDenied, protocol.SSHEventConnected,
			protocol.SSHEventDisconnected, protocol.SSHEventFailed, protocol.SSHEventWheelTaken, protocol.SSHEventHandedBack,
			protocol.SSHEventGrantCreated, protocol.SSHEventGrantEnded, protocol.SSHEventClosed,
			protocol.SSHEventSessionStarted, protocol.SSHEventSessionEnded, protocol.SSHEventAgentExited, protocol.SSHEventStopped,
			protocol.SSHEventCheckpoint, protocol.SSHEventPR, protocol.SSHEventCI, protocol.SSHEventReview, protocol.SSHEventHandoff,
		}},
	} {
		pinList(t, c.name, tsStringArray(t, src, c.name), c.want)
	}
}

func TestSSHSentencesParity(t *testing.T) {
	src := readSSHTS(t)
	for name, want := range map[string]string{
		"SSH_DATA_WARNING":       protocol.SSHDataWarning,
		"SSH_REMOTE_OUTLIVES":    protocol.SSHRemoteOutlives,
		"SSH_USER_IS_THE_LIMIT":  protocol.SSHUserIsTheLimit,
		"SSH_UNSANDBOXED_NOTE":   protocol.SSHUnsandboxedNote,
		"SSH_DRIVING_NOTE":       protocol.SSHDrivingNote,
		"SSH_TRANSCRIPT_WARNING": protocol.SSHTranscriptWarning,
	} {
		if got := tsString(t, src, name); got != want {
			t.Errorf("%s:\n ts %q\n go %q", name, got, want)
		}
	}
}

func TestSSHLimitsParity(t *testing.T) {
	src := readSSHTS(t)
	for name, want := range map[string]int{
		"SSH_MAX_SAY_BYTES":      protocol.SSHMaxSayBytes,
		"SSH_MAX_HANDBACK_BYTES": protocol.SSHMaxHandbackBytes,
		"SSH_MAX_POLL_BYTES":     protocol.SSHMaxPollBytes,
	} {
		m := regexp.MustCompile(`export const ` + name + ` = (\d+);`).FindStringSubmatch(src)
		if m == nil {
			t.Fatalf("%s not found", name)
		}
		if got, _ := strconv.Atoi(m[1]); got != want {
			t.Errorf("%s: ts %d, go %d", name, got, want)
		}
	}
}
