package tui

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"charm.land/lipgloss/v2"

	tea "charm.land/bubbletea/v2"
	"github.com/sushidev-team/lola/internal/config"
	"github.com/sushidev-team/lola/internal/protocol"
)

// A fake daemon: canned replies per command, every request recorded.
type sshFakeDaemon struct {
	replies map[string]any
	errs    map[string]string
	calls   []protocol.Request
}

func (d *sshFakeDaemon) install(t *testing.T) {
	t.Helper()
	old := requestFn
	t.Cleanup(func() { requestFn = old })
	requestFn = func(req protocol.Request) (*protocol.Response, error) {
		d.calls = append(d.calls, req)
		if e, ok := d.errs[req.Cmd]; ok {
			return &protocol.Response{OK: false, Error: e}, nil
		}
		raw, _ := json.Marshal(d.replies[req.Cmd])
		return &protocol.Response{OK: true, Data: raw}, nil
	}
}

func (d *sshFakeDaemon) last(cmd string) *protocol.Request {
	for i := len(d.calls) - 1; i >= 0; i-- {
		if d.calls[i].Cmd == cmd {
			return &d.calls[i]
		}
	}
	return nil
}

func (d *sshFakeDaemon) count(cmd string) int {
	n := 0
	for i := range d.calls {
		if d.calls[i].Cmd == cmd {
			n++
		}
	}
	return n
}

// run executes a command tree synchronously, feeding messages back into the
// model, the way the program would. Ticks are not followed: they never end.
func sshRun(m *rootModel, cmd tea.Cmd) {
	if cmd == nil {
		return
	}
	done := make(chan tea.Msg, 1)
	go func() { done <- cmd() }()
	var msg tea.Msg
	select {
	case msg = <-done:
	case <-time.After(30 * time.Millisecond): // a Tick: nothing to run in a test
		return
	}
	switch v := msg.(type) {
	case tea.BatchMsg:
		for _, c := range v {
			sshRun(m, c)
		}
	case nil:
	case statusTickMsg, sshTickMsg:
	default:
		_, next := m.Update(msg)
		sshRun(m, next)
	}
}

func sshKey(m *rootModel, keys ...string) {
	for _, k := range keys {
		_, cmd := m.Update(keyMsg(k))
		sshRun(m, cmd)
	}
}

func sshType(m *rootModel, text string) {
	for _, r := range text {
		_, cmd := m.Update(keyMsg(string(r)))
		sshRun(m, cmd)
	}
}

// The clock is pinned and moved by hand, so arming is never slept on.
func sshFreezeClock(t *testing.T) *time.Time {
	t.Helper()
	now := time.Date(2026, 11, 5, 14, 2, 0, 0, time.UTC)
	old := sshNow
	sshNow = func() time.Time { return now }
	t.Cleanup(func() { sshNow = old })
	return &now
}

func sshNoAttach(t *testing.T) *int {
	t.Helper()
	n := 0
	old := sshAttach
	sshAttach = func(*rootModel) (tea.Cmd, bool) { n++; return nil, true }
	t.Cleanup(func() { sshAttach = old })
	return &n
}

func sshConnFixture(tier string) protocol.SSHConnInfo {
	return protocol.SSHConnInfo{
		ID: "c1", Session: "s1", Host: "prod-api", Alias: "prod-api", Tier: tier,
		ResolvedUser: "deploy", ResolvedHostname: "10.0.1.4", Sandboxed: true,
		State: protocol.SSHConnConnected, Wheel: protocol.SSHWheelAgent,
		Tab: "s1-ssh-1", TabAlive: true,
	}
}

func sshBlockItem(id string, order int64, b protocol.SSHBlock) protocol.SSHTimelineItem {
	b.ID, b.Conn, b.Session = id, "c1", "s1"
	if b.Host == "" {
		b.Host = "prod-api"
	}
	if b.Actor == "" {
		b.Actor = protocol.SSHActorAgent
	}
	return protocol.SSHTimelineItem{ID: id, Kind: protocol.SSHItemBlock, Order: order, Seq: order, Block: &b}
}

func sshElementItem(id string, order int64, e protocol.SSHElement) protocol.SSHTimelineItem {
	e.ID = id
	if e.Revision == 0 {
		e.Revision = 1
	}
	return protocol.SSHTimelineItem{ID: id, Kind: protocol.SSHItemElement, Order: order, Seq: order, Element: &e}
}

func sshExit(n int) *int { return &n }

// sshWorkspace builds a model with the overlay open on session s1.
func sshWorkspace(t *testing.T, d *sshFakeDaemon, conns []protocol.SSHConnInfo, items ...protocol.SSHTimelineItem) *rootModel {
	t.Helper()
	m := newTestRoot(t)
	d.install(t)
	if d.replies == nil {
		d.replies = map[string]any{}
	}
	d.replies[protocol.CmdSSHTimeline] = protocol.SSHTimelineData{Session: "s1", Connections: conns, Items: items, LastSeq: int64(len(items))}
	data := cannedSessions()
	for i := range data.Sessions {
		if data.Sessions[i].ID == "s1" {
			data.Sessions[i].SSH = &protocol.SSHSessionInfo{Connections: conns}
			m.sessions.cursor = i
		}
	}
	m.Update(sessionsMsg{data: data})
	for i, s := range m.sessions.data.Sessions {
		if s.ID == "s1" {
			m.sessions.cursor = i
			m.sessions.selID = "s1"
		}
	}
	sshKey(m, "H")
	if !m.ssh.open {
		t.Fatal("H on a session with a connection must open the workspace")
	}
	return m
}

func sshPlain(m *rootModel) string { return stripANSI(m.viewString()) }

func sshFrameFits(t *testing.T, m *rootModel) {
	t.Helper()
	lines := strings.Split(m.viewString(), "\n")
	if len(lines) > m.height {
		t.Errorf("frame is %d lines in a %d line window", len(lines), m.height)
	}
	for _, l := range lines {
		if w := lineWidth(l); w > m.width {
			t.Errorf("line wider than %d: %q", m.width, stripANSI(l))
		}
	}
}

func lineWidth(l string) int { return len([]rune(stripANSI(l))) }

// --- entry points -----------------------------------------------------------

func TestSSHKeyOnSessionWithoutConnectionOpensPicker(t *testing.T) {
	m := newTestRoot(t)
	d := &sshFakeDaemon{replies: map[string]any{protocol.CmdSSHHosts: protocol.SSHHostsData{Hosts: []protocol.SSHHostInfo{
		{Name: "prod-api", Alias: "prod-api", User: "deploy", Tier: "production", DirectSandbox: "docker", DirectAgent: "claude"},
	}}}}
	d.install(t)
	m.Update(sessionsMsg{data: cannedSessions()})
	sshKey(m, "H")
	if m.ssh.picker == nil || m.ssh.open {
		t.Fatalf("expected the host picker (picker=%v open=%v)", m.ssh.picker, m.ssh.open)
	}
	v := sshPlain(m)
	for _, want := range []string{"New SSH session", "PRODUCTION", "prod-api", "Agent runs in a container",
		"Lola asks here if signing in needs anything.", "Production asks you to approve every command.", protocol.SSHDataWarning} {
		if !strings.Contains(v, want) {
			t.Errorf("picker missing %q:\n%s", want, v)
		}
	}
}

func TestSSHPickerNoHostsPointsAtSettings(t *testing.T) {
	m := newTestRoot(t)
	d := &sshFakeDaemon{replies: map[string]any{protocol.CmdSSHHosts: protocol.SSHHostsData{}}}
	d.install(t)
	sshKey(m, "H")
	v := sshPlain(m)
	if !strings.Contains(v, "No hosts yet.") || !strings.Contains(v, "Lola holds no address and no key.") {
		t.Errorf("no-hosts copy missing:\n%s", v)
	}
	sshKey(m, "S")
	if m.settings == nil || m.settings.tab != stSSH {
		t.Fatal("S must open settings on the SSH hosts tab")
	}
}

func TestSSHPickerDirectRefusedHostCannotStart(t *testing.T) {
	m := newTestRoot(t)
	refused := "billing-db is tier production and no sandbox is available. Set [sandbox].image"
	d := &sshFakeDaemon{replies: map[string]any{protocol.CmdSSHHosts: protocol.SSHHostsData{Hosts: []protocol.SSHHostInfo{
		{Name: "billing-db", Alias: "billing-db", Tier: "production", DirectRefused: refused},
		{Name: "stage", Alias: "stage", Tier: "staging"},
	}}}}
	d.install(t)
	sshKey(m, "H")
	if m.ssh.picker.cursor != 1 {
		t.Fatalf("the first USABLE host is selected, got %d", m.ssh.picker.cursor)
	}
	sshKey(m, "k")
	v := sshPlain(m)
	if !strings.Contains(v, "Cannot start") || !strings.Contains(v, "no sandbox is available") {
		t.Errorf("a refused host must say why, whole:\n%s", v)
	}
	sshKey(m, "enter")
	if m.ssh.picker.step != sshPickHost || d.count(protocol.CmdSSHOpen) != 0 {
		t.Error("a refused host must not advance, and must never send sshOpen")
	}
}

// Direct entry end to end: pick, brief, who drives, connect, then the session is
// selected, its workspace opens and the terminal has the keyboard.
func TestSSHPickerStartsADirectSession(t *testing.T) {
	attaches := sshNoAttach(t)
	m := newTestRoot(t)
	conn := sshConnFixture(protocol.SSHTierStaging)
	conn.State, conn.Session, conn.Entry = protocol.SSHConnConnecting, "s1", protocol.SSHEntryDirect
	d := &sshFakeDaemon{replies: map[string]any{
		protocol.CmdSSHHosts:    protocol.SSHHostsData{Hosts: []protocol.SSHHostInfo{{Name: "stage", Alias: "stage", Tier: "staging"}}},
		protocol.CmdSSHOpen:     protocol.SSHOpenData{Session: "s1", Conn: conn},
		protocol.CmdSSHTimeline: protocol.SSHTimelineData{Session: "s1", Connections: []protocol.SSHConnInfo{conn}},
		"sessions":              cannedSessions(),
	}}
	d.install(t)
	m.Update(sessionsMsg{data: cannedSessions()})
	sshKey(m, "H", "enter")
	sshType(m, "why restarts")
	sshKey(m, "tab")
	v := sshPlain(m)
	if !strings.Contains(v, "You start at the wheel.") {
		t.Errorf("drive-first copy missing:\n%s", v)
	}
	sshKey(m, "enter")
	req := d.last(protocol.CmdSSHOpen)
	if req == nil {
		t.Fatal("sshOpen was not sent")
	}
	var args protocol.SSHOpenArgs
	_ = json.Unmarshal(req.Args, &args)
	if args.Host != "stage" || args.Brief != "why restarts" || args.Wheel != protocol.SSHWheelHuman {
		t.Errorf("sshOpen args: %+v", args)
	}
	if m.ssh.pendingOpen == nil && !m.ssh.open {
		t.Fatal("the opened session must be pending or open")
	}
	m.Update(sessionsMsg{data: cannedSessions()})
	_, cmd := m.Update(sessionsMsg{data: cannedSessions()})
	sshRun(m, cmd)
	if !m.ssh.open || m.ssh.session != "s1" {
		t.Fatalf("workspace not open on the new session: %+v", m.ssh.session)
	}
	if sel := m.sessions.selected(); sel == nil || sel.ID != "s1" {
		t.Fatalf("new session not selected: %+v", sel)
	}
	if !m.embedFocused || *attaches == 0 {
		t.Errorf("the terminal must take the keyboard: focused=%v attaches=%d", m.embedFocused, *attaches)
	}
}

