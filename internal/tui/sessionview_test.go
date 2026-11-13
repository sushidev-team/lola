package tui

import (
	"reflect"
	"strings"
	"testing"

	"github.com/sushidev-team/lola/internal/protocol"
	"github.com/sushidev-team/lola/internal/state"
)

// mixedSessions is a deliberately out-of-order set spanning every sort tier,
// with intra-tier project/issue ties to exercise the stable tiebreak.
func mixedSessions() []protocol.SessionInfo {
	return []protocol.SessionInfo{
		{ID: "1", Project: "web", Issue: "ENG-9", Status: "merged"},
		{ID: "2", Project: "api", Issue: "ENG-2", Status: "working"},
		{ID: "3", Project: "web", Issue: "ENG-1", Status: "needs_input"},
		{ID: "4", Project: "api", Issue: "ENG-7", Status: "review_pending"},
		{ID: "5", Project: "web", Issue: "ENG-3", Status: "ci_failed"},
		{ID: "6", Project: "api", Issue: "ENG-4", Status: "working"},
		{ID: "7", Project: "api", Issue: "ENG-5", Status: "changes_requested"},
		{ID: "8", Project: "web", Issue: "ENG-8", Status: "approved"},
		{ID: "9", Project: "api", Issue: "ENG-6", Status: "dead"},
		{ID: "10", Project: "api", Issue: "ENG-1", Status: "ci_pending"},
	}
}

func statusOrder(in []protocol.SessionInfo) []string {
	ids := make([]string, len(in))
	for i, s := range in {
		ids[i] = s.ID
	}
	return ids
}

func TestSortSessionsAttentionFirst(t *testing.T) {
	in := mixedSessions()
	got := SortSessions(in)

	// Expected order by tier then project,issue:
	// tier0 needs_input:      s3 (web,ENG-1)
	// tier1 action-needed:    s7 (api,ENG-5 changes_requested), s5 (web,ENG-3 ci_failed)
	// tier2 active:           s10 (api,ENG-1 ci_pending), s2 (api,ENG-2), s6 (api,ENG-4)
	// tier3 parked:           s4 (api,ENG-7 review), s8 (web,ENG-8 approved)
	// tier5 done:             s9 (api,ENG-6 dead), s1 (web,ENG-9 merged)
	want := []string{"3", "7", "5", "10", "2", "6", "4", "8", "9", "1"}
	if g := statusOrder(got); !reflect.DeepEqual(g, want) {
		t.Errorf("SortSessions order = %v, want %v", g, want)
	}
}

func TestSortSessionsDoesNotMutate(t *testing.T) {
	in := mixedSessions()
	before := statusOrder(in)
	_ = SortSessions(in)
	if after := statusOrder(in); !reflect.DeepEqual(before, after) {
		t.Errorf("SortSessions mutated input: %v -> %v", before, after)
	}
}

func TestSortSessionsStableTiebreak(t *testing.T) {
	// Same tier (all working), same project — issue is the final deterministic key.
	in := []protocol.SessionInfo{
		{ID: "b", Project: "web", Issue: "ENG-2", Status: "working"},
		{ID: "a", Project: "web", Issue: "ENG-1", Status: "working"},
		{ID: "c", Project: "web", Issue: "ENG-3", Status: "working"},
	}
	got := statusOrder(SortSessions(in))
	want := []string{"a", "b", "c"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("tiebreak order = %v, want %v", got, want)
	}
}

func TestApply(t *testing.T) {
	in := []protocol.SessionInfo{
		{ID: "1", Project: "web", Issue: "ENG-100", Branch: "lola/auth", Status: "working"},
		{ID: "2", Project: "api", Issue: "ENG-200", Branch: "lola/billing", Status: "needs_input"},
		{ID: "3", Project: "web", Issue: "ENG-300", Branch: "lola/authz", Status: "ci_failed"},
		{ID: "4", Project: "api", Issue: "ENG-400", Branch: "lola/search", Status: "merged"},
	}
	ids := func(ss []protocol.SessionInfo) []string { return statusOrder(ss) }

	cases := []struct {
		name string
		f    Filter
		want []string
	}{
		{"empty matches all", Filter{}, []string{"1", "2", "3", "4"}},
		{"text over issue", Filter{Text: "eng-200"}, []string{"2"}},
		{"text over branch", Filter{Text: "auth"}, []string{"1", "3"}},
		{"text over project", Filter{Text: "api"}, []string{"2", "4"}},
		{"text over the legacy status word", Filter{Text: "ci_failed"}, []string{"3"}},
		// The Display word too, so "/needs you" finds what the pill actually says.
		{"text over the display word", Filter{Text: "needs you"}, []string{"2"}},
		{"text case-insensitive", Filter{Text: "AUTH"}, []string{"1", "3"}},
		{"text no match", Filter{Text: "zzz"}, nil},
		{"attention only", Filter{AttentionOnly: true}, []string{"2", "3"}},
		{"project exact", Filter{Project: "web"}, []string{"1", "3"}},
		{"status exact", Filter{Status: "merged"}, []string{"4"}},
		{"combined project+attention", Filter{Project: "web", AttentionOnly: true}, []string{"3"}},
		{"combined text+status", Filter{Text: "auth", Status: "working"}, []string{"1"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := ids(Apply(in, c.f))
			if len(got) == 0 && len(c.want) == 0 {
				return
			}
			if !reflect.DeepEqual(got, c.want) {
				t.Errorf("Apply(%+v) = %v, want %v", c.f, got, c.want)
			}
		})
	}
}

