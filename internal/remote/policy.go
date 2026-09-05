package remote

import (
	"strings"

	"github.com/sushidev-team/lola/internal/protocol"
)

// remoteCmdPrefix namespaces the commands this TRANSPORT speaks to itself —
// M1's in-band bearer-key hello, and whatever M2's pairing needs that does not
// warrant a frame type. They are consumed before dispatch, so one arriving mid
// connection is a client bug or a probe; either way it is denied here so that
// it can never be forwarded to d.handle and answered with `unknown cmd`, which
// would be a confusing way to learn the namespace leaks.
const remoteCmdPrefix = "remote."

// deniedCommands are refused for EVERY remote peer, in code and not in
// configuration, whatever any authorizer or any future capability tier says.
// Each entry is a command whose harm does not depend on who is asking:
//
//	stop            severs the caller's own transport and the daemon's.
//	reload          re-reads config and rebinds the listener the caller is
//	                speaking through.
//	renameProject   rewrites project identity; a config operation, and config
//	                is not a phone job.
//	hookEvent       the agent's OWN in-pane callback path, and a remote write
//	                straight into the send-keys safety gate. One forged frame
//	                with event "stop" sets AtPrompt AND AtPromptVerified and
//	                then flushes stashed review findings into a live agent's
//	                pane; one with "session_end" forges an exited axis and
//	                drives the reaction engine. Nothing about this path is
//	                remote by design.
//	pairBegin       enrolment and revocation are LOCAL operations at the
//	pairStatus      machine. A paired phone that could enrol would let a thief
//	pairConfirm     add a device that survives revoking the first; one that
//	devices         could revoke would let a thief cut off the operator's other
//	revokeDevice    devices. Listed now, before the commands exist, because the
//	                cost of listing them early is nothing and the cost of
//	                forgetting one later is the whole threat model.
//
//	regenerateRemoteKey
//	                rolls the shared bearer key, which is the ONLY revocation
//	                M1 has. A phone able to roll it could lock the operator out
//	                of their own daemon while keeping the connection it already
//	                holds — the precise inversion of what revocation is for.
//
// kill is deliberately NOT here: it is reachable, with Force cleared by
// normalizeRequest. Discarding a dirty worktree is the one gate teardown has,
// so the FIELD is denied rather than the command.
var deniedCommands = map[string]bool{
	"stop":                true,
	"reload":              true,
	"renameProject":       true,
	"hookEvent":           true,
	"pairBegin":           true,
	"pairStatus":          true,
	"pairConfirm":         true,
	"devices":             true,
	"revokeDevice":        true,
	"regenerateRemoteKey": true,
}

// CommandDenied reports whether cmd is refused for every remote peer
// unconditionally. The server calls it BEFORE the authorizer, so no
// implementation of Authorizer can grant one of these, and an empty command is
// denied too — a req frame with no Cmd names nothing to authorize.
//
// This is not the capability model. M2 adds a closed allowlist of tiers on top,
// where a command in no tier is refused as well; this list is the floor beneath
// that, and the two are independent on purpose. A command may only become
// remotely reachable by being written into a tier by hand, so adding a case to
// d.handle grants a phone exactly nothing.
func CommandDenied(cmd string) bool {
	if cmd == "" {
		return true
	}
	if strings.HasPrefix(cmd, remoteCmdPrefix) {
		return true
	}
	return deniedCommands[cmd]
}

// mutatingCommands are the commands whose remote use writes an audit line.
// A read is not audited because it would flood a log that is append-only and
// unrotated, and that a remote peer can now drive writes into; everything that
// CHANGES something is audited, and the line never carries the payload.
var mutatingCommands = map[string]bool{
	"answer":          true,
	"resolveConflict": true,
	"kill":            true,
	"revive":          true,
	"review":          true,
	"coderabbit":      true,
	"dev":             true,
	"devFreePort":     true,
	"switchAgent":     true,
	"enable":          true,
	"disable":         true,
	"pollOnce":        true,
	"open":            true,
	"openPr":          true,
	"openManual":      true,
	"openTicket":      true,
	"openURL":         true,
}

// auditable reports whether a remote command's use is worth a log line.
func auditable(cmd string) bool { return mutatingCommands[cmd] }

// normalizeRequest builds the protocol.Request that reaches Handle from the
// frame that arrived.
//
// Two things happen here and both are load-bearing.
//
// Cmd is taken from the ENVELOPE, never from the payload. Authorization reads
// the envelope's Cmd without unmarshalling anything, so a payload naming a
// different command would be authorized as one thing and executed as another —
// the plainest fail-open bug this transport could have.
//
// Force is cleared unconditionally. It is the one field whose misuse discards
// uncommitted work, and no remote caller has any business setting it. This is a
// blocklist of one and it is a STOPGAP: M2 replaces it with a per-command
// rebuild that populates a fresh Request only from that command's own field
// allowlist and discards everything else the phone sent, because the argument
// for stripping Force ("a future handler that forgets to check a capability
// must not be able to discard uncommitted work") argues just as well against a
// future handler that starts reading Ref or DryRun.
func normalizeRequest(cmd string, req protocol.Request) protocol.Request {
	req.Cmd = cmd
	req.Force = false
	return req
}
