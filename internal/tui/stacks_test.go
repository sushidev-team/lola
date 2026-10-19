package tui

import (
	"slices"
	"strings"
	"testing"

	"github.com/sushidev-team/lola/internal/config"
)

// stackProjects is a stack "nori" with two members, one of them listed BEFORE
// the stack in the file, plus a plain project between them.
func stackProjects() []config.Project {
	return []config.Project{
		{Name: "api", Stack: "nori", Path: "/tmp/api", Labels: []string{"apps/api"},
			// Stack-owned keys a member keeps (ignored) for leaving the stack.
			TeamID: "team-1", Agent: "codex", BranchPrefix: "x/", OnPRStateID: "st-pr", CommentOnSpawn: true, PRRequiresChecks: true},
		{Name: "solo", Path: "/tmp/solo"},
		{Name: "nori", Label: "Nori", Kind: config.ProjectKindStack, TeamID: "team-1",
			CycleMode: "none", MatchMode: "any", AssigneeMode: "anyone", DedupMode: "seen"},
		{Name: "web", Stack: "nori", Path: "/tmp/web"},
	}
}

func TestStackOrderPullsMembersUnderTheirStack(t *testing.T) {
	ps := stackProjects()
	var got []string
	for _, i := range stackOrder(ps) {
		got = append(got, ps[i].Name)
	}
	if want := []string{"solo", "nori", "api", "web"}; !slices.Equal(got, want) {
		t.Fatalf("order = %v, want %v", got, want)
	}
	// A member of a stack that does not exist keeps its place.
	orphan := []config.Project{{Name: "a", Stack: "gone"}, {Name: "b"}}
	if got := stackOrder(orphan); !slices.Equal(got, []int{0, 1}) {
		t.Fatalf("orphan order = %v", got)
	}
}

// A member's form shows only its repository: no Linear/agent/write-back field
// and no tab but Repo — and saving it keeps those hidden values verbatim.
func TestFormMemberHidesStackOwnedFieldsAndKeepsThem(t *testing.T) {
	f, path := newFormOn(t, stackProjects(), "api")
	if tabs := f.visibleTabs(); !slices.Equal(tabs, []formTab{tabRepo}) {
		t.Fatalf("member tabs = %v, want Repo only", tabs)
	}
	fs := f.fields()
	for _, hidden := range []fieldID{fAgent, fBranchPrefix, fEnabled, fTeam, fLabels, fOnPRState, fReview, fCap, fPRRequiresChecks} {
		if slices.Contains(fs, hidden) {
			t.Errorf("member form shows stack-owned field %d", hidden)
		}
	}
	for _, shown := range []fieldID{fStack, fAppLabels, fPath, fDevCommands} {
		if !slices.Contains(fs, shown) {
			t.Errorf("member form hides its own field %d", shown)
		}
	}
	f.switchTab(1)
	if f.tab != tabRepo {
		t.Fatalf("tab switch left the member's only tab: %v", f.tab)
	}
	if !strings.Contains(stripANSI(f.view(60)), "come from stack Nori") {
		t.Error("member form must say where its Linear/agent settings come from")
	}

	f.appLabels = []string{"apps/api", " api-v2 ", ""}
	if _, ev := f.save(); ev != formSaved {
		t.Fatalf("save failed: %v", f.errs)
	}
	got, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	p := got.ProjectByName("api")
	if !slices.Equal(p.Labels, []string{"apps/api", "api-v2"}) || p.Stack != "nori" {
		t.Errorf("labels/stack = %q/%q", p.Labels, p.Stack)
	}
	if p.TeamID != "team-1" || p.Agent != "codex" || p.BranchPrefix != "x/" || p.OnPRStateID != "st-pr" || !p.CommentOnSpawn || !p.PRRequiresChecks {
		t.Errorf("hidden stack-owned keys were not kept: %+v", p)
	}
}