func TestApplyDoesNotMutate(t *testing.T) {
	in := []protocol.SessionInfo{
		{ID: "1", Status: "working"},
		{ID: "2", Status: "needs_input"},
	}
	before := statusOrder(in)
	_ = Apply(in, Filter{AttentionOnly: true})
	if after := statusOrder(in); !reflect.DeepEqual(before, after) {
		t.Errorf("Apply mutated input: %v -> %v", before, after)
	}
}

func TestAttentionCount(t *testing.T) {
	if n := AttentionCount(mixedSessions()); n != 3 {
		// needs_input(1) + ci_failed(1) + changes_requested(1) = 3
		t.Errorf("AttentionCount = %d, want 3", n)
	}
	if n := AttentionCount(nil); n != 0 {
		t.Errorf("AttentionCount(nil) = %d, want 0", n)
	}
}

// allStatuses is the full derived status vocabulary the views must handle.
var allStatuses = state.AllStatuses()

func TestKanbanColumnsUniqueKeys(t *testing.T) {
	// Columns no longer carry a status set — membership is a function of the axis
	// PAIR (state.KanbanKeyFor) — so what is left to pin is that the keys are
	// unique and that every key the bucketing can produce is a real column.
	seenKey := map[string]bool{}
	for _, col := range KanbanColumns() {
		if seenKey[col.Key] {
			t.Errorf("duplicate column key %q", col.Key)
		}
		if col.Title == "" {
			t.Errorf("column %q has no title", col.Key)
		}
		seenKey[col.Key] = true
	}
	if !seenKey[kanbanFallbackKey] {
		t.Errorf("fallback key %q is not a real column", kanbanFallbackKey)
	}
	for _, si := range everyAxisPair() {
		if !seenKey[kanbanKeyFor(si)] {
			t.Errorf("kanbanKeyFor(%s/%s) = %q, not a column key",
				si.AgentState, si.Delivery, kanbanKeyFor(si))
		}
	}
}

// everyAxisPair is one session per (AgentState, DeliveryState) combination —
// the whole space the pair-based tables classify, including the pairs the old
// collapsed vocabulary could not name (a working agent under a red build, an
// exited agent over an approved PR).
func everyAxisPair() []protocol.SessionInfo {
	agents := []string{"starting", "working", "waiting_input", "idle", "exited", "dead", "shell", "orphaned"}
	deliveries := []string{"none", "draft", "ci_pending", "ci_failed", "merge_conflict",
		"changes_requested", "review_pending", "approved", "merged", "closed"}
	out := make([]protocol.SessionInfo, 0, len(agents)*len(deliveries))
	for _, a := range agents {
		for _, d := range deliveries {
			out = append(out, protocol.SessionInfo{ID: a + "/" + d, AgentState: a, Delivery: d})
		}
	}
	return out
}

func TestGroupKanbanEverySessionExactlyOneColumn(t *testing.T) {
	in := everyAxisPair()
	groups := GroupKanban(in)

	// Every column key from KanbanColumns is present (even if empty).
	for _, col := range KanbanColumns() {
		if _, ok := groups[col.Key]; !ok {
			t.Errorf("GroupKanban missing column key %q", col.Key)
		}
	}

	total := 0
	placed := map[string]int{}
	for _, sessions := range groups {
		total += len(sessions)
		for _, s := range sessions {
			placed[s.ID]++
		}
	}
	if total != len(in) {
		t.Errorf("GroupKanban placed %d sessions, want %d", total, len(in))
	}
	for _, s := range in {
		if placed[s.ID] != 1 {
			t.Errorf("session %q placed %d times, want exactly 1", s.ID, placed[s.ID])
		}
	}
}

