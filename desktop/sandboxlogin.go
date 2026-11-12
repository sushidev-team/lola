package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/charmbracelet/x/vt"
	"github.com/creack/pty"

	"github.com/sushidev-team/lola/internal/proctree"
)

// SandboxLoginService runs `claude setup-token` for the user and stores the
// token it prints in the keychain, so the Settings → Sandbox "Log in with
// Claude" button replaces copy-pasting a secret out of a terminal.
//
// The token must never reach the frontend, a log, an error or an event. Raw
// PTY bytes are therefore NEVER forwarded: the child's output is modelled by a
// virtual terminal (charmbracelet/x/vt) owned by this process, and the
// frontend receives full repaints of that SCREEN, built from the screen only
// after it has been checked for a token. A byte-stream filter cannot give that
// guarantee: claude's renderer diffs frames and is free to reach a cell by
// cursor movement, so a token need never appear contiguously in the bytes,
// while on the modelled screen it always does. The PTY is wide
// (loginPTYCols) so the token and the sign-in URL are never wrapped.
//
// The moment the screen holds anything token-shaped the run is SEALED: every
// later frame is a fixed notice, nothing from the screen. Sealing is sticky.
// The token is read from the screen once output has gone quiet (a frame is
// written in one burst, so quiet means complete), stored through the same
// validation and keychain seam as SetSandboxToken, and the child is ended.
//
// The child runs in a PTY of its own rather than in tmux: a tmux pane keeps
// scrollback, and the token would outlive the login there.
type SandboxLoginService struct {
	mu  sync.Mutex
	seq int
	run *loginRun

	// emit sends a Wails event. Set by main before Run (tests set their own);
	// a field rather than a SetApp method so it is not bound to the webview.
	emit func(name string, data any)
}

// Event names. Every payload carries the run id Start returned, so a page that
// started a new run ignores a late event from an old one.
const (
	evtSandboxLoginFrame  = "sandboxlogin:frame"  // SandboxLoginFrame
	evtSandboxLoginStored = "sandboxlogin:stored" // SandboxLoginDone (Stored=true)
	evtSandboxLoginDone   = "sandboxlogin:done"   // SandboxLoginDone
)

// SandboxLoginFrame is one full repaint of the login screen. Data is
// terminal text (clear + lines) for xterm.write.
type SandboxLoginFrame struct {
	Run  int    `json:"run"`
	Data string `json:"data"`
}

// SandboxLoginDone ends a run. Stored says the token is in the keychain; Error
// is "" on success and on a cancel. Neither ever carries the token.
type SandboxLoginDone struct {
	Run       int    `json:"run"`
	Stored    bool   `json:"stored"`
	Cancelled bool   `json:"cancelled"`
	Error     string `json:"error"`
}

const (
	// loginPTYCols keeps the token (~108 chars) and claude's sign-in URL on
	// one row each: a wrapped token would be read back truncated, and a
	// wrapped URL stops being one clickable link.
	loginPTYCols = 500
	loginMinRows = 12
	loginMaxRows = 200
	// sealedNotice replaces the whole screen once the token is visible.
	sealedNotice = "[token stored in the keychain]"
)

// Tunables, vars so tests can shrink them.
var (
	loginTimeout    = 10 * time.Minute
	loginQuiet      = 300 * time.Millisecond // no output this long = the frame is complete
	loginFrameEvery = 33 * time.Millisecond
	loginExitGrace  = 2 * time.Second // after the token is stored, for claude to exit on Enter
)

// loginProc is the child as the service sees it: output to read, input to
// write, a window to size, a way to end it. The exec seam (startLoginProc)
// returns one, so tests drive the whole pipeline with no PTY and no claude.
type loginProc interface {
	io.Reader
	io.Writer
	Resize(cols, rows int) error
	// Kill ends the child and its process group and releases the PTY.
	// Idempotent.
	Kill()
}

// Seams.
var (
	lookClaude     = func() (string, error) { return exec.LookPath("claude") }
	startLoginProc = startClaudeSetupToken
)

// startClaudeSetupToken runs `claude setup-token` in a fresh PTY. pty.Start
// makes it a session leader, so its pid is its process group and Kill can take
// the whole tree (the browser opener included) with it.
func startClaudeSetupToken(bin string, cols, rows int) (loginProc, error) {
	cmd := exec.Command(bin, "setup-token")
	cmd.Env = loginEnv()
	f, err := pty.StartWithSize(cmd, &pty.Winsize{Cols: clampWinDim(cols), Rows: clampWinDim(rows)})
	if err != nil {
		return nil, err
	}
	p := &ptyLoginProc{f: f, cmd: cmd, done: make(chan struct{})}
	go func() { _ = cmd.Wait(); close(p.done) }()
	return p, nil
}

