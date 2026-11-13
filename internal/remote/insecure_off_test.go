//go:build !lola_insecure

package remote

import (
	"context"
	"errors"
	"net"
	"strconv"
	"strings"
	"testing"
)

// This file is the untagged half of the M1 promise: a release binary cannot
// contain the bearer-key path, so it cannot listen at all. It is a separate
// file rather than a branch inside a shared test because the assertion is about
// what this BUILD contains, and a branch would pass either way.

// TestListenRefusesWithoutTheInsecureBuildTag. The refusal has to be an error
// AND a log line: a silently dead port is the failure mode this whole exercise
// exists to avoid rediscovering.
func TestListenRefusesWithoutTheInsecureBuildTag(t *testing.T) {
	port := freePort(t)
	var lines []string
	s, err := Listen(context.Background(), Options{
		Bind:   "localhost",
		Port:   port,
		Dir:    t.TempDir(),
		Handle: (&stubHandler{}).handle,
		Logf:   func(f string, a ...any) { lines = append(lines, fmtSprintf(f, a...)) },
	})
	if err == nil {
		s.Close()
		t.Fatal("Listen started a listener in a build with no way to authenticate a peer")
	}
	if !errors.Is(err, ErrNoAuthorizer) {
		t.Fatalf("got %v, want ErrNoAuthorizer", err)
	}
	if s != nil {
		t.Fatal("Listen returned a server alongside its error")
	}

	log := strings.Join(lines, "\n")
	if !strings.Contains(log, "lola_insecure") {
		t.Errorf("the refusal does not name the way out:\n%s", log)
	}

	// Nothing was bound: the port is still free.
	ln, lerr := net.Listen("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
	if lerr != nil {
		t.Fatalf("port %d is still held after a refused Listen: %v", port, lerr)
	}
	ln.Close()
}

// TestNewAuthorizerRefusesWithoutTheTag pins the single symbol the split turns
// on. Both builds provide it; only one can answer.
func TestNewAuthorizerRefusesWithoutTheTag(t *testing.T) {
	// The environment variable is deliberately set: even with a perfectly good
	// key present, this build must not grow an authenticator.
	t.Setenv("LOLA_REMOTE_INSECURE_KEY", strings.Repeat("k", 32))

	a, err := newAuthorizer(func(string, ...any) {})
	if err == nil {
		t.Fatal("newAuthorizer produced an authorizer in an untagged build")
	}
	if a != nil {
		t.Fatal("newAuthorizer returned an authorizer alongside its error")
	}
	if !errors.Is(err, ErrNoAuthorizer) {
		t.Fatalf("got %v, want ErrNoAuthorizer", err)
	}
}

// TestNoInsecureKeyIsEverRead is the property the build tag buys, stated as a
// test: this build never consults the environment, so an operator who left the
// M1 variable exported on their machine does not accidentally open a listener
// after upgrading.
func TestNoInsecureKeyIsEverRead(t *testing.T) {
	t.Setenv("LOLA_REMOTE_INSECURE_KEY", "a-perfectly-good-32-character-key")
	s, err := Listen(context.Background(), Options{
		Bind: "localhost", Port: freePort(t), Dir: t.TempDir(),
		Handle: (&stubHandler{}).handle,
	})
	if err == nil {
		s.Close()
		t.Fatal("Listen succeeded with the M1 environment key set; the tag is not gating the path")
	}
	if !errors.Is(err, ErrNoAuthorizer) {
		t.Fatalf("got %v, want ErrNoAuthorizer", err)
	}
}
