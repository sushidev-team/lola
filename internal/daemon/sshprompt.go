package daemon

// A shell's question, forwarded to the timeline. Two readers find one, both for the human's
// shell tab only: during sign-in the monitor reads the pane each tick, because ssh asks
// before there is any shell to hook, and inside a command the tap sees output that stopped
// mid-line. Neither runs in any other state.
//
// The question is text a remote program printed, so it can be a fake. It reaches a human's
// card as it is, and nothing else. It decides nothing, and no state or block follows from
// it. A pending prompt lives only in this memory. That keeps it out of every file, and a
// daemon restart simply drops it: the human still has the terminal.
//
// The answer is the one secret on any lola path. handleSSHPromptAnswer hands it to the pane
// through tmux.PasteSecret and forgets it. It is never logged, stored or returned.

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/sushidev-team/lola/internal/protocol"
	"github.com/sushidev-team/lola/internal/sshstore"
	"github.com/sushidev-team/lola/internal/sshterm"
)

const (
	// sshPromptQuiet is how long output must rest before its tail is read as a question.
	// A program that prints "password: " and carries on within this is not asking.
	sshPromptQuiet = 250 * time.Millisecond
	sshPromptTail  = 4 << 10
	// A pane answers an echoed line at once. These bound the discard if it never does, so
	// a program that stays silent cannot cost the output after it.
	sshAnswerDropWindow = 3 * time.Second
	sshAnswerDropBytes  = 16 << 10
)

// sshPromptRec is one connection's prompt state.
type sshPromptRec struct {
	pending *protocol.SSHPromptInfo
	word    string // what answers yes
	row     int    // the pane line, in the sign-in stage
	open    bool   // output stopped mid-line, with no question recognized

	// The last answer's question, so the same one coming back reads as "not accepted".
	answered     bool
	answeredStg  string
	answeredBlk  string
	answeredLine string
	answeredRow  int
	attempts     int

	// dropNext tells the tap to discard the line the answer may have echoed.
	dropNext bool

	// signInAsked holds the sign-in questions answered with a secret, so the pane can be
	// checked for an echo once the connection is up. The questions, never the answers.
	signInAsked []string
}

// sshPrompts holds every connection's prompt state. It has a lock of its own so filling a
// connection inside a store callback cannot deadlock against sshState.mu.
type sshPrompts struct {
	mu   sync.Mutex
	recs map[string]*sshPromptRec
}

func (p *sshPrompts) rec(conn string) *sshPromptRec {
	if p.recs == nil {
		p.recs = map[string]*sshPromptRec{}
	}
	r := p.recs[conn]
	if r == nil {
		r = &sshPromptRec{}
		p.recs[conn] = r
	}
	return r
}

// fill puts the live prompt on a connection copy bound for a human.
func (p *sshPrompts) fill(c *protocol.SSHConnInfo) {
	p.mu.Lock()
	defer p.mu.Unlock()
	r := p.recs[c.ID]
	if r == nil {
		return
	}
	if r.pending != nil {
		q := *r.pending
		q.Context = append([]string(nil), r.pending.Context...)
		c.Prompt = &q
	}
	c.TermOpenLine = r.open
}

func (p *sshPrompts) forget(conn string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	delete(p.recs, conn)
}

// forgetStage ends a stage. A connection coming up must not drop a command's prompt that a
// reopened shell may hold, and the reverse.
func (p *sshPrompts) forgetStage(conn, stage string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	r := p.recs[conn]
	if r == nil {
		return
	}
	if r.pending != nil && r.pending.Stage == stage {
		r.pending = nil
	}
	if r.answeredStg == stage {
		r.answered, r.attempts = false, 0
	}
}

