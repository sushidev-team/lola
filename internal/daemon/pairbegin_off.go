//go:build !lola_insecure

package daemon

import (
	"context"
	"errors"
	"os"

	"github.com/sushidev-team/lola/internal/protocol"
)

// This is the ordinary build, and it has no connect code to hand out.
//
// M1's only authentication is a shared bearer key, so a connect code is a
// container for one — which makes assembling a code exactly the kind of thing a
// release binary must not be able to do by accident. The whole handler
// therefore lives behind //go:build lola_insecure and this file provides the
// same symbol with the only safe answer.
//
// The refusal is structural, not conditional: nothing here reads a key,
// composes a code or calls remote.EncodeConnectCode, so an untagged binary
// carries no reachable path to one at all. That is the same argument
// remotelisten_off.go makes about the listener, and it is made the same way so
// the two absences can be checked identically.
//
// It is an ERROR rather than a PairBeginData.Problem because this is not a
// state the operator can fix in the app: no configuration change produces a
// code from this binary. The sentence names the build flag so a reader learns
// the way out in one place, the way listenRemote's refusal does.
//
// IT NAMES THE BINARY THAT ANSWERED, because "this binary" is the one thing the
// reader cannot see. The refusal is a property of how the RUNNING daemon was
// built, and the daemon a machine is running is rarely the one its operator
// last built: a `lola run` resolved from PATH, an app or TUI restart that
// re-execs whatever started it, a stray `go run .` whose executable is a temp
// file under /var/folders. That last one produced this exact message on a
// machine whose PATH binary was correctly tagged — the message was true, and
// unactionable, because nothing said which daemon was speaking.
func (d *Daemon) handlePairBegin(_ context.Context) (protocol.PairBeginData, error) {
	where := ""
	if exe, err := os.Executable(); err == nil && exe != "" {
		where = " The daemon answering is " + exe + "; rebuild and restart it (make daemon-dev)."
	}
	return protocol.PairBeginData{}, errors.New(
		"this binary cannot hand out a connect code: M1 authenticates a phone with the insecure " +
			"LOLA_REMOTE_INSECURE_KEY bearer key, and that path is compiled only with -tags lola_insecure." +
			where)
}
