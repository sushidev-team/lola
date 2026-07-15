package tui

import (
	"reflect"
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/sushidev-team/lola/internal/protocol"
)

// boardSessions is a canned snapshot spanning every Kanban column and both
// attention/non-attention statuses, with two working sessions so within-column
// navigation is exercised.
func boardSessions() *protocol.SessionsData {
	return &protocol.SessionsData{Sessions: []protocol.SessionInfo{
		{ID: "need", Project: "web", Issue: "ENG-1", Status: "needs_input", TmuxName: "t-need", Source: "native"},
		{ID: "work", Project: "api", Issue: "ENG-2", Status: "working", Source: "native"},
		{ID: "work2", Project: "api", Issue: "ENG-3", Status: "working", Source: "native"},
		{ID: "fix", Project: "web", Issue: "ENG-4", Status: "ci_failed", Source: "native"},
		{ID: "appr", Project: "api", Issue: "ENG-5", Status: "approved", Source: "native"},
		{ID: "done", Project: "web", Issue: "ENG-6", Status: "merged", Source: "native"},
	}}
}

// A fresh snapshot pins selection to the attention-first top of the active
// lens (needs_input), and the List lens renders attention-first.
func TestListMoveByID(t *testing.T) {
	m := newTestRoot(t)
	m.tab = tabSessions
	m.Update(sessionsMsg{data: boardSessions()})

	if m.sessions.selID != "need" {
		t.Fatalf("initial selection = %q, want need (attention-first)", m.sessions.selID)
	}
	// List order: need, fix, work, work2, appr, done.
	m.Update(keyMsg("j"))
	if m.sessions.selID != "fix" {
		t.Fatalf("j = %q, want fix", m.sessions.selID)
	}
	m.Update(keyMsg("k"))
	if m.sessions.selID != "need" {
		t.Fatalf("k = %q, want need", m.sessions.selID)
	}
}

// "V" cycles the lens; the summary label and the visible layout change.
func TestViewSwitchChangesRender(t *testing.T) {
	m := newTestRoot(t)
	m.tab = tabSessions
	m.width = 200
	m.Update(sessionsMsg{data: boardSessions()})

	v := m.viewString()
	if !strings.Contains(v, "list") {
		t.Errorf("list lens must label itself in the summary:\n%s", v)
	}
	if strings.Contains(v, "Needs You") {
		t.Errorf("list lens must not render kanban columns:\n%s", v)
	}

	m.Update(keyMsg("V"))
	if m.sessions.view != viewKanban {
		t.Fatalf("V must switch to the kanban lens, view = %d", m.sessions.view)
	}
	v = m.viewString()
	if !strings.Contains(v, "kanban") {
		t.Errorf("kanban lens must label itself in the summary:\n%s", v)
	}
	for _, title := range []string{"Needs You", "Working", "In Review", "Done"} {
		if !strings.Contains(v, title) {
			t.Errorf("kanban lens must render column title %q:\n%s", title, v)
		}
	}
}

// The header summary reports the attention queue depth from AttentionCount.
func TestSummaryReportsAttentionCount(t *testing.T) {
	m := newTestRoot(t)
	m.tab = tabSessions
	m.width = 200
	m.Update(sessionsMsg{data: boardSessions()})
	// needs_input(need) + ci_failed(fix) = 2 need you.
	if !strings.Contains(m.viewString(), "2 need you") {
		t.Errorf("summary must show the attention count:\n%s", m.viewString())
	}
}

