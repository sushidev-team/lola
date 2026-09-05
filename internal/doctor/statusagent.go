package doctor

// statusagent.go carries ONE health fact across a process boundary: the
// daemon's [statusagent] circuit breaker.
//
// Every other check in this package probes something the checking process can
// see for itself — a binary on PATH, a socket, the config file. The status
// interpreter's breaker cannot be: it is per-daemon-PROCESS runtime state
// (internal/daemon/statusagentwire.go) deliberately kept OUT of config.toml,
// because a restart must re-arm it and lola must never disable a feature in
// the user's config on their behalf. Meanwhile `lola doctor`, the TUI overlay
// and the app's doctor pane all run outside the daemon. Without a channel, a
// tripped breaker would be exactly as silent as the failures it exists to
// announce — which is the whole bug being fixed.
//
// So the daemon leaves a breadcrumb and this package reads it. Three rules
// keep that honest:
//
//   - The file is a one-way REPORT, never the state of record. The daemon
//     never reads it back to decide anything; the breaker itself lives in
//     memory. A lost, stale or hand-deleted file can therefore cost a warning
//     and nothing else — it can never change what the interpreter does.
//   - It is stamped with the writing daemon's pid and ignored once that
//     process is gone, so a daemon that died while tripped cannot make the
//     next one look broken. The daemon also clears it whenever the interpreter
//     is (re)armed, which covers the ordinary restart/reload path; the pid
//     check only covers the crash.
//   - Reason is rendered agent/CLI output, i.e. untrusted. The WRITER
//     sanitizes and clips it before it ever reaches this file, and the only
//     thing this package does with it is put it in a Result.Detail — never in
//     argv, never back to an agent.

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"time"

	"github.com/sushidev-team/lola/internal/config"
)

// breakerFile is the breadcrumb's name under ~/.lola/cache/ (the same runtime
// scratch directory the review passes and the Linear metadata caches use — it
// is derived state, safe to delete at any time).
const breakerFile = "statusagent-breaker.json"

// StatusAgentBreaker is the report a daemon writes when its status
// interpreter's circuit breaker trips. The format lives here rather than in
// internal/daemon because this package is its only READER; the daemon owns the
// policy, this package owns the wire shape.
type StatusAgentBreaker struct {
	// PID is the daemon that wrote the file, stamped by ReportStatusAgentBreaker.
	PID int `json:"pid"`
	// Failures is the consecutive-failure count at the moment of the trip.
	Failures int `json:"failures"`
	// Reason is the last failure, already sanitized and clipped by the writer.
	Reason string `json:"reason"`
	// TrippedAt is when the breaker opened; RetryAt is when it will spend one
	// half-open probe. Both are informational — the daemon re-derives them.
	TrippedAt time.Time `json:"tripped_at"`
	RetryAt   time.Time `json:"retry_at"`
}

// processAlive is a seam so the stale-breadcrumb path is testable without
// hunting for a pid that is reliably dead.
var processAlive = pidAlive

// breakerPath resolves the breadcrumb under $LOLA_HOME. Both directions go
// through it so the writer and the reader can never drift apart.
func breakerPath() (string, error) {
	home, err := config.Home()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, "cache", breakerFile), nil
}

// ReportStatusAgentBreaker records a TRIPPED breaker for the health checks to
// find. The daemon calls it on the trip and on every re-trip (the reason and
// the retry time move on). It stamps the caller's own pid, so the daemon never
// has to think about liveness, and writes atomically (temp+rename, 0600) like
// everything else lola puts on disk.
func ReportStatusAgentBreaker(b StatusAgentBreaker) error {
	path, err := breakerPath()
	if err != nil {
		return err
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	b.PID = os.Getpid()
	data, err := json.Marshal(b)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, breakerFile+".tmp*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer os.Remove(name) // no-op once the rename succeeded
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(name, 0o600); err != nil {
		return err
	}
	return os.Rename(name, path)
}

// ClearStatusAgentBreaker removes the breadcrumb. The daemon calls it whenever
// the interpreter is (re)armed and whenever an interpretation succeeds. An
// absent file is success: the report is derived state, so "already gone" is
// exactly the outcome asked for.
func ClearStatusAgentBreaker() error {
	path, err := breakerPath()
	if err != nil {
		return err
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

// StatusAgentBreakerReport returns the breadcrumb iff it describes a breaker
// that is tripped RIGHT NOW in a daemon that is still running. Every failure
// mode — no home, no file, unreadable, unparsable, no trip time, dead writer —
// reports nothing, because this may only ever ADD a warning that a daemon put
// there on purpose. Exported to complete the Report/Clear/read trio, so a
// caller can ask the question without knowing where the file lives.
func StatusAgentBreakerReport() (StatusAgentBreaker, bool) {
	path, err := breakerPath()
	if err != nil {
		return StatusAgentBreaker{}, false
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return StatusAgentBreaker{}, false
	}
	var b StatusAgentBreaker
	if err := json.Unmarshal(data, &b); err != nil {
		return StatusAgentBreaker{}, false
	}
	if b.TrippedAt.IsZero() || !processAlive(b.PID) {
		return StatusAgentBreaker{}, false
	}
	return b, true
}

// statusAgentResult reports a TRIPPED status interpreter — and nothing at all
// otherwise, following repairResult's shape: a healthy machine adds no row.
// An OK row here would be noise on the overwhelming majority of installs,
// where [statusagent] is simply off (it defaults to disabled).
//
// NOT critical: the interpreter is a display overlay, so a tripped breaker
// costs a nicety, never a spawn, a PR or a reaction. The detail says so, and
// says how to re-arm it, because the operator's only other option is to guess.
func statusAgentResult() (Result, bool) {
	b, ok := StatusAgentBreakerReport()
	if !ok {
		return Result{}, false
	}
	reason := b.Reason
	if reason == "" {
		reason = "no reason recorded"
	}
	detail := fmt.Sprintf("disabled after %d consecutive failures: %s", b.Failures, reason)
	if !b.RetryAt.IsZero() {
		detail += " — retrying once at " + b.RetryAt.Local().Format("15:04")
	}
	detail += " (display-only, so nothing else is affected; restarting the daemon re-arms it)"
	return Result{Name: checkStatusAgent, OK: false, Critical: false, Detail: detail}, true
}

// pidAlive reports whether pid still names a running process. Signal 0
// delivers nothing and only probes: a dead pid answers os.ErrProcessDone,
// while a live one owned by another user answers EPERM. Anything that is not a
// definite "gone" therefore counts as ALIVE — an ambiguous answer must never
// hide a real tripped breaker, which is the failure this whole file exists to
// stop being silent about.
func pidAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	p, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	return !errors.Is(p.Signal(syscall.Signal(0)), os.ErrProcessDone)
}