// A stack has no checkout: its form hides every repository field, lists its
// members read-only, and saves without a path.
func TestFormStackHidesRepositoryFields(t *testing.T) {
	f, path := newFormOn(t, stackProjects(), "nori")
	fs := f.fields()
	for _, hidden := range []fieldID{fPath, fRepo, fDefaultBranch, fSymlinks, fPostCreate, fEnv, fDevCommands, fPortRange, fPortsPerSession, fPreRemove, fStack, fAppLabels} {
		if slices.Contains(fs, hidden) {
			t.Errorf("stack form shows repository field %d", hidden)
		}
	}
	if !slices.Contains(fs, fMembers) {
		t.Fatal("stack form must list its members")
	}
	if m := stripANSI(f.display(fMembers)); !strings.Contains(m, "api (apps/api)") || !strings.Contains(m, "web (every issue)") {
		t.Errorf("members = %q", m)
	}
	f.poll.OnPRStateID = "st"
	f.tab = tabWriteback
	if !slices.Contains(f.fields(), fPRRequiresChecks) {
		t.Error("pr_requires_checks gates the stack's write-back — its form must show it")
	}
	f.poll.Label = "Nori Stack"
	if _, ev := f.save(); ev != formSaved {
		t.Fatalf("save failed: %v", f.errs)
	}
	got, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if p := got.ProjectByName("nori"); !p.IsStack() || p.Label != "Nori Stack" || p.Path != "" {
		t.Errorf("saved stack = %+v", p)
	}
}

// Joining a stack from a plain project's form round-trips stack + labels, and
// leaving it drops the labels (they only mean anything on a member).
func TestFormJoinAndLeaveStack(t *testing.T) {
	f, path := newFormOn(t, stackProjects(), "solo")
	if !slices.Contains(f.fields(), fStack) {
		t.Fatal("a plain project must be offered the Stack field when a stack exists")
	}
	f.openPicker(fStack)
	if f.picker == nil || len(f.picker.opts) != 2 || f.picker.opts[1].id != "nori" {
		t.Fatalf("stack picker = %+v", f.picker)
	}
	f.picker.cursor = 1
	f.pickerKey(keyMsg("enter"))
	if f.poll.Stack != "nori" || !slices.Contains(f.fields(), fAppLabels) {
		t.Fatalf("after pick: stack=%q fields=%v", f.poll.Stack, f.fields())
	}
	f.appLabels = []string{"apps/solo"}
	if _, ev := f.save(); ev != formSaved {
		t.Fatalf("save failed: %v", f.errs)
	}
	got, _ := config.Load(path)
	if p := got.ProjectByName("solo"); p.Stack != "nori" || !slices.Equal(p.Labels, []string{"apps/solo"}) {
		t.Fatalf("joined = %+v", p)
	}

	f.poll.Stack = ""
	if _, ev := f.save(); ev != formSaved {
		t.Fatalf("leave failed: %v", f.errs)
	}
	got, _ = config.Load(path)
	if p := got.ProjectByName("solo"); p.Stack != "" || len(p.Labels) != 0 {
		t.Fatalf("left = %+v", p)
	}
}

// 't' on a project list opens a NEW stack form: no folder browser, kind set,
// and it saves with just a label.
func TestNewStackFromProjectList(t *testing.T) {
	f, path := newNativeTestForm(t, nil)
	m := &rootModel{cfg: f.cfg, focus: focusPolls, view: viewCockpit}
	m.updateList(keyMsg("t"))
	if m.form == nil {
		t.Fatal("t must open a form")
	}
	sf := m.form
	if sf.dirs != nil {
		t.Error("a stack has no checkout — the folder browser must not open")
	}
	if !sf.poll.IsStack() || !strings.Contains(stripANSI(sf.view(40)), "New stack") {
		t.Fatalf("form is not a new stack: kind=%q", sf.poll.Kind)
	}
	for _, r := range "Nori" {
		sf.key(keyMsg(string(r)))
	}
	if sf.poll.Name != "nori" {
		t.Fatalf("id = %q, want derived from the label", sf.poll.Name)
	}
	if _, ev := sf.save(); ev != formSaved {
		t.Fatalf("save failed: %v", sf.errs)
	}
	got, _ := config.Load(path)
	if p := got.ProjectByName("nori"); p == nil || !p.IsStack() {
		t.Fatalf("stack not created: %+v", p)
	}
}

// The rail draws a stack's members indented under it, and only the stack
// row carries a poll dot.
func TestRailIndentsStackMembers(t *testing.T) {
	m := &rootModel{cfg: &config.Config{Projects: stackProjects()}}
	rows := m.projectRailBody(40, 10)
	var plain []string
	for _, r := range rows {
		plain = append(plain, strings.TrimRight(stripANSI(r), " "))
	}
	if len(plain) != 4 || !strings.Contains(plain[1], "▤ Nori") ||
		!strings.Contains(plain[2], "  └ api") || !strings.Contains(plain[3], "  └ web") {
		t.Fatalf("rail = %q", plain)
	}
	if strings.Contains(plain[2], "●") || strings.Contains(plain[2], "○") {
		t.Errorf("member row shows a poll dot: %q", plain[2])
	}
}
