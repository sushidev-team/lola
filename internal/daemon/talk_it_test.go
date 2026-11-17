package daemon

// An opt-in integration test of the conversation commands against a REAL tmux server and a
// fake agent that draws what Claude Code draws. Everything else in this package fakes the
// pane, so nothing else would notice bytes that do not survive tmux, a cursor that is not
// where the proof expects it, or a hook that fires in another order.
//
// Run it with:
//
//	LOLA_TALK_IT=1 go test ./internal/daemon -run TestTalkIT -v
//
// It never starts a real coding agent. The tmux server has a private socket for each test,
// named through [tmux].socket_name, which production's tmuxClient reads, so the daemon's own
// seams are exercised unchanged and tmux -L lola is never spoken to. LOLA_HOME and
// CLAUDE_CONFIG_DIR both point into a temporary directory.

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sushidev-team/lola/internal/linear"
	"github.com/sushidev-team/lola/internal/protocol"
	"github.com/sushidev-team/lola/internal/session"
	"github.com/sushidev-team/lola/internal/sshstore"
	"github.com/sushidev-team/lola/internal/state"
	"github.com/sushidev-team/lola/internal/tmux"
	"github.com/sushidev-team/lola/internal/usage"
)

const itAgentSession = "11111111-1111-4111-8111-111111111111"

// itFakeAgent is the fake agent. The main loop reads one FIFO that carries both the terminal's
// bytes and a tick, because bash 3.2's read has no sub-second timeout and forces its own
// terminal settings on a tty. It records what it received byte for byte, and writes a
// transcript in the record shapes measured from real files.
//
// A message starting with one of these words picks what it does next. "work:<ticks>" works
// that many tenths of a second, "perm" asks a permission question, "modal" opens a dialog,
// and anything else works briefly and answers.
const itFakeAgent = `#!/usr/bin/env bash
export LC_ALL=C
dir="$FAKE_DIR"; log="$FAKE_LOG"
mkdir -p "$(dirname "$log")"
cd "$FAKE_CWD" || exit 1
fifo="$dir/in.fifo"; rm -f "$fifo"; mkfifo "$fifo"
stty -icanon -echo -isig -icrnl -inlcr -ixon min 1 time 0
printf '\033[?2004h'

exec 4<>"$fifo"
cat <&0 >&4 &
catpid=$!
( me=$$; while kill -0 "$me" 2>/dev/null; do printf '\002'; sleep 0.1; done ) >&4 &
tickpid=$!
trap 'kill $catpid $tickpid 2>/dev/null' EXIT HUP TERM

ESC=$(printf '\033'); CR=$(printf '\r'); TICK=$(printf '\002'); KILL=$(printf '\025'); NL='
'
rule=$(printf '─%.0s' 1 2 3 4 5 6 7 8 9 10 11 12 13 14 15 16 17 18 19 20 21 22 23 24 25 26 27 28 29 30 31 32 33 34 35 36 37 38 39 40 41 42 43 44 45 46 47 48 49 50 51 52 53 54 55 56 57 58 59 60)
dash=$(printf '╌%.0s' 1 2 3 4 5 6 7 8 9 10 11 12 13 14 15 16 17 18 19 20 21 22 23 24 25 26 27 28 29 30 31 32 33 34 35 36 37 38 39 40 41 42 43 44 45 46 47 48 49 50 51 52 53 54 55 56 57 58 59 60)
over=$(printf '▔%.0s' 1 2 3 4 5 6 7 8 9 10 11 12 13 14 15 16 17 18 19 20 21 22 23 24 25 26 27 28 29 30 31 32 33 34 35 36 37 38 39 40 41 42 43 44 45 46 47 48 49 50 51 52 53 54 55 56 57 58 59 60)

state=rest; draft=""; shown=""; esc=""; esct=0; inpaste=0; paste=""; pastes=0; wire=""
n=0; rec=0; work=0; pending=""; dirty=0; last="Ready."; spin=0; toolid=""

event() { printf '%s\n' "$1" >> "$dir/events"; }
stamp() { date -u +%Y-%m-%dT%H:%M:%SZ; }
# A function called in $(...) cannot move a counter, so the number comes from the file's own length.
uuid() { printf '00000000-0000-4000-8000-%012x' "$(( $(wc -l < "$log" 2>/dev/null || echo 0) + 1 ))"; }
jq_s() {
  local s="$1"
  s=${s//\\/\\\\}; s=${s//\"/\\\"}; s=${s//$NL/\\n}; s=${s//$CR/\\r}; s=${s//	/\\t}
  printf '%s' "$s"
}
user_rec() { printf '{"type":"user","uuid":"%s","timestamp":"%s","cwd":"%s","message":{"role":"user","content":"%s"}}\n' "$(uuid)" "$(stamp)" "$FAKE_CWD" "$(jq_s "$1")" >> "$log"; }
text_rec() { printf '{"type":"assistant","uuid":"%s","timestamp":"%s","cwd":"%s","message":{"model":"fake","content":[{"type":"text","text":"%s"}]}}\n' "$(uuid)" "$(stamp)" "$FAKE_CWD" "$(jq_s "$1")" >> "$log"; }
tool_rec() { printf '{"type":"assistant","uuid":"%s","timestamp":"%s","cwd":"%s","message":{"model":"fake","content":[{"type":"tool_use","id":"%s","name":"Bash","input":{"command":"%s"}}]}}\n' "$(uuid)" "$(stamp)" "$FAKE_CWD" "$1" "$(jq_s "$2")" >> "$log"; }
result_rec() { printf '{"type":"user","uuid":"%s","timestamp":"%s","cwd":"%s","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"%s","content":"%s","is_error":%s}]}}\n' "$(uuid)" "$(stamp)" "$FAKE_CWD" "$1" "$(jq_s "$2")" "$3" >> "$log"; }

# One write per frame, as a real TUI renderer does, so a capture never lands on half a screen.
draw() { local frame; frame=$(frame); printf '%s' "$frame"; }
frame() {
  printf '\033[?25h\033[2J\033[H'
  printf '\033[1;1HFake agent'
  printf '\033[3;1H%s' "$last"
  case "$state" in
    work)
      printf '\033[8;1H✻ Harmonizing… (%ss · ↓ 17.9k tokens)' "$((spin/10))"
      ;;
    rest)
      printf '\033[8;1H✻ Cogitated for 13s'
      ;;
    perm)
      printf '\033[?25l\033[6;1H%s' "$rule"
      printf '\033[7;1H Bash command'
      printf '\033[9;1H%s' "$dash"
      printf '\033[10;1H touch perm-a.txt'
      printf '\033[11;1H%s' "$dash"
      printf '\033[13;1H Do you want to proceed?'
      printf '\033[14;1H ❯ 1. Yes'
      printf '\033[15;1H   2. Yes, and always allow access to this folder'
      printf '\033[16;1H   3. No'
      printf '\033[18;1H Esc to cancel · Tab to amend'
      return
      ;;
    modal)
      printf '\033[?25l\033[6;1H%s' "$over"
      printf '\033[7;1H Some settings dialog'
      printf '\033[9;1H Esc to cancel'
      return
      ;;
  esac
  printf '\033[9;1H%s' "$rule"
  printf '\033[10;1H❯\302\240%s' "$shown"
  printf '\033[11;1H%s' "$rule"
  printf '\033[12;1H  ⏵⏵ auto mode on (shift+tab to cycle)'
  printf '\033[10;%dH' "$((3 + ${#shown}))"
}

rest() {
  state=rest; last="$1"; shown=""
  text_rec "$1"
  draw
  event stop
}

submit() {
  local plain="$draft" logged="$wire"
  n=$((n+1))
  printf '%s' "$plain" > "$dir/received.$n"
  draft=""; shown=""; wire=""; pastes=0
  user_rec "$logged"
  event user_prompt
  case "$plain" in
    work:*) state=work; work=${plain#work:}; work=${work%%[!0-9]*}; pending="Finished the work."; spin=0; draw ;;
    perm*) state=perm; toolid="toolu_$n"; tool_rec "$toolid" "touch perm-a.txt"; draw; event notification ;;
    modal*) state=modal; draw ;;
    *) state=work; work=8; pending="Got it, $(printf '%s' "$plain" | wc -c | tr -d ' ') bytes."; spin=0; draw ;;
  esac
}

on_esc() {
  printf 'ESC %s\n' "$state" >> "$dir/keys"
  case "$state" in
    work)
      user_rec "[Request interrupted by user]"
      state=rest; last="Interrupted."; draw
      ;;
    perm)
      result_rec "$toolid" "The user doesn't want to proceed with this tool use." true
      user_rec "[Request interrupted by user for tool use]"
      state=rest; last="Rejected."; draw
      ;;
    modal) state=rest; last="Dialog closed."; draw ;;
  esac
}

text_byte() {
  local c="$1"
  case "$state" in
    rest)
      if [ "$c" = "$CR" ]; then
        [ -n "$draft" ] && submit
        return
      fi
      if [ "$c" = "$KILL" ]; then
        draft=""; wire=""; shown=""; dirty=1
        return
      fi
      draft="$draft$c"; wire="$wire$c"; shown="$shown$c"; dirty=1
      ;;
    perm)
      printf '%s\n' "$c" >> "$dir/keys"
      case "$c" in
        1|2)
          result_rec "$toolid" "ran it" false
          state=work; work=6; spin=0; pending="Touched the file."; last="Allowed."; draw
          ;;
        3)
          result_rec "$toolid" "The user doesn't want to proceed with this tool use." true
          user_rec "[Request interrupted by user for tool use]"
          state=rest; last="Rejected."; draw
          ;;
      esac
      ;;
    modal) printf '%s\n' "$c" >> "$dir/keys" ;;
    work)
      # Nothing lola types may land here: the gate must have held it back.
      printf '%s' "$c" >> "$dir/midturn"
      ;;
  esac
}

on_tick() {
  if [ "$esc" = "$ESC" ]; then
    esct=$((esct+1))
    if [ "$esct" -ge 2 ]; then esc=""; [ "$inpaste" = 1 ] || on_esc; fi
  fi
  if [ "$state" = work ]; then
    spin=$((spin+1))
    work=$((work-1))
    if [ "$work" -le 0 ]; then rest "$pending"; else [ $((spin%10)) -eq 0 ] && draw; fi
  fi
  if [ "$dirty" = 1 ]; then dirty=0; draw; fi
}

end_paste() {
  inpaste=0
  pastes=$((pastes+1))
  draft="$draft$paste"
  # The shape measured from real logs. The wrapper reaches the log, never the screen.
  if [ ${#paste} -gt 800 ]; then
    wire="$wire$NL$NL<pasted_content id=\"abcd$pastes\">$NL$paste$NL</pasted_content id=\"abcd$pastes\">$NL"
    shown="$shown[Pasted text #$pastes]"
  else
    wire="$wire$paste"
    shown="$shown[Pasted]"
  fi
  paste=""; dirty=1
}

draw
event start
while IFS= read -r -n1 -d '' c; do
  if [ "$c" = "$TICK" ]; then on_tick; continue; fi
  if [ -n "$esc" ] || [ "$c" = "$ESC" ]; then
    esc="$esc$c"; esct=0
    case "$esc" in
      "$ESC[200~") esc=""; inpaste=1; paste=""; continue ;;
      "$ESC[201~") esc=""; end_paste; continue ;;
      "$ESC"|"$ESC["|"$ESC[2"|"$ESC[20"|"$ESC[200"|"$ESC[201") continue ;;
    esac
    # Not a sequence this agent knows. Inside a paste its bytes are text.
    if [ "$inpaste" = 1 ]; then paste="$paste$esc"; fi
    esc=""
    continue
  fi
  if [ "$inpaste" = 1 ]; then
    [ "$c" = "$CR" ] && c="$NL"
    paste="$paste$c"
    continue
  fi
  text_byte "$c"
done <&4
`

