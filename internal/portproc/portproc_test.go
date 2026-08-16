package portproc

import (
	"context"
	"errors"
	"testing"
)

// fakeLsof answers the two passes Listeners makes, in order.
func fakeLsof(t *testing.T, sockets, cwds string, cwdErr error) (*Finder, *[][]string) {
	t.Helper()
	var calls [][]string
	f := &Finder{Exec: func(_ context.Context, _ string, args ...string) ([]byte, error) {
		calls = append(calls, args)
		if len(calls) == 1 {
			return []byte(sockets), nil
		}
		if cwdErr != nil {
			return nil, cwdErr
		}
		return []byte(cwds), nil
	}}
	return f, &calls
}

// The whole contract: pid, command, port and the process's cwd, joined across
// lsof's two answers. The cwd is what ties a listener to a lola worktree — the
// only handle lola has, since the port lives inside `composer dev` and not in
// config.
func TestListenersJoinsSocketsWithWorkingDirectories(t *testing.T) {
	f, calls := fakeLsof(t,
		"p63787\ncphp\nn127.0.0.1:8000\np92791\ncnode\nn[::1]:5175\nn*:5176\n",
		"p63787\nn/Users/m/.lola/worktrees/nori-app/lola-nori-app-nor-368\np92791\nn/Volumes/Git/other\n",
		nil)

	got, err := f.Listeners(context.Background())
	if err != nil {
		t.Fatalf("Listeners: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("got %d listeners, want 3: %+v", len(got), got)
	}
	if got[0] != (Listener{PID: 63787, Command: "php", Port: 8000, Addr: "127.0.0.1:8000", Dir: "/Users/m/.lola/worktrees/nori-app/lola-nori-app-nor-368"}) {
		t.Errorf("first listener = %+v", got[0])
	}
	// IPv6 and wildcard addresses carry their port after the LAST colon.
	if got[1].Port != 5175 || got[2].Port != 5176 {
		t.Errorf("ports = %d, %d, want 5175, 5176", got[1].Port, got[2].Port)
	}
	// Both sockets of one process share its cwd.
	if got[1].Dir != "/Volumes/Git/other" || got[2].Dir != "/Volumes/Git/other" {
		t.Errorf("dirs = %q, %q, want the process's cwd on both", got[1].Dir, got[2].Dir)
	}
	// The second pass asks only about the pids the first one found, once each.
	if len(*calls) != 2 {
		t.Fatalf("made %d lsof calls, want 2", len(*calls))
	}
	last := (*calls)[1]
	if last[len(last)-1] != "63787,92791" {
		t.Errorf("cwd pass asked for %q, want the two pids exactly once each", last[len(last)-1])
	}
}

// A cwd pass that fails must not cost the sockets: a listener with no Dir
// matches no worktree, which is the direction that kills nothing.
func TestListenersKeepsSocketsWhenTheDirectoryPassFails(t *testing.T) {
	f, _ := fakeLsof(t, "p1\ncphp\nn127.0.0.1:8000\n", "", errors.New("lsof: boom"))

	got, err := f.Listeners(context.Background())
	if err != nil {
		t.Fatalf("Listeners: %v", err)
	}
	if len(got) != 1 || got[0].Dir != "" {
		t.Fatalf("got %+v, want one listener with an empty Dir", got)
	}
}

// lsof exits 1 with no output when nothing is listening. Nothing found is not
// an error.
func TestListenersOnAnEmptyMachine(t *testing.T) {
	f, calls := fakeLsof(t, "", "", nil)
	got, err := f.Listeners(context.Background())
	if err != nil || got != nil {
		t.Fatalf("Listeners = %v, %v; want nothing and no error", got, err)
	}
	if len(*calls) != 1 {
		t.Errorf("made %d calls, want 1 — no pids means no second pass", len(*calls))
	}
}

// Output lsof would never print must be dropped, never guessed at: the caller's
// next act is killing what this reports.
func TestListenersDropsUnparsableRecords(t *testing.T) {
	f, _ := fakeLsof(t, "pnot-a-pid\ncphp\nn127.0.0.1:8000\np42\ncnode\nnsome-unix-socket\n", "p42\nn/tmp\n", nil)
	got, err := f.Listeners(context.Background())
	if err != nil {
		t.Fatalf("Listeners: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("got %+v, want nothing — neither record carries a usable pid and port", got)
	}
}