// set records a detected question. A question already pending keeps its id, so a redraw
// cannot reset a card someone is typing into. The same question on a new line, after an
// answer, is that answer having failed.
func (p *sshPrompts) set(conn, stage, block, command string, q sshterm.Prompt, row int, now time.Time) {
	p.mu.Lock()
	defer p.mu.Unlock()
	r := p.rec(conn)
	r.open = false
	if c := r.pending; c != nil && c.Stage == stage && c.Block == block && c.Line == q.Line && r.row == row {
		return
	}
	attempt := 0
	if r.answered && r.answeredStg == stage && r.answeredBlk == block && r.answeredLine == q.Line {
		if stage == protocol.SSHPromptSignIn && r.answeredRow == row {
			return // ssh has not read the answer yet
		}
		r.attempts++
		attempt = r.attempts
	} else {
		r.answered, r.attempts = false, 0
	}
	id, err := sshPromptID()
	if err != nil {
		return
	}
	r.row, r.word = row, q.Word
	r.pending = &protocol.SSHPromptInfo{
		ID: id, Stage: stage, Kind: q.Kind, Line: q.Line, Context: q.Context,
		Block: block, Command: command, Attempt: attempt, Since: now,
	}
}

func (p *sshPrompts) clear(conn string, open bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	r := p.recs[conn]
	if r == nil {
		if !open {
			return
		}
		r = p.rec(conn)
	}
	r.pending, r.open = nil, open
}

// claim takes a pending prompt, so two answers cannot both type. The caller puts it back
// with restore when it could not reach the pane.
func (p *sshPrompts) claim(conn, id string) (protocol.SSHPromptInfo, string, int, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	r := p.recs[conn]
	if r == nil || r.pending == nil || r.pending.ID != id {
		return protocol.SSHPromptInfo{}, "", 0, false
	}
	q := *r.pending
	r.pending = nil
	return q, r.word, r.row, true
}

func (p *sshPrompts) restore(conn string, q protocol.SSHPromptInfo) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if r := p.recs[conn]; r != nil && r.pending == nil {
		r.pending = &q
	}
}

func (p *sshPrompts) markAnswered(conn string, q protocol.SSHPromptInfo, row int, arm bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	r := p.rec(conn)
	r.answered, r.answeredStg, r.answeredBlk, r.answeredLine, r.answeredRow = true, q.Stage, q.Block, q.Line, row
	r.dropNext = arm
	if q.Stage == protocol.SSHPromptSignIn && q.Kind == protocol.SSHPromptSecret {
		r.signInAsked = append(r.signInAsked, strings.TrimSpace(q.Line))
	}
}

func (p *sshPrompts) takeSignInAsked(conn string) []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	r := p.recs[conn]
	if r == nil {
		return nil
	}
	asked := r.signInAsked
	r.signInAsked = nil
	return asked
}

func (p *sshPrompts) disarm(conn string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if r := p.recs[conn]; r != nil {
		r.dropNext = false
	}
}

func (p *sshPrompts) takeDrop(conn string) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	r := p.recs[conn]
	if r == nil || !r.dropNext {
		return false
	}
	r.dropNext = false
	return true
}

func sshPromptID() (string, error) {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(b[:]), nil
}

// sshReadSignInPrompt is one read of a connection coming up. A tmux that cannot answer
// changes nothing, and a tab that is not running has no question.
func (d *Daemon) sshReadSignInPrompt(connID, tab string, alive bool) {
	m := d.ssh
	pr, ok := d.sshTmuxClient().(sshPrompter)
	if !ok || tab == "" {
		return
	}
	if !alive {
		m.prompts.clear(connID, false)
		return
	}
	ctx, cancel := context.WithTimeout(m.ctx, sshTabExec)
	defer cancel()
	in, err := pr.PaneInput(ctx, tab)
	if err != nil {
		return
	}
	if !in.Open {
		m.prompts.clear(connID, false)
		return
	}
	q, ok := sshterm.DetectPrompt(in.Text)
	if !ok {
		m.prompts.clear(connID, false)
		return
	}
	m.prompts.set(connID, protocol.SSHPromptSignIn, "", "", q, in.Row, m.now())
}

