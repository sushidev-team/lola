//go:build lola_insecure

package daemon

import (
	"context"

	"github.com/sushidev-team/lola/internal/remote"
)

// This is the M1 build, and everything about it is temporary by construction.
//
// There is no cryptography in M1: a phone proves itself with a shared bearer
// key read from LOLA_REMOTE_INSECURE_KEY. "Deleted, not disabled, in M2" is a
// promise enforced by memory, so it is enforced by the compiler instead — this
// file only exists under //go:build lola_insecure, and remotelisten_off.go is
// what every ordinary build gets.
//
// The split is at the CALL SITE rather than only inside internal/remote, whose
// Listen refuses in an untagged build anyway, because the two refusals are not
// the same thing. A release binary built without the tag contains no reachable
// call to remote.Listen at all, so the listener is absent by construction and
// `go tool nm` says so; a runtime error would only be absent by behaviour.
//
// remote.Listen adds the two rails that make this survivable: it forces the
// bind to loopback whatever [remote].bind says, so a shared secret never
// reaches a network interface, and it logs a warning on every accept.

// listenRemote starts the listener through the insecure M1 bearer-key path.
func (d *Daemon) listenRemote(ctx context.Context, opts remote.Options) (*remote.Server, error) {
	return remote.Listen(ctx, opts)
}