// --- three voices -----------------------------------------------------------

// Lola's facts are on a band and the agent's why is NOT, which is the whole point.
func TestSSHBlockWhyIsOutsideTheBand(t *testing.T) {
	sshFreezeClock(t)
	d := &sshFakeDaemon{}
	m := sshWorkspace(t, d, []protocol.SSHConnInfo{sshConnFixture(protocol.SSHTierProduction)},
		sshBlockItem("b1", 1, protocol.SSHBlock{Command: "df -h", Why: "check free space", Status: protocol.SSHBlockFailed, ExitCode: sshExit(3), DurationMs: 400, StdoutBytes: 2048, Preview: "Active: failed\n"}))
	band := bgSGR(colSel)
	var cmdLine, whyLine string
	for _, l := range strings.Split(m.viewString(), "\n") {
		plain := stripANSI(l)
		if strings.Contains(plain, "$ df -h") {
			cmdLine = l
		}
		if strings.Contains(plain, "check free space") {
			whyLine = l
		}
	}
	if !strings.Contains(cmdLine, band) {
		t.Errorf("a block's command is Lola's voice and needs the band: %q", cmdLine)
	}
	if whyLine == "" {
		t.Fatal("the why is missing")
	}
	if strings.Contains(whyLine, band) {
		t.Errorf("a why is a CLAIM and must never sit on the band: %q", whyLine)
	}
	if !strings.Contains(stripANSI(whyLine), "○ Claude: check free space") {
		t.Errorf("the why must wear the agent's voice: %q", stripANSI(whyLine))
	}
}

// A non-zero exit is not a verdict: the row says the number, never "failed".
func TestSSHNonZeroExitIsNotAVerdict(t *testing.T) {
	sshFreezeClock(t)
	d := &sshFakeDaemon{}
	m := sshWorkspace(t, d, []protocol.SSHConnInfo{sshConnFixture(protocol.SSHTierProduction)},
		sshBlockItem("b1", 1, protocol.SSHBlock{Command: "grep nope log", Status: protocol.SSHBlockFailed, ExitCode: sshExit(1), DurationMs: 100}))
	v := sshPlain(m)
	if !strings.Contains(v, "exit 1") {
		t.Errorf("missing the exit:\n%s", v)
	}
	if strings.Contains(strings.ToLower(v), "failed") {
		t.Errorf("a row never says failed:\n%s", v)
	}
}

func TestSSHElementsAreClaims(t *testing.T) {
	sshFreezeClock(t)
	d := &sshFakeDaemon{}
	conns := []protocol.SSHConnInfo{sshConnFixture(protocol.SSHTierProduction)}
	m := sshWorkspace(t, d, conns,
		sshBlockItem("b1", 1, protocol.SSHBlock{Command: "df", Status: protocol.SSHBlockFailed, ExitCode: sshExit(3), DurationMs: 400}),
		sshElementItem("e1", 2, protocol.SSHElement{Kind: "hypothesis", Title: "disk is full", State: "confirmed"}),
		sshElementItem("e2", 3, protocol.SSHElement{Kind: "finding", Severity: "critical", Title: "rotation stopped",
			Evidence: []protocol.SSHElementEvidence{{Block: "b1", Verified: true}, {Block: "b99", Verified: false}}}),
		sshElementItem("e3", 4, protocol.SSHElement{Kind: "brand-new-kind", Title: "from a newer daemon"}),
	)
	v := sshPlain(m)
	for _, want := range []string{"Agent says confirmed", "Critical", "[b1 exit 3 · 0.4s]", "b99 not on record", "from a newer daemon"} {
		if !strings.Contains(v, want) {
			t.Errorf("missing %q:\n%s", want, v)
		}
	}
	lowered := strings.ToLower(v)
	if strings.Contains(lowered, "verified") {
		t.Errorf("an evidence plate must never say verified:\n%s", v)
	}
	band := bgSGR(colSel)
	for _, l := range strings.Split(m.viewString(), "\n") {
		plain := stripANSI(l)
		if strings.Contains(plain, "disk is full") && (strings.Contains(l, band) || strings.Contains(plain, "✓")) {
			t.Errorf("a confirmed hypothesis must have no band and no tick: %q", l)
		}
	}
}

func TestSSHFiltersAndFolds(t *testing.T) {
	sshFreezeClock(t)
	d := &sshFakeDaemon{}
	ok := func(id string, order int64) protocol.SSHTimelineItem {
		return sshBlockItem(id, order, protocol.SSHBlock{Command: "true " + id, Status: protocol.SSHBlockSucceeded, ExitCode: sshExit(0), DurationMs: 1000, Why: "routine " + id})
	}
	m := sshWorkspace(t, d, []protocol.SSHConnInfo{sshConnFixture(protocol.SSHTierDev)},
		ok("b1", 1), ok("b2", 2), ok("b3", 3), ok("b4", 4),
		sshElementItem("e1", 5, protocol.SSHElement{Kind: "finding", Severity: "high", Title: "a finding", Evidence: []protocol.SSHElementEvidence{{Block: "b4", Verified: true}}}),
		sshElementItem("n1", 6, protocol.SSHElement{Kind: "narration", Title: "just talking"}),
	)
	v := sshPlain(m)
	if !strings.Contains(v, "3 commands · all exit 0 · 3.0s total") {
		t.Errorf("three clean commands must fold:\n%s", v)
	}
	// b4 is evidence, so a jump must always be able to land on it.
	if !strings.Contains(v, "$ true b4") {
		t.Errorf("a block evidence points at must never fold:\n%s", v)
	}
	sshKey(m, "F") // Facts
	v = sshPlain(m)
	if strings.Contains(v, "commands · all exit 0") || !strings.Contains(v, "$ true b1") {
		t.Errorf("Facts is the audit view and folds nothing:\n%s", v)
	}
	if strings.Contains(v, "routine b1") || strings.Contains(v, "just talking") || strings.Contains(v, "a finding") {
		t.Errorf("Facts must hide every claim, whys too:\n%s", v)
	}
	sshKey(m, "F") // Findings
	v = sshPlain(m)
	if !strings.Contains(v, "a finding") || strings.Contains(v, "just talking") || strings.Contains(v, "$ true b1") {
		t.Errorf("Findings shows conclusions only:\n%s", v)
	}
}

// Dig: claim to the exact command in one key, and back without losing the place.
func TestSSHEvidenceJumpAndBack(t *testing.T) {
	sshFreezeClock(t)
	d := &sshFakeDaemon{}
	m := sshWorkspace(t, d, []protocol.SSHConnInfo{sshConnFixture(protocol.SSHTierDev)},
		sshBlockItem("b1", 1, protocol.SSHBlock{Command: "df -h /var", Status: protocol.SSHBlockFailed, ExitCode: sshExit(3)}),
		sshElementItem("e1", 2, protocol.SSHElement{Kind: "finding", Severity: "high", Title: "full", Evidence: []protocol.SSHElementEvidence{{Block: "b1", Verified: true}}}),
	)
	sshKey(m, "F", "F") // Findings hides commands, and the jump must still land.
	sshKey(m, "e")
	if m.ssh.filter != sshFilterAll || m.ssh.selKey != "b1" || !m.ssh.expanded["b1"] {
		t.Fatalf("jump: filter=%v sel=%q expanded=%v", m.ssh.filter, m.ssh.selKey, m.ssh.expanded["b1"])
	}
	if v := sshPlain(m); !strings.Contains(v, "b back to where you were") || !strings.Contains(v, "Runs in") {
		t.Errorf("the block must open and offer a way back:\n%s", v)
	}
	sshKey(m, "b")
	if m.ssh.selKey != "e1" {
		t.Errorf("back: sel=%q", m.ssh.selKey)
	}
}

// --- approvals --------------------------------------------------------------

func sshPendingFixture() protocol.SSHTimelineItem {
	return sshBlockItem("b5", 5, protocol.SSHBlock{Command: "systemctl restart api", Cwd: "/srv/api", Why: "service is wedged", Tier: protocol.SSHTierProduction,
		Status: protocol.SSHBlockPendingApproval, TimeoutSeconds: 120})
}

func TestSSHApprovalCardShowsEverythingAndArms(t *testing.T) {
	now := sshFreezeClock(t)
	d := &sshFakeDaemon{replies: map[string]any{protocol.CmdSSHApprove: protocol.SSHBlock{Status: protocol.SSHBlockQueued}}}
	conn := sshConnFixture(protocol.SSHTierProduction)
	conn.Sandboxed = false
	m := sshWorkspace(t, d, []protocol.SSHConnInfo{conn}, sshPendingFixture())
	v := sshPlain(m)
	for _, want := range []string{"Claude wants to run a command on prod-api", "PRODUCTION", "$ systemctl restart api", "in /srv/api", "deploy@10.0.1.4",
		"Claude says: service is wedged", protocol.SSHDataWarning, protocol.SSHUserIsTheLimit, protocol.SSHUnsandboxedNote,
		"Production asks for every command.", "Y run once on prod-api", "N deny"} {
		if !strings.Contains(v, want) {
			t.Errorf("approval card missing %q:\n%s", want, v)
		}
	}
	// The grant control does not exist on production, whatever a daemon claims.
	if strings.Contains(v, "grant:") {
		t.Errorf("production must have no grant control:\n%s", v)
	}
	sshKey(m, "Y")
	if d.count(protocol.CmdSSHApprove) != 0 {
		t.Fatal("Y inside the arming window must do nothing")
	}
	sshKey(m, "y")
	*now = now.Add(sshArmDelay)
	sshKey(m, "y")
	if d.count(protocol.CmdSSHApprove) != 0 {
		t.Fatal("bare y is not accepted")
	}
	sshKey(m, "Y")
	req := d.last(protocol.CmdSSHApprove)
	if req == nil {
		t.Fatal("armed Y must approve")
	}
	var args protocol.SSHApproveArgs
	_ = json.Unmarshal(req.Args, &args)
	if args.Block != "b5" || args.Conn != "c1" || args.Decision != protocol.SSHDecisionApprove || args.GrantMinutes != 0 {
		t.Errorf("approve args: %+v", args)
	}
}

// A card that changes under the human's finger re-arms: a new command is a new
// question, however fast the keystroke.
func TestSSHNewCommandRearms(t *testing.T) {
	now := sshFreezeClock(t)
	d := &sshFakeDaemon{}
	conns := []protocol.SSHConnInfo{sshConnFixture(protocol.SSHTierProduction)}
	m := sshWorkspace(t, d, conns, sshPendingFixture())
	*now = now.Add(time.Second)
	changed := sshPendingFixture()
	changed.Block.Command = "rm -rf /srv/api"
	m.Update(sshTimelineMsg{gen: m.ssh.gen, session: "s1", data: &protocol.SSHTimelineData{Session: "s1", Connections: conns, Items: []protocol.SSHTimelineItem{changed}, LastSeq: 9}})
	sshKey(m, "Y")
	if d.count(protocol.CmdSSHApprove) != 0 {
		t.Fatal("a changed command must re-arm")
	}
}

