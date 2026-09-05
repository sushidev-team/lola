//go:build !lola_insecure

package daemon

import (
	"context"
	"errors"
	"net"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/sushidev-team/lola/internal/config"
	"github.com/sushidev-team/lola/internal/panebus"
	"github.com/sushidev-team/lola/internal/session"
)

// TestRemoteListenerIsAbsentWithoutTheInsecureBuildTag is the untagged half of
// M1's compiler-enforced promise. The only authentication M1 has is a shared
// bearer key read from the environment, and a release binary must not be able
// to do that by accident — so an ordinary build binds NOTHING even for an
// operator whose config asks for a listener, and says why in one line naming
// the tag.
//
// The port assertion is the one that matters. "d.remote is nil" would also hold
// for a listener that came up and was forgotten about; a port that is still
// free proves no socket was opened.
func TestRemoteListenerIsAbsentWithoutTheInsecureBuildTag(t *testing.T) {
	d, logBuf := newLoggingRemoteDaemon(t)
	port := freePort(t)
	d.cfg.Remote = config.RemoteConfig{Enabled: true, Bind: "localhost", Port: port}

	// A key in the environment must not change the answer: the refusal is the
	// build's, not the configuration's.
	t.Setenv("LOLA_REMOTE_INSECURE_KEY", "a-key-long-enough-to-be-accepted")

	// A session the identity gate will accept, so the Subscribe below reaches
	// the registry's own closed check rather than stopping at the gate.
	d.sessions.Upsert(session.Session{ID: "lola-web-eng-42", TmuxName: "lola-web-eng-42"})

	var built *panebus.Registry
	d.paneRegistry = func() *panebus.Registry {
		built = panebus.NewFake().Registry()
		return built
	}

	d.startRemote(context.Background())
	t.Cleanup(d.stopRemote)

	if d.remote != nil {
		t.Fatal("an untagged build must not start a phone listener")
	}
	if d.panes != nil {
		t.Error("no listener means no pane registry is retained")
	}

	// Nothing bound the port.
	ln, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
	if err != nil {
		t.Fatalf("port %d is in use, so something listened after all: %v", port, err)
	}
	ln.Close()

	// The registry startRemote built on the way to the refusal is released, not
	// leaked with a tmux attach machine behind it.
	if built != nil {
		if _, err := built.Subscribe(context.Background(), "lola-web-eng-42"); !errors.Is(err, panebus.ErrClosed) {
			t.Errorf("the pane registry must be closed when the listener refuses; Subscribe = %v", err)
		}
	}

	out := logBuf.String()
	for _, want := range []string{"remote: not listening", "lola_insecure"} {
		if !strings.Contains(out, want) {
			t.Errorf("daemon log = %q, want it to contain %q — an operator asking why the phone cannot connect reads this line", out, want)
		}
	}
}

// TestReloadIntoAnEnabledTableStillBindsNothing covers the reload path in an
// untagged build: an operator flipping [remote].enabled at runtime gets the
// same refusal, not a half-started listener.
func TestReloadIntoAnEnabledTableStillBindsNothing(t *testing.T) {
	d, logBuf := newLoggingRemoteDaemon(t)
	d.shutdownCtx = context.Background()
	d.cfg.Remote = config.RemoteConfig{Enabled: true, Bind: "localhost", Port: freePort(t)}
	d.paneRegistry = func() *panebus.Registry { return panebus.NewFake().Registry() }

	done := make(chan struct{})
	go func() { defer close(done); d.reloadRemote() }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("reloadRemote blocked")
	}
	if d.remote != nil {
		t.Fatal("reload must not start a listener in an untagged build")
	}
	if !strings.Contains(logBuf.String(), "lola_insecure") {
		t.Errorf("daemon log = %q, want the refusal to name the build tag", logBuf.String())
	}
}
