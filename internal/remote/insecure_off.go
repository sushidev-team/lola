//go:build !lola_insecure

package remote

import (
	"context"
	"fmt"
)

// This is the ordinary build, and it has no way to authenticate a phone.
//
// M1's only authentication is a shared bearer key read from the environment,
// which is a thing a release binary must not be able to do by accident. So the
// whole path lives behind //go:build lola_insecure and this file provides the
// same two symbols with the only safe answer: no authorizer, therefore no
// listener, and one log line that names both the reason and the way out rather
// than leaving an operator to discover a silently dead port.
//
// M2 deletes insecure.go and replaces newAuthorizer here with the device
// registry and mutual TLS, at which point this file goes away and Listen stops
// being tag-split.

// Listen refuses: this binary carries no way to authenticate a remote peer.
func Listen(_ context.Context, opts Options) (*Server, error) {
	logf := opts.logf()
	// Asked through the same symbol the tagged build asks, so there is exactly
	// one place an authorizer can come from and exactly one answer here.
	_, err := newAuthorizer(logf)
	logf("remote: [remote] is enabled but this build has no phone listener; " +
		"M1 authentication is the insecure bearer-key path and is only compiled with -tags lola_insecure")
	return nil, err
}

// newAuthorizer refuses for the same reason. It exists so Listen — in either
// build — has exactly one symbol to ask for an authorizer.
func newAuthorizer(func(string, ...any)) (Authorizer, error) {
	return nil, fmt.Errorf("%w: build with -tags lola_insecure for the M1 bearer-key path", ErrNoAuthorizer)
}