// itLogWriter puts the daemon's log under the test, where a failure shows it.
type itLogWriter struct{ t *testing.T }

func (w itLogWriter) Write(p []byte) (int, error) {
	defer func() { _ = recover() }() // a goroutine may outlive the test by a moment
	w.t.Logf("daemon: %s", strings.TrimRight(string(p), "\n"))
	return len(p), nil
}

type talkRig struct {
	t    *testing.T
	d    *Daemon
	id   string
	dir  string
	tm   *tmux.Client
	path string
}

func newTalkRig(t *testing.T) *talkRig {
	t.Helper()
	if os.Getenv("LOLA_TALK_IT") != "1" {
		t.Skip("set LOLA_TALK_IT=1 to run against a real tmux server")
	}
	for _, bin := range []string{"tmux", "bash"} {
		if _, err := exec.LookPath(bin); err != nil {
			t.Skipf("no %s", bin)
		}
	}
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("LOLA_HOME", home)
	claudeDir := filepath.Join(home, "claude")
	t.Setenv("CLAUDE_CONFIG_DIR", claudeDir)

	var r [6]byte
	_, _ = rand.Read(r[:])
	sock := "lola-talk-it-" + hex.EncodeToString(r[:])
	cfg := nativeTestConfig(nativePoll("p1"))
	cfg.Tmux.SocketName = sock

	// newDaemon rather than newTestDaemon: the point is production's seams, unchanged.
	d := newDaemon(cfg, &linear.Fake{}, log.New(itLogWriter{t}, "", 0), home)
	d.native = &fakeNative{}
	d.runtimeHealth = func(string) error { return nil }
	d.checkpoints = nil
	if got := d.tmuxClient().SocketName; got != sock {
		t.Fatalf("the daemon would talk to tmux -L %s, want %s", got, sock)
	}
	if usage.ProjectsRoot() != filepath.Join(claudeDir, "projects") {
		t.Fatalf("projects root escaped the temp dir: %s", usage.ProjectsRoot())
	}
	d.startTimeline()
	t.Cleanup(func() {
		d.stopTimeline()
		d.stopSSH()
	})

	id := "lola-p1-it-1"
	worktree := filepath.Join(home, "worktrees", "p1", id)
	if err := os.MkdirAll(worktree, 0o755); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	script := filepath.Join(dir, "agent.sh")
	if err := os.WriteFile(script, []byte(itFakeAgent), 0o755); err != nil {
		t.Fatal(err)
	}
	transcript := filepath.Join(usage.SlugDir(usage.ProjectsRoot(), worktree), itAgentSession+".jsonl")
	if err := os.MkdirAll(filepath.Dir(transcript), 0o755); err != nil {
		t.Fatal(err)
	}
	tm := d.tmuxClient()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := fmt.Sprintf("FAKE_DIR=%q FAKE_LOG=%q FAKE_CWD=%q exec bash %q", dir, transcript, worktree, script)
	if err := tm.NewSessionSized(ctx, id, worktree, cmd, 100, 30); err != nil {
		t.Fatalf("new session: %v", err)
	}
	t.Cleanup(func() {
		_ = exec.Command("tmux", "-L", sock, "kill-server").Run()
	})

	s := session.Session{ID: id, Source: "native", Project: "p1", Issue: "IT-1", TmuxName: id, Agent: "claude"}
	s.SetAgentState(state.AgentStarting, "", time.Now())
	d.sessions.Upsert(s)

	rig := &talkRig{t: t, d: d, id: id, dir: dir, tm: tm, path: transcript}
	rig.pumpHooks()
	rig.waitFor("the agent to draw", func() bool { return strings.Contains(rig.pane(), "Fake agent") })
	return rig
}