// sshNoteSignInEcho looks for an answer the pane echoed during sign-in, as a prompt that
// leaves echo on does. There is no tap at sign-in to see it, and the transcript reads the
// pane, sign-in lines included, so this is checked when the connection comes up. It needs no
// copy of the answer: an honest prompt's line ends at the question, and anything after the
// question is what was typed. A pane it cannot read counts as an echo, since the only cost is
// a transcript not offered.
func (d *Daemon) sshNoteSignInEcho(store *sshstore.Store, connID string) {
	asked := d.ssh.prompts.takeSignInAsked(connID)
	if len(asked) == 0 {
		return
	}
	c, ok := sshConnCopy(store, connID)
	tm := d.sshTmuxClient()
	if !ok || tm == nil || c.Tab == "" {
		return
	}
	tab := c.Tab
	ctx, cancel := context.WithTimeout(d.ssh.ctx, sshTabExec)
	defer cancel()
	text, err := tm.CaptureFrom(ctx, tab, -1)
	if err != nil || sshEchoedAnswer(text, asked) {
		_ = store.Update(func(st *sshstore.State) error {
			if c := st.Conn(connID); c != nil {
				c.AnswerEchoTab = tab
			}
			return nil
		})
	}
}

func sshEchoedAnswer(pane string, asked []string) bool {
	for _, line := range strings.Split(pane, "\n") {
		line = sshterm.CleanLine(strings.TrimRight(line, "\r"))
		for _, q := range asked {
			if rest, ok := strings.CutPrefix(strings.TrimSpace(line), q); ok && strings.TrimSpace(rest) != "" {
				return true
			}
		}
	}
	return false
}

// sshPromptGone is the answer for a card that outlived its question. Typing into whatever
// the pane shows now is exactly what this refuses.
var sshPromptGone = errors.New("That question has gone.")

// handleSSHPromptAnswer types a human's answer into their own shell tab. It is a human
// command, so the bridge and phones never reach it, and it has no cli verb because a secret
// must not sit in an argv. Order is the guard: the id is claimed first, so two answers
// cannot both type, and the pane must still show the question at this moment, or a stale
// card would type into a shell.
func (d *Daemon) handleSSHPromptAnswer(ctx context.Context, a protocol.SSHPromptAnswerArgs) (protocol.SSHPromptAnswerData, error) {
	if err := sshValidateAnswer(a); err != nil {
		return protocol.SSHPromptAnswerData{}, err
	}
	store, err := d.sshConnStore(a.Conn)
	if err != nil {
		return protocol.SSHPromptAnswerData{}, sshPromptGone
	}
	c, ok := sshConnCopy(store, a.Conn)
	if !ok {
		return protocol.SSHPromptAnswerData{}, sshPromptGone
	}
	pr, ok := d.sshTmuxClient().(sshPrompter)
	if !ok || c.Tab == "" {
		return protocol.SSHPromptAnswerData{}, sshPromptGone
	}
	m := d.ssh
	q, word, row, ok := m.prompts.claim(a.Conn, a.Prompt)
	if !ok {
		return protocol.SSHPromptAnswerData{}, sshPromptGone
	}
	text := a.Secret
	switch a.Action {
	case protocol.SSHPromptSend:
		if q.Kind != protocol.SSHPromptSecret {
			m.prompts.restore(a.Conn, q)
			return protocol.SSHPromptAnswerData{}, errors.New("This question takes yes or no.")
		}
	case protocol.SSHPromptYes, protocol.SSHPromptNo:
		if q.Kind != protocol.SSHPromptConfirm {
			m.prompts.restore(a.Conn, q)
			return protocol.SSHPromptAnswerData{}, errors.New("This question takes a typed answer.")
		}
		text = word
		if a.Action == protocol.SSHPromptNo {
			text = "n"
			if word == "yes" {
				text = "no"
			}
		}
	}
	if !sshPromptStageLive(store, a.Conn, q) {
		return protocol.SSHPromptAnswerData{}, sshPromptGone
	}
	sctx, cancel := context.WithTimeout(ctx, sshTabExec)
	defer cancel()
	in, err := pr.PaneInput(sctx, c.Tab)
	if err != nil {
		m.prompts.restore(a.Conn, q)
		return protocol.SSHPromptAnswerData{}, errors.New("Lola could not read the terminal.")
	}
	now, seen := sshterm.Prompt{}, false
	if in.Open {
		now, seen = sshterm.DetectPrompt(in.Text)
	}
	if !seen || !sshSamePromptLine(now.Line, q.Line) {
		return protocol.SSHPromptAnswerData{}, sshPromptGone
	}
	if a.Action == protocol.SSHPromptCancel {
		if err := pr.SendInterrupt(sctx, c.Tab); err != nil {
			m.prompts.restore(a.Conn, q)
			return protocol.SSHPromptAnswerData{}, errors.New("Lola could not reach the terminal.")
		}
		if q.Stage == protocol.SSHPromptSignIn {
			d.sshMarkFailed(store, a.Conn, "Sign-in cancelled.")
		}
		return protocol.SSHPromptAnswerData{Status: protocol.SSHPromptCancelled}, nil
	}
	// Armed before the paste, so an echo cannot arrive ahead of the guard.
	m.prompts.markAnswered(a.Conn, q, row, q.Stage == protocol.SSHPromptCommand)
	if err := pr.PasteSecret(sctx, c.Tab, text); err != nil {
		m.prompts.disarm(a.Conn)
		m.prompts.restore(a.Conn, q)
		// tmux's message names no input, and this one names nothing at all, because an
		// error is the one thing every caller prints.
		d.logf("", "ssh: %s: could not answer a prompt", a.Conn)
		return protocol.SSHPromptAnswerData{}, errors.New("Lola could not reach the terminal.")
	}
	if q.Stage == protocol.SSHPromptSignIn {
		d.sshStartMonitor(store, a.Conn) // the next question or the master is a moment away
	}
	return protocol.SSHPromptAnswerData{Status: protocol.SSHPromptSent}, nil
}