// "/" opens a live filter that narrows every visible row via Apply; enter
// applies and keeps it, esc clears and closes.
func TestFilterNarrowsList(t *testing.T) {
	m := newTestRoot(t)
	m.tab = tabSessions
	m.width = 200
	m.Update(sessionsMsg{data: boardSessions()})
	fakeRequest(t, nil, mustData(t, protocol.PaneData{}), nil)

	m.Update(keyMsg("/"))
	if !m.sessions.filtering {
		t.Fatal("/ must open the filter bar")
	}
	for _, r := range "api" {
		m.Update(keyMsg(string(r)))
	}
	if m.sessions.filter.Text != "api" {
		t.Fatalf("filter text = %q, want api", m.sessions.filter.Text)
	}

	v := m.viewString()
	for _, want := range []string{"ENG-2", "ENG-3", "ENG-5"} { // project=api
		if !strings.Contains(v, want) {
			t.Errorf("filtered view missing %q:\n%s", want, v)
		}
	}
	for _, hidden := range []string{"ENG-1", "ENG-4", "ENG-6"} { // project=web
		if strings.Contains(v, hidden) {
			t.Errorf("filtered view must hide %q:\n%s", hidden, v)
		}
	}
	// The "/" prefix is styled, so strip ANSI before matching the echoed query
	// (lipgloss v2 renders color even under the no-TTY test profile).
	if !strings.Contains(stripANSI(v), "/api") {
		t.Errorf("filter bar must echo the query:\n%s", v)
	}

	m.Update(keyMsg("enter"))
	if m.sessions.filtering {
		t.Error("enter must close the filter bar")
	}
	if m.sessions.filter.Text != "api" {
		t.Errorf("enter must keep the applied filter, got %q", m.sessions.filter.Text)
	}

	m.Update(keyMsg("/"))
	m.Update(keyMsg("esc"))
	if m.sessions.filtering || m.sessions.filter.Text != "" {
		t.Errorf("esc must clear and close the filter (filtering=%v text=%q)", m.sessions.filtering, m.sessions.filter.Text)
	}
}

// "!" toggles AttentionOnly ("who needs me"): only blocked/broken sessions
// remain, and the summary flags the mode.
func TestAttentionOnlyToggle(t *testing.T) {
	m := newTestRoot(t)
	m.tab = tabSessions
	m.width = 200
	m.Update(sessionsMsg{data: boardSessions()})
	fakeRequest(t, nil, mustData(t, protocol.PaneData{}), nil)

	m.Update(keyMsg("!"))
	if !m.sessions.filter.AttentionOnly {
		t.Fatal("! must enable attention-only")
	}
	v := m.viewString()
	for _, want := range []string{"ENG-1", "ENG-4"} { // needs_input + ci_failed
		if !strings.Contains(v, want) {
			t.Errorf("attention-only view missing %q:\n%s", want, v)
		}
	}
	for _, hidden := range []string{"ENG-2", "ENG-3", "ENG-5", "ENG-6"} {
		if strings.Contains(v, hidden) {
			t.Errorf("attention-only view must hide %q:\n%s", hidden, v)
		}
	}
	if !strings.Contains(v, "needs-you only") {
		t.Errorf("summary must flag attention-only:\n%s", v)
	}

	m.Update(keyMsg("!"))
	if m.sessions.filter.AttentionOnly {
		t.Fatal("! must toggle attention-only back off")
	}
}

// GroupKanban places each session in its intended column, and the board renders
// every column's cards.
func TestKanbanPlacesCardsInColumns(t *testing.T) {
	m := newTestRoot(t)
	m.tab = tabSessions
	m.width = 200
	m.sessions.data = boardSessions()
	m.sessions.view = viewKanban

	cols, groups := m.sessions.kanbanLayout()
	want := map[string][]string{
		"needs":   {"need"},
		"working": {"work", "work2"},
		"fixing":  {"fix"},
		"review":  {"appr"},
		"done":    {"done"},
	}
	for _, c := range cols {
		if got := statusOrder(groups[c.Key]); !reflect.DeepEqual(got, want[c.Key]) {
			t.Errorf("column %q = %v, want %v", c.Key, got, want[c.Key])
		}
	}

	v := m.viewString()
	for _, issue := range []string{"ENG-1", "ENG-2", "ENG-3", "ENG-4", "ENG-5", "ENG-6"} {
		if !strings.Contains(v, issue) {
			t.Errorf("kanban board missing card %q:\n%s", issue, v)
		}
	}
}