// pumpHooks forwards what the fake agent reports, as `lola hook` would. Esc reports nothing,
// which is also what the real one does.
func (r *talkRig) pumpHooks() {
	done := make(chan struct{})
	stopped := make(chan struct{})
	r.t.Cleanup(func() {
		close(done)
		<-stopped
		r.d.drainConnWork()
	})
	go func() {
		defer close(stopped)
		seen := 0
		for {
			select {
			case <-done:
				return
			case <-time.After(20 * time.Millisecond):
			}
			raw, _ := os.ReadFile(filepath.Join(r.dir, "events"))
			lines := strings.Split(strings.TrimRight(string(raw), "\n"), "\n")
			if len(raw) == 0 {
				lines = nil
			}
			for ; seen < len(lines); seen++ {
				if lines[seen] == "start" {
					continue
				}
				hook := &protocol.HookPayload{AgentSessionID: itAgentSession, TranscriptPath: r.path}
				if lines[seen] == "notification" {
					hook.Message = "Claude needs your permission to use Bash"
					hook.Reason = "permission_prompt"
				}
				r.d.handle(context.Background(), protocol.Request{Cmd: "hookEvent", Session: r.id, Event: lines[seen], Hook: hook})
			}
		}
	}()
}

func (r *talkRig) waitFor(what string, ok func() bool) {
	r.t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if ok() {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	raw, _ := os.ReadFile(r.path)
	r.t.Fatalf("timed out waiting for %s\npane:\n%s\ntranscript:\n%s", what, r.pane(), raw)
}

func (r *talkRig) pane() string {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	out, _ := r.tm.CaptureVisible(ctx, r.id)
	return out
}

func talkCall[R any](r *talkRig, cmd string, args any) (R, error) {
	r.t.Helper()
	var out R
	raw, err := json.Marshal(args)
	if err != nil {
		r.t.Fatal(err)
	}
	resp := r.d.handle(context.Background(), protocol.Request{Cmd: cmd, Args: raw})
	if !resp.OK {
		return out, fmt.Errorf("%s", resp.Error)
	}
	if len(resp.Data) > 0 {
		if err := json.Unmarshal(resp.Data, &out); err != nil {
			r.t.Fatalf("%s: %v", cmd, err)
		}
	}
	return out, nil
}

func (r *talkRig) state() protocol.TalkStateData {
	r.t.Helper()
	got, err := talkCall[protocol.TalkStateData](r, protocol.CmdTalkState, protocol.TalkStateArgs{Session: r.id})
	if err != nil {
		r.t.Fatalf("talkState: %v", err)
	}
	return got
}

func (r *talkRig) say(text string) protocol.AgentSayData {
	r.t.Helper()
	got, err := talkCall[protocol.AgentSayData](r, protocol.CmdAgentSay, protocol.AgentSayArgs{Session: r.id, Text: text})
	if err != nil {
		r.t.Fatalf("agentSay: %v", err)
	}
	return got
}

func (r *talkRig) waitComposer(want string) {
	r.t.Helper()
	last := ""
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if last = r.state().Composer; last == want {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	r.t.Fatalf("composer %q, want %q\npane:\n%s", last, want, r.pane())
}

// received is what the fake agent got as its n-th message, byte for byte.
func (r *talkRig) received(n int) string {
	r.t.Helper()
	path := filepath.Join(r.dir, fmt.Sprintf("received.%d", n))
	r.waitFor(fmt.Sprintf("message %d", n), func() bool {
		_, err := os.Stat(path)
		return err == nil
	})
	raw, err := os.ReadFile(path)
	if err != nil {
		r.t.Fatal(err)
	}
	return string(raw)
}

func (r *talkRig) receivedCount() int {
	m, _ := filepath.Glob(filepath.Join(r.dir, "received.*"))
	return len(m)
}

func (r *talkRig) keys() []string {
	raw, _ := os.ReadFile(filepath.Join(r.dir, "keys"))
	if len(raw) == 0 {
		return nil
	}
	return strings.Split(strings.TrimRight(string(raw), "\n"), "\n")
}

func (r *talkRig) midturn() string {
	raw, _ := os.ReadFile(filepath.Join(r.dir, "midturn"))
	return string(raw)
}

func (r *talkRig) feed() protocol.AgentFeedData {
	r.t.Helper()
	got, err := talkCall[protocol.AgentFeedData](r, protocol.CmdAgentFeed, protocol.AgentFeedArgs{Session: r.id})
	if err != nil {
		r.t.Fatalf("agentFeed: %v", err)
	}
	return got
}

func (r *talkRig) waitRows(what string, ok func([]protocol.AgentFeedItem) bool) []protocol.AgentFeedItem {
	r.t.Helper()
	var items []protocol.AgentFeedItem
	r.waitFor(what, func() bool {
		items = r.feed().Items
		return ok(items)
	})
	return items
}

func itKinds(items []protocol.AgentFeedItem) string {
	var out []string
	for _, it := range items {
		k := it.Kind
		if it.Tool != nil {
			k += ":" + it.Tool.Status
		}
		out = append(out, k)
	}
	return strings.Join(out, " ")
}

func itHasKind(items []protocol.AgentFeedItem, kind string) bool {
	for _, it := range items {
		if it.Kind == kind {
			return true
		}
	}
	return false
}

// A short message at rest arrives exactly, is marked delivered and comes back as an echo.
func TestTalkITSayAtRest(t *testing.T) {
	r := newTalkRig(t)
	r.waitComposer(protocol.ComposerResting)
	got := r.say("hello there, can you check this?")
	if !got.Delivered {
		t.Fatalf("not delivered: %+v\npane:\n%s", got, r.pane())
	}
	if text := r.received(1); text != "hello there, can you check this?" {
		t.Fatalf("received %q", text)
	}
	items := r.waitRows("the answer", func(items []protocol.AgentFeedItem) bool {
		return itHasKind(items, protocol.FeedItemText)
	})
	if items[0].Kind != protocol.FeedItemUser {
		t.Fatalf("rows: %s", itKinds(items))
	}
	// The send is recorded on the timeline worker, so its echo mark can land a moment later.
	r.waitRows("the echo mark", func(items []protocol.AgentFeedItem) bool { return items[0].Echo })
}

// 2 KB of many lines goes through tmux's paste path and must arrive with every byte and line
// break intact, and be recognized in the log through the paste wrapper.
func TestTalkITSayMultiLine2KB(t *testing.T) {
	r := newTalkRig(t)
	r.waitComposer(protocol.ComposerResting)
	var b strings.Builder
	for i := 0; b.Len() < 2048; i++ {
		fmt.Fprintf(&b, "line %03d: 'single' \"double\" $HOME `tick` \\back *glob* ümläut 日本語 ✓\n", i)
		if i%7 == 6 {
			b.WriteString("\n")
		}
	}
	text := strings.TrimSpace(b.String())
	got := r.say(text)
	if !got.Delivered {
		t.Fatalf("not delivered: %+v\npane:\n%s", got, r.pane())
	}
	want := humanText(text)
	if have := r.received(1); have != want {
		t.Fatalf("not exact: %d bytes, want %d\n%s", len(have), len(want), firstDiff(have, want))
	}
	items := r.waitRows("the answer", func(items []protocol.AgentFeedItem) bool {
		return itHasKind(items, protocol.FeedItemText)
	})
	user := items[0]
	if user.Kind != protocol.FeedItemUser {
		t.Fatalf("rows: %s", itKinds(items))
	}
	if strings.Contains(user.Text, "pasted_content") {
		t.Errorf("the wrapper reached the row")
	}
	if user.Text != want {
		t.Errorf("row text differs\n%s", firstDiff(user.Text, want))
	}
	r.waitRows("the echo mark", func(items []protocol.AgentFeedItem) bool { return items[0].Echo })
}

// A multi-line message under the paste threshold takes send-keys -l, the other path.
func TestTalkITSayShortMultiLine(t *testing.T) {
	r := newTalkRig(t)
	r.waitComposer(protocol.ComposerResting)
	text := "first line\nsecond line\n\nfourth line"
	if got := r.say(text); !got.Delivered {
		t.Fatalf("not delivered: %+v", got)
	}
	if have := r.received(1); have != text {
		t.Fatalf("received %q", have)
	}
}

// A leading ! must not reach the composer as bash mode, even through the human path.
func TestTalkITSayBangIsDefused(t *testing.T) {
	r := newTalkRig(t)
	r.waitComposer(protocol.ComposerResting)
	if got := r.say("!rm -rf nothing"); !got.Delivered {
		t.Fatalf("not delivered: %+v", got)
	}
	if have := r.received(1); strings.HasPrefix(have, "!") {
		t.Fatalf("a leading ! reached the prompt: %q", have)
	}
	items := r.waitRows("the answer", func(items []protocol.AgentFeedItem) bool {
		return itHasKind(items, protocol.FeedItemText)
	})
	if items[0].Kind != protocol.FeedItemUser {
		t.Fatalf("rows: %s", itKinds(items))
	}
	r.waitRows("the echo mark", func(items []protocol.AgentFeedItem) bool { return items[0].Echo })
}

// While the agent works a message queues, nothing reaches the pane, and the Stop hook flushes it.
func TestTalkITSayWhileWorkingQueuesAndFlushes(t *testing.T) {
	r := newTalkRig(t)
	r.waitComposer(protocol.ComposerResting)
	if got := r.say("work:30"); !got.Delivered {
		t.Fatalf("not delivered: %+v", got)
	}
	r.waitComposer(protocol.ComposerWorking)

	got := r.say("do this next")
	if got.Delivered || got.Why == "" {
		t.Fatalf("a message said mid-turn: %+v", got)
	}
	time.Sleep(500 * time.Millisecond)
	if n := r.receivedCount(); n != 1 {
		t.Fatalf("%d messages reached the agent while it worked", n)
	}
	if m := r.midturn(); m != "" {
		t.Fatalf("%d bytes reached the pane mid-turn", len(m))
	}

	if have := r.received(2); have != "do this next" {
		t.Fatalf("flushed %q", have)
	}
	if m := r.midturn(); m != "" {
		t.Fatalf("%d bytes reached the pane mid-turn", len(m))
	}
	store, err := r.d.sshStore(r.id, false)
	if err != nil || store == nil {
		t.Fatalf("store: %v", err)
	}
	r.waitFor("the item to read delivered", func() bool { return sshItemDelivered(store, got.Item) })
	store.View(func(st *sshstore.State) {
		if len(st.Queue) != 0 {
			t.Errorf("%d entries left in the queue", len(st.Queue))
		}
	})
}

// Stop sends exactly one Esc to a working pane, holds the queue, and sends nothing at rest.
func TestTalkITStop(t *testing.T) {
	r := newTalkRig(t)
	r.waitComposer(protocol.ComposerResting)
	if got := r.say("work:200"); !got.Delivered {
		t.Fatalf("not delivered: %+v", got)
	}
	r.waitComposer(protocol.ComposerWorking)
	if got := r.say("after the stop"); got.Delivered {
		t.Fatalf("delivered mid-turn: %+v", got)
	}

	stop, err := talkCall[protocol.AgentStopData](r, protocol.CmdAgentStop, protocol.AgentStopArgs{Session: r.id})
	if err != nil {
		t.Fatal(err)
	}
	if !stop.Stopped || stop.Held != 1 {
		t.Fatalf("stop: %+v", stop)
	}
	r.waitFor("the Esc", func() bool { return len(r.keys()) == 1 })
	if k := r.keys()[0]; k != "ESC work" {
		t.Fatalf("key %q", k)
	}
	r.waitComposer(protocol.ComposerResting)
	if s := r.state(); !s.QueueHeld {
		t.Errorf("queue not held after a stop: %+v", s)
	}

	// The row says so, and the turn's rows stop at it.
	r.waitRows("the interrupt", func(items []protocol.AgentFeedItem) bool {
		return itHasKind(items, protocol.FeedItemNotice)
	})

	// At rest an Esc means something else, so a second Stop sends none.
	time.Sleep(stopDebounce)
	again, err := talkCall[protocol.AgentStopData](r, protocol.CmdAgentStop, protocol.AgentStopArgs{Session: r.id})
	if err != nil {
		t.Fatal(err)
	}
	if again.Stopped {
		t.Errorf("second stop: %+v", again)
	}
	time.Sleep(300 * time.Millisecond)
	if k := r.keys(); len(k) != 1 {
		t.Errorf("keys after a stop at rest: %v", k)
	}

	// Nothing automated types during the hold, whatever the flush paths try.
	r.d.flushReviewHandoffs(context.Background(), r.id)
	time.Sleep(300 * time.Millisecond)
	if n := r.receivedCount(); n != 1 {
		t.Fatalf("%d messages after a held flush", n)
	}

	rel, err := talkCall[protocol.AgentSayData](r, protocol.CmdAgentQueueRelease, protocol.AgentQueueReleaseArgs{Session: r.id})
	if err != nil {
		t.Fatal(err)
	}
	if !rel.Delivered {
		t.Fatalf("release: %+v\npane:\n%s", rel, r.pane())
	}
	if have := r.received(2); have != "after the stop" {
		t.Fatalf("released %q", have)
	}
}

// Stop & send interrupts, waits for the pane, and delivers in the same press.
func TestTalkITStopAndSend(t *testing.T) {
	r := newTalkRig(t)
	r.waitComposer(protocol.ComposerResting)
	if got := r.say("work:200"); !got.Delivered {
		t.Fatalf("not delivered: %+v", got)
	}
	r.waitComposer(protocol.ComposerWorking)
	got, err := talkCall[protocol.AgentSayData](r, protocol.CmdAgentSay, protocol.AgentSayArgs{Session: r.id, Text: "change of plan", StopFirst: true})
	if err != nil {
		t.Fatal(err)
	}
	if !got.Delivered {
		t.Fatalf("stop and send: %+v\npane:\n%s", got, r.pane())
	}
	if have := r.received(2); have != "change of plan" {
		t.Fatalf("received %q", have)
	}
	if m := r.midturn(); m != "" {
		t.Fatalf("%d bytes landed mid-turn: the Esc had not settled", len(m))
	}
}

// A printed permission prompt becomes a card with the pane's own choices, a stale hash is
// refused, and one press is one digit with no Enter.
func TestTalkITPermission(t *testing.T) {
	r := newTalkRig(t)
	r.waitComposer(protocol.ComposerResting)
	if got := r.say("perm please"); !got.Delivered {
		t.Fatalf("not delivered: %+v", got)
	}
	r.waitComposer(protocol.ComposerPrompt)
	st := r.state()
	if st.Prompt == nil || st.Prompt.Kind != "permission" {
		t.Fatalf("prompt: %+v\npane:\n%s", st.Prompt, r.pane())
	}
	if len(st.Prompt.Choices) != 3 || st.Prompt.Choices[0].Key != "1" || st.Prompt.Choices[2].Label != "No" {
		t.Fatalf("choices: %+v", st.Prompt.Choices)
	}
	if !strings.Contains(strings.Join(st.Prompt.Lines, "\n"), "touch perm-a.txt") {
		t.Fatalf("the card does not say what it asks: %q", st.Prompt.Lines)
	}
	s, _ := r.d.sessions.Get(r.id)
	if s.AgentState != state.AgentWaitingInput || s.InputReason != state.InputPermission {
		t.Errorf("axis %s/%s", s.AgentState, s.InputReason)
	}

	// Typing into a question is refused, and stays queued.
	said := r.say("are you there")
	if said.Delivered {
		t.Fatalf("typed into a question: %+v", said)
	}

	stale, err := talkCall[protocol.AgentChooseData](r, protocol.CmdAgentChoose, protocol.AgentChooseArgs{Session: r.id, Hash: "0000", Key: "1"})
	if err != nil {
		t.Fatal(err)
	}
	if stale.Sent || stale.Why == "" || stale.Prompt == nil {
		t.Fatalf("stale: %+v", stale)
	}
	gone, err := talkCall[protocol.AgentChooseData](r, protocol.CmdAgentChoose, protocol.AgentChooseArgs{Session: r.id, Hash: st.Prompt.Hash, Key: "9"})
	if err != nil {
		t.Fatal(err)
	}
	if gone.Sent {
		t.Fatalf("an answer not offered went: %+v", gone)
	}
	time.Sleep(200 * time.Millisecond)
	if k := r.keys(); len(k) != 0 {
		t.Fatalf("keys after refusals: %v", k)
	}

	ok, err := talkCall[protocol.AgentChooseData](r, protocol.CmdAgentChoose, protocol.AgentChooseArgs{Session: r.id, Hash: st.Prompt.Hash, Key: "1"})
	if err != nil {
		t.Fatal(err)
	}
	if !ok.Sent {
		t.Fatalf("choose: %+v\npane:\n%s", ok, r.pane())
	}
	r.waitFor("the digit", func() bool { return len(r.keys()) >= 1 })
	if k := r.keys(); len(k) != 1 || k[0] != "1" {
		t.Fatalf("keys: %v", k)
	}
	if got := r.state().Prompt; got != nil {
		t.Errorf("a prompt after the answer: %+v", got)
	}

	// The turn finishes and the message said during the question goes after it.
	if have := r.received(2); have != "are you there" {
		t.Fatalf("flushed %q", have)
	}
	items := r.waitRows("the tool row", func(items []protocol.AgentFeedItem) bool {
		for _, it := range items {
			if it.Tool != nil && it.Tool.Status == "ok" {
				return true
			}
		}
		return false
	})
	if !itHasKind(items, protocol.FeedItemText) {
		t.Errorf("rows: %s", itKinds(items))
	}
	store, _ := r.d.sshStore(r.id, false)
	found := false
	store.View(func(st *sshstore.State) {
		for _, it := range st.Items {
			if it.Human != nil && it.Human.Origin == protocol.SSHHumanChoice {
				found = true
			}
		}
	})
	if !found {
		t.Errorf("no row for the pressed key")
	}
}

// A question answered in the terminal, with no press through lola, must clear the card.
func TestTalkITPermissionAnsweredInTerminal(t *testing.T) {
	r := newTalkRig(t)
	r.waitComposer(protocol.ComposerResting)
	if got := r.say("perm please"); !got.Delivered {
		t.Fatalf("not delivered: %+v", got)
	}
	r.waitComposer(protocol.ComposerPrompt)
	// The hook lands first here. TestTalkITLatePermissionNotification is the other order.
	r.waitFor("the hook to park the axis", func() bool {
		s, _ := r.d.sessions.Get(r.id)
		return s.AgentState == state.AgentWaitingInput && s.InputReason == state.InputPermission
	})
	r.waitComposer(protocol.ComposerPrompt)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := r.tm.SendKey(ctx, r.id, "3"); err != nil {
		t.Fatal(err)
	}
	// Nothing polls until the pane has settled, so this is the designed path and no race.
	r.waitFor("the fake agent to rest", func() bool { return strings.Contains(r.pane(), "Rejected.") })
	time.Sleep(100 * time.Millisecond)
	r.waitComposer(protocol.ComposerResting)
	if got := r.state().Prompt; got != nil {
		t.Errorf("card still up: %+v", got)
	}
	items := r.waitRows("the rejected row", func(items []protocol.AgentFeedItem) bool {
		for _, it := range items {
			if it.Tool != nil && it.Tool.Status == "interrupted" {
				return true
			}
		}
		return false
	})
	if !itHasKind(items, protocol.FeedItemNotice) {
		t.Errorf("rows: %s", itKinds(items))
	}
	// lola must be able to speak again without a person clearing anything.
	if got := r.say("carry on"); !got.Delivered {
		t.Fatalf("say after the answer: %+v\npane:\n%s", got, r.pane())
	}
}

// A modal is never typed into, and the card offers no keys.
func TestTalkITModal(t *testing.T) {
	r := newTalkRig(t)
	r.waitComposer(protocol.ComposerResting)
	if got := r.say("modal please"); !got.Delivered {
		t.Fatalf("not delivered: %+v", got)
	}
	r.waitComposer(protocol.ComposerModal)
	st := r.state()
	if st.Prompt == nil || st.Prompt.Kind != "modal" || len(st.Prompt.Choices) != 0 {
		t.Fatalf("prompt: %+v", st.Prompt)
	}
	got := r.say("hello")
	if got.Delivered {
		t.Fatalf("typed into a modal: %+v", got)
	}
	time.Sleep(200 * time.Millisecond)
	if k := r.keys(); len(k) != 0 {
		t.Fatalf("keys reached the modal: %v", k)
	}
	stop, err := talkCall[protocol.AgentStopData](r, protocol.CmdAgentStop, protocol.AgentStopArgs{Session: r.id})
	if err != nil {
		t.Fatal(err)
	}
	if stop.Stopped {
		t.Errorf("Stop pressed Esc into a modal: %+v", stop)
	}
}

// A draft a person left in the composer is never merged with, and is named as the reason.
func TestTalkITDraftIsNotTypedOver(t *testing.T) {
	r := newTalkRig(t)
	r.waitComposer(protocol.ComposerResting)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := r.tm.SendText(ctx, r.id, "half a thought", false); err != nil {
		t.Fatal(err)
	}
	r.waitComposer(protocol.ComposerDraft)
	got := r.say("from the app")
	if got.Delivered || !strings.Contains(got.Why, "draft") {
		t.Fatalf("say over a draft: %+v", got)
	}
	time.Sleep(300 * time.Millisecond)
	if n := r.receivedCount(); n != 0 {
		t.Fatalf("%d messages submitted", n)
	}
}

// Feed paging, turn grouping and a change of an open row, on what the fake agent wrote.
func TestTalkITFeedRows(t *testing.T) {
	r := newTalkRig(t)
	r.waitComposer(protocol.ComposerResting)
	if got := r.say("first thing"); !got.Delivered {
		t.Fatalf("not delivered: %+v", got)
	}
	r.waitRows("first answer", func(items []protocol.AgentFeedItem) bool {
		return itHasKind(items, protocol.FeedItemText)
	})
	r.waitComposer(protocol.ComposerResting)

	first := r.feed()
	if first.Epoch == "" || first.Health != protocol.FeedOK || first.LastSeq == 0 {
		t.Fatalf("first page: %+v", first)
	}
	if got := r.state(); got.FeedSeq == 0 || !got.Caps.Feed {
		t.Errorf("talkState feed fields: %+v", got)
	}
	if got := r.say("perm please"); !got.Delivered {
		t.Fatalf("not delivered: %+v\npane:\n%s", got, r.pane())
	}
	r.waitComposer(protocol.ComposerPrompt)
	var open protocol.AgentFeedItem
	r.waitFor("a running row", func() bool {
		next, err := talkCall[protocol.AgentFeedData](r, protocol.CmdAgentFeed, protocol.AgentFeedArgs{Session: r.id, Epoch: first.Epoch, After: first.LastSeq})
		if err != nil {
			t.Fatal(err)
		}
		for _, it := range next.Items {
			if it.Tool != nil {
				open = it
				return true
			}
		}
		return false
	})
	if open.Tool.Status != "running" || open.Tool.Summary != "touch perm-a.txt" {
		t.Fatalf("open row: %+v", open.Tool)
	}
	st := r.state()
	if _, err := talkCall[protocol.AgentChooseData](r, protocol.CmdAgentChoose, protocol.AgentChooseArgs{Session: r.id, Hash: st.Prompt.Hash, Key: "1"}); err != nil {
		t.Fatal(err)
	}
	items := r.waitRows("the row to finish", func(items []protocol.AgentFeedItem) bool {
		for _, it := range items {
			if it.ID == open.ID && it.Tool.Status == "ok" {
				return true
			}
		}
		return false
	})
	turns := map[string]bool{}
	for _, it := range items {
		if it.Turn == "" {
			t.Errorf("a row with no turn: %+v", it)
		}
		turns[it.Turn] = true
	}
	if len(turns) != 2 {
		t.Errorf("%d turns in: %s", len(turns), itKinds(items))
	}
	out, err := talkCall[protocol.AgentFeedOutputData](r, protocol.CmdAgentFeedOutput, protocol.AgentFeedOutputArgs{Session: r.id, Item: open.ID})
	if err != nil {
		t.Fatal(err)
	}
	if out.Text != "ran it" {
		t.Errorf("output %q", out.Text)
	}
}

func firstDiff(a, b string) string {
	n := min(len(a), len(b))
	for i := 0; i < n; i++ {
		if a[i] != b[i] {
			lo := max(0, i-30)
			return fmt.Sprintf("byte %d:\n have %q\n want %q", i, a[lo:min(i+30, len(a))], b[lo:min(i+30, len(b))])
		}
	}
	return fmt.Sprintf("one is a prefix of the other: %d and %d bytes", len(a), len(b))
}

// Whatever a person wrote arrives as written. Each row here is something a composer really
// produces: code with indentation and blank runs, a Windows paste, trailing spaces.
func TestTalkITSayKeepsTheShapeOfText(t *testing.T) {
	cases := []struct{ name, text, want string }{
		{"indented code", "look:\n    if x {\n        y()\n    }\nend", ""},
		{"three blank lines", "one\n\n\n\ntwo", ""},
		{"trailing spaces inside", "one  \ntwo", ""},
		{"crlf", "one\r\ntwo", "one\ntwo"},
		{"long indented paste", strings.Repeat("    indented line of code with some length here\n\n\n", 30) + "end", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := newTalkRig(t)
			r.waitComposer(protocol.ComposerResting)
			if got := r.say(c.text); !got.Delivered {
				t.Fatalf("not delivered: %+v", got)
			}
			want := c.want
			if want == "" {
				want = c.text
			}
			if have := r.received(1); have != want {
				t.Fatalf("changed in transit\n%s", firstDiff(have, want))
			}
		})
	}
}

// Nothing in a message may submit early or leave the bracketed paste.
func TestTalkITSayCannotEscapeThePrompt(t *testing.T) {
	long := strings.Repeat("padding ", 150)
	cases := []struct{ name, text string }{
		{"bare CR", "one\rtwo"},
		{"paste terminator", long + "\x1b[201~\rsecond message"},
		{"C1 CSI terminator", long + "\u009b201~\rsecond message"},
		{"ctrl c", "one\x03two"},
		{"esc", "one\x1btwo"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := newTalkRig(t)
			r.waitComposer(protocol.ComposerResting)
			if got := r.say(c.text); !got.Delivered {
				t.Fatalf("not delivered: %+v", got)
			}
			r.received(1)
			r.waitComposer(protocol.ComposerResting)
			if n := r.receivedCount(); n != 1 {
				t.Fatalf("one send became %d messages", n)
			}
			for _, k := range r.keys() {
				if strings.HasPrefix(k, "ESC") {
					t.Fatalf("an Esc reached the agent: %v", r.keys())
				}
			}
		})
	}
}