func TestSSHLongCommandMustBeReadBeforeApproving(t *testing.T) {
	now := sshFreezeClock(t)
	long := strings.Repeat("echo line\n", 14) + "rm -rf /"
	item := sshBlockItem("b5", 5, protocol.SSHBlock{Command: long, Tier: protocol.SSHTierProduction, Status: protocol.SSHBlockPendingApproval})
	d := &sshFakeDaemon{replies: map[string]any{protocol.CmdSSHApprove: protocol.SSHBlock{Status: protocol.SSHBlockQueued}}}
	m := sshWorkspace(t, d, []protocol.SSHConnInfo{sshConnFixture(protocol.SSHTierProduction)}, item)
	*now = now.Add(time.Second)
	v := sshPlain(m)
	if !strings.Contains(v, "more lines: enter to read all") || !strings.Contains(v, "disabled until you read it") {
		t.Errorf("a long command must say so:\n%s", v)
	}
	sshKey(m, "Y")
	if d.count(protocol.CmdSSHApprove) != 0 {
		t.Fatal("Y must stay disabled until the whole command was opened")
	}
	sshKey(m, "enter")
	if m.ssh.pager == nil || !m.ssh.pager.cmdOnly {
		t.Fatal("enter must open the whole command")
	}
	if v := sshPlain(m); !strings.Contains(v, "The whole command") {
		t.Errorf("pager:\n%s", v)
	}
	sshKey(m, "G")
	if v := sshPlain(m); !strings.Contains(v, "rm -rf /") {
		t.Errorf("the tail of the command must be reachable:\n%s", v)
	}
	sshKey(m, "esc", "Y")
	if d.count(protocol.CmdSSHApprove) != 1 {
		t.Fatal("after reading, Y must approve")
	}
}

func TestSSHDenyPinsTheCardAndCarriesAReason(t *testing.T) {
	now := sshFreezeClock(t)
	d := &sshFakeDaemon{replies: map[string]any{protocol.CmdSSHApprove: protocol.SSHBlock{Status: protocol.SSHBlockDenied}}}
	conns := []protocol.SSHConnInfo{sshConnFixture(protocol.SSHTierProduction)}
	m := sshWorkspace(t, d, conns, sshPendingFixture())
	*now = now.Add(time.Second)
	sshKey(m, "N")
	if m.ssh.input != sshInputDeny {
		t.Fatal("N opens the reason prompt")
	}
	sshType(m, "not on Friday")
	// A second command arrives while typing: the answer must still land on b5.
	second := sshBlockItem("b4", 4, protocol.SSHBlock{Command: "older", Status: protocol.SSHBlockPendingApproval})
	m.Update(sshTimelineMsg{gen: m.ssh.gen, session: "s1", data: &protocol.SSHTimelineData{Session: "s1", Connections: conns, Items: []protocol.SSHTimelineItem{second}, LastSeq: 9}})
	sshKey(m, "enter")
	var args protocol.SSHApproveArgs
	_ = json.Unmarshal(d.last(protocol.CmdSSHApprove).Args, &args)
	if args.Block != "b5" || args.Decision != protocol.SSHDecisionDeny || args.Reason != "not on Friday" {
		t.Errorf("deny args: %+v", args)
	}
	if !strings.Contains(sshPlain(m), "Claude gets your reason with its next command.") {
		t.Error("the reply must say where the reason goes")
	}
}

func TestSSHGrantControlOnlyWhereAllowedAndOffByDefault(t *testing.T) {
	now := sshFreezeClock(t)
	conn := sshConnFixture(protocol.SSHTierStaging)
	conn.GrantMaxMinutes = 30
	item := sshBlockItem("b5", 5, protocol.SSHBlock{Command: "ls", Tier: protocol.SSHTierStaging, Status: protocol.SSHBlockPendingApproval})
	d := &sshFakeDaemon{replies: map[string]any{protocol.CmdSSHApprove: protocol.SSHBlock{Status: protocol.SSHBlockQueued}}}
	m := sshWorkspace(t, d, []protocol.SSHConnInfo{conn}, item)
	*now = now.Add(time.Second)
	if v := sshPlain(m); !strings.Contains(v, "t grant: off") || !strings.Contains(v, "Y run once on prod-api") {
		t.Errorf("grant off by default:\n%s", v)
	}
	sshKey(m, "t")
	v := sshPlain(m)
	if !strings.Contains(v, "Y run and allow for 5 min") || !strings.Contains(v, "A grant is not limited to this command.") {
		t.Errorf("a grant must say how wide it is:\n%s", v)
	}
	sshKey(m, "Y")
	var args protocol.SSHApproveArgs
	_ = json.Unmarshal(d.last(protocol.CmdSSHApprove).Args, &args)
	if args.GrantMinutes != 5 {
		t.Errorf("grant minutes: %d", args.GrantMinutes)
	}
	if got := sshGrantChoices(&protocol.SSHConnInfo{Tier: protocol.SSHTierProduction, GrantMaxMinutes: 60}); got != nil {
		t.Errorf("production offers no grant, whatever GrantMaxMinutes says: %v", got)
	}
	if got := sshGrantChoices(&protocol.SSHConnInfo{Tier: "typo", GrantMaxMinutes: 60}); got != nil {
		t.Errorf("an unknown tier reads as production: %v", got)
	}
}

func TestSSHGoCardNothingBeforeThePress(t *testing.T) {
	now := sshFreezeClock(t)
	sshNoAttach(t)
	conn := sshConnFixture(protocol.SSHTierProduction)
	conn.State, conn.Tab, conn.TabAlive, conn.Why, conn.Scope = protocol.SSHConnRequested, "", false, "need logs", "read logs"
	after := conn
	after.State, after.Tab = protocol.SSHConnConnecting, "s1-ssh-1"
	d := &sshFakeDaemon{replies: map[string]any{protocol.CmdSSHGo: after}}
	m := sshWorkspace(t, d, []protocol.SSHConnInfo{conn})
	*now = now.Add(time.Second)
	v := sshPlain(m)
	for _, want := range []string{"Claude asks to connect", "PRODUCTION", "says why: need logs", "says it will: read logs",
		"Nothing connects until you press this.", protocol.SSHDataWarning, "Y open terminal and connect"} {
		if !strings.Contains(v, want) {
			t.Errorf("go card missing %q:\n%s", want, v)
		}
	}
	if d.count(protocol.CmdSSHGo) != 0 {
		t.Fatal("no go before the press")
	}
	sshKey(m, "Y")
	if d.count(protocol.CmdSSHGo) != 1 {
		t.Fatal("Y gives the go")
	}
	if !m.embedFocused {
		t.Error("the terminal takes focus after a go: signing in is next")
	}
}

func TestSSHEveryConnectionStateHasWords(t *testing.T) {
	now := sshFreezeClock(t)
	cases := []struct {
		state string
		edit  func(*protocol.SSHConnInfo)
		want  []string
	}{
		{protocol.SSHConnConnecting, nil, []string{"Connecting…"}},
		{protocol.SSHConnAuthenticating, func(c *protocol.SSHConnInfo) { c.ConnectDeadline = now.Add(4*time.Minute + 12*time.Second) },
			[]string{"Signing in", "Gives up in 4:12", "Waiting for input?"}},
		{protocol.SSHConnConnected, func(c *protocol.SSHConnInfo) { c.TabAlive = false; c.IdleLingerSeconds = 900 },
			[]string{"Shell closed", "Your shell on prod-api ended.", "It closes by itself after 15 minutes with no client.", "T open a new shell"}},
		{protocol.SSHConnDisconnected, nil, []string{"Disconnected", "Connection to prod-api dropped.", "Y reconnect", protocol.SSHDataWarning}},
		{protocol.SSHConnDisconnected, func(c *protocol.SSHConnInfo) { c.StateDetail = "closed after idle"; c.IdleLingerSeconds = 900 },
			[]string{"Idle, connection closed", "with nothing using it"}},
		{protocol.SSHConnFailed, nil, []string{"Could not connect", "ssh's message is in the terminal.", "Y try again"}},
		{protocol.SSHConnClosed, func(c *protocol.SSHConnInfo) { c.CommandsRun = 2; c.Entry = protocol.SSHEntryDirect },
			[]string{"Closed", "The record of what ran stays here", "may keep running", "+ new session on prod-api"}},
		{protocol.SSHConnDenied, nil, []string{"Denied"}},
	}
	for _, tc := range cases {
		*now = now.Add(time.Second)
		c := sshConnFixture(protocol.SSHTierProduction)
		c.State = tc.state
		if tc.edit != nil {
			tc.edit(&c)
		}
		d := &sshFakeDaemon{}
		m := sshWorkspace(t, d, []protocol.SSHConnInfo{c})
		v := sshPlain(m)
		for _, want := range tc.want {
			if !strings.Contains(v, want) {
				t.Errorf("state %s missing %q:\n%s", tc.state, want, v)
			}
		}
		sshFrameFits(t, m)
	}
}

func TestSSHBlockStatusTable(t *testing.T) {
	now := sshFreezeClock(t)
	cases := []struct {
		b    protocol.SSHBlock
		want []string
	}{
		{protocol.SSHBlock{Status: protocol.SSHBlockQueued, Approval: protocol.SSHApprovalInfo{Granted: true}}, []string{"Queued", "Approved. Waiting for a free slot."}},
		{protocol.SSHBlock{Status: protocol.SSHBlockRunning, StartedAt: now.Add(-72 * time.Second), TimeoutSeconds: 120, Preview: "tailing\n"}, []string{"1m 12s of 2m", "tailing"}},
		{protocol.SSHBlock{Status: protocol.SSHBlockDenied, Approval: protocol.SSHApprovalInfo{Reason: "not today"}}, []string{"Denied", "You denied this.", "▌not today"}},
		{protocol.SSHBlock{Status: protocol.SSHBlockExpired, CreatedAt: now.Add(-30 * time.Minute), EndedAt: *now}, []string{"Expired", "Nobody answered within 30 minutes. It did not run."}},
		{protocol.SSHBlock{Status: protocol.SSHBlockRefused, RefusalCode: protocol.SSHRefuseHumanControl}, []string{"Did not run", "You had the wheel."}},
		{protocol.SSHBlock{Status: protocol.SSHBlockTimedOut, TimeoutSeconds: 120, DurationMs: 120000}, []string{"Timed out", "Stopped locally at the 2m limit. The remote process may still be running."}},
		{protocol.SSHBlock{Status: protocol.SSHBlockStopped, StartedAt: now.Add(-time.Second), DurationMs: 1000}, []string{"Stopped", protocol.SSHRemoteOutlives}},
		{protocol.SSHBlock{Status: protocol.SSHBlockLost, StatusDetail: "The connection dropped."}, []string{"Result unknown", "The connection dropped."}},
		{protocol.SSHBlock{Status: "from_the_future"}, []string{"from_the_future"}},
		{protocol.SSHBlock{Status: protocol.SSHBlockSucceeded, ExitCode: sshExit(0), Actor: protocol.SSHActorHuman}, []string{"you ran this", "exit 0"}},
		{protocol.SSHBlock{Status: protocol.SSHBlockSucceeded, ExitCode: sshExit(0), Approval: protocol.SSHApprovalInfo{Mode: protocol.SSHApprovalGrant}}, []string{"grant", "exit 0"}},
	}
	for _, tc := range cases {
		b := tc.b
		b.Command = "the-command"
		d := &sshFakeDaemon{}
		m := sshWorkspace(t, d, []protocol.SSHConnInfo{sshConnFixture(protocol.SSHTierDev)}, sshBlockItem("b1", 1, b))
		v := sshPlain(m)
		for _, want := range tc.want {
			if !strings.Contains(v, want) {
				t.Errorf("status %s missing %q:\n%s", tc.b.Status, want, v)
			}
		}
		if tc.b.Status == "from_the_future" && strings.Contains(v, "✓") {
			t.Errorf("an unknown status must never read as a pass:\n%s", v)
		}
	}
}