// sshValidateAnswer refuses what could do more than answer. A control character could
// submit early and turn the rest of the text into a command if the question was a fake.
func sshValidateAnswer(a protocol.SSHPromptAnswerArgs) error {
	switch a.Action {
	case protocol.SSHPromptSend:
		if a.Secret == "" {
			return errors.New("There is nothing to send.")
		}
		if len(a.Secret) > protocol.SSHMaxPromptSecretBytes {
			return errors.New("That answer is too long.")
		}
		if !utf8.ValidString(a.Secret) || strings.IndexFunc(a.Secret, unicode.IsControl) >= 0 {
			return errors.New("That answer holds a character a terminal would act on. Use the terminal.")
		}
	case protocol.SSHPromptYes, protocol.SSHPromptNo, protocol.SSHPromptCancel:
		if a.Secret != "" {
			return errors.New("This action takes no text.")
		}
	default:
		return errors.New("Unknown prompt action.")
	}
	return nil
}

// sshPromptStageLive re-reads the record: the stage the question belongs to must still be
// running.
func sshPromptStageLive(store *sshstore.Store, conn string, q protocol.SSHPromptInfo) bool {
	live := false
	store.View(func(st *sshstore.State) {
		c := st.Conn(conn)
		if c == nil || !c.TabAlive {
			return
		}
		switch q.Stage {
		case protocol.SSHPromptSignIn:
			live = c.State == protocol.SSHConnConnecting || c.State == protocol.SSHConnAuthenticating
		case protocol.SSHPromptCommand:
			if it := st.Item(q.Block); it != nil && it.Block != nil {
				live = it.Block.Status == protocol.SSHBlockRunning
			}
		}
	})
	return live
}

// sshSamePromptLine compares what the tail said with what the screen shows, which may
// differ in spacing only.
func sshSamePromptLine(a, b string) bool {
	return strings.Join(strings.Fields(a), " ") == strings.Join(strings.Fields(b), " ")
}