// Over the send cap nothing may be cut without the person hearing about it.
func TestTalkITSayOverTheCapSaysSo(t *testing.T) {
	r := newTalkRig(t)
	r.waitComposer(protocol.ComposerResting)
	text := strings.TrimSpace(strings.Repeat("0123456789 abcdefghij\n", 700)) // about 15 KB
	got, err := talkCall[protocol.AgentSayData](r, protocol.CmdAgentSay, protocol.AgentSayArgs{Session: r.id, Text: text})
	if err != nil {
		return // refusing is an honest answer
	}
	have := r.received(1)
	if have != text {
		t.Fatalf("a %d byte message arrived as %d bytes and the reply was %+v", len(text), len(have), got)
	}
}

// Several messages said during one turn all arrive, once, and read as lola's own send.
func TestTalkITQueuedBatch(t *testing.T) {
	r := newTalkRig(t)
	r.waitComposer(protocol.ComposerResting)
	if got := r.say("work:25"); !got.Delivered {
		t.Fatalf("not delivered: %+v", got)
	}
	r.waitComposer(protocol.ComposerWorking)
	first := r.say("first queued")
	second := r.say("second queued")
	third := r.say("third queued")
	if first.Delivered || second.Delivered || third.Delivered {
		t.Fatalf("delivered mid-turn: %+v %+v %+v", first, second, third)
	}
	joined := r.received(2)
	for r.receivedCount() < 4 && !strings.Contains(joined, "third queued") {
		time.Sleep(100 * time.Millisecond)
		joined = ""
		for i := 2; i <= r.receivedCount(); i++ {
			joined += r.received(i) + "\n"
		}
	}
	for _, want := range []string{"first queued", "second queued", "third queued"} {
		if strings.Count(joined, want) != 1 {
			t.Fatalf("%q arrived %d times in %q", want, strings.Count(joined, want), joined)
		}
	}
	store, _ := r.d.sshStore(r.id, false)
	r.waitFor("all three to read delivered", func() bool {
		return sshItemDelivered(store, first.Item) && sshItemDelivered(store, second.Item) && sshItemDelivered(store, third.Item)
	})
	r.waitRows("no row to read as typed in the terminal", func(items []protocol.AgentFeedItem) bool {
		for _, it := range items {
			if it.Kind == protocol.FeedItemUser && !it.Echo {
				return false
			}
		}
		return true
	})
}

