package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/sushidev-team/lola/hubproto"
	"github.com/sushidev-team/lola/internal/board"
	"github.com/sushidev-team/lola/internal/claimaudit"
	"github.com/sushidev-team/lola/internal/protocol"
	"github.com/sushidev-team/lola/internal/session"
)

// Agent self-reports (`lola report …` → cmd=agentReport).
//
// Reports are DISPLAY ONLY: this handler writes Session.Board, or Session.Tasks
// for the shared-task verbs. Neither can drive lifecycle. A report touches
// no axis, no freshness stamp, no AtPrompt gate and no guard — it is the agent's
// claim about itself, and its context (PR diff, CI logs, issue text) is
// attacker-influenceable. It is not even activity evidence: hooks and the pane
// own that, and a report loop must never be able to keep a stuck session looking
// alive.

// reportBurst / reportWindow bound how often one session may report. A real
// agent reports a handful of times per task step; the cap only exists so a
// looping agent cannot turn the socket into a store-rewrite treadmill.
const (
	reportBurst  = 60
	reportWindow = time.Minute
)

// reportLimiter is a per-session sliding-window counter.
type reportLimiter struct {
	mu   sync.Mutex
	hits map[string][]time.Time
}

// reportSweepAt is the map size past which allow prunes idle sessions.
const reportSweepAt = 256

func (l *reportLimiter) allow(id string, now time.Time) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.hits == nil {
		l.hits = map[string][]time.Time{}
	}
	cut := now.Add(-reportWindow)
	// Bound the map: drop every session whose window has emptied once it grows,
	// so ids from finished sessions do not accumulate for the daemon's lifetime.
	if len(l.hits) > reportSweepAt {
		for k, v := range l.hits {
			if len(v) == 0 || !v[len(v)-1].After(cut) {
				delete(l.hits, k)
			}
		}
	}
	h := l.hits[id]
	i := 0
	for i < len(h) && h[i].Before(cut) {
		i++
	}
	h = h[i:]
	if len(h) >= reportBurst {
		l.hits[id] = h
		return false
	}
	l.hits[id] = append(h, now)
	return true
}

// errReportRateLimited is returned (not logged per hit) when a session exceeds
// reportBurst within reportWindow.
var errReportRateLimited = errors.New("agentReport: rate limited — report on meaningful changes only")

// handleAgentReport applies one report to the session's board. A malformed
// report is answered with board's usage error so the agent can correct itself;
// an unknown session is an error too (unlike hookEvent, nothing here sits on the
// agent's critical path — the CLI decides what to do with the answer).
func (d *Daemon) handleAgentReport(req protocol.Request) protocol.Response {
	var a protocol.ReportArgs
	if len(req.Args) > 0 {
		if err := json.Unmarshal(req.Args, &a); err != nil {
			return protocol.Response{OK: false, Error: "agentReport: bad args: " + err.Error()}
		}
	}
	if len(a.Argv) > 0 && a.Argv[0] == "task" {
		a.Argv = a.Argv[1:]
		req.Args, _ = json.Marshal(a)
		return d.handleSessionTask(context.Background(), req, "agent")
	}
	if len(a.Argv) > 0 && strings.EqualFold(a.Argv[0], "ports") {
		return d.handleReportPorts(req, a.Argv[1:])
	}
	if len(a.Argv) > 0 && strings.EqualFold(a.Argv[0], "nofix") {
		return d.handleReportNoFix(req)
	}
	if req.Session == "" {
		return protocol.Response{OK: false, Error: "agentReport: no session ($LOLA_SESSION unset)"}
	}
	// Unknown sessions are refused BEFORE the limiter, so an arbitrary id never
	// gets a limiter entry.
	if _, ok := d.sessions.Get(req.Session); !ok {
		return protocol.Response{OK: false, Error: "agentReport: unknown session " + req.Session}
	}
	now := time.Now()
	if !d.reports.allow(req.Session, now) {
		return protocol.Response{OK: false, Error: errReportRateLimited.Error()}
	}
	var applyErr error
	_, known := d.sessions.Update(req.Session, func(sess *session.Session) bool {
		nb, err := board.Apply(sess.Board, a.Argv, now)
		if err != nil {
			applyErr = err
			return false
		}
		sess.Board = nb
		return true
	})
	if !known {
		return protocol.Response{OK: false, Error: "agentReport: unknown session " + req.Session}
	}
	if applyErr != nil {
		return protocol.Response{OK: false, Error: applyErr.Error()}
	}
	if err := d.sessions.Save(); err != nil {
		d.logf("", "agentReport: persist sessions: %v", err)
	}
	return protocol.Response{OK: true}
}

