//go:build !lola_insecure

package daemon

import (
	"context"
	"strings"
	"testing"

	"github.com/sushidev-team/lola/internal/config"
	"github.com/sushidev-team/lola/internal/protocol"
)

// TestPairBeginRefusesWithoutTheInsecureBuildTag is the untagged half of the
// promise. A connect code is a container for M1's shared bearer key, so a
// release binary must have no way to assemble one — not a way that happens to
// return an empty field.
//
// The refusal is an ERROR rather than a Problem because no configuration change
// produces a code from this binary, and it names the build tag so a reader
// learns the way out where they hit the wall.
func TestPairBeginRefusesWithoutTheInsecureBuildTag(t *testing.T) {
	d := newRemoteTestDaemon(t)
	// Enabled, and with a key in the environment: the refusal is the BUILD's,
	// not the configuration's.
	d.cfg.Remote = config.RemoteConfig{Enabled: true, Bind: "localhost", Port: freePort(t)}
	// Spelled out rather than taken from remote.InsecureKeyEnv, which is itself
	// behind the tag: an untagged build does not have the constant either.
	t.Setenv("LOLA_REMOTE_INSECURE_KEY", "a-key-long-enough-to-be-accepted")

	got, err := d.handlePairBegin(context.Background())
	if err == nil {
		t.Fatal("an untagged build must refuse to hand out a connect code")
	}
	if got.Code != "" || got.Key != "" {
		t.Fatal("a refusal must carry neither a code nor a key")
	}
	if !strings.Contains(err.Error(), "lola_insecure") {
		t.Errorf("the refusal should name the build tag, got %q", err)
	}
}

// TestPairBeginOverTheSocketRefusesRatherThanReturningNothing: the case is
// wired in every build, so an operator on a release binary is told why rather
// than being told the command does not exist.
func TestPairBeginOverTheSocketRefusesRatherThanReturningNothing(t *testing.T) {
	d := newRemoteTestDaemon(t)

	resp := d.handle(context.Background(), protocol.Request{Cmd: "pairBegin"})
	if resp.OK {
		t.Fatal("an untagged build must not answer pairBegin with a code")
	}
	if strings.Contains(resp.Error, "unknown cmd") {
		t.Errorf("the refusal should name the reason, not the missing case: %q", resp.Error)
	}
	if len(resp.Data) != 0 {
		t.Error("a refusal carries no data")
	}
}