// A human denying in the terminal, in a session nobody had open, fires no hook. Opening the view
// afterwards must not leave a resting pane refusing every message as "asking something".
func TestTalkITPermissionDeniedWhileNobodyWatched(t *testing.T) {
	r := newTalkRig(t)
	r.waitComposer(protocol.ComposerResting)
	if got := r.say("perm please"); !got.Delivered {
		t.Fatalf("not delivered: %+v", got)
	}
	r.waitFor("the hook to park the axis", func() bool {
		s, _ := r.d.sessions.Get(r.id)
		return s.AgentState == state.AgentWaitingInput && s.InputReason == state.InputPermission
	})
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := r.tm.SendKey(ctx, r.id, "3"); err != nil {
		t.Fatal(err)
	}
	r.waitFor("the fake agent to rest", func() bool { return strings.Contains(r.pane(), "Rejected.") })
	r.waitComposer(protocol.ComposerResting)
	if got := r.say("carry on"); !got.Delivered {
		s, _ := r.d.sessions.Get(r.id)
		t.Fatalf("composer rests but say answers %+v, axis %s/%s", got, s.AgentState, s.InputReason)
	}
}

// The Notification hook is async and can land after the answer, which the hook handler's own
// comment says of permission_request. The notification path must not re-park a settled session.
func TestTalkITLatePermissionNotification(t *testing.T) {
	r := newTalkRig(t)
	r.waitComposer(protocol.ComposerResting)
	if got := r.say("perm please"); !got.Delivered {
		t.Fatalf("not delivered: %+v", got)
	}
	r.waitComposer(protocol.ComposerPrompt)
	st := r.state()
	if _, err := talkCall[protocol.AgentChooseData](r, protocol.CmdAgentChoose, protocol.AgentChooseArgs{Session: r.id, Hash: st.Prompt.Hash, Key: "3"}); err != nil {
		t.Fatal(err)
	}
	r.waitComposer(protocol.ComposerResting)
	r.d.handle(context.Background(), protocol.Request{Cmd: "hookEvent", Session: r.id, Event: "notification", Hook: &protocol.HookPayload{
		Message: "Claude needs your permission to use Bash", Reason: "permission_prompt",
	}})
	r.waitComposer(protocol.ComposerResting)
	if got := r.say("carry on"); !got.Delivered {
		s, _ := r.d.sessions.Get(r.id)
		t.Fatalf("composer rests but say answers %+v, axis %s/%s", got, s.AgentState, s.InputReason)
	}
}

