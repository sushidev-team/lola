package main

import (
	"encoding/json"
	"errors"
	"io"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

// A token in the real shape: sk-ant-oat01- plus base64url characters.
const testLoginToken = "sk-ant-oat01-Zq7_Kx9-Lm2Np4Rs6Tv8Wy0Ab1Cd3Ef5Gh7Ij9Kl1Mn3Op5Qr7St9Uv1Wx3Yz5aB7cD9eF1gH3iJ5kL7mN9oP1qR3sT5uV7wAA"

// fakeLoginProc plays a scripted child: each Read hands back the next chunk,
// and the child "exits" (EOF) on Kill, on Enter when exitOnEnter, or when the
// script is closed.
type fakeLoginProc struct {
	out         chan []byte
	done        chan struct{}
	exitOnEnter bool

	mu      sync.Mutex
	in      strings.Builder
	killed  bool
	stopped sync.Once
}

func newFakeLoginProc() *fakeLoginProc {
	return &fakeLoginProc{out: make(chan []byte, 256), done: make(chan struct{}), exitOnEnter: true}
}

func (p *fakeLoginProc) Read(b []byte) (int, error) {
	select {
	case c, ok := <-p.out:
		if !ok {
			return 0, io.EOF
		}
		return copy(b, c), nil
	case <-p.done:
		return 0, io.EOF
	}
}

func (p *fakeLoginProc) Write(b []byte) (int, error) {
	p.mu.Lock()
	p.in.Write(b)
	p.mu.Unlock()
	if p.exitOnEnter && strings.Contains(string(b), "\r") {
		p.stop()
	}
	return len(b), nil
}

func (p *fakeLoginProc) Resize(int, int) error { return nil }
func (p *fakeLoginProc) Kill() {
	p.mu.Lock()
	p.killed = true
	p.mu.Unlock()
	p.stop()
}
func (p *fakeLoginProc) stop() { p.stopped.Do(func() { close(p.done) }) }
func (p *fakeLoginProc) input() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.in.String()
}

func (p *fakeLoginProc) wasKilled() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.killed
}

// emitted records every event the service sends, as the JSON the webview
// would receive.
type emitted struct {
	mu     sync.Mutex
	events []string
	names  []string
	done   chan SandboxLoginDone
}

func (e *emitted) emit(name string, data any) {
	b, _ := json.Marshal(data)
	e.mu.Lock()
	e.events = append(e.events, string(b))
	e.names = append(e.names, name)
	e.mu.Unlock()
	if name == evtSandboxLoginDone {
		e.done <- data.(SandboxLoginDone)
	}
}

func (e *emitted) all() []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]string(nil), e.events...)
}

func (e *emitted) has(name string) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	for _, n := range e.names {
		if n == name {
			return true
		}
	}
	return false
}

func (e *emitted) waitDone(t *testing.T) SandboxLoginDone {
	t.Helper()
	select {
	case d := <-e.done:
		return d
	case <-time.After(5 * time.Second):
		t.Fatal("no done event")
	}
	return SandboxLoginDone{}
}

// newLoginHarness wires a service to a fake child and fast timings.
func newLoginHarness(t *testing.T) (*SandboxLoginService, *fakeLoginProc, *emitted, *[]string) {
	t.Helper()
	q, f, g, look, start := loginQuiet, loginFrameEvery, loginExitGrace, lookClaude, startLoginProc
	t.Cleanup(func() {
		loginQuiet, loginFrameEvery, loginExitGrace, lookClaude, startLoginProc = q, f, g, look, start
	})
	loginQuiet, loginFrameEvery, loginExitGrace = 80*time.Millisecond, 5*time.Millisecond, 300*time.Millisecond
	lookClaude = func() (string, error) { return "/bin/claude", nil }
	proc := newFakeLoginProc()
	startLoginProc = func(bin string, cols, rows int) (loginProc, error) {
		if cols < 400 {
			t.Errorf("PTY only %d columns wide", cols)
		}
		return proc, nil
	}
	var stored []string
	st := storeSandboxToken
	t.Cleanup(func() { storeSandboxToken = st })
	storeSandboxToken = func(_, v string) error { stored = append(stored, v); return nil }
	ev := &emitted{done: make(chan SandboxLoginDone, 4)}
	s := &SandboxLoginService{emit: ev.emit}
	return s, proc, ev, &stored
}

// assertNoTokenLeak fails if any event holds 8 or more consecutive token
// characters.
func assertNoTokenLeak(t *testing.T, events []string) {
	t.Helper()
	for _, e := range events {
		for i := 0; i+8 <= len(testLoginToken); i++ {
			if sub := testLoginToken[i : i+8]; strings.Contains(e, sub) {
				t.Fatalf("event leaks token substring %q: %q", sub, e)
			}
		}
	}
}

const (
	loginPre   = "\x1b[?25l\x1b[2K\x1b[1G✓ Long-lived authentication token created successfully!\r\n\r\nYour OAuth token (valid for 1 year):\r\n\r\n"
	loginAfter = "\r\n\r\nStore this token securely. You won't be able to see it again.\r\n"
)