func TestSSHUnknownTierIsProduction(t *testing.T) {
	if got := stripANSI(sshTierTag("")); got != "PRODUCTION" {
		t.Errorf("unset tier: %q", got)
	}
	if got := stripANSI(sshTierTag("qa")); got != "PRODUCTION" {
		t.Errorf("unknown tier: %q", got)
	}
}

// --- the wheel --------------------------------------------------------------

func TestSSHTakingTheWheelIsInstantAndFocusesTheTerminal(t *testing.T) {
	sshFreezeClock(t)
	attaches := sshNoAttach(t)
	conn := sshConnFixture(protocol.SSHTierProduction)
	after := conn
	after.Wheel, after.WheelSince = protocol.SSHWheelHuman, sshNow()
	d := &sshFakeDaemon{replies: map[string]any{protocol.CmdSSHTakeWheel: protocol.SSHTakeWheelData{Conn: after}}}
	m := sshWorkspace(t, d, []protocol.SSHConnInfo{conn})
	sshKey(m, "W")
	if d.count(protocol.CmdSSHTakeWheel) != 1 {
		t.Fatal("W takes the wheel with no confirmation")
	}
	if !m.embedFocused || *attaches == 0 {
		t.Fatalf("focus must jump into the ssh tab: focused=%v attaches=%d", m.embedFocused, *attaches)
	}
	if m.embedTab["s1"] == 0 || !sshIsTab("s1", m.shellNames["s1"][m.embedTab["s1"]-1]) {
		t.Errorf("the active tab must be the ssh tab: %v %v", m.embedTab, m.shellNames)
	}
	sshKey(m, "ctrl+q")
	if m.embedFocused || !m.ssh.open {
		t.Fatal("^q must come back to the overlay")
	}
	m.ssh.setConnInfo(after)
	v := sshPlain(m)
	for _, want := range []string{"wheel YOU", "You have the wheel.", protocol.SSHDrivingNote, "W to hand back."} {
		if !strings.Contains(v, want) {
			t.Errorf("driving state missing %q:\n%s", want, v)
		}
	}
}

func TestSSHDrivingBarListsWhatIsStillRunning(t *testing.T) {
	now := sshFreezeClock(t)
	conn := sshConnFixture(protocol.SSHTierProduction)
	conn.Wheel = protocol.SSHWheelHuman
	d := &sshFakeDaemon{replies: map[string]any{protocol.CmdSSHStop: protocol.SSHStopData{Warning: protocol.SSHRemoteOutlives}}}
	m := sshWorkspace(t, d, []protocol.SSHConnInfo{conn},
		sshBlockItem("b1", 1, protocol.SSHBlock{Command: "journalctl -fu api", Status: protocol.SSHBlockRunning, StartedAt: now.Add(-41 * time.Second)}))
	v := sshPlain(m)
	for _, want := range []string{"1 command is still running:", "journalctl -fu api", "41s", protocol.SSHRemoteOutlives} {
		if !strings.Contains(v, want) {
			t.Errorf("driving bar missing %q:\n%s", want, v)
		}
	}
	sshKey(m, "X")
	var args protocol.SSHStopArgs
	_ = json.Unmarshal(d.last(protocol.CmdSSHStop).Args, &args)
	if args.Block != "b1" {
		t.Errorf("stop: %+v", args)
	}
}

func sshHandbackModel(t *testing.T, transcript string) (*rootModel, *sshFakeDaemon) {
	t.Helper()
	sshFreezeClock(t)
	conn := sshConnFixture(protocol.SSHTierProduction)
	conn.Wheel, conn.WheelSince = protocol.SSHWheelHuman, sshNow()
	after := conn
	after.Wheel = protocol.SSHWheelAgent
	d := &sshFakeDaemon{replies: map[string]any{
		protocol.CmdSSHHandbackPreview: protocol.SSHHandbackPreviewData{Transcript: transcript},
		protocol.CmdSSHHandback:        protocol.SSHHandbackData{Conn: after},
	}}
	m := sshWorkspace(t, d, []protocol.SSHConnInfo{conn})
	sshKey(m, "W")
	if m.ssh.handback == nil {
		t.Fatal("W while driving opens handback")
	}
	return m, d
}

// Handback: the transcript is visible, a line can be removed, the choice is
// explicit, and a letter typed in the note stays a letter.
func TestSSHHandbackSendsExactlyWhatWasShown(t *testing.T) {
	m, d := sshHandbackModel(t, "$ logrotate -f\n$ export TOKEN=secret\n$ df -h\n")
	v := sshPlain(m)
	for _, want := range []string{"HAND BACK TO CLAUDE", "$ logrotate -f", protocol.SSHTranscriptWarning} {
		if !strings.Contains(v, want) {
			t.Errorf("handback missing %q:\n%s", want, v)
		}
	}
	sshType(m, "Yes, rotated. See")
	if d.count(protocol.CmdSSHHandback) != 0 {
		t.Fatal("Y typed in the note must stay a letter")
	}
	sshKey(m, "enter")
	if d.count(protocol.CmdSSHHandback) != 0 {
		t.Fatal("enter chooses nothing: no key may choose for the human")
	}
	sshKey(m, "tab", "j", "d", "Y")
	var args protocol.SSHHandbackArgs
	_ = json.Unmarshal(d.last(protocol.CmdSSHHandback).Args, &args)
	if args.Note != "Yes, rotated. See" {
		t.Errorf("note: %q", args.Note)
	}
	if args.Transcript != "$ logrotate -f\n$ df -h" {
		t.Errorf("the transcript must be exactly what was left on screen: %q", args.Transcript)
	}
	if m.ssh.handback != nil {
		t.Error("the form closes after handing back")
	}
}

func TestSSHHandbackWithoutTranscriptSendsNone(t *testing.T) {
	m, d := sshHandbackModel(t, "$ secret\n")
	sshKey(m, "tab", "S")
	var args protocol.SSHHandbackArgs
	_ = json.Unmarshal(d.last(protocol.CmdSSHHandback).Args, &args)
	if args.Transcript != "" {
		t.Errorf("S must send no transcript, got %q", args.Transcript)
	}
}

func TestSSHHandbackEmptyTranscriptCannotChooseWith(t *testing.T) {
	m, d := sshHandbackModel(t, "only\n")
	sshKey(m, "tab", "d", "Y")
	if d.count(protocol.CmdSSHHandback) != 0 {
		t.Fatal("nothing left: Y must not send")
	}
	if !strings.Contains(sshPlain(m), "Nothing to send") {
		t.Errorf("must say why:\n%s", sshPlain(m))
	}
}

func TestSSHHandbackEscKeepsDriving(t *testing.T) {
	sshNoAttach(t)
	m, d := sshHandbackModel(t, "x\n")
	sshKey(m, "esc")
	if m.ssh.handback != nil || d.count(protocol.CmdSSHHandback) != 0 {
		t.Fatal("esc must change nothing")
	}
	if !m.embedFocused {
		t.Error("esc returns the human to the terminal")
	}
}

func TestSSHHandbackPreviewFailureStillAllowsANote(t *testing.T) {
	sshFreezeClock(t)
	conn := sshConnFixture(protocol.SSHTierProduction)
	conn.Wheel = protocol.SSHWheelHuman
	d := &sshFakeDaemon{errs: map[string]string{protocol.CmdSSHHandbackPreview: "no pane"},
		replies: map[string]any{protocol.CmdSSHHandback: protocol.SSHHandbackData{Conn: conn}}}
	m := sshWorkspace(t, d, []protocol.SSHConnInfo{conn})
	sshKey(m, "W")
	if v := sshPlain(m); !strings.Contains(v, "Could not read your terminal. You can still hand back with a note.") {
		t.Errorf("copy missing:\n%s", v)
	}
}

// --- talking ----------------------------------------------------------------

func TestSSHComposerStateLineIsDerivedFromFacts(t *testing.T) {
	sshFreezeClock(t)
	d := &sshFakeDaemon{replies: map[string]any{protocol.CmdSSHSay: protocol.SSHSayData{Item: "h1"}}}
	m := sshWorkspace(t, d, []protocol.SSHConnInfo{sshConnFixture(protocol.SSHTierDev)})
	set := func(agentState, reason string) {
		for i := range m.sessions.data.Sessions {
			if m.sessions.data.Sessions[i].ID == "s1" {
				m.sessions.data.Sessions[i].AgentState = agentState
				m.sessions.data.Sessions[i].InputReason = reason
			}
		}
	}
	cases := []struct{ state, reason, want string }{
		{"working", "", "Claude is working. It gets your message at its next command."},
		{"idle", "", "Claude is waiting."},
		{"waiting_input", "idle_notification", "Claude is waiting."},
		{"waiting_input", "permission_prompt", "Claude is waiting on a prompt in its own terminal."},
		{"exited", "", "Claude has exited. Your message waits until it is back."},
	}
	for _, tc := range cases {
		set(tc.state, tc.reason)
		if v := sshPlain(m); !strings.Contains(v, tc.want) {
			t.Errorf("state %s/%s missing %q:\n%s", tc.state, tc.reason, tc.want, v)
		}
	}
	sshKey(m, "i")
	sshType(m, "look at rotation")
	sshKey(m, "enter")
	var args protocol.SSHSayArgs
	_ = json.Unmarshal(d.last(protocol.CmdSSHSay).Args, &args)
	if args.Session != "s1" || args.Text != "look at rotation" {
		t.Errorf("say args: %+v", args)
	}
}

func TestSSHHumanDeliveryWordsAndRecall(t *testing.T) {
	now := sshFreezeClock(t)
	human := func(id string, order int64, h protocol.SSHHumanMessage) protocol.SSHTimelineItem {
		return protocol.SSHTimelineItem{ID: id, Kind: protocol.SSHItemHuman, Order: order, Seq: order, Human: &h}
	}
	d := &sshFakeDaemon{replies: map[string]any{protocol.CmdSSHRecall: protocol.SSHRecallData{Recalled: false}}}
	m := sshWorkspace(t, d, []protocol.SSHConnInfo{sshConnFixture(protocol.SSHTierDev)},
		human("h1", 1, protocol.SSHHumanMessage{Text: "first", Delivered: true, Via: protocol.SSHViaReply, DeliveredAt: *now}),
		human("h2", 2, protocol.SSHHumanMessage{Text: "second", Delivered: true, Via: protocol.SSHViaPrompt, DeliveredAt: *now}),
		human("h3", 3, protocol.SSHHumanMessage{Text: "third", Recalled: true}),
		human("h4", 4, protocol.SSHHumanMessage{Text: "fourth"}),
	)
	v := sshPlain(m)
	for _, want := range []string{"▌first", "Delivered with its last command", "Delivered at its prompt", "Recalled", "Queued · R recall"} {
		if !strings.Contains(v, want) {
			t.Errorf("delivery words missing %q:\n%s", want, v)
		}
	}
	if strings.Contains(v, "Read") && !strings.Contains(v, "Read ") {
		t.Errorf("it never says Read:\n%s", v)
	}
	sshKey(m, "R")
	var args protocol.SSHRecallArgs
	_ = json.Unmarshal(d.last(protocol.CmdSSHRecall).Args, &args)
	if args.Item != "h4" {
		t.Errorf("recall must take the newest queued one: %+v", args)
	}
	if v := sshPlain(m); !strings.Contains(v, "Too late: Claude already has this.") {
		t.Errorf("losing the race must be said:\n%s", v)
	}
}

// --- proposals and questions ------------------------------------------------