// A client attached to the pane defers automatic sends and says why, and a press still goes.
func TestTalkITAttachedTerminalDefersTheFlush(t *testing.T) {
	r := newTalkRig(t)
	r.waitComposer(protocol.ComposerResting)
	sock := r.tm.SocketName
	attach := exec.Command("script", "-q", "/dev/null", "tmux", "-L", sock, "attach", "-t", "="+r.id)
	stdin, err := attach.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	defer stdin.Close()
	if err := attach.Start(); err != nil {
		t.Skipf("no pty for an attached client: %v", err)
	}
	t.Cleanup(func() {
		_ = attach.Process.Kill()
		_ = attach.Wait()
	})
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	deadline := time.Now().Add(3 * time.Second)
	for {
		if on, _ := r.tm.HasAttachedClient(ctx, r.id); on {
			break
		}
		if time.Now().After(deadline) {
			t.Skip("the attached client did not come up in this environment")
		}
		time.Sleep(50 * time.Millisecond)
	}
	if got := r.say("work:15"); !got.Delivered {
		t.Fatalf("a press with a terminal attached: %+v\npane:\n%s", got, r.pane())
	}
	r.waitComposer(protocol.ComposerWorking)
	queued := r.say("next one")
	if queued.Delivered {
		t.Fatalf("delivered mid-turn: %+v", queued)
	}
	r.waitComposer(protocol.ComposerResting)
	r.waitFor("the deferral to be said", func() bool { return r.state().Deferred != "" })
	if n := r.receivedCount(); n != 1 {
		t.Fatalf("%d messages with a terminal attached", n)
	}
	rel, err := talkCall[protocol.AgentSayData](r, protocol.CmdAgentQueueRelease, protocol.AgentQueueReleaseArgs{Session: r.id})
	if err != nil {
		t.Fatal(err)
	}
	if !rel.Delivered {
		t.Fatalf("a press did not go: %+v", rel)
	}
	if have := r.received(2); have != "next one" {
		t.Fatalf("received %q", have)
	}
	r.waitFor("the deferral to clear", func() bool { return r.state().Deferred == "" })
}

