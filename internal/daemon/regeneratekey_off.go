//go:build !lola_insecure

package daemon

import (
	"context"
	"errors"
)

// This is the ordinary build, which has no shared bearer key to roll.
//
// M1's key exists only under //go:build lola_insecure, so there is nothing here
// to regenerate and nothing on disk this binary wrote. The refusal is
// structural for the same reason pairbegin_off.go's is: no code path in an
// untagged binary reads, writes or replaces a key, so the capability is absent
// by construction rather than by a runtime check that could be got past.
//
// It is an ERROR rather than a rendered problem string because no configuration
// change makes this binary able to do it — the same distinction pairbegin_off.go
// draws, and the sentence names the build flag so a reader learns the way out
// in one place.
func (d *Daemon) handleRegenerateRemoteKey(_ context.Context) error {
	return errors.New(
		"this binary has no phone bearer key to regenerate: M1's shared key is compiled only " +
			"with -tags lola_insecure, and milestone 2 replaces it with per-device revocation")
}