// loginEnv is the process env (PATH already augmented by ensurePATH) with a
// real TERM, the way TermService's children get it.
func loginEnv() []string {
	env := make([]string, 0, len(os.Environ())+2)
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(kv, "TERM=") {
			env = append(env, kv)
		}
	}
	env = append(env, "TERM=xterm-256color")
	if os.Getenv("LANG") == "" {
		env = append(env, "LANG=en_US.UTF-8")
	}
	return env
}

type ptyLoginProc struct {
	f    *os.File
	cmd  *exec.Cmd
	done chan struct{}
	once sync.Once
}

func (p *ptyLoginProc) Read(b []byte) (int, error)  { return p.f.Read(b) }
func (p *ptyLoginProc) Write(b []byte) (int, error) { return p.f.Write(b) }
func (p *ptyLoginProc) Resize(cols, rows int) error {
	return pty.Setsize(p.f, &pty.Winsize{Cols: clampWinDim(cols), Rows: clampWinDim(rows)})
}

func (p *ptyLoginProc) Kill() {
	p.once.Do(func() {
		select {
		case <-p.done:
		default:
			if p.cmd.Process != nil {
				_ = proctree.KillGroup(p.cmd.Process.Pid, time.Second)
				_ = p.cmd.Process.Kill()
			}
		}
		_ = p.f.Close()
	})
}

// ServiceShutdown ends a login still running when the app quits, so no
// claude outlives the app.
func (s *SandboxLoginService) ServiceShutdown() error {
	s.mu.Lock()
	r := s.run
	s.run = nil
	s.mu.Unlock()
	if r != nil {
		r.finish(SandboxLoginDone{Cancelled: true})
	}
	return nil
}

// Start runs `claude setup-token` and returns the run id the events carry.
// One run at a time: a new Start ends the previous one (a reloaded page must
// be able to start over). cols/rows are the frontend terminal's size; the
// PTY is always loginPTYCols wide.
func (s *SandboxLoginService) Start(cols, rows int) (int, error) {
	_ = cols // the PTY width is fixed; the frontend wraps the repaint itself
	if s.emit == nil {
		return 0, errors.New("login service not wired to app")
	}
	bin, err := lookClaude()
	if err != nil || bin == "" {
		return 0, errors.New("claude was not found on PATH. Install Claude Code, then try again")
	}
	rows = clampLoginRows(rows)

	s.mu.Lock()
	old := s.run
	s.run = nil
	s.seq++
	id := s.seq
	s.mu.Unlock()
	if old != nil {
		old.finish(SandboxLoginDone{Cancelled: true})
	}

	proc, err := startLoginProc(bin, loginPTYCols, rows)
	if err != nil {
		return 0, fmt.Errorf("could not start claude setup-token: %w", err)
	}
	r := newLoginRun(id, proc, loginPTYCols, rows, s.emit, func(r *loginRun) {
		s.mu.Lock()
		if s.run == r {
			s.run = nil
		}
		s.mu.Unlock()
	})
	s.mu.Lock()
	s.run = r
	s.mu.Unlock()
	r.start()
	return id, nil
}

// Write forwards keystrokes (claude may ask to paste an authorization code).
// A write to an ended or replaced run is a silent no-op.
func (s *SandboxLoginService) Write(run int, data string) error {
	if r := s.current(run); r != nil {
		r.write([]byte(data))
	}
	return nil
}

// Resize follows the frontend terminal's height; the width stays fixed.
func (s *SandboxLoginService) Resize(run, cols, rows int) error {
	_ = cols
	if r := s.current(run); r != nil && rows > 0 {
		r.resize(clampLoginRows(rows))
	}
	return nil
}

// Cancel ends the run and its process group. Idempotent.
func (s *SandboxLoginService) Cancel(run int) error {
	s.mu.Lock()
	r := s.run
	if r == nil || r.id != run {
		s.mu.Unlock()
		return nil
	}
	s.run = nil
	s.mu.Unlock()
	r.finish(SandboxLoginDone{Cancelled: true})
	return nil
}

func (s *SandboxLoginService) current(run int) *loginRun {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.run != nil && s.run.id == run {
		return s.run
	}
	return nil
}

func clampLoginRows(rows int) int {
	return min(max(rows, loginMinRows), loginMaxRows)
}

// --- one run ------------------------------------------------------------------

// tokenRe is a complete setup token as it is read off the screen.
var tokenRe = regexp.MustCompile(`sk-ant-oat01-[A-Za-z0-9_-]{20,}`)

// longRunRe finds token-charset runs long enough to be a secret. Used only to
// SEAL (never to capture), so a token whose prefix has not reached the screen
// yet still cannot be shown.
var longRunRe = regexp.MustCompile(`[A-Za-z0-9_-]{40,}`)

