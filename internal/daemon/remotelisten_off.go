//go:build !lola_insecure

package daemon

import (
	"context"
	"fmt"

	"github.com/sushidev-team/lola/internal/remote"
)

// This is the ordinary build, and it has no way to authenticate a phone.
//
// M1's only authentication is a shared bearer key read from the environment,
// which is a thing a release binary must not be able to do by accident. So the
// whole path lives behind //go:build lola_insecure and this file provides the
// same symbol with the only safe answer: no listener, and one log line naming
// both the reason and the way out rather than leaving an operator to discover
// a silently dead port.
//
// The refusal is deliberately structural. Nothing here calls remote.Listen, so
// an untagged binary carries no reachable path to a listener at all — not a
// path that happens to return an error.
//
// M2 replaces the bearer key with the device registry and mutual TLS, at which
// point both files go away and startRemote calls remote.Listen directly.

// listenRemote refuses: this binary carries no way to authenticate a remote
// peer. The error is the whole log line startRemote writes, so an operator who
// enabled [remote] and is asking why the phone cannot connect reads the reason
// and the fix in one place.
func (d *Daemon) listenRemote(_ context.Context, _ remote.Options) (*remote.Server, error) {
	return nil, fmt.Errorf("[remote] is enabled but this binary has no phone listener: " +
		"M1 authentication is the insecure LOLA_REMOTE_INSECURE_KEY bearer-key path, " +
		"compiled only with -tags lola_insecure")
}