// boardInfo flattens a session's board for the wire, nil when nothing was
// reported. audit is the board's claim audit (claimaudit.go), carried beside
// the claims it judges — display-only, like everything else here.
func boardInfo(b board.Board, audit claimaudit.Result, now time.Time) *protocol.BoardInfo {
	if b.Empty() {
		return nil
	}
	pct, derived, ok := b.Percent()
	bi := &protocol.BoardInfo{
		Phase:           string(b.Phase),
		HasProgress:     ok,
		Percent:         pct,
		ProgressDerived: derived,
		ProgressLabel:   b.ProgressLabel,
		Done:            b.Done(),
		Total:           b.Total(),
		Current:         b.Current(),
		Blocked:         b.Blocked,
		BlockedAt:       b.BlockedAt,
		Note:            b.Note,
		UpdatedAt:       b.UpdatedAt,
	}
	if !b.UpdatedAt.IsZero() {
		bi.UpdatedAgo = formatAgo(now.Sub(b.UpdatedAt))
	}
	for _, t := range b.Todos {
		bi.Todos = append(bi.Todos, protocol.BoardTodo{Text: t.Text, State: string(t.State)})
	}
	for _, c := range b.Checks {
		bc := protocol.BoardCheck{Name: c.Name, State: string(c.State), Summary: c.Summary}
		if fd, ok := audit.Checks[c.Name]; ok {
			bc.Evidence, bc.EvidenceNote = string(fd.Verdict), fd.Note
		}
		bi.Checks = append(bi.Checks, bc)
	}
	bi.Mismatches = audit.Warnings
	return bi
}

// handleReportNoFix is `lola report nofix`: the agent of a hub dep_advisory
// session found no published fixed version. It only flags the session's open
// hub work; hubOutcomeFor turns that into a no_fix_available fail, which gives
// the item back to the hub. A claim that can only return work needs no proof.
func (d *Daemon) handleReportNoFix(req protocol.Request) protocol.Response {
	if req.Session == "" {
		return protocol.Response{OK: false, Error: "agentReport: no session ($LOLA_SESSION unset)"}
	}
	found := false
	_, known := d.sessions.Update(req.Session, func(sess *session.Session) bool {
		for _, w := range sess.HubWorks() {
			if w.Open() && w.Kind == string(hubproto.KindDepAdvisory) && !w.NoFix {
				w.NoFix, found = true, true
			}
		}
		return found
	})
	if !known {
		return protocol.Response{OK: false, Error: "agentReport: unknown session " + req.Session}
	}
	if !found {
		return protocol.Response{OK: false, Error: "agentReport: nofix: this session holds no open hub advisory"}
	}
	if err := d.sessions.Save(); err != nil {
		d.logf("", "agentReport: persist sessions: %v", err)
	}
	return protocol.Response{OK: true}
}

// maxPortAskBytes bounds the reason an agent gives for a port-setup request.
const maxPortAskBytes = 300

// portSetupAsk is what a client may show: the agent's reason, only while the
// project still runs without leased ports.
func portSetupAsk(ask string, parallel bool) string {
	if parallel {
		return ""
	}
	return ask
}

// handleReportPorts is `lola report ports "<why>"`: the agent says its dev
// server cannot run beside another one (a fixed port) and asks the human to set
// up dynamic ports. It only records a line the UI shows next to the dev toggle;
// nothing is written to config, and the human reviews the change themselves.
func (d *Daemon) handleReportPorts(req protocol.Request, words []string) protocol.Response {
	if req.Session == "" {
		return protocol.Response{OK: false, Error: "agentReport: no session ($LOLA_SESSION unset)"}
	}
	why := strings.Join(strings.Fields(sanitizeAgentText(strings.Join(words, " "))), " ")
	if why == "" {
		return protocol.Response{OK: false, Error: "agentReport: ports: say why dynamic ports are needed"}
	}
	if len(why) > maxPortAskBytes {
		why = strings.ToValidUTF8(why[:maxPortAskBytes], "")
	}
	if !d.reports.allow(req.Session, time.Now()) {
		return protocol.Response{OK: false, Error: errReportRateLimited.Error()}
	}
	_, known := d.sessions.Update(req.Session, func(sess *session.Session) bool {
		sess.PortSetupAsk = why
		return true
	})
	if !known {
		return protocol.Response{OK: false, Error: "agentReport: unknown session " + req.Session}
	}
	if err := d.sessions.Save(); err != nil {
		d.logf("", "agentReport: persist sessions: %v", err)
	}
	return protocol.Response{OK: true}
}
