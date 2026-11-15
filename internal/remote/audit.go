package remote

import (
	"encoding/json"
	"strings"
	"unicode"

	"github.com/sushidev-team/lola/internal/protocol"
)

// The audit trail is the only forensic capability this transport has. A req
// frame is audited iff its tier is not read, whether it succeeded, failed or was
// refused for tier or field reasons; reads are not audited because the daemon's
// log is append-only and unrotated and a remote peer can drive writes into it.
//
// A line NEVER carries a payload: answer text may hold a pasted token, openManual
// and openTicket carry prompt seeds, openURL's own handler logs a full URL. Only
// the fields a spec lists as audit fields (bools and enums) are named, and every
// string that reaches the line is clipped and stripped of control characters by
// logSafe, because every one of them is peer-supplied.

// auditRecord is one audit line's content.
type auditRecord struct {
	tier    string
	cmd     string
	session string
	ok      bool
	code    string   // refusal code; "" for none
	fields  []string // "k=v" pairs, already safe
	dropped []string // fields the rebuild removed
}

const auditFormat = "remote: audit device=%s name=%s via=%s tier=%s cmd=%s session=%s ok=%t code=%s fields=%s dropped=%s"

func (c *conn) auditArgs(r auditRecord) []any {
	return []any{logSafe(c.peer.DeviceID), logSafe(c.peer.Label), logSafe(c.peer.Via), logSafe(r.tier), logSafe(r.cmd),
		logSafe(r.session), r.ok, logSafe(r.code), joinOrDash(r.fields), joinOrDash(r.dropped)}
}

// audit writes the line for a req frame of this connection. Every caller is
// bounded by something that costs the peer: a rate bucket that admitted the
// frame, or a strike.
func (c *conn) audit(r auditRecord) {
	if r.tier == protocol.TierRead {
		return
	}
	c.srv.opts.logf()(auditFormat, c.auditArgs(r)...)
}

// auditThrottled writes the line for a refusal that costs the peer nothing
// else (a rate-limit refusal, too many in flight, a refused attachImage part
// that paid no write token), through the device's log bucket. Suppressed lines
// are counted on the next one, so the trail says how much it left out.
func (c *conn) auditThrottled(r auditRecord) {
	if r.tier == protocol.TierRead {
		return
	}
	c.logDevice(auditFormat, c.auditArgs(r)...)
}

// logDevice writes a line this connection's authenticated device can cause at
// will through the device's log bucket, shared by all its connections.
func (c *conn) logDevice(format string, args ...any) {
	ok, n := c.srv.lim.allowLog(c.peer.DeviceID)
	if !ok {
		return
	}
	if n > 0 {
		format += "; %d similar lines suppressed"
		args = append(args, n)
	}
	c.srv.opts.logf()(format, args...)
}

// logPeer writes a line about what the peer sent: through the device's bucket
// once it is authenticated, through the global pre-auth bucket before.
func (c *conn) logPeer(format string, args ...any) {
	if c.peer.DeviceID == "" {
		c.srv.logPreAuth(format, args...)
		return
	}
	c.logDevice(format, args...)
}

// auditPTYWrite is the once-per-(connection, pane) line for raw keystrokes.
func (c *conn) auditPTYWrite(pane string) {
	c.srv.opts.logf()("remote: audit device=%s name=%s via=%s tier=shell pty-write pane=%s",
		logSafe(c.peer.DeviceID), logSafe(c.peer.Label), logSafe(c.peer.Via), logSafe(pane))
}

func joinOrDash(in []string) string {
	if len(in) == 0 {
		return "-"
	}
	out := make([]string, len(in))
	for i, s := range in {
		out[i] = logSafe(s)
	}
	return strings.Join(out, ",")
}

// auditFields renders the audit fields a command's spec names from the REBUILT
// request. Only scalars are rendered; a name with no scalar value (absent, an
// object, an array) is skipped, so nothing structured and nothing long reaches
// the line.
func auditFields(names []string, req protocol.Request) []string {
	if len(names) == 0 {
		return nil
	}
	flatVals := map[string]any{}
	if b, err := json.Marshal(req); err == nil {
		_ = json.Unmarshal(b, &flatVals)
	}
	var argVals map[string]any
	if len(req.Args) > 0 {
		_ = json.Unmarshal(req.Args, &argVals)
	}
	var out []string
	for _, n := range names {
		v, ok := argVals[n]
		if !ok {
			v, ok = flatVals[n]
		}
		if !ok {
			continue
		}
		switch x := v.(type) {
		case bool:
			out = append(out, n+"="+boolWord(x))
		case float64:
			b, _ := json.Marshal(x)
			out = append(out, n+"="+string(b))
		case string:
			out = append(out, n+"="+logSafe(x))
		}
	}
	return out
}

func boolWord(b bool) string {
	if b {
		return "true"
	}
	return "false"
}

// logSafe clips an untrusted string and strips whatever would forge a log line.
// Session ids, command names and device labels all reach the audit line, and
// all of them are peer-supplied; a newline in one of them would write a second
// line that reads as the daemon's own.
func logSafe(s string) string {
	const max = 64
	if s == "" {
		return "-"
	}
	var b strings.Builder
	for i, r := range s {
		if i >= max {
			b.WriteString("...")
			break
		}
		if r == unicode.ReplacementChar || unicode.IsControl(r) || unicode.IsSpace(r) && r != ' ' {
			b.WriteByte('?')
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}