// feed sends chunks with a pause shorter than loginQuiet between them.
func feed(p *fakeLoginProc, chunks ...string) {
	for _, c := range chunks {
		p.out <- []byte(c)
		time.Sleep(2 * time.Millisecond)
	}
}

func TestSandboxLoginStoresTheTokenAndNeverEmitsIt(t *testing.T) {
	full := loginPre + "\x1b[1m" + testLoginToken + "\x1b[22m" + loginAfter
	tokAt := len(loginPre) + len("\x1b[1m")
	type script struct {
		name   string
		chunks []string
	}
	var scripts []script
	// Two chunks, split at every 7th byte through the token and around it.
	for cut := tokAt - 6; cut < tokAt+len(testLoginToken)+6; cut += 7 {
		scripts = append(scripts, script{"split", []string{full[:cut], full[cut:]}})
	}
	// Many tiny chunks, styles changing inside the token.
	var tiny []string
	tiny = append(tiny, loginPre)
	for i := 0; i < len(testLoginToken); i += 5 {
		end := min(i+5, len(testLoginToken))
		tiny = append(tiny, "\x1b[0m\x1b[1;36m"+testLoginToken[i:end])
	}
	tiny = append(tiny, "\x1b[0m"+loginAfter)
	scripts = append(scripts, script{"styled-tiny", tiny})
	// Out of order: the token's tail is drawn first by cursor addressing, so
	// the bytes never hold the token contiguously and the prefix comes last.
	scripts = append(scripts, script{"cursor", []string{
		loginPre,
		"\x1b[5;61H" + testLoginToken[60:],
		"\x1b[5;1H" + testLoginToken[:60],
		"\x1b[7;1H" + strings.TrimLeft(loginAfter, "\r\n"),
	}})
	// Twice: a re-render paints the same token again.
	scripts = append(scripts, script{"redraw", []string{full, "\x1b[H\x1b[2J" + full}})

	for _, sc := range scripts {
		s, proc, ev, stored := newLoginHarness(t)
		run, err := s.Start(100, 24)
		if err != nil {
			t.Fatal(err)
		}
		feed(proc, sc.chunks...)
		d := ev.waitDone(t)
		if !d.Stored || d.Error != "" || d.Run != run {
			t.Fatalf("%s: done %+v", sc.name, d)
		}
		if len(*stored) != 1 || (*stored)[0] != testLoginToken {
			t.Fatalf("%s: stored %q", sc.name, *stored)
		}
		if !ev.has(evtSandboxLoginStored) {
			t.Fatalf("%s: no stored event", sc.name)
		}
		if !strings.Contains(proc.input(), "\r") {
			t.Fatalf("%s: Enter not sent after storing", sc.name)
		}
		events := ev.all()
		assertNoTokenLeak(t, events)
		if !strings.Contains(strings.Join(events, ""), sealedNotice) {
			t.Fatalf("%s: no sealed notice in %q", sc.name, events)
		}
	}
}

func TestSandboxLoginPassesOrdinaryOutputPromptly(t *testing.T) {
	s, proc, ev, stored := newLoginHarness(t)
	if _, err := s.Start(100, 24); err != nil {
		t.Fatal(err)
	}
	url := "https://claude.ai/oauth/authorize?code=true&client_id=9d1c250a&code_challenge=AbCdEfGhIjKlMnOpQrStUvWxYz0123456789AbCdEfGh&state=Zz9Yy8Xx7Ww6Vv5Uu4Tt3Ss2Rr1Qq0PpOoNnMmLlKkJj"
	proc.out <- []byte("Opening browser to sign in…\r\n\x1b[2mBrowser didn't open? Use the url below to sign in\x1b[22m\r\n\r\n" + url + "\r\n")
	deadline := time.Now().Add(time.Second)
	for {
		all := strings.Join(ev.all(), "")
		// JSON escapes ESC and '&'; compare on the decoded frames.
		var got string
		for _, e := range ev.all() {
			var f SandboxLoginFrame
			if json.Unmarshal([]byte(e), &f) == nil {
				got += f.Data
			}
		}
		if strings.Contains(got, "Opening browser to sign in…") && strings.Contains(got, url) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("ordinary output not passed through: %q", all)
		}
		time.Sleep(5 * time.Millisecond)
	}
	if strings.Contains(strings.Join(ev.all(), ""), sealedNotice) {
		t.Fatal("a sign-in URL sealed the run")
	}
	close(proc.out) // the child exits without printing a token
	d := ev.waitDone(t)
	if d.Stored || d.Error == "" || len(*stored) != 0 {
		t.Fatalf("done %+v stored %q", d, *stored)
	}
}

func TestSandboxLoginForwardsInputAndCancels(t *testing.T) {
	s, proc, ev, _ := newLoginHarness(t)
	proc.exitOnEnter = false
	run, err := s.Start(80, 24)
	if err != nil {
		t.Fatal(err)
	}
	_ = s.Write(run, "abc-code")
	_ = s.Write(run+1, "stale")
	if got := proc.input(); !strings.Contains(got, "abc-code") || strings.Contains(got, "stale") {
		t.Fatalf("input %q", got)
	}
	if err := s.Cancel(run); err != nil {
		t.Fatal(err)
	}
	d := ev.waitDone(t)
	if !d.Cancelled || d.Stored || !proc.wasKilled() {
		t.Fatalf("done %+v killed %v", d, proc.wasKilled())
	}
	if s.current(run) != nil {
		t.Fatal("run still registered")
	}
}