func TestSSHProposalNeverRunsWithoutTheCheckAndPinsItsRevision(t *testing.T) {
	now := sshFreezeClock(t)
	d := &sshFakeDaemon{replies: map[string]any{protocol.CmdSSHProposal: protocol.SSHProposalData{}}}
	conns := []protocol.SSHConnInfo{sshConnFixture(protocol.SSHTierDev)}
	prop := sshElementItem("p1", 1, protocol.SSHElement{Kind: "proposal", Title: "Rotate logs", Command: "logrotate -f", Revision: 3})
	m := sshWorkspace(t, d, conns, prop)
	v := sshPlain(m)
	if !strings.Contains(v, "Proposed") || !strings.Contains(v, "┆ $ logrotate -f") {
		t.Errorf("proposal body:\n%s", v)
	}
	if d.count(protocol.CmdSSHProposal) != 0 {
		t.Fatal("a proposal never runs by itself")
	}
	sshKey(m, "P")
	*now = now.Add(time.Second)
	v = sshPlain(m)
	for _, want := range []string{"Run this yourself on prod-api?", "You are running this, not Claude. It is recorded as yours.", protocol.SSHDataWarning, "Y run on prod-api"} {
		if !strings.Contains(v, want) {
			t.Errorf("run check missing %q:\n%s", want, v)
		}
	}
	sshKey(m, "Y")
	var args protocol.SSHProposalArgs
	_ = json.Unmarshal(d.last(protocol.CmdSSHProposal).Args, &args)
	if args.Element != "p1" || args.Revision != 3 || args.Action != protocol.SSHProposalRun {
		t.Errorf("proposal args: %+v", args)
	}
}

// The agent rewrites a proposal under an open check: the check closes and nothing
// runs, because the command on screen is no longer the one that would.
func TestSSHRewrittenProposalClosesTheCheck(t *testing.T) {
	now := sshFreezeClock(t)
	d := &sshFakeDaemon{}
	conns := []protocol.SSHConnInfo{sshConnFixture(protocol.SSHTierDev)}
	m := sshWorkspace(t, d, conns, sshElementItem("p1", 1, protocol.SSHElement{Kind: "proposal", Command: "ls", Revision: 1}))
	sshKey(m, "P")
	*now = now.Add(time.Second)
	rewritten := sshElementItem("p1", 1, protocol.SSHElement{Kind: "proposal", Command: "rm -rf /", Revision: 2})
	m.Update(sshTimelineMsg{gen: m.ssh.gen, session: "s1", data: &protocol.SSHTimelineData{Session: "s1", Connections: conns, Items: []protocol.SSHTimelineItem{rewritten}, LastSeq: 9}})
	if m.ssh.runCheck != nil {
		t.Fatal("the check must close")
	}
	if v := sshPlain(m); !strings.Contains(v, "Claude changed this proposal. Read it again.") {
		t.Errorf("must say so:\n%s", v)
	}
	sshKey(m, "Y")
	if d.count(protocol.CmdSSHProposal) != 0 {
		t.Fatal("nothing may run")
	}
}

func TestSSHQuestionChoiceSendsTheRevisionSeen(t *testing.T) {
	sshFreezeClock(t)
	d := &sshFakeDaemon{replies: map[string]any{protocol.CmdSSHAnswer: protocol.SSHSayData{Item: "h1"}}}
	m := sshWorkspace(t, d, []protocol.SSHConnInfo{sshConnFixture(protocol.SSHTierDev)},
		sshElementItem("q1", 1, protocol.SSHElement{Kind: "question", Title: "Keep digging?", Choices: []string{"Keep going", "Wait for me"}, Revision: 2}))
	if v := sshPlain(m); !strings.Contains(v, "Asks") || !strings.Contains(v, "2 Wait for me") {
		t.Errorf("question:\n%s", v)
	}
	sshKey(m, "2")
	var args protocol.SSHAnswerArgs
	_ = json.Unmarshal(d.last(protocol.CmdSSHAnswer).Args, &args)
	if args.Text != "Wait for me" || args.Revision != 2 || args.Element != "q1" {
		t.Errorf("answer args: %+v", args)
	}
}

// --- the pager --------------------------------------------------------------

func TestSSHPagerStreamsAndTheMissingMiddle(t *testing.T) {
	sshFreezeClock(t)
	d := &sshFakeDaemon{replies: map[string]any{protocol.CmdSSHOutput: protocol.SSHOutputData{
		Data: "head line\n", NextOffset: 10, TotalBytes: 10, EOF: true, DroppedBytes: 3 * 1024 * 1024, Tail: "tail line\n"}}}
	m := sshWorkspace(t, d, []protocol.SSHConnInfo{sshConnFixture(protocol.SSHTierDev)},
		sshBlockItem("b1", 1, protocol.SSHBlock{Command: "cat big", Status: protocol.SSHBlockFailed, ExitCode: sshExit(1), StartedAt: sshNow(), StderrBytes: 9, StdoutBytes: 10}))
	sshKey(m, "enter")
	if m.ssh.pager != nil || !m.ssh.expanded["b1"] {
		t.Fatal("the first enter shows the facts")
	}
	if v := sshPlain(m); !strings.Contains(v, "Asked by") {
		t.Errorf("facts:\n%s", v)
	}
	sshKey(m, "enter")
	if m.ssh.pager == nil || m.ssh.pager.stream != protocol.SSHStreamStderr {
		t.Fatalf("a failed block with stderr opens on stderr: %+v", m.ssh.pager)
	}
	v := sshPlain(m)
	for _, want := range []string{"head line", "3.0 MB not kept", "The middle is gone.", "tail line", "$ cat big"} {
		if !strings.Contains(v, want) {
			t.Errorf("pager missing %q:\n%s", want, v)
		}
	}
	sshKey(m, "tab")
	if m.ssh.pager.stream != protocol.SSHStreamStdout {
		t.Error("tab switches stream")
	}
	var args protocol.SSHOutputArgs
	_ = json.Unmarshal(d.last(protocol.CmdSSHOutput).Args, &args)
	if args.Stream != protocol.SSHStreamStdout || args.Session != "s1" || args.Block != "b1" {
		t.Errorf("output args: %+v", args)
	}
	sshKey(m, "esc")
	if m.ssh.pager != nil || !m.ssh.open {
		t.Error("esc leaves the pager, not the workspace")
	}
}

func TestSSHPagerEmptiesAndHostileOutput(t *testing.T) {
	sshFreezeClock(t)
	d := &sshFakeDaemon{replies: map[string]any{protocol.CmdSSHOutput: protocol.SSHOutputData{EOF: true}}}
	m := sshWorkspace(t, d, []protocol.SSHConnInfo{sshConnFixture(protocol.SSHTierDev)},
		sshBlockItem("b1", 1, protocol.SSHBlock{Command: "x", Status: protocol.SSHBlockRefused}))
	sshKey(m, "enter", "enter")
	if v := sshPlain(m); !strings.Contains(v, "This command did not run, so there is no output.") {
		t.Errorf("empty copy:\n%s", v)
	}
	sshKey(m, "esc")

	// A raw escape in anything the agent or the host wrote must not reach the frame.
	evil := "\x1b]52;c;ZXZpbA==\x07\x1b[2Jpwned"
	d.replies[protocol.CmdSSHOutput] = protocol.SSHOutputData{Data: evil, NextOffset: int64(len(evil)), TotalBytes: int64(len(evil)), EOF: true}
	conns := []protocol.SSHConnInfo{sshConnFixture(protocol.SSHTierDev)}
	m2 := sshWorkspace(t, d, conns,
		sshBlockItem("b2", 1, protocol.SSHBlock{Command: "x" + evil, Why: evil, Status: protocol.SSHBlockFailed, ExitCode: sshExit(1), StartedAt: sshNow(), Preview: evil, StdoutBytes: 9}),
		sshElementItem("e1", 2, protocol.SSHElement{Kind: "summary", Title: evil, Body: evil}))
	checkClean := func(frame string) {
		t.Helper()
		if strings.Contains(frame, "\x1b]52") || strings.Contains(frame, "\x1b[2J") {
			t.Errorf("a control sequence from untrusted text reached the frame: %q", frame)
		}
	}
	checkClean(m2.viewString())
	sshKey(m2, "k", "enter")
	checkClean(m2.viewString())
	sshKey(m2, "enter")
	checkClean(m2.viewString())
}

// --- teardown, close, tabs --------------------------------------------------

func TestSSHCloseAsksNamingWhatMaySurvive(t *testing.T) {
	sshFreezeClock(t)
	conn := sshConnFixture(protocol.SSHTierProduction)
	conn.CommandsRun = 3
	d := &sshFakeDaemon{replies: map[string]any{protocol.CmdSSHClose: protocol.SSHCloseData{Conn: conn, Warnings: []string{protocol.SSHRemoteOutlives}}}}
	m := sshWorkspace(t, d, []protocol.SSHConnInfo{conn})
	sshKey(m, "C")
	v := sshPlain(m)
	if !strings.Contains(v, "Close the connection to prod-api?") || !strings.Contains(v, protocol.SSHRemoteOutlives) {
		t.Errorf("close prompt:\n%s", v)
	}
	sshKey(m, "n")
	if d.count(protocol.CmdSSHClose) != 0 {
		t.Fatal("n keeps it")
	}
	sshKey(m, "C", "y")
	if d.count(protocol.CmdSSHClose) != 1 {
		t.Fatal("y closes")
	}
}

func TestSSHStrayKeysAreSwallowed(t *testing.T) {
	sshFreezeClock(t)
	d := &sshFakeDaemon{}
	m := sshWorkspace(t, d, []protocol.SSHConnInfo{sshConnFixture(protocol.SSHTierProduction)})
	sshKey(m, "x", "D", "s", "q")
	if m.sessions.confirmKill || !m.ssh.open {
		t.Error("a cockpit key must not act from inside the workspace")
	}
}

// Anchored at both ends, so one session's tab is never taken for a sibling's.
func TestSSHTabRecognitionIsAnchored(t *testing.T) {
	if !sshIsTab("lola-fe-42", "lola-fe-42-ssh-1") {
		t.Error("own tab")
	}
	if sshIsTab("lola-fe-4", "lola-fe-42-ssh-1") {
		t.Error("a sibling's tab must not match")
	}
	if sshIsTab("lola-fe-42", "lola-fe-42-shell-1") {
		t.Error("a shell is not an ssh tab")
	}
	if got := sshTabLabel("s", "s-ssh-1"); got != "ssh" {
		t.Errorf("label: %q", got)
	}
	if got := sshTabLabel("s", "s-ssh-2"); got != "ssh2" {
		t.Errorf("label: %q", got)
	}
}

func TestSSHTabCannotBeClosedFromTheTabStrip(t *testing.T) {
	m := newTestRoot(t)
	m.Update(sessionsMsg{data: cannedSessions()})
	sel := m.sessions.selected()
	m.shellNames = map[string][]string{sel.ID: {sel.ID + "-ssh-1"}}
	m.embedTab = map[string]int{sel.ID: 1}
	m.closeActiveShell()
	if !strings.Contains(m.sessions.flash, "H, then C closes the connection") {
		t.Errorf("flash: %q", m.sessions.flash)
	}
	if len(m.shellNames[sel.ID]) != 1 {
		t.Error("the tab must stay")
	}
}

// --- the cockpit glance -----------------------------------------------------