// sshTapPrompt is the tap reader's prompt state. A timer reads the tail, so it has a lock.
// Holding it across prompts.set is what keeps a timer that lost the race with a block's end
// from leaving a card behind for a command that finished.
type sshTapPrompt struct {
	mu      sync.Mutex
	live    bool
	block   string
	command string
	tail    []byte
	timer   *time.Timer

	// Discarding the line an answer may have echoed.
	dropping  bool
	dropUntil time.Time
	dropped   int
	echoed    bool
}

func (r *sshTapReader) promptStart(block, command string) {
	t := &r.prompt
	t.mu.Lock()
	defer t.mu.Unlock()
	t.live, t.block, t.command, t.tail, t.dropping = true, block, sshClean(command, 120), nil, false
}

func (r *sshTapReader) promptEnd() {
	t := &r.prompt
	t.mu.Lock()
	defer t.mu.Unlock()
	t.live, t.tail = false, nil
	if t.timer != nil {
		t.timer.Stop()
	}
	r.d.ssh.prompts.clear(r.conn.ID, false)
	r.d.ssh.prompts.disarm(r.conn.ID)
}

// promptOutput keeps the tail and waits for output to rest before reading it.
func (r *sshTapReader) promptOutput(p []byte) {
	t := &r.prompt
	t.mu.Lock()
	defer t.mu.Unlock()
	t.tail = append(t.tail, p...)
	if n := len(t.tail); n > sshPromptTail {
		t.tail = append(t.tail[:0], t.tail[n-sshPromptTail:]...)
	}
	if t.timer != nil {
		t.timer.Reset(sshPromptQuiet)
		return
	}
	t.timer = time.AfterFunc(sshPromptQuiet, r.promptRead)
}

func (r *sshTapReader) promptRead() {
	t := &r.prompt
	t.mu.Lock()
	defer t.mu.Unlock()
	if !t.live {
		return
	}
	m := r.d.ssh
	tail := string(t.tail)
	q, ok := sshterm.DetectPrompt(tail)
	if !ok {
		m.prompts.clear(r.conn.ID, sshterm.OpenLine(tail))
		return
	}
	m.prompts.set(r.conn.ID, protocol.SSHPromptCommand, t.block, t.command, q, 0, m.now())
}

// dropAnswerLine removes the line an answer may have echoed, before it can reach the sink,
// the preview or the tail. An honest prompt turns echo off and prints only a newline, so the
// first line after an answer is either empty or the answer. It cannot catch a program that
// prints the answer back later: such a program already has it. docs/ssh-sessions.md says so.
func (r *sshTapReader) dropAnswerLine(p []byte) []byte {
	t := &r.prompt
	t.mu.Lock()
	defer t.mu.Unlock()
	if r.d.ssh.prompts.takeDrop(r.conn.ID) {
		t.dropping, t.dropUntil, t.dropped, t.echoed, t.tail = true, time.Now().Add(sshAnswerDropWindow), 0, false, nil
	}
	if !t.dropping {
		return p
	}
	if time.Now().After(t.dropUntil) || t.dropped > sshAnswerDropBytes {
		t.dropping = false
		return p
	}
	line, rest := p, []byte(nil)
	done := false
	for i, b := range p {
		if b == '\n' {
			line, rest, done = p[:i], p[i+1:], true
			break
		}
	}
	t.dropped += len(line)
	t.echoed = t.echoed || sshterm.CleanLine(strings.ReplaceAll(string(line), "\r", "")) != ""
	if done {
		t.dropping = false
		if t.echoed {
			r.noteAnswerEchoed()
		}
	}
	return rest
}

// noteAnswerEchoed records that this tab's history may hold an answer.
func (r *sshTapReader) noteAnswerEchoed() {
	_ = r.store.Update(func(st *sshstore.State) error {
		if c := st.Conn(r.conn.ID); c != nil {
			c.AnswerEchoTab = c.Tab
		}
		return nil
	})
}
