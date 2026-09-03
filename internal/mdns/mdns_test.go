package mdns

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeProc is a Process whose exit the test decides.
//
// It also ends when its context is cancelled, because that is what the real
// child does: exec.CommandContext kills the process, Wait returns, and the
// supervisor's goroutine can finish. A fake that ignored cancellation would
// wedge Stop forever — which is a property of the fake, not of the code.
type fakeProc struct {
	exit chan error
	ctx  context.Context
}

func (p *fakeProc) Wait() error {
	select {
	case err := <-p.exit:
		return err
	case <-p.ctx.Done():
		return p.ctx.Err()
	}
}

// recorder is a Starter that records every launch and hands back a process the
// test can end.
type recorder struct {
	mu     sync.Mutex
	args   [][]string
	procs  []*fakeProc
	failed error
	spawn  chan struct{}
}

func newRecorder() *recorder {
	return &recorder{spawn: make(chan struct{}, 16)}
}

func (r *recorder) start(ctx context.Context, _ string, args []string) (Process, error) {
	r.mu.Lock()
	r.args = append(r.args, append([]string(nil), args...))
	err := r.failed
	p := &fakeProc{exit: make(chan error, 1), ctx: ctx}
	if err == nil {
		r.procs = append(r.procs, p)
	}
	r.mu.Unlock()
	select {
	case r.spawn <- struct{}{}:
	default:
	}
	if err != nil {
		return nil, err
	}
	return p, nil
}

func (r *recorder) launches() [][]string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([][]string(nil), r.args...)
}

func (r *recorder) waitForLaunch(t *testing.T) {
	t.Helper()
	select {
	case <-r.spawn:
	case <-time.After(2 * time.Second):
		t.Fatal("no registration was started")
	}
}

// The argv IS the contract with dns-sd, so it is pinned exactly.
// The production delay is five seconds, which is a supervisor's pace rather
// than a test's.
func shortenRestart(t *testing.T) {
	t.Helper()
	prev := restartDelay
	restartDelay = 20 * time.Millisecond
	t.Cleanup(func() { restartDelay = prev })
}

func TestArgs(t *testing.T) {
	got, err := Args(Service{
		Instance: "lola on marvin",
		Port:     7717,
		TXT:      map[string]string{TXTVersion: Version, TXTPin: "abc="},
	})
	if err != nil {
		t.Fatalf("Args: %v", err)
	}
	want := []string{"-R", "lola on marvin", "_lola._tcp", "local", "7717", "pin=abc=", "v=1"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("argv:\n got %v\nwant %v", got, want)
	}
}

// TXT pairs are emitted in sorted key order, so the argv a machine produces is
// reproducible rather than map-iteration order.
func TestArgsSortsTXT(t *testing.T) {
	got, err := Args(Service{Instance: "x", Port: 1, TXT: map[string]string{"z": "1", "a": "2", "m": "3"}})
	if err != nil {
		t.Fatalf("Args: %v", err)
	}
	if strings.Join(got[5:], ",") != "a=2,m=3,z=1" {
		t.Fatalf("TXT order: %v", got[5:])
	}
}

// Control characters never reach an announcement every device on the network
// reads, nor an argv element.
func TestArgsSanitizes(t *testing.T) {
	got, err := Args(Service{
		Instance: "lola\x07 on\nmarvin ",
		Port:     7717,
		TXT:      map[string]string{TXTPin: "ab\x00c"},
	})
	if err != nil {
		t.Fatalf("Args: %v", err)
	}
	if got[1] != "lola onmarvin" {
		t.Fatalf("instance = %q", got[1])
	}
	if got[5] != "pin=abc" {
		t.Fatalf("txt = %q", got[5])
	}
}

// A service that cannot be described is refused rather than advertised wrong.
func TestArgsRefusesNonsense(t *testing.T) {
	if _, err := Args(Service{Instance: "  ", Port: 7717}); err == nil {
		t.Error("an empty instance name was accepted")
	}
	for _, port := range []int{0, -1, 70000} {
		if _, err := Args(Service{Instance: "x", Port: port}); err == nil {
			t.Errorf("port %d was accepted", port)
		}
	}
}

func TestStartRegistersAndStopWithdraws(t *testing.T) {
	r := newRecorder()
	a := New("dns-sd", r.start, nil)
	if err := a.Start(Service{Instance: "lola", Port: 7717}); err != nil {
		t.Fatalf("Start: %v", err)
	}
	r.waitForLaunch(t)
	if len(r.launches()) != 1 {
		t.Fatalf("launches = %d, want 1", len(r.launches()))
	}
	// Stop must RETURN once the child is gone: the registration lives exactly
	// as long as the process, so a Stop that raced its own child would leave
	// the daemon advertising an address it no longer listens on.
	a.Stop()
	a.Stop() // idempotent
}

// A child that dies is restarted, because a registration that stopped silently
// is a phone that simply stops finding this Mac.
func TestSuperviseRestartsADeadChild(t *testing.T) {
	shortenRestart(t)
	r := newRecorder()
	a := New("dns-sd", r.start, nil)
	t.Cleanup(a.Stop)
	if err := a.Start(Service{Instance: "lola", Port: 7717}); err != nil {
		t.Fatalf("Start: %v", err)
	}
	r.waitForLaunch(t)
	r.mu.Lock()
	proc := r.procs[0]
	r.mu.Unlock()
	proc.exit <- errors.New("registration lost")

	// The restart is delayed on purpose (a permanent failure must not spin), so
	// this waits for the delay rather than asserting immediately.
	select {
	case <-r.spawn:
	case <-time.After(2 * time.Second):
		t.Fatal("a dead registration was never restarted")
	}
}

// No dns-sd at all costs discovery and nothing else: Start still succeeds, the
// listener is untouched, and the operator is told once rather than every retry.
func TestAFailedLaunchIsReportedOnceAndNeverFatal(t *testing.T) {
	shortenRestart(t)
	r := newRecorder()
	r.failed = errors.New("exec: dns-sd: not found")
	var mu sync.Mutex
	var lines []string
	a := New("dns-sd", r.start, func(f string, v ...any) {
		mu.Lock()
		lines = append(lines, f)
		mu.Unlock()
	})
	t.Cleanup(a.Stop)
	if err := a.Start(Service{Instance: "lola", Port: 7717}); err != nil {
		t.Fatalf("Start must not fail when dns-sd is missing: %v", err)
	}
	r.waitForLaunch(t)
	mu.Lock()
	n := len(lines)
	mu.Unlock()
	if n != 1 {
		t.Fatalf("logged %d lines for one failure, want 1", n)
	}
}

// Starting again replaces the previous registration rather than adding one: a
// rebind must not leave the old advertisement running beside the new.
func TestStartReplacesThePreviousRegistration(t *testing.T) {
	r := newRecorder()
	a := New("dns-sd", r.start, nil)
	t.Cleanup(a.Stop)
	if err := a.Start(Service{Instance: "one", Port: 7717}); err != nil {
		t.Fatalf("Start: %v", err)
	}
	r.waitForLaunch(t)
	// Nothing releases the first child by hand: Start cancels its context, and
	// that is exactly what ends the real one.
	if err := a.Start(Service{Instance: "two", Port: 7718}); err != nil {
		t.Fatalf("second Start: %v", err)
	}
	r.waitForLaunch(t)
	all := r.launches()
	if len(all) != 2 {
		t.Fatalf("launches = %d, want 2", len(all))
	}
	if all[1][1] != "two" || all[1][4] != "7718" {
		t.Fatalf("second registration is %v", all[1])
	}
}