// Kanban cursor navigation: left/right move across (skipping empty) columns,
// up/down move within a column, and each move selects the expected session.
func TestKanbanNavigation(t *testing.T) {
	m := newTestRoot(t)
	m.tab = tabSessions
	m.sessions.data = boardSessions()
	m.sessions.view = viewKanban
	m.sessions.selID, m.sessions.cursor = "need", 0

	step := func(key, want string) {
		t.Helper()
		m.Update(keyMsg(key))
		if m.sessions.selID != want {
			t.Fatalf("%s -> %q, want %q", key, m.sessions.selID, want)
		}
	}
	step("l", "work")  // needs -> working (first card)
	step("j", "work2") // down within working
	step("k", "work")  // up within working
	step("l", "fix")   // working -> fixing
	step("l", "appr")  // fixing -> review
	step("l", "done")  // review -> done
	step("l", "done")  // already rightmost: no move
	step("h", "appr")  // done -> review
}

// Selection (by session ID) survives a lens switch in both directions.
func TestViewSwitchPreservesSelection(t *testing.T) {
	m := newTestRoot(t)
	m.tab = tabSessions
	m.width = 200
	m.sessions.data = boardSessions()
	m.sessions.selID, m.sessions.cursor = "fix", 3

	m.Update(keyMsg("V"))
	if m.sessions.view != viewKanban {
		t.Fatal("V must switch to kanban")
	}
	if m.sessions.selID != "fix" {
		t.Errorf("selection lost switching to kanban: %q", m.sessions.selID)
	}
	if sel := m.sessions.selected(); sel == nil || sel.ID != "fix" {
		t.Errorf("selected() = %v, want fix", sel)
	}

	m.Update(keyMsg("V"))
	if m.sessions.view != viewList {
		t.Fatal("V must switch back to list")
	}
	if m.sessions.selID != "fix" {
		t.Errorf("selection lost switching back to list: %q", m.sessions.selID)
	}
}

// A reorder/prune under the cursor keeps the same session focused (ID-pinned),
// and a vanished selection falls to the attention-first top.
func TestSelectionPinnedByIDAcrossRefresh(t *testing.T) {
	m := newTestRoot(t)
	m.tab = tabSessions
	m.Update(sessionsMsg{data: boardSessions()})
	m.sessions.selID, m.sessions.cursor = "work2", indexOfID(m.sessions.data.Sessions, "work2")

	// Same sessions, reversed order: the pinned ID must still resolve to work2.
	rev := boardSessions()
	for i, j := 0, len(rev.Sessions)-1; i < j; i, j = i+1, j-1 {
		rev.Sessions[i], rev.Sessions[j] = rev.Sessions[j], rev.Sessions[i]
	}
	m.Update(sessionsMsg{data: rev})
	if sel := m.sessions.selected(); sel == nil || sel.ID != "work2" {
		t.Fatalf("selected() = %v, want work2 after reorder", sel)
	}

	// Now drop the selected session entirely: selection falls to the top of the
	// List lens (attention-first = needs_input).
	m.Update(sessionsMsg{data: &protocol.SessionsData{Sessions: []protocol.SessionInfo{
		{ID: "need", Project: "web", Issue: "ENG-1", Status: "needs_input", Source: "native"},
		{ID: "work", Project: "api", Issue: "ENG-2", Status: "working", Source: "native"},
	}}})
	if m.sessions.selID != "need" {
		t.Errorf("vanished selection must fall to attention-first top, got %q", m.sessions.selID)
	}
}

// The P7 attention/answer card stays reachable from BOTH lenses: the selection
// drives the same cmd=pane glance and inline answer regardless of layout.
func TestAttentionCardReachableInBothLenses(t *testing.T) {
	pd := &protocol.PaneData{
		Text: "Proceed?\n1. Yes\n2. No\n", HasQuestion: true, Prompt: "Proceed?",
		Choices: []protocol.PaneChoice{{Key: "1", Label: "Yes"}, {Key: "2", Label: "No"}},
	}
	m := needsInputRoot(t, pd) // list lens by default, single needs_input session

	if v := m.viewString(); !strings.Contains(v, "Proceed?") || !strings.Contains(v, "a: answer") {
		t.Errorf("attention card must render in the list lens:\n%s", v)
	}

	m.Update(keyMsg("V"))
	v := m.viewString()
	if !strings.Contains(v, "Proceed?") || !strings.Contains(v, "a: answer") {
		t.Errorf("attention card must stay reachable in the kanban lens:\n%s", v)
	}
	m.Update(keyMsg("a"))
	if !m.sessions.answering {
		t.Fatal("a must arm the answer card from the kanban lens")
	}
}