func TestSSHChipPriorityAndAttention(t *testing.T) {
	now := sshFreezeClock(t)
	c := func(edit func(*protocol.SSHConnInfo)) protocol.SessionInfo {
		conn := sshConnFixture(protocol.SSHTierProduction)
		edit(&conn)
		return protocol.SessionInfo{ID: "s", SSH: &protocol.SSHSessionInfo{Connections: []protocol.SSHConnInfo{conn}}}
	}
	cases := []struct {
		edit func(*protocol.SSHConnInfo)
		want string
	}{
		{func(c *protocol.SSHConnInfo) { c.State = protocol.SSHConnAuthenticating }, "sign in"},
		{func(c *protocol.SSHConnInfo) { c.PendingApprovals = 2 }, "2↑"},
		{func(c *protocol.SSHConnInfo) { c.State = protocol.SSHConnRequested }, "wants to connect"},
		{func(c *protocol.SSHConnInfo) { c.Wheel = protocol.SSHWheelHuman }, "you"},
		{func(c *protocol.SSHConnInfo) { c.Grant = &protocol.SSHGrantInfo{ExpiresAt: now.Add(90 * time.Second)} }, "grant 1:30"},
		{func(c *protocol.SSHConnInfo) { c.State = protocol.SSHConnConnecting }, "connecting…"},
		{func(c *protocol.SSHConnInfo) { c.State = protocol.SSHConnDisconnected }, "disconnected"},
		{func(c *protocol.SSHConnInfo) { c.State = protocol.SSHConnFailed }, "could not connect"},
		{func(c *protocol.SSHConnInfo) { c.RunningBlocks = 1 }, "running"},
		{func(c *protocol.SSHConnInfo) {}, "connected"},
	}
	for _, tc := range cases {
		got := stripANSI(sshChip(c(tc.edit)))
		if !strings.HasPrefix(got, "PROD prod-api ") || !strings.Contains(got, tc.want) {
			t.Errorf("chip %q, want %q", got, tc.want)
		}
	}
	if got := sshChip(c(func(c *protocol.SSHConnInfo) { c.State = protocol.SSHConnClosed })); got != "" {
		t.Errorf("an ended connection has no chip: %q", got)
	}

	// NeedsHuman ORs into attention, and an agent's open question does not.
	quiet := protocol.SessionInfo{ID: "s", SSH: &protocol.SSHSessionInfo{OpenItems: 4, Headline: "critical!"}}
	if needsHuman(quiet) || waitingOnHuman(quiet) {
		t.Error("nothing an agent posts may raise attention")
	}
	loud := protocol.SessionInfo{ID: "s", SSH: &protocol.SSHSessionInfo{NeedsHuman: true}}
	if !needsHuman(loud) || !waitingOnHuman(loud) {
		t.Error("a daemon fact must raise attention")
	}
}

func TestSSHCockpitRowDetailAndKeybar(t *testing.T) {
	m := newTestRoot(t)
	data := cannedSessions()
	conn := sshConnFixture(protocol.SSHTierProduction)
	conn.PendingApprovals = 2
	for i := range data.Sessions {
		data.Sessions[i].SSH = nil
	}
	data.Sessions[0].SSH = &protocol.SSHSessionInfo{Connections: []protocol.SSHConnInfo{conn}, NeedsHuman: true, Headline: "Disk fills", RunningCommand: "df -h"}
	subject := data.Sessions[0]
	m.Update(sessionsMsg{data: data})
	m.selectID(subject.ID)
	v := stripANSI(strings.Join(m.cockpitLines(), "\n"))
	for _, want := range []string{"PROD prod-api 2↑", "H ssh 2"} {
		if !strings.Contains(v, want) {
			t.Errorf("cockpit missing %q:\n%s", want, v)
		}
	}
	lines := strings.Join(sshDetailLines(subject, 90), "\n")
	for _, want := range []string{"PROD prod-api", "wheel agent", "○ Disk fills", "df -h", "H workspace · 2 to approve"} {
		if !strings.Contains(stripANSI(lines), want) {
			t.Errorf("detail missing %q:\n%s", want, stripANSI(lines))
		}
	}
}

// --- theme and layout -------------------------------------------------------

// Every style here is palette-derived, so a flavor change must repaint it. A style
// built once at init would keep the previous flavor's colors, silently.
func TestSSHStylesFollowTheTheme(t *testing.T) {
	t.Cleanup(func() { applyTheme("") })
	applyTheme("catppuccin-mocha")
	mocha := sshTierProd.Render("x") + sshPlate.Render("x") + sshAccent.Render("x") + sshStrike.Render("x") + sshInfo.Render("x") + sshWheelYou.Render("x")
	applyTheme("catppuccin-latte")
	latte := sshTierProd.Render("x") + sshPlate.Render("x") + sshAccent.Render("x") + sshStrike.Render("x") + sshInfo.Render("x") + sshWheelYou.Render("x")
	if mocha == latte {
		t.Error("ssh styles did not repaint: register them in rebuildStyles")
	}
}

// A wrapped row smears the repaint, so every frame is bounded, at any size, with
// the longest copy this view has.
func TestSSHFramesStayBounded(t *testing.T) {
	now := sshFreezeClock(t)
	long := strings.Repeat("very long words ", 40)
	conn := sshConnFixture(protocol.SSHTierProduction)
	conn.Sandboxed = false
	conn.Why, conn.Scope = long, long
	second := sshConnFixture(protocol.SSHTierStaging)
	second.ID, second.Host, second.State, second.PendingApprovals, second.Why = "c2", "stage-api", protocol.SSHConnRequested, 1, long
	items := []protocol.SSHTimelineItem{
		sshBlockItem("b1", 1, protocol.SSHBlock{Command: long, Why: long, Status: protocol.SSHBlockFailed, ExitCode: sshExit(1), Preview: long}),
		sshBlockItem("b2", 2, protocol.SSHBlock{Command: long, Why: long, Status: protocol.SSHBlockPendingApproval, ApprovalExpiresAt: now.Add(time.Minute)}),
		sshElementItem("e1", 3, protocol.SSHElement{Kind: "summary", Title: long, Body: long, UpdatedAt: *now}),
		sshElementItem("e2", 4, protocol.SSHElement{Kind: "plan", Title: long, Steps: []protocol.SSHElementStep{{Text: long, State: "active"}, {Text: long, State: "done"}}}),
		sshElementItem("e3", 5, protocol.SSHElement{Kind: "metric", Title: long, Metrics: []protocol.SSHElementMetric{{Name: long, Value: long}}}),
		sshElementItem("e4", 6, protocol.SSHElement{Kind: "proposal", Title: long, Command: long, Cwd: long}),
		sshElementItem("e5", 7, protocol.SSHElement{Kind: "question", Title: long, Choices: []string{long, long}}),
	}
	for _, size := range [][2]int{{200, 60}, {100, 30}, {72, 18}, {40, 12}} {
		d := &sshFakeDaemon{}
		m := sshWorkspace(t, d, []protocol.SSHConnInfo{conn, second}, items...)
		m.width, m.height = size[0], size[1]
		sshFrameFits(t, m)
		for _, key := range []string{"k", "k", "enter", "k", "enter"} {
			sshKey(m, key)
			sshFrameFits(t, m)
		}
	}
}

// --- host settings ----------------------------------------------------------

func TestSSHSettingsRoundTripAndRemove(t *testing.T) {
	m := newTestRoot(t)
	m.cfg.SSHHosts = []config.SSHHost{{Alias: "prod-api", Tier: config.SSHTierProduction, User: "deploy", Port: 2222, IdentityFile: "~/.ssh/custom key"}}
	f := newSettingsForm(m.cfgPath, m.cfg)
	f.tab = stSSH
	if f.dirty() {
		t.Fatal("an untouched form must not read as dirty")
	}
	vis := f.visible()
	labels := map[string]bool{}
	for _, i := range vis {
		labels[f.fields[i].label] = true
	}
	for _, want := range []string{"SSH alias", "What it means", "Remote user", "Tier", "Allow unsandboxed", "Default directory", "Add host", "Remove host"} {
		if !labels[want] {
			t.Errorf("settings tab missing %q", want)
		}
	}

	// Add one, fill it, save, and it is in the file.
	f.focusSSH(sshAddKey)
	f.update(keyMsg("enter"))
	serial := f.sshKeys[len(f.sshKeys)-1]
	f.field(sshHostKey(serial, "alias")).text = "stage-api"
	f.field(sshHostKey(serial, "tier")).text = config.SSHTierStaging
	f.field(sshHostKey(serial, "grant_max")).text = "15"
	if _, ev := f.update(keyMsg("ctrl+s")); ev != settingsFormSaved {
		t.Fatalf("save failed: %s", f.err)
	}
	loaded, err := config.Load(m.cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded.SSHHosts) != 2 || loaded.SSHHosts[1].Alias != "stage-api" || loaded.SSHHosts[1].GrantMaxMinutes != 15 || loaded.SSHHosts[0].User != "deploy" || loaded.SSHHosts[0].Port != 2222 || loaded.SSHHosts[0].IdentityFile != "~/.ssh/custom key" {
		t.Fatalf("saved hosts: %+v", loaded.SSHHosts)
	}

	// Remove takes the shifted Y, and any other key keeps it.
	f = newSettingsForm(m.cfgPath, loaded)
	first := f.sshKeys[0]
	f.focusSSH(sshHostKey(first, "remove"))
	f.update(keyMsg("enter"))
	if !strings.Contains(stripANSI(strings.Join(f.footerLines(), "\n")), "Remove prod-api?") {
		t.Errorf("remove must ask: %v", f.footerLines())
	}
	f.update(keyMsg("y"))
	if f.field(sshHostKey(first, "alias")) == nil {
		t.Fatal("bare y must not remove")
	}
	f.focusSSH(sshHostKey(first, "remove"))
	f.update(keyMsg("enter"))
	f.update(keyMsg("Y"))
	if f.field(sshHostKey(first, "alias")) != nil {
		t.Fatal("Y removes")
	}
}

func TestSSHSettingsTierAdviceFollowsTheTier(t *testing.T) {
	m := newTestRoot(t)
	m.cfg.SSHHosts = []config.SSHHost{{Alias: "a", Tier: config.SSHTierStaging}}
	f := newSettingsForm(m.cfgPath, m.cfg)
	s := f.sshKeys[0]
	f.focusSSH(sshHostKey(s, "tier"))
	if got := stripANSI(strings.Join(f.footerLines(), "\n")); !strings.Contains(got, "up to 30 minutes") {
		t.Errorf("staging: %s", got)
	}
	f.field(sshHostKey(s, "tier")).text = config.SSHTierProduction
	if got := stripANSI(strings.Join(f.footerLines(), "\n")); !strings.Contains(got, "No grants. Refuses agents that are not sandboxed.") {
		t.Errorf("production: %s", got)
	}
	f.focusSSH(sshHostKey(s, "user"))
	if got := stripANSI(strings.Join(f.footerLines(), "\n")); !strings.Contains(got, "Without a restricted user") {
		t.Errorf("user: %s", got)
	}
}

func TestSSHSettingsInvalidHostKeepsTheFormAndTheFile(t *testing.T) {
	m := newTestRoot(t)
	f := newSettingsForm(m.cfgPath, m.cfg)
	f.focusSSH(sshAddKey)
	f.update(keyMsg("enter"))
	s := f.sshKeys[0]
	f.field(sshHostKey(s, "alias")).text = "bad alias"
	if _, ev := f.update(keyMsg("ctrl+s")); ev == settingsFormSaved {
		t.Fatal("an invalid alias must not save")
	}
	if !strings.Contains(f.err, "alias") {
		t.Errorf("error should name the field: %q", f.err)
	}
	if len(m.cfg.SSHHosts) != 0 {
		t.Errorf("a failed save must roll back: %+v", m.cfg.SSHHosts)
	}
}

