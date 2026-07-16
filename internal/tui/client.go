// Socket client for CLI subcommands (status/stop/reload/enable/disable/
// pollOnce) plus the daemon.log tail. The interactive TUI reuses request().
package tui

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/sushidev-team/lola/internal/config"
	"github.com/sushidev-team/lola/internal/protocol"
)

var errDaemonDown = errors.New("daemon not running (start with: lola run)")

var (
	tblHeader = lipgloss.NewStyle().Foreground(lipgloss.Color(colFaint)) // muted uppercase column heads
	badText   = lipgloss.NewStyle().Foreground(lipgloss.Color(colBad))
	goodText  = lipgloss.NewStyle().Foreground(lipgloss.Color(colGood))
	warnText  = lipgloss.NewStyle().Foreground(lipgloss.Color(colWarn))
	faintText = lipgloss.NewStyle().Foreground(lipgloss.Color(colFaint))
)

func socketPath() (string, error) {
	home, err := config.Home()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, "lola.sock"), nil
}

// requestRaw writes one raw JSON request line to the daemon socket and
// decodes the single JSON response line.
func requestRaw(raw string) (*protocol.Response, error) {
	sock, err := socketPath()
	if err != nil {
		return nil, err
	}
	conn, err := net.Dial("unix", sock)
	if err != nil {
		return nil, errDaemonDown
	}
	defer conn.Close()
	// pollOnce runs a full tick synchronously; leave generous room.
	_ = conn.SetDeadline(time.Now().Add(5 * time.Minute))

	if _, err := conn.Write([]byte(strings.TrimRight(raw, "\n") + "\n")); err != nil {
		return nil, err
	}
	line, err := bufio.NewReaderSize(conn, 1<<20).ReadBytes('\n')
	// Accept a response the daemon terminated by closing instead of "\n".
	if err != nil && !(errors.Is(err, io.EOF) && len(line) > 0) {
		return nil, fmt.Errorf("read daemon response: %w", err)
	}
	var resp protocol.Response
	if err := json.Unmarshal(line, &resp); err != nil {
		return nil, fmt.Errorf("bad daemon response: %w", err)
	}
	return &resp, nil
}

func request(req protocol.Request) (*protocol.Response, error) {
	raw, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}
	return requestRaw(string(raw))
}

// requestFn is the socket round-trip used by the interactive pane/answer
// commands, indirected so model-level tests can inject canned daemon responses
// (and assert the request they issued) without a live socket.
var requestFn = request

// Send writes one JSON request line to the daemon socket and pretty-prints
// the response. A non-OK response is returned as an error so the CLI exits 1.
func Send(raw string) error {
	var req protocol.Request
	if err := json.Unmarshal([]byte(raw), &req); err != nil {
		return fmt.Errorf("bad request: %w", err)
	}
	resp, err := requestRaw(raw)
	if err != nil {
		return err
	}
	if !resp.OK {
		if resp.Error == "" {
			return errors.New("daemon reported failure")
		}
		return errors.New(resp.Error)
	}

	switch req.Cmd {
	case "status":
		var d protocol.StatusData
		if err := json.Unmarshal(resp.Data, &d); err != nil {
			return fmt.Errorf("bad status data: %w", err)
		}
		fmt.Print(renderStatus(&d))
	case "pollOnce":
		var d protocol.PollOnceData
		if err := json.Unmarshal(resp.Data, &d); err != nil {
			return fmt.Errorf("bad pollOnce data: %w", err)
		}
		fmt.Print(renderMatches(&d))
	case "kill":
		var d protocol.KillData
		if err := json.Unmarshal(resp.Data, &d); err != nil {
			return fmt.Errorf("bad kill data: %w", err)
		}
		if d.Message != "" {
			fmt.Println(d.Message)
		} else {
			fmt.Println("ok")
		}
	case "review":
		var d protocol.ReviewData
		if err := json.Unmarshal(resp.Data, &d); err != nil {
			return fmt.Errorf("bad review data: %w", err)
		}
		if d.Message != "" {
			fmt.Println(d.Message)
		} else {
			fmt.Println("ok")
		}
	case "coderabbit":
		var d protocol.CodeRabbitData
		if err := json.Unmarshal(resp.Data, &d); err != nil {
			return fmt.Errorf("bad coderabbit data: %w", err)
		}
		if d.Message != "" {
			fmt.Println(d.Message)
		} else {
			fmt.Println("ok")
		}
	default:
		fmt.Println("ok")
	}
	return nil
}