// The kanban board degrades to a condensed vertical grouping at small widths
// rather than smearing unreadable column slivers.
func TestKanbanNarrowFallback(t *testing.T) {
	m := newTestRoot(t)
	m.tab = tabSessions
	m.width = 30 // below the column-legibility floor
	m.sessions.data = boardSessions()
	m.sessions.view = viewKanban

	v := m.viewString()
	if !strings.Contains(v, "board condensed") {
		t.Errorf("narrow board must show the condensed hint:\n%s", v)
	}
	if !strings.Contains(v, "Needs You") {
		t.Errorf("condensed board must still group by column title:\n%s", v)
	}
}

// The narrow board must clip EVERY line — including the ~50-col condensed hint
// and the per-column title lines — to the terminal width. The fallback stays
// active up to width 73, so at these narrow widths an unclipped line would
// physically wrap and desync bubbletea's line-count repaint.
func TestKanbanNarrowClipsAllLines(t *testing.T) {
	m := newTestRoot(t)
	m.tab = tabSessions
	m.width = 30 // hint (~50 cols) is wider than this
	m.sessions.data = boardSessions()
	m.sessions.view = viewKanban

	cols, groups := m.sessions.kanbanLayout()
	for _, ln := range strings.Split(m.kanbanNarrow(cols, groups, m.width), "\n") {
		if w := lipgloss.Width(ln); w > m.width {
			t.Errorf("narrow-board line width = %d, want <= %d (no wrap):\n%q", w, m.width, ln)
		}
	}
}

// With an APPLIED filter (not the open "/" bar), the 5s refresh must not keep
// the cursor pinned to a session that slid OUT of the filter: the list would
// then show no cursor while the detail pane and destructive keys still act on
// the invisible session. When another visible attention session remains, the
// pin falls to it; when none remain, the selection clears.
func TestAppliedFilterRepinsWhenSelectionLeavesView(t *testing.T) {
	data := func(aStatus string) *protocol.SessionsData {
		return &protocol.SessionsData{Sessions: []protocol.SessionInfo{
			{ID: "a", Project: "web", Issue: "ENG-1", Status: aStatus, TmuxName: "t-a", Source: "native"},
			{ID: "b", Project: "web", Issue: "ENG-2", Status: "needs_input", TmuxName: "t-b", Source: "native"},
			{ID: "c", Project: "web", Issue: "ENG-3", Status: "working", TmuxName: "t-c", Source: "native"},
		}}
	}
	m := newTestRoot(t)
	m.tab = tabSessions
	m.width = 200
	m.Update(sessionsMsg{data: data("needs_input")})
	m.sessions.filter.AttentionOnly = true
	m.sessions.selID, m.sessions.cursor = "a", indexOfID(m.sessions.data.Sessions, "a")

	// Refresh: a progresses to working and leaves the attention filter. b (still
	// needs_input) remains visible, so the pin must move to it — never stay on a.
	m.Update(sessionsMsg{data: data("working")})
	if m.sessions.selID != "b" {
		t.Fatalf("selection must re-pin to the visible attention session, got %q", m.sessions.selID)
	}
	if sel := m.sessions.selected(); sel == nil || sel.ID != "b" {
		t.Fatalf("selected() = %v, want b (visible)", sel)
	}

	// Now b also leaves the filter: no attention session remains visible, so the
	// selection must clear rather than stay pinned to an invisible row.
	both := &protocol.SessionsData{Sessions: []protocol.SessionInfo{
		{ID: "a", Project: "web", Issue: "ENG-1", Status: "working", TmuxName: "t-a", Source: "native"},
		{ID: "b", Project: "web", Issue: "ENG-2", Status: "working", TmuxName: "t-b", Source: "native"},
	}}
	m.Update(sessionsMsg{data: both})
	if m.sessions.selID != "" {
		t.Errorf("selID must clear when nothing is visible under the filter, got %q", m.sessions.selID)
	}
	if sel := m.sessions.selected(); sel != nil {
		t.Errorf("selected() must be nil when nothing is visible, got %v", sel)
	}
	if v := m.viewString(); !strings.Contains(v, "no sessions match the filter") {
		t.Errorf("filtered-empty list must say so and render no detail card:\n%s", v)
	}
}