// A record from a daemon older than the axis split carries only the rolled-up
// Status; axesOf backfills both axes from it, so the whole legacy vocabulary
// still buckets, sorts and colors. This is the compatibility floor — the TUI is
// a client of whatever daemon happens to be running.
func TestLegacyStatusBackfillsBothAxes(t *testing.T) {
	cases := map[string]struct {
		agent    state.AgentState
		delivery state.DeliveryState
		column   string
	}{
		"working":           {state.AgentWorking, state.DeliveryNone, "working"},
		"idle":              {state.AgentIdle, state.DeliveryNone, "working"},
		"needs_input":       {state.AgentWaitingInput, state.DeliveryNone, "needs"},
		"session_ended":     {state.AgentExited, state.DeliveryNone, "done"},
		"dead":              {state.AgentDead, state.DeliveryNone, "done"},
		"shell":             {state.AgentShell, state.DeliveryNone, "working"},
		"orphaned":          {state.AgentOrphaned, state.DeliveryNone, "working"},
		"draft":             {state.AgentIdle, state.DeliveryDraft, "working"},
		"ci_pending":        {state.AgentIdle, state.DeliveryCIPending, "working"},
		"ci_failed":         {state.AgentIdle, state.DeliveryCIFailed, "fixing"},
		"merge_conflict":    {state.AgentIdle, state.DeliveryMergeConflict, "fixing"},
		"changes_requested": {state.AgentIdle, state.DeliveryChangesRequested, "fixing"},
		"review_pending":    {state.AgentIdle, state.DeliveryReviewPending, "review"},
		"approved":          {state.AgentIdle, state.DeliveryApproved, "review"},
		"merged":            {state.AgentIdle, state.DeliveryMerged, "done"},
		"closed":            {state.AgentIdle, state.DeliveryClosed, "done"},
	}
	for _, status := range allStatuses {
		want, ok := cases[status]
		if !ok {
			t.Fatalf("the rolled-up vocabulary grew a word this test does not pin: %q", status)
		}
		si := protocol.SessionInfo{ID: status, Status: status}
		a, d := axesOf(si)
		if a != want.agent || d != want.delivery {
			t.Errorf("axesOf(%q) = (%q, %q), want (%q, %q)", status, a, d, want.agent, want.delivery)
		}
		if got := kanbanKeyFor(si); got != want.column {
			t.Errorf("kanbanKeyFor(%q) = %q, want %q", status, got, want.column)
		}
	}
	// A live daemon's explicit axes always win over the rolled-up word — the
	// backfill is a fallback, never an override.
	a, d := axesOf(protocol.SessionInfo{Status: "ci_pending", AgentState: "working", Delivery: "ci_failed"})
	if a != state.AgentWorking || d != state.DeliveryCIFailed {
		t.Errorf("explicit axes = (%q, %q), want (working, ci_failed)", a, d)
	}
}

// A word the vocabulary has not caught up to must read as a LIVE session, not a
// dead one — hiding an unrecognized agent from the views built to surface it is
// the one failure mode that costs work.
func TestUnknownStateFallsBackToLive(t *testing.T) {
	si := protocol.SessionInfo{ID: "x", AgentState: "totally_made_up"}
	if got := displayOf(si); got != state.DisplayWorking {
		t.Errorf("displayOf(unknown agent) = %q, want %q", got, state.DisplayWorking)
	}
	if got := kanbanKeyFor(si); got != kanbanFallbackKey {
		t.Errorf("kanbanKeyFor(unknown) = %q, want fallback %q", got, kanbanFallbackKey)
	}
	if got := kanbanKeyFor(protocol.SessionInfo{Status: "totally_made_up"}); got != kanbanFallbackKey {
		t.Errorf("kanbanKeyFor(unknown legacy status) = %q, want fallback %q", got, kanbanFallbackKey)
	}
}

func TestSessionDisplayReusesStyleAndHasBadge(t *testing.T) {
	seen := map[string]state.Display{}
	for _, d := range state.AllDisplays() {
		si := protocol.SessionInfo{AgentState: agentStateFor(d)}
		got := sessionDisplay(si)
		if got.Badge == "" {
			t.Errorf("sessionDisplay(%q) empty badge", d)
		}
		if len([]rune(got.Badge)) > 2 {
			t.Errorf("sessionDisplay(%q) badge %q longer than 2 chars", d, got.Badge)
		}
		if prev, dup := seen[got.Badge]; dup {
			t.Errorf("badge %q shared by %q and %q", got.Badge, prev, d)
		}
		seen[got.Badge] = d
		// Style must be exactly what displayStyle yields (no divergence).
		if got.Style.GetForeground() != displayStyle(d).GetForeground() {
			t.Errorf("sessionDisplay(%q) style diverged from displayStyle", d)
		}
	}
}

// agentStateFor is the inverse of state.DisplayFor for the pill values that
// have a single obvious agent word — enough to drive a wire record in a test.
func agentStateFor(d state.Display) string {
	switch d {
	case state.DisplayIdle:
		return "idle"
	case state.DisplayNeedsYou:
		return "waiting_input"
	case state.DisplayGone:
		return "dead"
	case state.DisplayShell:
		return "shell"
	case state.DisplayOrphaned:
		return "orphaned"
	}
	return "working"
}