type loginRun struct {
	id    int
	proc  loginProc
	emit  func(string, any)
	onEnd func(*loginRun)

	emu *vt.Emulator
	// Timings, read once at Start so a running run never reads the tunables.
	timeout, quietFor, frameEvery, exitGrace time.Duration

	mu       sync.Mutex // guards emu and the fields below
	dirty    bool
	sealed   bool
	lastOut  time.Time
	stored   bool
	ended    bool
	finished chan struct{}
	outErr   error // read loop ended
}

func newLoginRun(id int, proc loginProc, cols, rows int, emit func(string, any), onEnd func(*loginRun)) *loginRun {
	return &loginRun{
		id: id, proc: proc, emit: emit, onEnd: onEnd,
		emu:      vt.NewEmulator(cols, rows),
		finished: make(chan struct{}),
		timeout:  loginTimeout, quietFor: loginQuiet, frameEvery: loginFrameEvery, exitGrace: loginExitGrace,
	}
}

func (r *loginRun) start() {
	go r.readLoop()
	go r.responseLoop()
	go r.tickLoop()
}

// readLoop models every byte on the screen. Nothing read here leaves Go.
func (r *loginRun) readLoop() {
	buf := make([]byte, 32*1024)
	for {
		n, err := r.proc.Read(buf)
		if n > 0 {
			r.mu.Lock()
			if !r.ended {
				_, _ = r.emu.Write(buf[:n])
				r.dirty = true
				r.lastOut = time.Now()
				r.checkSealLocked()
			}
			r.mu.Unlock()
		}
		if err != nil {
			r.mu.Lock()
			r.outErr = err
			r.mu.Unlock()
			return
		}
	}
}

// responseLoop answers the child's terminal queries (device attributes,
// cursor position) the way a real terminal would: the emulator writes its
// replies into a pipe that blocks until read, and an interactive program
// waits for them.
func (r *loginRun) responseLoop() {
	buf := make([]byte, 4096)
	for {
		n, err := r.emu.Read(buf)
		if n > 0 {
			_, _ = r.proc.Write(buf[:n])
		}
		if err != nil {
			return
		}
	}
}

// tickLoop paints frames, captures the token once output is quiet, notices the
// child's exit and enforces the overall bound.
func (r *loginRun) tickLoop() {
	tick := time.NewTicker(r.frameEvery)
	defer tick.Stop()
	deadline := time.NewTimer(r.timeout)
	defer deadline.Stop()
	for {
		select {
		case <-r.finished:
			return
		case <-deadline.C:
			r.finish(SandboxLoginDone{Error: fmt.Sprintf("The login timed out after %d minutes.", max(1, int(r.timeout/time.Minute)))})
			return
		case <-tick.C:
		}
		if r.step() {
			return
		}
	}
}

// step does one tick's work and reports whether the run is over.
func (r *loginRun) step() bool {
	r.mu.Lock()
	if r.ended {
		r.mu.Unlock()
		return true
	}
	var frame string
	if r.dirty {
		r.dirty = false
		frame = r.frameLocked()
	}
	quiet := !r.lastOut.IsZero() && time.Since(r.lastOut) >= r.quietFor
	exited := r.outErr != nil
	var token string
	if r.sealed && !r.stored && (quiet || exited) {
		token = tokenRe.FindString(r.screenTextLocked())
	}
	r.mu.Unlock()

	if frame != "" {
		r.emit(evtSandboxLoginFrame, SandboxLoginFrame{Run: r.id, Data: frame})
	}
	if token != "" {
		r.store(token)
		return true
	}
	if exited {
		// Output ended (the child exited) with no token on the screen.
		r.finish(SandboxLoginDone{Error: "claude setup-token ended without a token."})
		return true
	}
	return false
}

// store saves the token through SetSandboxToken's path, then ends the child:
// Enter first (setup-token may wait for a key), then the process group.
func (r *loginRun) store(token string) {
	if err := (&ConfigService{}).SetSandboxToken(token); err != nil {
		// SetSandboxToken's errors never carry the token; say only what failed.
		r.finish(SandboxLoginDone{Error: "Claude printed a token, but storing it failed: " + scrubToken(err.Error(), token)})
		return
	}
	r.mu.Lock()
	r.stored = true
	r.mu.Unlock()
	r.emit(evtSandboxLoginStored, SandboxLoginDone{Run: r.id, Stored: true})
	r.write([]byte("\r"))
	go func() {
		deadline := time.Now().Add(r.exitGrace)
		for time.Now().Before(deadline) {
			r.mu.Lock()
			gone := r.outErr != nil
			r.mu.Unlock()
			if gone {
				break
			}
			time.Sleep(50 * time.Millisecond)
		}
		r.finish(SandboxLoginDone{Stored: true})
	}()
}