// Nothing in this package may ever approve from a global key: Y lives only in the
// overlay, which the cockpit's terminal focus sits in front of.
func TestSSHFocusedTerminalNeverApproves(t *testing.T) {
	now := sshFreezeClock(t)
	sshNoAttach(t)
	d := &sshFakeDaemon{}
	m := sshWorkspace(t, d, []protocol.SSHConnInfo{sshConnFixture(protocol.SSHTierProduction)}, sshPendingFixture())
	*now = now.Add(time.Second)
	sshKey(m, "T")
	if !m.embedFocused {
		t.Fatal("T focuses the terminal")
	}
	sshKey(m, "Y")
	if d.count(protocol.CmdSSHApprove) != 0 {
		t.Fatal("a keystroke meant for the terminal must never approve")
	}
}

// Nothing here renders daemon or agent text through a format string or a wire
// path: every wire string passes one of these helpers. A grep, like the desktop's
// Controls test, because a new raw path would look perfectly fine on a clean host.
func TestSSHViewsNeverPrintWireStringsRaw(t *testing.T) {
	bad := regexp.MustCompile(`(faintText|warnText|badText|goodText|sshInk|sshInfo|statusOrange|sshAccent)\.Render\((c|b|e|ev|h|mt|st|o|conn)\.(Why|Scope|Title|Body|Command|Text|Note|Value|Name|Preview|StatusDetail|StateDetail|Label|Host)\)`)
	for _, file := range []string{"sshview.go", "sshpager.go", "sshcockpit.go"} {
		src, err := os.ReadFile(filepath.Join(".", file))
		if err != nil {
			t.Fatal(err)
		}
		if loc := bad.Find(src); loc != nil {
			t.Errorf("%s renders a wire string unsanitized: %s", file, fmt.Sprintf("%q", loc))
		}
	}
}

// The daemon's revision pin refusing is the same news as the check closing, and
// must read the same way.
func TestSSHServerRevisionRefusalSaysSo(t *testing.T) {
	now := sshFreezeClock(t)
	d := &sshFakeDaemon{errs: map[string]string{protocol.CmdSSHProposal: "the proposal has changed since you read it, so nothing ran"}}
	m := sshWorkspace(t, d, []protocol.SSHConnInfo{sshConnFixture(protocol.SSHTierDev)},
		sshElementItem("p1", 1, protocol.SSHElement{Kind: "proposal", Command: "ls"}))
	sshKey(m, "P")
	*now = now.Add(time.Second)
	sshKey(m, "Y")
	if v := sshPlain(m); !strings.Contains(v, "Claude changed this proposal. Read it again.") {
		t.Errorf("refusal copy:\n%s", v)
	}
}

// A paste lands in whichever field owns the keyboard, in the workspace too:
// bubbletea delivers it as its own message, which no key handler ever sees.
func TestSSHPasteReachesTheOpenField(t *testing.T) {
	sshFreezeClock(t)
	d := &sshFakeDaemon{}
	m := sshWorkspace(t, d, []protocol.SSHConnInfo{sshConnFixture(protocol.SSHTierDev)})
	m.Update(tea.PasteMsg{Content: "dropped: no field is open"})
	sshKey(m, "i")
	m.Update(tea.PasteMsg{Content: "first\nsecond"})
	if m.ssh.inputBuf != "first second" {
		t.Errorf("composer paste: %q", m.ssh.inputBuf)
	}
	// esc leaves the field and keeps the draft.
	sshKey(m, "esc", "i")
	if m.ssh.inputBuf != "first second" {
		t.Errorf("draft lost: %q", m.ssh.inputBuf)
	}
}

func TestSSHDirectSessionKillNamesTheRecord(t *testing.T) {
	m := newTestRoot(t)
	data := cannedSessions()
	data.Sessions[0].Location = "ssh"
	id := data.Sessions[0].ID
	m.Update(sessionsMsg{data: data})
	m.sessions.confirmKill, m.sessions.killTarget = true, id
	if got := stripANSI(m.cockpitMessage()); !strings.Contains(got, "stays on disk for 14 days") {
		t.Errorf("kill copy: %q", got)
	}
}

// TestSSHDumpFrame prints a representative frame with -run TestSSHDumpFrame -v, for
// a human reading the design. It asserts nothing.
func TestSSHDumpFrame(t *testing.T) {
	if os.Getenv("LOLA_SSH_DUMP") == "" {
		t.Skip("set LOLA_SSH_DUMP=1 to print a frame")
	}
	now := sshFreezeClock(t)
	conn := sshConnFixture(protocol.SSHTierProduction)
	conn.ConnectedAt = now.Add(-12 * time.Minute)
	conn.PendingApprovals = 1
	m := sshWorkspace(t, &sshFakeDaemon{}, []protocol.SSHConnInfo{conn},
		protocol.SSHTimelineItem{ID: "y1", Kind: protocol.SSHItemSystem, Order: 1, At: *now, System: &protocol.SSHSystemEvent{Kind: protocol.SSHEventConnected, Text: "Connected to prod-api.", Conn: "c1"}},
		sshElementItem("n1", 2, protocol.SSHElement{Kind: "narration", Title: "I'll check whether the disk is full."}),
		sshBlockItem("b1", 3, protocol.SSHBlock{Command: "df -h /var", Why: "check free space on the log volume", Status: protocol.SSHBlockSucceeded, ExitCode: sshExit(0), DurationMs: 300, CreatedAt: *now}),
		sshBlockItem("b2", 4, protocol.SSHBlock{Command: "systemctl status api", Why: "see whether the service restarted", Status: protocol.SSHBlockFailed, ExitCode: sshExit(3), DurationMs: 400, StdoutBytes: 2048, CreatedAt: *now, Preview: "Active: failed (Result: exit-code) since Wed 13:58:11 UTC\n"}),
		sshElementItem("s1a", 5, protocol.SSHElement{Kind: "summary", Title: "Disk on /var fills every 40 minutes. Log rotation stopped after the 2 Nov deploy.", UpdatedAt: now.Add(-2 * time.Minute)}),
		sshElementItem("p1", 6, protocol.SSHElement{Kind: "plan", Title: "Fix the restarts", Steps: []protocol.SSHElementStep{{Text: "Check disk", State: "done"}, {Text: "Check log rotation config", State: "active"}, {Text: "Restart", State: "pending"}}}),
		sshElementItem("h1", 7, protocol.SSHElement{Kind: "hypothesis", Title: "The disk fills up", State: "confirmed"}),
		sshElementItem("f1", 8, protocol.SSHElement{Kind: "finding", Severity: "high", Title: "Log rotation has not run since 2 Nov.", Evidence: []protocol.SSHElementEvidence{{Block: "b2", Verified: true}, {Block: "b1", Verified: true}}}),
		protocol.SSHTimelineItem{ID: "u1", Kind: protocol.SSHItemHuman, Order: 9, Human: &protocol.SSHHumanMessage{Text: "Look at the log rotation too.", Delivered: true, Via: protocol.SSHViaReply, DeliveredAt: *now}},
		sshBlockItem("b5", 10, protocol.SSHBlock{Command: "systemctl restart api", Cwd: "/srv/api", Why: "service is wedged after the disk filled up", Tier: protocol.SSHTierProduction, Status: protocol.SSHBlockPendingApproval, TimeoutSeconds: 120, CreatedAt: *now, ApprovalExpiresAt: now.Add(29 * time.Minute)}),
	)
	*now = now.Add(time.Second)
	m.width, m.height = 100, 44
	t.Log("\n" + sshPlain(m))
}

// An ended connection leaves the switcher but keeps its place in the stream. The one
// on screen stays, or the chip row would lose its selection.
func TestSSHSwitcherOffersOnlyWhatIsInPlay(t *testing.T) {
	s := &sshModel{connID: "live", conns: []protocol.SSHConnInfo{
		{ID: "live", Host: "dev", State: protocol.SSHConnConnected},
		{ID: "no", Host: "prod", State: protocol.SSHConnDenied},
		{ID: "done", Host: "prod", State: protocol.SSHConnClosed},
		{ID: "ask", Host: "stg", State: protocol.SSHConnRequested},
	}}
	got := s.switchable()
	if len(got) != 2 || got[0].ID != "live" || got[1].ID != "ask" {
		t.Fatalf("switchable = %+v", got)
	}
	s.cycleConn(1)
	if s.connID != "ask" {
		t.Fatalf("cycle landed on %s", s.connID)
	}
	s.connID = "done"
	if got := s.switchable(); len(got) != 3 {
		t.Fatalf("the connection on screen must stay offered: %+v", got)
	}
}

// Only the daemon's "already decided" reads as a refusal. A grant the host does not
// allow is not one, and calling it refused would hide why.
func TestSSHApproveErrorSaysWhatHappened(t *testing.T) {
	s := &sshModel{}
	s.noteActionErr(sshDoneMsg{action: "approve", err: fmt.Errorf("block b1 is expired, it is not waiting for approval")})
	if !strings.Contains(s.flash, "already refused") {
		t.Fatalf("flash = %q", s.flash)
	}
	s.noteActionErr(sshDoneMsg{action: "approve", err: fmt.Errorf("prod approves every command, so it cannot have a grant")})
	if strings.Contains(s.flash, "already refused") || !strings.Contains(s.flash, "cannot have a grant") {
		t.Fatalf("flash = %q", s.flash)
	}
}

// A new ssh session names a host, not a project, so it starts from the home screen too.
func TestSSHHostPickerOpensFromHome(t *testing.T) {
	d := &sshFakeDaemon{replies: map[string]any{protocol.CmdSSHHosts: protocol.SSHHostsData{Hosts: []protocol.SSHHostInfo{{Name: "dev", Alias: "dev", Tier: "dev"}}}}}
	d.install(t)
	m := homeRoot(t)
	sshKey(m, "H")
	if m.ssh.picker == nil {
		t.Fatal("H on the home screen must open the host picker")
	}
}

func TestSSHDurAndBytesMatchTheApp(t *testing.T) {
	for d, want := range map[time.Duration]string{
		2 * time.Minute:              "2m",
		time.Minute + 12*time.Second: "1m 12s",
		time.Hour:                    "1h",
		time.Hour + 3*time.Minute:    "1h 3m",
		4300 * time.Millisecond:      "4.3s",
	} {
		if got := sshDur(d); got != want {
			t.Errorf("sshDur(%v) = %q, want %q", d, got, want)
		}
	}
	for n, want := range map[int64]string{
		512:              "512 B",
		2150:             "2 KB",
		1024 * 1024:      "1.0 MB",
		11 * 1024 * 1024: "11 MB",
	} {
		if got := sshBytes(n); got != want {
			t.Errorf("sshBytes(%d) = %q, want %q", n, got, want)
		}
	}
	for d, want := range map[time.Duration]string{
		28*time.Minute + 55*time.Second: "29 min",
		5 * time.Minute:                 "5 min",
		4*time.Minute + 59*time.Second:  "4m 59s",
	} {
		if got := sshExpiry(d); got != want {
			t.Errorf("sshExpiry(%v) = %q, want %q", d, got, want)
		}
	}
}

func TestSSHPendingRowHasNoAgeForAZeroTime(t *testing.T) {
	got := sshBlockRight(&protocol.SSHBlock{Status: protocol.SSHBlockPendingApproval}, time.Now())
	if strings.Contains(got, "h ") || strings.Contains(got, "·") {
		t.Fatalf("zero CreatedAt rendered an age: %q", got)
	}
}

