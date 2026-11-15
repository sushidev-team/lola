package daemon

import (
	"context"

	"github.com/sushidev-team/lola/internal/remote"
)

// listenRemote starts the phone listener. It is the one place the daemon
// constructs one.
//
// Authentication is the device registry and mutual TLS inside remote.Listen
// (Options.Devices, Options.Pairer); nothing here decides who may connect, and
// there is no build tag: every binary carries the same listener.
func (d *Daemon) listenRemote(ctx context.Context, opts remote.Options) (*remote.Server, error) {
	return remote.Listen(ctx, opts)
}
