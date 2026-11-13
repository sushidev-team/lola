package usage

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

// opencode keeps its sessions in a SQLite database
// ($XDG_DATA_HOME/opencode/opencode.db, else ~/.local/share/opencode/), one
// row per message with its token counts, its cost as opencode itself priced
// it, and — on the session row — the directory it ran in. That directory is
// how a message is attributed to a lola worktree, exactly like codex's cwd.
//
// Go's stdlib has no SQLite and lola adds no dependency for it, so the
// database is read through the `sqlite3` CLI (macOS ships /usr/bin/sqlite3)
// in -readonly mode, bounded by opencodeQueryTimeout. A machine without the
// CLI simply reports no opencode figure (fail open: absent, not zero).
//
// The query selects only sessions whose directory lies under lola's
// worktrees, so a user's own opencode work is never read, and is incremental:
// each pass asks only for messages updated since the last one (minus a
// margin), and a message is REPLACED by id, so a message still streaming its
// counts is re-read without being double-counted. 4k messages, 0.1s.
//
// Only numbers leave the query — json_extract in SQL, never the message text.

const (
	opencodeQueryTimeout = 10 * time.Second
	// opencodeMargin re-reads a little history each pass, so a message
	// updated while the previous query ran is not missed.
	opencodeMargin = 2 * time.Minute
)

// OpencodeDB is opencode's database path; "" when no home dir resolves.
func OpencodeDB() string {
	if d := os.Getenv("XDG_DATA_HOME"); d != "" {
		return filepath.Join(d, "opencode", "opencode.db")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".local", "share", "opencode", "opencode.db")
}

// OpencodeScanner sums opencode's messages per worktree. Safe for concurrent
// use.
type OpencodeScanner struct {
	mu     sync.Mutex
	prefix string
	// RunSQL is the exec seam: it runs query against db and returns the
	// sqlite3 CLI's -json output.
	RunSQL    func(ctx context.Context, db, query string) ([]byte, error)
	watermark int64 // ms: the newest time_updated seen
	byDir     map[string]map[string]ocMsg
}

type ocMsg struct {
	at time.Time
	t  Totals
}

// NewOpencodeScanner returns a scanner for messages run under prefix
// (normally ~/.lola/worktrees).
func NewOpencodeScanner(prefix string) *OpencodeScanner {
	return &OpencodeScanner{prefix: filepath.Clean(prefix), RunSQL: runSQLite, byDir: map[string]map[string]ocMsg{}}
}

func runSQLite(ctx context.Context, db, query string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, opencodeQueryTimeout)
	defer cancel()
	// The query goes on STDIN, not argv: it is not secret, but it is long,
	// and argv is visible to every `ps`.
	cmd := exec.CommandContext(ctx, "sqlite3", "-readonly", "-json", db)
	cmd.Stdin = strings.NewReader(query)
	return cmd.Output()
}

// sqlString quotes s as a SQL string literal.
func sqlString(s string) string { return "'" + strings.ReplaceAll(s, "'", "''") + "'" }