// An empty timeline only claims "Connected" on a connected connection.
func TestSSHEmptyStreamDoesNotInventAConnection(t *testing.T) {
	sshFreezeClock(t)
	for _, st := range []string{protocol.SSHConnRequested, protocol.SSHConnAuthenticating, protocol.SSHConnFailed, protocol.SSHConnClosed} {
		c := sshConnFixture(protocol.SSHTierProduction)
		c.State = st
		m := sshWorkspace(t, &sshFakeDaemon{}, []protocol.SSHConnInfo{c})
		if v := sshPlain(m); strings.Contains(v, "has not run anything yet") {
			t.Errorf("state %s claims to be connected:\n%s", st, v)
		}
	}
	c := sshConnFixture(protocol.SSHTierProduction)
	c.State = protocol.SSHConnConnected
	m := sshWorkspace(t, &sshFakeDaemon{}, []protocol.SSHConnInfo{c})
	if v := sshPlain(m); !strings.Contains(v, "Connected.") {
		t.Errorf("connected lost its line:\n%s", v)
	}
}

// A connection that is over has no wheel and no terminal to offer.
func TestSSHEndedConnectionDropsWheelAndKeys(t *testing.T) {
	sshFreezeClock(t)
	c := sshConnFixture(protocol.SSHTierProduction)
	c.State = protocol.SSHConnClosed
	m := sshWorkspace(t, &sshFakeDaemon{}, []protocol.SSHConnInfo{c})
	v := sshPlain(m)
	for _, gone := range []string{"wheel AGENT", "W wheel", "T terminal", "C close"} {
		if strings.Contains(v, gone) {
			t.Errorf("a closed connection still shows %q:\n%s", gone, v)
		}
	}
}

func TestSSHOneLostCommandIsSingular(t *testing.T) {
	sshFreezeClock(t)
	c := sshConnFixture(protocol.SSHTierProduction)
	c.State = protocol.SSHConnDisconnected
	m := sshWorkspace(t, &sshFakeDaemon{}, []protocol.SSHConnInfo{c})
	m.ssh.items = []protocol.SSHTimelineItem{{ID: "b1", Kind: protocol.SSHItemBlock, Block: &protocol.SSHBlock{ID: "b1", Conn: c.ID, Status: protocol.SSHBlockLost, Command: "x"}}}
	v := strings.Join(strings.Fields(sshPlain(m)), " ")
	if strings.Contains(v, "command were") || !strings.Contains(v, "1 command was running. Its result is unknown") {
		t.Fatalf("grammar:\n%s", v)
	}
}

func TestSSHBriefSummaryClipsWithAnEllipsis(t *testing.T) {
	sshFreezeClock(t)
	c := sshConnFixture(protocol.SSHTierProduction)
	m := sshWorkspace(t, &sshFakeDaemon{}, []protocol.SSHConnInfo{c})
	long := strings.Repeat("after the 2 Nov deploy ", 20)
	m.ssh.items = []protocol.SSHTimelineItem{{ID: "e1", Kind: protocol.SSHItemElement, Element: &protocol.SSHElement{ID: "e1", Kind: "summary", Title: long}}}
	lines := m.sshBriefLines(60)
	found := false
	for _, l := range lines {
		if strings.Contains(l, "…") && lipgloss.Width(l) <= 60 {
			found = true
		}
	}
	if !found {
		t.Fatalf("no ellipsis within 60 columns: %q", lines)
	}
}

func TestSSHPickerConnectsTheSelectedSession(t *testing.T) {
	m := newTestRoot(t)
	d := &sshFakeDaemon{replies: map[string]any{
		protocol.CmdSSHHosts:   protocol.SSHHostsData{Hosts: []protocol.SSHHostInfo{{Name: "stage", Alias: "stage", Tier: "staging"}}},
		protocol.CmdSSHConnect: protocol.SSHConnectData{Conn: protocol.SSHConnInfo{ID: "c1", Host: "stage", State: protocol.SSHConnConnecting}},
	}}
	d.install(t)
	m.Update(sessionsMsg{data: cannedSessions()})
	sshKey(m, "H")
	if !strings.Contains(sshPlain(m), "c connect this session") {
		t.Fatalf("the key is not offered:\n%s", sshPlain(m))
	}
	sshKey(m, "c")
	if d.count(protocol.CmdSSHConnect) != 1 || d.count(protocol.CmdSSHOpen) != 0 {
		t.Fatalf("connect=%d open=%d", d.count(protocol.CmdSSHConnect), d.count(protocol.CmdSSHOpen))
	}
	if m.ssh.picker != nil || !m.ssh.open {
		t.Fatalf("expected the workspace (picker=%v open=%v)", m.ssh.picker, m.ssh.open)
	}
}

func TestSSHPickerWithNoSessionOffersNoConnect(t *testing.T) {
	m := newTestRoot(t)
	d := &sshFakeDaemon{replies: map[string]any{
		protocol.CmdSSHHosts: protocol.SSHHostsData{Hosts: []protocol.SSHHostInfo{{Name: "stage", Alias: "stage", Tier: "staging"}}},
	}}
	d.install(t)
	sshKey(m, "H")
	if strings.Contains(sshPlain(m), "c connect this session") {
		t.Fatalf("offered with nothing selected:\n%s", sshPlain(m))
	}
	sshKey(m, "c")
	if d.count(protocol.CmdSSHConnect) != 0 {
		t.Fatal("connected with nothing selected")
	}
}

func TestSSHBangLineRunsOnTheHostAndArmsOnProduction(t *testing.T) {
	for _, c := range []struct {
		tier   string
		enters int
	}{{"dev", 1}, {"production", 2}} {
		t.Run(c.tier, func(t *testing.T) {
			s := &sshModel{session: "s1", input: sshInputSay, conns: []protocol.SSHConnInfo{{ID: "c1", Host: "web", Tier: c.tier, State: protocol.SSHConnConnected}}, connID: "c1"}
			var cmd tea.Cmd
			for i := 0; i < c.enters; i++ {
				cmd = s.sshRunLine("uptime", "!uptime")
				if i < c.enters-1 && (cmd != nil || s.inputBuf != "!uptime") {
					t.Fatalf("enter %d ran it, or lost the line", i)
				}
			}
			if cmd == nil {
				t.Fatal("never ran")
			}
		})
	}
}

// --- a shell's question ------------------------------------------------------

func sshPromptWorkspace(t *testing.T, q protocol.SSHPromptInfo) (*rootModel, *sshFakeDaemon, *time.Time) {
	t.Helper()
	now := sshFreezeClock(t)
	c := sshConnFixture(protocol.SSHTierDev)
	c.Prompt = &q
	d := &sshFakeDaemon{replies: map[string]any{protocol.CmdSSHPromptAnswer: protocol.SSHPromptAnswerData{Status: protocol.SSHPromptSent}}}
	m := sshWorkspace(t, d, []protocol.SSHConnInfo{c})
	*now = now.Add(2 * sshArmDelay)
	return m, d, now
}

func TestSSHPromptIsMaskedAndSentOnce(t *testing.T) {
	const secret = "hunter2 with space"
	q := protocol.SSHPromptInfo{ID: "q1", Stage: protocol.SSHPromptCommand, Kind: protocol.SSHPromptSecret, Line: "[sudo] password for m:", Command: "sudo ls", Attempt: 1}
	m, d, _ := sshPromptWorkspace(t, q)
	v := sshPlain(m)
	for _, want := range []string{"[sudo] password for m:", "sudo ls", "Not accepted. Try again.", "Y answer", "X cancel", "T terminal"} {
		if !strings.Contains(v, want) {
			t.Errorf("card missing %q:\n%s", want, v)
		}
	}
	sshKey(m, "Y")
	if m.ssh.input != sshInputSecret {
		t.Fatalf("input = %v", m.ssh.input)
	}
	m.Update(tea.PasteMsg{Content: secret + "\n"})
	v = sshPlain(m)
	if strings.Contains(v, secret) || strings.Contains(v, "hunter") {
		t.Fatalf("the answer is on screen:\n%s", v)
	}
	if !strings.Contains(v, strings.Repeat("•", len(secret))) {
		t.Fatalf("no mask:\n%s", v)
	}
	if d.count(protocol.CmdSSHPromptAnswer) != 0 {
		t.Fatal("sent before enter")
	}
	sshKey(m, "enter")
	if d.count(protocol.CmdSSHPromptAnswer) != 1 {
		t.Fatal("not sent")
	}
	var a protocol.SSHPromptAnswerArgs
	_ = json.Unmarshal(d.last(protocol.CmdSSHPromptAnswer).Args, &a)
	if a.Secret != secret || a.Prompt != "q1" || a.Action != protocol.SSHPromptSend {
		t.Fatalf("args = %+v", a)
	}
	if m.ssh.inputBuf != "" || strings.Contains(sshPlain(m), secret) {
		t.Fatal("the field was not cleared")
	}
	for _, draft := range m.ssh.drafts {
		if strings.Contains(draft, secret) {
			t.Fatal("the answer reached a draft")
		}
	}
}

func TestSSHPromptEscapeForgetsWhatWasTyped(t *testing.T) {
	m, d, _ := sshPromptWorkspace(t, protocol.SSHPromptInfo{ID: "q1", Stage: protocol.SSHPromptSignIn, Kind: protocol.SSHPromptSecret, Line: "Password:"})
	sshKey(m, "Y")
	m.Update(tea.PasteMsg{Content: "half"})
	sshKey(m, "esc")
	if m.ssh.input != sshInputNone || m.ssh.inputBuf != "" {
		t.Fatalf("input %v buf %q", m.ssh.input, m.ssh.inputBuf)
	}
	if d.count(protocol.CmdSSHPromptAnswer) != 0 {
		t.Fatal("esc sent something")
	}
	for _, draft := range m.ssh.drafts {
		if draft != "" {
			t.Fatalf("draft = %q", draft)
		}
	}
}

func TestSSHPromptConfirmAndCancelTakeOneKey(t *testing.T) {
	for _, tc := range []struct{ key, action string }{{"Y", protocol.SSHPromptYes}, {"N", protocol.SSHPromptNo}, {"X", protocol.SSHPromptCancel}} {
		m, d, _ := sshPromptWorkspace(t, protocol.SSHPromptInfo{
			ID: "q1", Stage: protocol.SSHPromptSignIn, Kind: protocol.SSHPromptConfirm,
			Line: "Are you sure (yes/no)?", Context: []string{"key fingerprint is SHA256:abc."},
		})
		if v := sshPlain(m); !strings.Contains(v, "SHA256:abc") || !strings.Contains(v, "Y yes") {
			t.Fatalf("card:\n%s", v)
		}
		sshKey(m, tc.key)
		var a protocol.SSHPromptAnswerArgs
		if r := d.last(protocol.CmdSSHPromptAnswer); r != nil {
			_ = json.Unmarshal(r.Args, &a)
		}
		if a.Action != tc.action || a.Secret != "" {
			t.Fatalf("%s: %+v", tc.key, a)
		}
	}
}

func TestSSHPromptIgnoresAKeyAlreadyOnItsWay(t *testing.T) {
	sshFreezeClock(t)
	c := sshConnFixture(protocol.SSHTierDev)
	c.Prompt = &protocol.SSHPromptInfo{ID: "q1", Stage: protocol.SSHPromptSignIn, Kind: protocol.SSHPromptConfirm, Line: "Sure (yes/no)?"}
	d := &sshFakeDaemon{}
	m := sshWorkspace(t, d, []protocol.SSHConnInfo{c})
	sshKey(m, "Y")
	sshKey(m, "N")
	if d.count(protocol.CmdSSHPromptAnswer) != 0 {
		t.Fatal("a card answered before it armed")
	}
}