func renderStatus(d *protocol.StatusData) string {
	var b strings.Builder
	runtimeState := "ok"
	if !d.RuntimeOK {
		runtimeState = "missing tools"
		if d.RuntimeErr != "" {
			runtimeState = d.RuntimeErr
		}
	}
	fmt.Fprintf(&b, "runtime: %s   linear: %s\n\n",
		yesNoStyled(d.RuntimeOK, runtimeState, runtimeState),
		yesNoStyled(d.LinearOK, "ok", "error"))
	rows := make([][]string, 0, len(d.Polls))
	for _, p := range d.Polls {
		rows = append(rows, []string{
			p.Name, yesNo(p.Enabled), fmtAgo(p.LastRun), fmtAgo(p.LastSpawn), yesNo(p.Running), p.LastError,
		})
	}
	b.WriteString(renderTable([]string{"POLL", "ENABLED", "LAST RUN", "LAST SPAWN", "RUNNING", "ERROR"}, rows))
	return b.String()
}

func renderMatches(d *protocol.PollOnceData) string {
	var b strings.Builder
	mode := ""
	if d.DryRun {
		mode = " (dry-run)"
	}
	fmt.Fprintf(&b, "poll %s%s: %d match(es)\n\n", d.Poll, mode, len(d.Matches))
	rows := make([][]string, 0, len(d.Matches))
	for _, m := range d.Matches {
		rows = append(rows, []string{m.Identifier, m.Title, m.Action, m.Reason})
	}
	b.WriteString(renderTable([]string{"IDENTIFIER", "TITLE", "ACTION", "REASON"}, rows))
	return b.String()
}

func colWidths(headers []string, rows [][]string) []int {
	w := make([]int, len(headers))
	for i, h := range headers {
		w[i] = lipgloss.Width(h)
	}
	for _, r := range rows {
		for i, c := range r {
			if i < len(w) && lipgloss.Width(c) > w[i] {
				w[i] = lipgloss.Width(c)
			}
		}
	}
	return w
}

func padCells(cells []string, w []int) string {
	parts := make([]string, len(cells))
	for i, c := range cells {
		n := 0
		if i < len(w) {
			n = w[i] - lipgloss.Width(c)
		}
		if n < 0 {
			n = 0
		}
		parts[i] = c + strings.Repeat(" ", n)
	}
	return strings.TrimRight(strings.Join(parts, "  "), " ")
}

func renderTable(headers []string, rows [][]string) string {
	w := colWidths(headers, rows)
	var b strings.Builder
	b.WriteString(tblHeader.Render(padCells(headers, w)) + "\n")
	if len(rows) == 0 {
		b.WriteString(faintText.Render("(none)") + "\n")
	}
	for _, r := range rows {
		b.WriteString(padCells(r, w) + "\n")
	}
	return b.String()
}

func yesNo(v bool) string {
	if v {
		return "yes"
	}
	return "no"
}

func yesNoStyled(v bool, yes, no string) string {
	if v {
		return goodText.Render(yes)
	}
	return badText.Render(no)
}

func fmtAgo(t time.Time) string {
	if t.IsZero() {
		return "-"
	}
	d := time.Since(t)
	if d < 0 {
		d = 0
	}
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds ago", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	default:
		return t.Local().Format("2006-01-02 15:04")
	}
}

// Logs prints daemon.log lines, filtered to lines containing "[<poll>]" when
// poll is non-empty. With follow it re-reads appended bytes every 500ms until
// SIGINT/SIGTERM.
func Logs(poll string, follow bool) error {
	home, err := config.Home()
	if err != nil {
		return err
	}
	path := filepath.Join(home, "daemon.log")
	f, err := os.Open(path)
	if errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("no log file at %s (has the daemon run yet?)", path)
	}
	if err != nil {
		return err
	}
	defer f.Close()

	match := func(line string) bool {
		return poll == "" || strings.Contains(line, "["+poll+"]")
	}

	var (
		off     int64
		partial string
	)
	readNew := func() error {
		fi, err := f.Stat()
		if err != nil {
			return err
		}
		size := fi.Size()
		if size < off { // truncated/rotated in place: start over
			off, partial = 0, ""
		}
		if size == off {
			return nil
		}
		buf := make([]byte, size-off)
		n, err := f.ReadAt(buf, off)
		if err != nil && !errors.Is(err, io.EOF) {
			return err
		}
		off += int64(n)
		lines := strings.Split(partial+string(buf[:n]), "\n")
		partial = lines[len(lines)-1] // incomplete tail (or "")
		for _, ln := range lines[:len(lines)-1] {
			if match(ln) {
				fmt.Println(ln)
			}
		}
		return nil
	}

	if err := readNew(); err != nil {
		return err
	}
	if !follow {
		if partial != "" && match(partial) {
			fmt.Println(partial)
		}
		return nil
	}

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(sig)
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-sig:
			return nil
		case <-ticker.C:
			if err := readNew(); err != nil {
				return err
			}
		}
	}
}