// Scan reads the messages updated since the last scan. A missing database is
// not an error (opencode was never run); a failing query is, and changes
// nothing.
func (o *OpencodeScanner) Scan(ctx context.Context, db string, now time.Time) error {
	if db == "" || o.prefix == "" || o.prefix == "." {
		return nil
	}
	if _, err := os.Stat(db); err != nil {
		return nil
	}
	o.mu.Lock()
	since := max(o.watermark-opencodeMargin.Milliseconds(), 0)
	o.mu.Unlock()
	cutoff := now.AddDate(0, 0, -(KeepDays + 1)).UnixMilli()
	dir := o.prefix + string(filepath.Separator)
	query := fmt.Sprintf(`SELECT m.id AS id, s.directory AS dir, m.time_created AS created, m.time_updated AS updated,
 coalesce(json_extract(m.data,'$.tokens.input'),0) AS i, coalesce(json_extract(m.data,'$.tokens.output'),0) AS o,
 coalesce(json_extract(m.data,'$.tokens.reasoning'),0) AS r, coalesce(json_extract(m.data,'$.tokens.cache.read'),0) AS cr,
 coalesce(json_extract(m.data,'$.tokens.cache.write'),0) AS cw, coalesce(json_extract(m.data,'$.cost'),0) AS cost
FROM message m JOIN session s ON s.id = m.session_id
WHERE m.session_id IN (SELECT id FROM session WHERE substr(directory,1,%d) = %s AND time_updated > %d)
 AND m.time_updated > %d AND m.time_created >= %d AND json_extract(m.data,'$.role') = 'assistant';
`, utf8.RuneCountInString(dir), sqlString(dir), since, since, cutoff)
	out, err := o.RunSQL(ctx, db, query)
	if err != nil {
		return fmt.Errorf("opencode db: %w", err)
	}
	var rows []struct {
		ID      string  `json:"id"`
		Dir     string  `json:"dir"`
		Created int64   `json:"created"`
		Updated int64   `json:"updated"`
		In      int64   `json:"i"`
		Out     int64   `json:"o"`
		Reason  int64   `json:"r"`
		Read    int64   `json:"cr"`
		Write   int64   `json:"cw"`
		Cost    float64 `json:"cost"`
	}
	if len(strings.TrimSpace(string(out))) > 0 { // sqlite3 prints nothing for no rows
		if err := json.Unmarshal(out, &rows); err != nil {
			return fmt.Errorf("opencode db: %w", err)
		}
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	for _, r := range rows {
		d := filepath.Clean(r.Dir)
		if !within(d, o.prefix) {
			continue
		}
		m := o.byDir[d]
		if m == nil {
			m = map[string]ocMsg{}
			o.byDir[d] = m
		}
		// input EXCLUDES the cache; reasoning is reported beside output.
		m[r.ID] = ocMsg{at: time.UnixMilli(r.Created), t: Totals{
			Input: max(r.In, 0), Output: max(r.Out, 0) + max(r.Reason, 0),
			CacheRead: max(r.Read, 0), CacheWrite: max(r.Write, 0), CostUSD: max(r.Cost, 0)}}
		o.watermark = max(o.watermark, r.Updated)
	}
	// Forget what fell out of the ledger's window.
	old := time.UnixMilli(cutoff)
	for d, m := range o.byDir {
		for id, msg := range m {
			if msg.at.Before(old) {
				delete(m, id)
			}
		}
		if len(m) == 0 {
			delete(o.byDir, d)
		}
	}
	return nil
}

// For sums, per local day, every message run in dir or below it; each day's
// Slots counts its distinct active windows.
func (o *OpencodeScanner) For(dir string) map[string]Totals {
	o.mu.Lock()
	defer o.mu.Unlock()
	if dir == "" {
		return nil
	}
	dir = filepath.Clean(dir)
	var out map[string]Totals
	slots := map[int64]bool{}
	for d, m := range o.byDir {
		if !within(d, dir) {
			continue
		}
		if out == nil {
			out = map[string]Totals{}
		}
		for _, msg := range m {
			t := msg.t
			if slot := msg.at.Unix() / int64(SlotDuration/time.Second); !slots[slot] {
				slots[slot] = true
				t.Slots = 1
			}
			day := msg.at.Local().Format(DayFormat)
			cur := out[day]
			cur.Add(t)
			out[day] = cur
		}
	}
	return out
}

// Recent sums dir's usage in the windows ending after since (see
// Scanner.Recent).
func (o *OpencodeScanner) Recent(dir string, since time.Time) Totals {
	o.mu.Lock()
	defer o.mu.Unlock()
	var t Totals
	dir = filepath.Clean(dir)
	from := since.Unix() / int64(SlotDuration/time.Second)
	for d, m := range o.byDir {
		if !within(d, dir) {
			continue
		}
		for _, msg := range m {
			if msg.at.Unix()/int64(SlotDuration/time.Second) >= from {
				t.Add(msg.t)
			}
		}
	}
	return t
}