// The two attention questions are DIFFERENT, and the UI asks each in its own
// place: waitingOnHuman ("is this session asking ME something", the "!" marker
// and the n/N jump) is the agent axis alone; needsHuman ("does a human have to
// look at this", the triage count and the attention filter) is the predicate
// over both axes.
func TestAttentionPredicatesAreDistinct(t *testing.T) {
	redBuild := protocol.SessionInfo{AgentState: "working", Delivery: "ci_failed"}
	if waitingOnHuman(redBuild) {
		t.Error("a working agent under a red build is not waiting at a prompt")
	}
	if !needsHuman(redBuild) {
		t.Error("a red build needs a human")
	}
	blocked := protocol.SessionInfo{AgentState: "waiting_input", Delivery: "review_pending"}
	if !waitingOnHuman(blocked) || !needsHuman(blocked) {
		t.Error("a blocked agent is both waiting and needing a human")
	}
	quiet := protocol.SessionInfo{AgentState: "idle", Delivery: "review_pending"}
	if waitingOnHuman(quiet) || needsHuman(quiet) {
		t.Error("an idle agent parked on a reviewer needs nothing")
	}
}

// answerable mirrors internal/daemon/answer.go's gate exactly: the affordance
// and the daemon must not disagree, in either direction.
func TestAnswerableMirrorsTheDaemonGate(t *testing.T) {
	cases := []struct {
		name string
		si   protocol.SessionInfo
		want bool
	}{
		{"question", protocol.SessionInfo{AgentState: "waiting_input", InputReason: "question"}, true},
		{"permission", protocol.SessionInfo{AgentState: "waiting_input", InputReason: "permission_prompt"}, true},
		{"legacy reasonless block", protocol.SessionInfo{AgentState: "waiting_input"}, true},
		{"modal swallows prose", protocol.SessionInfo{AgentState: "waiting_input", InputReason: "dialog"}, false},
		{"quota cannot act on a reply", protocol.SessionInfo{AgentState: "waiting_input", InputReason: "quota_limited"}, false},
		// The case the old string gate refused: a finished turn resting at its
		// prompt is now AgentIdle, and it is exactly where a human wants to reply.
		{"idle at its prompt", protocol.SessionInfo{AgentState: "idle", AtPrompt: true}, true},
		{"idle, gate closed", protocol.SessionInfo{AgentState: "idle"}, false},
		{"mid-turn", protocol.SessionInfo{AgentState: "working", AtPrompt: true}, false},
		{"gone", protocol.SessionInfo{AgentState: "dead", AtPrompt: true}, false},
		// A legacy record: the backfill makes needs_input answerable, as before.
		{"legacy needs_input", protocol.SessionInfo{Status: "needs_input"}, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := answerable(c.si); got != c.want {
				t.Errorf("answerable = %v, want %v", got, c.want)
			}
		})
	}
}

// inputReasonLabel rides inside the needs_you pill, so it is kept short and the
// misleading identifiers get a human phrase ("quota_limited" reads like a
// dashboard state, "permission_prompt" says twice what "permission" says once);
// everything else de-underscores, and "" stays empty so a reasonless block
// renders a bare pill.
func TestInputReasonLabel(t *testing.T) {
	cases := map[string]string{
		"quota_limited":     "usage limit",
		"permission_prompt": "permission",
		"idle_notification": "idle nudge",
		"question":          "question",
		"some_future_word":  "some future word",
		"":                  "",
	}
	for in, want := range cases {
		if got := inputReasonLabel(in); got != want {
			t.Errorf("inputReasonLabel(%q) = %q, want %q", in, got, want)
		}
	}
	if line := agentDetailLine(protocol.SessionInfo{AgentState: "waiting_input", InputReason: "quota_limited"}); !strings.Contains(line, "usage limit") {
		t.Errorf("agentDetailLine must render the human phrase, got %q", line)
	}
}

// statusLabel humanizes every raw status word — a rendered "ci_failed" reads
// like a translation placeholder — and the fallback de-underscores unknowns.
func TestStatusLabelNeverRendersUnderscores(t *testing.T) {
	for _, s := range allStatuses {
		if strings.Contains(statusLabel(s), "_") {
			t.Errorf("statusLabel(%q) = %q renders a raw identifier", s, statusLabel(s))
		}
	}
	cases := map[string]string{
		"ci_failed":        "ci failed",
		"ci_pending":       "ci running",
		"needs_input":      "needs you",
		"waiting_input":    "waiting",
		"some_future_word": "some future word",
		"working":          "working",
	}
	for in, want := range cases {
		if got := statusLabel(in); got != want {
			t.Errorf("statusLabel(%q) = %q, want %q", in, got, want)
		}
	}
}