// How long one poll takes against a real server. A view asks once a second.
func TestTalkITStateIsCheap(t *testing.T) {
	r := newTalkRig(t)
	r.waitComposer(protocol.ComposerResting)
	start := time.Now()
	const polls = 20
	for i := 0; i < polls; i++ {
		r.state()
	}
	each := time.Since(start) / polls
	t.Logf("talkState at rest: %s per call", each)
	if each > 150*time.Millisecond {
		t.Errorf("talkState takes %s", each)
	}
}

// Someone denies in the terminal and starts typing straight away, then clears what they typed.
// The first read after the answer sees a draft, so it proves nothing, and it must not use up the
// only chance to notice that the question has gone.
func TestTalkITPermissionDeniedThenDraftThenClear(t *testing.T) {
	r := newTalkRig(t)
	r.waitComposer(protocol.ComposerResting)
	if got := r.say("perm please"); !got.Delivered {
		t.Fatalf("not delivered: %+v", got)
	}
	r.waitComposer(protocol.ComposerPrompt)
	r.waitFor("the hook to park the axis", func() bool {
		s, _ := r.d.sessions.Get(r.id)
		return s.AgentState == state.AgentWaitingInput && s.InputReason == state.InputPermission
	})
	r.waitComposer(protocol.ComposerPrompt)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := r.tm.SendKey(ctx, r.id, "3"); err != nil {
		t.Fatal(err)
	}
	r.waitFor("the fake agent to rest", func() bool { return strings.Contains(r.pane(), "Rejected.") })
	if err := r.tm.SendText(ctx, r.id, "x", false); err != nil {
		t.Fatal(err)
	}
	r.waitFor("the draft to show", func() bool { return strings.Contains(r.pane(), "\u00a0x") })
	r.waitComposer(protocol.ComposerDraft)
	if err := exec.CommandContext(ctx, "tmux", "-L", r.tm.SocketName, "send-keys", "-t", "="+r.id+":", "C-u").Run(); err != nil {
		t.Fatal(err)
	}
	r.waitComposer(protocol.ComposerResting)
	if got := r.say("carry on"); !got.Delivered {
		s, _ := r.d.sessions.Get(r.id)
		t.Fatalf("composer rests but say answers %+v, axis %s/%s", got, s.AgentState, s.InputReason)
	}
}

