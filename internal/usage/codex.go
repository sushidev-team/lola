package usage

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Codex keeps its session logs by DATE, not by working directory
// (~/.codex/sessions/YYYY/MM/DD/rollout-*.jsonl), so the claude trick — one
// directory per worktree — does not apply. Instead each log's first line
// (`session_meta`) names the cwd it ran in, and CodexScanner attributes the
// log to whichever worktree that cwd lies in. That covers a codex worker and
// a codex-session review pass alike: both run in the session's worktree.
//
// Usage comes from `token_count` events, whose `total_token_usage` is
// CUMULATIVE per log; each event contributes its DELTA over the previous one,
// so a re-emitted event adds nothing and nothing is double-counted.
//
// codex logs carry no price lola trusts (the model ids are not in the price
// table), so CostUSD stays 0: codex usage is tokens only, and is ranked
// against other codex sessions by Weighted tokens (TokenScale).
//
// Only logs whose cwd lies under the scanner's prefix (lola's worktrees) are
// read past their first line — a user's own codex work is none of lola's
// business and can be hundreds of megabytes.

// codexKeepDirs bounds how many day directories a scan walks: the ledger's
// history plus a day of slack for a log started before midnight.
const codexKeepDirs = KeepDays + 1

// maxCodexLine bounds one log line; session_meta carries the base
// instructions and can be large, but never this large.
const maxCodexLine = 4 << 20

// CodexScanner sums codex session logs per worktree, incrementally. Safe for
// concurrent use.
type CodexScanner struct {
	mu     sync.Mutex
	prefix string
	files  map[string]*codexFile
}

type codexFile struct {
	size, offset int64
	metaRead     bool
	cwd          string // "" = not lola's: never read further
	last         Totals // the last cumulative total_token_usage
	days         map[string]Totals
	slots        map[int64]Totals
}

// NewCodexScanner returns a scanner that reads only logs whose cwd lies under
// prefix (normally ~/.lola/worktrees).
func NewCodexScanner(prefix string) *CodexScanner {
	return &CodexScanner{prefix: filepath.Clean(prefix), files: map[string]*codexFile{}}
}

// Scan walks the newest day directories under root (CodexHome()/sessions)
// and reads what each log appended since the last scan. Logs that fell out of
// the window are forgotten.
func (c *CodexScanner) Scan(root string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	seen := map[string]bool{}
	n := 0
	for _, y := range descSubdirs(root) {
		for _, m := range descSubdirs(y) {
			for _, d := range descSubdirs(m) {
				if n++; n > codexKeepDirs {
					goto done
				}
				ents, _ := os.ReadDir(d)
				for _, e := range ents {
					if !e.Type().IsRegular() || !strings.HasSuffix(e.Name(), ".jsonl") {
						continue
					}
					info, err := e.Info()
					if err != nil {
						continue
					}
					path := filepath.Join(d, e.Name())
					seen[path] = true
					c.update(path, info.Size())
				}
			}
		}
	}
done:
	for p := range c.files {
		if !seen[p] {
			delete(c.files, p)
		}
	}
}

func descSubdirs(dir string) []string {
	ents, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []string
	for i := len(ents) - 1; i >= 0; i-- { // names are dates: lexical = chronological
		if ents[i].IsDir() {
			out = append(out, filepath.Join(dir, ents[i].Name()))
		}
	}
	return out
}

func (c *CodexScanner) update(path string, size int64) {
	f := c.files[path]
	if f != nil && size < f.size {
		f = nil // a log only grows; one that shrank is read again from scratch
	}
	if f == nil {
		f = &codexFile{days: map[string]Totals{}, slots: map[int64]Totals{}}
		c.files[path] = f
	}
	f.size = size
	if f.metaRead && f.cwd == "" || size == f.offset {
		return
	}
	fh, err := os.Open(path)
	if err != nil {
		return
	}
	defer fh.Close()
	if _, err := fh.Seek(f.offset, io.SeekStart); err != nil {
		return
	}
	r := bufio.NewReaderSize(fh, 64<<10)
	for {
		line, err := readLine(r)
		if err != nil {
			return // EOF on a partial tail: re-read it next time
		}
		f.offset += int64(len(line))
		if !f.metaRead {
			f.metaRead = true
			f.cwd = c.lolaCwd(line)
			if f.cwd == "" {
				return
			}
			continue
		}
		if bytes.Contains(line, codexTokenKey) {
			f.add(line)
		}
	}
}

