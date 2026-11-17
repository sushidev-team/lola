package daemon

import (
	"context"

	"github.com/sushidev-team/lola/internal/protocol"
)

// errWatchNeedsStream answers watch/unwatch on the unix socket. Streams exist
// only on a remote connection, which consumes both commands before Handle, so
// a request that reaches the daemon here came from a one-shot socket client.
const errWatchNeedsStream = "watch needs a stream connection"

// handlePush routes the push-side commands: watch/unwatch (refused here) and
// the two attachment commands. ok is false for every other command, which
// handle then dispatches as usual. Contract: docs/mobile/02-events.md §10.4, §12.
func (d *Daemon) handlePush(ctx context.Context, req protocol.Request) (protocol.Response, bool) {
	switch req.Cmd {
	case protocol.CmdWatch, protocol.CmdUnwatch:
		return protocol.Response{OK: false, Error: errWatchNeedsStream}, true
	case protocol.CmdAttachImage:
		var a protocol.AttachImageArgs
		if err := decodeArgs(req.Args, &a); err != nil {
			return protocol.Response{OK: false, Error: "bad request"}, true
		}
		data, err := d.handleAttachImage(ctx, a)
		if err != nil {
			return protocol.Response{OK: false, Error: err.Error()}, true
		}
		return dataResponse(data), true
	case protocol.CmdAttachmentImage:
		var a protocol.AttachmentImageArgs
		if err := decodeArgs(req.Args, &a); err != nil {
			return protocol.Response{OK: false, Error: "bad request"}, true
		}
		data, err := d.handleAttachmentImage(ctx, a)
		if err != nil {
			return protocol.Response{OK: false, Error: err.Error()}, true
		}
		return dataResponse(data), true
	}
	return protocol.Response{}, false
}
