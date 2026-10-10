package daemon

import (
	"github.com/sushidev-team/lola/internal/agent"
	"github.com/sushidev-team/lola/internal/board"
	"github.com/sushidev-team/lola/internal/claimaudit"
	"github.com/sushidev-team/lola/internal/session"
	"github.com/sushidev-team/lola/internal/state"
)

// Claim audits: the agent's self-reported checks (`lola report check tests
// pass`) compared with what its own transcript shows it ran, and with the PR's
// CI rollup. See internal/claimaudit.
//
// DISPLAY-ONLY, exactly like the board it audits: the result rides
// SessionInfo.board (boardInfo is its one reader) and nothing in the control
// loop — axes, slot counting, reactions, write-back, send-keys gates, dispatch —
// may read it. A mismatch is something for a human to look at, not a fact any
// automation can act on: the transcript is model output, and the classifier is
// a heuristic over command lines.

// claimEvidence is the incremental transcript scanner. A package-level pure
// cache for the same reasons as observer.go's `transcripts`: keyed by absolute
// path, holding no config and no lifecycle, its entries re-validated against
// the file's identity on every Scan.
var claimEvidence = claimaudit.NewScanner()

// scanClaimEvidence advances the transcript scan of every session whose board
// makes an auditable claim. Once per observe cycle, read-only, and bounded per
// file (claimaudit's maxScanBytes) — only APPENDED bytes are read after the
// first pass. Gated to claude like agentlog's read: only Claude Code writes a
// transcript, and Session.TranscriptPath comes from its hook payloads alone.
func scanClaimEvidence(snap []session.Session) {
	keep := map[string]bool{}
	for _, s := range snap {
		if s.TranscriptPath == "" || agent.Parse(s.Agent) != agent.Claude {
			continue
		}
		keep[s.TranscriptPath] = true
		if auditable(s.Board) {
			claimEvidence.Scan(s.TranscriptPath)
		}
	}
	claimEvidence.Retain(keep)
}

// auditable reports whether a board carries a pass claim the scan could judge.
func auditable(b board.Board) bool {
	for _, c := range b.Checks {
		if c.State == board.CheckPass && claimaudit.CategoryForCheck(c.Name) != "" {
			return true
		}
	}
	return false
}

// auditClaims judges s's board against what is ALREADY known: the scan cache
// (no I/O here — sessionsData serves a cache and must never touch a file) and
// the delivery axis's CI fact.
func auditClaims(s session.Session) claimaudit.Result {
	f := claimaudit.Facts{CIFailed: s.Delivery == state.DeliveryCIFailed}
	if s.TranscriptPath != "" && agent.Parse(s.Agent) == agent.Claude {
		if l, ok := claimEvidence.Ledger(s.TranscriptPath); ok {
			f.Ledger = &l
		}
	}
	return claimaudit.Audit(s.Board, f)
}