func TestSandboxLoginOneRunAtATime(t *testing.T) {
	s, first, ev, _ := newLoginHarness(t)
	run1, _ := s.Start(80, 24)
	second := newFakeLoginProc()
	startLoginProc = func(string, int, int) (loginProc, error) { return second, nil }
	run2, err := s.Start(80, 24)
	if err != nil || run2 == run1 {
		t.Fatalf("%d %d %v", run1, run2, err)
	}
	if d := ev.waitDone(t); d.Run != run1 || !d.Cancelled || !first.wasKilled() {
		t.Fatalf("first run not ended: %+v", d)
	}
	_ = s.ServiceShutdown()
	if d := ev.waitDone(t); d.Run != run2 || !second.wasKilled() {
		t.Fatalf("shutdown did not end the run: %+v", d)
	}
}

func TestSandboxLoginMissingClaude(t *testing.T) {
	s, _, _, _ := newLoginHarness(t)
	lookClaude = func() (string, error) { return "", errors.New("exec: not found") }
	if _, err := s.Start(80, 24); err == nil || !strings.Contains(err.Error(), "claude was not found") {
		t.Fatalf("err %v", err)
	}
}

func TestSandboxLoginStoreFailureKeepsTheTokenOut(t *testing.T) {
	s, proc, ev, _ := newLoginHarness(t)
	storeSandboxToken = func(_, v string) error { return errors.New("keychain locked: " + v) }
	if _, err := s.Start(80, 24); err != nil {
		t.Fatal(err)
	}
	feed(proc, loginPre+testLoginToken+loginAfter)
	d := ev.waitDone(t)
	if d.Stored || !strings.Contains(d.Error, "keychain locked") {
		t.Fatalf("done %+v", d)
	}
	assertNoTokenLeak(t, ev.all())
	if !proc.wasKilled() {
		t.Fatal("child not ended")
	}
}

func TestSandboxLoginTimesOut(t *testing.T) {
	s, proc, ev, _ := newLoginHarness(t)
	old := loginTimeout
	t.Cleanup(func() { loginTimeout = old })
	loginTimeout = 50 * time.Millisecond
	if _, err := s.Start(80, 24); err != nil {
		t.Fatal(err)
	}
	if d := ev.waitDone(t); !strings.Contains(d.Error, "timed out") || !proc.wasKilled() {
		t.Fatalf("done %+v", d)
	}
}

func TestLooksLikeToken(t *testing.T) {
	for text, want := range map[string]bool{
		"Opening browser to sign in…":                                        false,
		"https://claude.ai/x?state=AbCdEfGhIjKlMnOpQrStUvWxYz0123456789AbCd": false,
		"sk-ant": true,
		"  Zq7_Kx9-Lm2Np4Rs6Tv8Wy0Ab1Cd3Ef5Gh7Ij9Kl1Mn3Op5": true,
		"────────────────────────────────────────────────": false,
		"------------------------------------------------": false,
	} {
		if got := looksLikeToken(text); got != want {
			t.Errorf("%q: %v", text, got)
		}
	}
}

func TestTrimRenderedRight(t *testing.T) {
	for in, want := range map[string]string{
		"hello   ":                  "hello",
		"\x1b[1mhi\x1b[m    \x1b[m": "\x1b[1mhi\x1b[m\x1b[m",
		"\x1b]8;;https://x\x1b\\link\x1b]8;;\x1b\\  ": "\x1b]8;;https://x\x1b\\link\x1b]8;;\x1b\\",
		"\x1b[1mhello": "\x1b[1mhello",
	} {
		if got := trimRenderedRight(in); got != want {
			t.Errorf("%q: %q", in, got)
		}
	}
}

// The real PTY path, with a script standing in for claude: the token goes
// through a kernel PTY, Enter reaches the child's stdin, and the run ends.
func TestSandboxLoginRealPTY(t *testing.T) {
	s, _, ev, stored := newLoginHarness(t)
	startLoginProc = startClaudeSetupToken
	dir := t.TempDir()
	script := dir + "/claude"
	body := "#!/bin/sh\n[ \"$1\" = setup-token ] || exit 2\nprintf 'Your OAuth token (valid for 1 year):\\n\\n\\033[1m%s\\033[0m\\n\\nStore this token securely.\\n' '" + testLoginToken + "'\nread _\n"
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	lookClaude = func() (string, error) { return script, nil }
	if _, err := s.Start(80, 24); err != nil {
		t.Fatal(err)
	}
	d := ev.waitDone(t)
	if !d.Stored || len(*stored) != 1 || (*stored)[0] != testLoginToken {
		t.Fatalf("done %+v stored %q", d, *stored)
	}
	assertNoTokenLeak(t, ev.all())
}