// scrubToken is belt and braces for an error string: keychain errors are
// written never to include the value, and this makes it a property here too.
func scrubToken(msg, token string) string {
	return strings.ReplaceAll(msg, token, sealedNotice)
}

// finish ends the run once: kill the child, release the emulator's reply
// pipe, emit the done event, drop the run from the service.
func (r *loginRun) finish(d SandboxLoginDone) {
	r.mu.Lock()
	if r.ended {
		r.mu.Unlock()
		return
	}
	r.ended = true
	if r.stored {
		d.Stored = true
		d.Error = ""
	}
	r.mu.Unlock()
	close(r.finished)
	r.proc.Kill()
	if pw, ok := r.emu.InputPipe().(interface{ CloseWithError(error) error }); ok {
		_ = pw.CloseWithError(io.EOF) // unblock responseLoop (see vtterm.closeEmulatorInput)
	}
	d.Run = r.id
	r.emit(evtSandboxLoginDone, d)
	if r.onEnd != nil {
		r.onEnd(r)
	}
}

func (r *loginRun) write(b []byte) {
	r.mu.Lock()
	ended := r.ended
	r.mu.Unlock()
	if !ended && len(b) > 0 {
		_, _ = r.proc.Write(b)
	}
}

func (r *loginRun) resize(rows int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.ended || rows == r.emu.Height() {
		return
	}
	_ = r.proc.Resize(loginPTYCols, rows)
	r.emu.Resize(loginPTYCols, rows)
	r.dirty = true
	r.checkSealLocked()
}

// screenTextLocked is the visible screen as plain text, one row per line.
func (r *loginRun) screenTextLocked() string {
	return r.emu.String()
}

// checkSealLocked seals the run once the screen shows anything token-shaped:
// the prefix itself, or a long token-charset run outside a URL.
func (r *loginRun) checkSealLocked() {
	if !r.sealed && looksLikeToken(r.screenTextLocked()) {
		r.sealed = true
	}
}

func looksLikeToken(text string) bool {
	if strings.Contains(text, "sk-ant") {
		return true
	}
	for _, word := range strings.Fields(text) {
		if strings.Contains(word, "://") {
			continue // the sign-in URL's query values are long and must stay clickable
		}
		for _, run := range longRunRe.FindAllString(word, -1) {
			if strings.ContainsAny(run, "0123456789") && strings.IndexFunc(run, isLetter) >= 0 {
				return true
			}
		}
	}
	return false
}

func isLetter(c rune) bool { return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') }

// frameLocked builds one repaint. Before the seal it is the modelled screen
// (styles and OSC 8 links kept, trailing blanks dropped so the frontend's
// narrower terminal does not wrap a row of spaces); after it, only the notice.
// The seal check runs on the same screen under the same lock, so no frame is
// ever built from a screen that showed a token.
func (r *loginRun) frameLocked() string {
	const home = "\x1b[?25l\x1b[H\x1b[2J\x1b[3J"
	if r.sealed {
		return home + "Claude printed a token.\r\n\r\n  " + sealedNotice + "\r\n"
	}
	lines := strings.Split(strings.TrimRight(r.emu.Render(), "\n"), "\n")
	for i, l := range lines {
		lines[i] = trimRenderedRight(l)
	}
	for len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return home + strings.Join(lines, "\r\n")
}

// trimRenderedRight drops trailing spaces from a rendered row, keeping the
// escape sequences that end it (a closing SGR reset or link end).
func trimRenderedRight(l string) string {
	tail := ""
	for {
		l = strings.TrimRight(l, " ")
		i := strings.LastIndex(l, "\x1b")
		if i < 0 {
			break
		}
		if l[i:] == "\x1b\\" { // ST: the sequence starts at the ESC before it
			j := strings.LastIndex(l[:i], "\x1b")
			if j < 0 {
				break
			}
			i = j
		}
		if !isCompleteEscape(l[i:]) {
			break
		}
		tail = l[i:] + tail
		l = l[:i]
	}
	return l + tail
}

// isCompleteEscape reports whether s is exactly one CSI or OSC sequence.
func isCompleteEscape(s string) bool {
	if len(s) < 3 || s[0] != '\x1b' {
		return false
	}
	switch s[1] {
	case '[':
		last := s[len(s)-1]
		if last < 0x40 || last > 0x7e {
			return false
		}
		for _, c := range []byte(s[2 : len(s)-1]) {
			if c < 0x20 || c > 0x3f {
				return false
			}
		}
		return true
	case ']':
		body := s[2:]
		switch {
		case strings.HasSuffix(body, "\x07"):
			body = body[:len(body)-1]
		case strings.HasSuffix(body, "\x1b\\"):
			body = body[:len(body)-2]
		default:
			return false
		}
		return !strings.ContainsAny(body, "\x1b\x07")
	}
	return false
}