// readLine returns one complete line (newline included) or an error; a line
// longer than maxCodexLine is consumed and returned truncated so the offset
// still advances past it.
func readLine(r *bufio.Reader) ([]byte, error) {
	var buf []byte
	for {
		chunk, err := r.ReadSlice('\n')
		if len(buf)+len(chunk) <= maxCodexLine {
			buf = append(buf, chunk...)
		} else {
			buf = append(buf, chunk[:max(0, maxCodexLine-len(buf))]...)
		}
		switch err {
		case nil:
			return buf, nil
		case bufio.ErrBufferFull:
			continue
		default:
			return nil, err
		}
	}
}

// lolaCwd is the session_meta cwd when it lies under the prefix, else "".
func (c *CodexScanner) lolaCwd(line []byte) string {
	var meta struct {
		Type    string `json:"type"`
		Payload struct {
			Cwd string `json:"cwd"`
		} `json:"payload"`
	}
	if json.Unmarshal(line, &meta) != nil || meta.Type != "session_meta" || meta.Payload.Cwd == "" {
		return ""
	}
	cwd := filepath.Clean(meta.Payload.Cwd)
	if c.prefix == "" || c.prefix == "." || !within(cwd, c.prefix) {
		return ""
	}
	return cwd
}

func within(path, dir string) bool {
	return path == dir || strings.HasPrefix(path, dir+string(filepath.Separator))
}

var codexTokenKey = []byte(`"token_count"`)

type codexUsage struct {
	Input  int64 `json:"input_tokens"`
	Cached int64 `json:"cached_input_tokens"`
	Write  int64 `json:"cache_write_input_tokens"`
	Output int64 `json:"output_tokens"`
}

func (f *codexFile) add(line []byte) {
	var ev struct {
		Timestamp string `json:"timestamp"`
		Payload   struct {
			Type string `json:"type"`
			Info *struct {
				Total *codexUsage `json:"total_token_usage"`
			} `json:"info"`
		} `json:"payload"`
	}
	if json.Unmarshal(line, &ev) != nil || ev.Payload.Type != "token_count" || ev.Payload.Info == nil || ev.Payload.Info.Total == nil {
		return
	}
	u := ev.Payload.Info.Total
	// input_tokens INCLUDES the cached part; output_tokens includes reasoning.
	cur := Totals{Input: max(u.Input-u.Cached-u.Write, 0), CacheRead: u.Cached, CacheWrite: u.Write, Output: u.Output}
	d := Totals{Input: cur.Input - f.last.Input, CacheRead: cur.CacheRead - f.last.CacheRead,
		CacheWrite: cur.CacheWrite - f.last.CacheWrite, Output: cur.Output - f.last.Output}
	if d.Input < 0 || d.CacheRead < 0 || d.CacheWrite < 0 || d.Output < 0 {
		d = cur // the counter restarted: count from zero again
	}
	f.last = cur
	if d.IsZero() {
		return
	}
	at, err := time.Parse(time.RFC3339Nano, ev.Timestamp)
	if err != nil {
		return
	}
	day := at.Local().Format(DayFormat)
	slot := at.Unix() / int64(SlotDuration/time.Second)
	sl, active := f.slots[slot]
	sl.Add(d)
	f.slots[slot] = sl
	if !active {
		d.Slots = 1
	}
	t := f.days[day]
	t.Add(d)
	f.days[day] = t
}

// For sums, per local day, every log that ran in dir or below it.
func (c *CodexScanner) For(dir string) map[string]Totals {
	c.mu.Lock()
	defer c.mu.Unlock()
	if dir == "" {
		return nil
	}
	dir = filepath.Clean(dir)
	var out map[string]Totals
	for _, f := range c.files {
		if f.cwd == "" || !within(f.cwd, dir) {
			continue
		}
		if out == nil {
			out = map[string]Totals{}
		}
		for day, t := range f.days {
			cur := out[day]
			cur.Add(t)
			out[day] = cur
		}
	}
	return out
}

// Recent sums dir's usage in the windows ending after since (see
// Scanner.Recent).
func (c *CodexScanner) Recent(dir string, since time.Time) Totals {
	c.mu.Lock()
	defer c.mu.Unlock()
	var t Totals
	dir = filepath.Clean(dir)
	from := since.Unix() / int64(SlotDuration/time.Second)
	for _, f := range c.files {
		if f.cwd == "" || !within(f.cwd, dir) {
			continue
		}
		for k, v := range f.slots {
			if k >= from {
				t.Add(v)
			}
		}
	}
	return t
}

// MergeDays adds b's days into a (a may be nil); it returns the result.
func MergeDays(a, b map[string]Totals) map[string]Totals {
	if len(b) == 0 {
		return a
	}
	if a == nil {
		a = map[string]Totals{}
	}
	for day, t := range b {
		cur := a[day]
		// Two agents active in the same window both count it; Slots is a
		// rate denominator, and over-counting it errs toward a LOWER rate.
		cur.Add(t)
		a[day] = cur
	}
	return a
}
