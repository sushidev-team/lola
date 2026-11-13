//go:build lola_insecure

package daemon

import (
	"context"

	"github.com/sushidev-team/lola/internal/remote"
)

// cmd=regenerateRemoteKey, the M1 build.
//
// Rolling the shared bearer key is the ONLY revocation milestone 1 has. M2
// brings the real one — per-device identities, each revocable on its own — and
// this command is deleted with the rest of the tag. Until then a key that could
// never be rolled would be shared forever, including with whoever once
// photographed the QR over the operator's shoulder, so the blunt instrument is
// worth having.
//
// It is BLUNT, and the reply says so rather than implying a precision it does
// not have: every phone loses access, because every phone holds the same key.
//
// The command is in internal/remote's deniedCommands, so no phone can ask for
// it. That is not belt-and-braces: a device able to roll the key could lock the
// operator out of their own daemon while keeping the connection it already
// holds, which is the exact inversion of what revocation is for.
//
// TAG-SPLIT for the same reason pairbegin.go is. An ordinary build has no
// bearer key to roll and no file to write, and the absence is structural rather
// than conditional — nothing here is reachable from an untagged binary.
func (d *Daemon) handleRegenerateRemoteKey(ctx context.Context) error {
	if err := remote.RegenerateInsecureKey(d.home, func(f string, a ...any) {
		d.logf("", f, a...)
	}); err != nil {
		return err
	}

	// The new key only reaches the authorizer built at listen time, so the
	// listener is REBUILT here rather than left running with the old one.
	// Without this the reply would be true about the file and false about the
	// daemon — the old key would keep working until something else restarted
	// it, which is the worst possible outcome for a command whose whole purpose
	// is to stop a key working.
	//
	// reloadRemote rather than a hand-rolled stop/start: it already sequences
	// the two correctly and already falls back to context.Background for a
	// Daemon that never ran Run, which is every test.
	d.reloadRemote()
	return nil
}