// A poll whose two execs straddle the answer: the capture still shows the dialog and the cursor
// is already back in the composer. It reads as a draft and spends the card's memory, and the
// session then refuses every message. Timing dependent by nature, because the window is the gap
// between capture-pane and display-message, so this one polls as fast as it can.
func TestTalkITPermissionPollStraddlesTheAnswer(t *testing.T) {
	r := newTalkRig(t)
	r.waitComposer(protocol.ComposerResting)
	if got := r.say("perm please"); !got.Delivered {
		t.Fatalf("not delivered: %+v", got)
	}
	r.waitComposer(protocol.ComposerPrompt)
	r.waitFor("the hook to park the axis", func() bool {
		s, _ := r.d.sessions.Get(r.id)
		return s.AgentState == state.AgentWaitingInput && s.InputReason == state.InputPermission
	})
	r.waitComposer(protocol.ComposerPrompt)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := r.tm.SendKey(ctx, r.id, "3"); err != nil {
		t.Fatal(err)
	}
	r.waitComposer(protocol.ComposerResting)
	if got := r.say("carry on"); !got.Delivered {
		s, _ := r.d.sessions.Get(r.id)
		t.Fatalf("composer rests but say answers %+v, axis %s/%s", got, s.AgentState, s.InputReason)
	}
}
