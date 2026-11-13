package sshelement

import (
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/sushidev-team/lola/internal/protocol"
)

var t0 = time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC)

func mustParse(t *testing.T, argv ...string) Post {
	t.Helper()
	p, err := Parse(argv)
	if err != nil {
		t.Fatalf("Parse(%q): %v", argv, err)
	}
	return p
}

func mustNew(t *testing.T, argv ...string) Element {
	t.Helper()
	e, err := mustParse(t, argv...).New("e1", t0)
	if err != nil {
		t.Fatalf("New(%q): %v", argv, err)
	}
	return e
}

func TestEveryKindParses(t *testing.T) {
	cases := map[Kind][]string{
		KindSummary:        {"summary", "disk", "is", "full"},
		KindNarration:      {"narration", "reading logs"},
		KindHypothesis:     {"hypothesis", "log rotation stopped"},
		KindFinding:        {"finding", "/var at 99%", "--severity", "high"},
		KindPlan:           {"plan", "--step", "check disk", "--step", "done: read logs"},
		KindMetric:         {"metric", "--kv", "load=1.2", "--kv", "disk=99 %"},
		KindEvidence:       {"evidence", "the disk is full", "--evidence", "b1"},
		KindRecommendation: {"recommendation", "rotate logs"},
		KindProposal:       {"proposal", "Rotate", "--command", "logrotate -f /etc/logrotate.conf"},
		KindQuestion:       {"question", "Restart nginx?", "--choice", "Yes", "--choice", "No"},
		KindWidget:         {"widget", "Disk", "--html", "<b>hi</b>"},
	}
	for _, k := range Kinds {
		argv, ok := cases[k]
		if !ok {
			t.Fatalf("no test case for kind %s", k)
		}
		if got := mustNew(t, argv...); got.Kind != k || got.ID != "e1" || got.Revision != 1 {
			t.Errorf("%s: %+v", k, got)
		}
	}
}

func TestRequiredFieldsAndFlags(t *testing.T) {
	bad := [][]string{
		{},
		{"banner", "x"},
		{"summary"},
		{"summary", "x", "--severity", "high"}, // flag of another kind
		{"summary", "x", "--nope", "y"},
		{"summary", "x", "--body"},
		{"summary", "x", "--body", "a", "--body", "b"},
		{"finding", "x"},
		{"finding", "x", "--severity", "apocalyptic"},
		{"hypothesis", "x", "--state", "maybe"},
		{"plan"},
		{"plan", "--step", "done:"},
		{"metric"},
		{"metric", "--kv", "noequals"},
		{"evidence", "x"},
		{"evidence", "x", "--evidence", "not-a-block"},
		{"proposal", "x"},
		{"proposal", "x", "--command", "   "},
		{"question"},
		{"summary", "x", "--ref", "bad ref"},
		{"summary", "x", "--host", "-oProxyCommand=evil"},
	}
	for _, argv := range bad {
		if p, err := Parse(argv); err == nil {
			if _, err := p.New("e1", t0); err == nil {
				t.Errorf("Parse(%q) accepted", argv)
			}
		}
	}
}

func TestPlanStepsAndMetricUnits(t *testing.T) {
	e := mustNew(t, "plan", "Triage", "--step", "done: read logs", "--step", "active: check disk",
		"--step", "note: not a state", "--step", "plain")
	want := []Step{{"read logs", StepDone}, {"check disk", StepActive}, {"note: not a state", StepPending}, {"plain", StepPending}}
	if !reflect.DeepEqual(e.Steps, want) {
		t.Fatalf("steps = %+v", e.Steps)
	}
	m := mustNew(t, "metric", "--kv", "disk=99 %", "--kv", "state=three word value")
	if m.Metrics[0] != (Metric{"disk", "99", "%"}) || m.Metrics[1].Value != "three word value" || m.Metrics[1].Unit != "" {
		t.Fatalf("metrics = %+v", m.Metrics)
	}
}

func TestCapsAndSanitizing(t *testing.T) {
	e := mustNew(t, "summary", "\x1b[31mred\x1b[0m \x1b]8;;http://evil\x07link\x1b]8;;\x07 \u202egnp.exe", "--body", "a\r\n\n\n\nb\x07")
	if e.Title != "red link gnp.exe" {
		t.Fatalf("title = %q", e.Title)
	}
	if e.Body != "a\n\nb" {
		t.Fatalf("body = %q", e.Body)
	}
	long := mustNew(t, "hypothesis", strings.Repeat("x", MaxTitleRunes*2))
	if n := len([]rune(long.Title)); n != MaxTitleRunes || !strings.HasSuffix(long.Title, "…") {
		t.Fatalf("clip: %d %q", n, long.Title)
	}
	// Prose is the agent's answer: its lines and its length survive up to the body cap.
	for _, kind := range []string{"summary", "narration", "recommendation"} {
		answer := "3 containers:\n- registry\n- db " + strings.Repeat("x", MaxTitleRunes)
		if got := mustNew(t, kind, answer).Title; got != answer {
			t.Fatalf("%s lost its text: %q", kind, got)
		}
		over := mustNew(t, kind, "head\n"+strings.Repeat("x", MaxBodyRunes*2)).Title
		if n := len([]rune(over)); n != MaxBodyRunes {
			t.Fatalf("%s cap: %d", kind, n)
		}
	}
	var steps []string
	for range MaxSteps + 1 {
		steps = append(steps, "--step", "x")
	}
	if _, err := Parse(append([]string{"plan"}, steps...)); err == nil {
		t.Fatal("too many steps accepted")
	}
	if _, err := Parse(make([]string, maxArgvEntries+1)); err == nil {
		t.Fatal("too many argv entries accepted")
	}
}

func TestUpdateByRefTouchesOnlyWhatItNames(t *testing.T) {
	first := mustParse(t, "hypothesis", "logs fill the disk", "--ref", "h1", "--body", "first look")
	e, err := first.New("e7", t0)
	if err != nil {
		t.Fatal(err)
	}
	up := mustParse(t, "hypothesis", "--ref", "h1", "--state", "confirmed", "--evidence", "b3")
	n, err := up.Merge(e, t0.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if n.State != HypothesisConfirmed || n.Title != "logs fill the disk" || n.Body != "first look" || n.ID != "e7" ||
		n.Revision != 2 || !n.CreatedAt.Equal(t0) || !n.UpdatedAt.Equal(t0.Add(time.Minute)) || len(n.Evidence) != 1 {
		t.Fatalf("merged = %+v", n)
	}
	if e.State != HypothesisOpen || e.Revision != 1 || len(e.Evidence) != 0 {
		t.Fatalf("Merge mutated its input: %+v", e)
	}
	if _, err := mustParse(t, "finding", "x", "--severity", "low", "--ref", "h1").Merge(e, t0); err == nil {
		t.Fatal("a ref must not change kind")
	}
	// A ref with nothing behind it is a creation, and creation rules apply.
	if _, err := mustParse(t, "finding", "--ref", "f1").New("e8", t0); err == nil {
		t.Fatal("creation through a ref skipped the required fields")
	}
}

func TestAgentCannotForgeFactsOrOutcomes(t *testing.T) {
	for _, argv := range [][]string{
		{"summary", "x", "--verified", "true"},
		{"summary", "x", "--outcome", "yes"},
		{"proposal", "x", "--command", "ls", "--state", "accepted"},
		{"question", "x", "--state", "answered"},
	} {
		if _, err := Parse(argv); err == nil {
			t.Errorf("Parse(%q) accepted a daemon-owned field", argv)
		}
	}
	e := mustNew(t, "evidence", "claim", "--evidence", "b1")
	if e.Evidence[0].Verified {
		t.Fatal("evidence is verified before the daemon resolved it")
	}
}

func TestResolveChecksEveryLink(t *testing.T) {
	code := 1
	known := map[string]BlockFact{"b1": {Status: "failed", ExitCode: &code}}
	lookup := func(id string) (BlockFact, bool) { f, ok := known[id]; return f, ok }

	e := mustNew(t, "evidence", "claim", "--evidence", "b1,b1")
	if err := Resolve(&e, lookup); err != nil {
		t.Fatal(err)
	}
	if len(e.Evidence) != 1 || !e.Evidence[0].Verified || e.Evidence[0].Status != "failed" || *e.Evidence[0].ExitCode != 1 {
		t.Fatalf("evidence = %+v", e.Evidence)
	}
	bad := mustNew(t, "evidence", "claim", "--evidence", "b1,b99")
	if err := Resolve(&bad, lookup); err == nil || !strings.Contains(err.Error(), "b99") {
		t.Fatalf("unknown block not rejected: %v", err)
	}
}

func TestHumanOutcomesCloseTheElement(t *testing.T) {
	q := mustNew(t, "question", "Restart?", "--choice", "Yes", "--ref", "q1")
	if !q.AwaitsHuman() {
		t.Fatal("open question should await a human")
	}
	if _, err := Answer(q, "  \x1b[2J ", t0); err == nil {
		t.Fatal("empty answer accepted")
	}
	a, err := Answer(q, "Yes", t0)
	if err != nil || a.State != QuestionAnswered || a.Outcome.Text != "Yes" || a.AwaitsHuman() {
		t.Fatalf("Answer: %+v %v", a, err)
	}
	if q.Outcome != nil {
		t.Fatal("Answer mutated its input")
	}
	if _, err := Answer(a, "No", t0); !errors.Is(err, ErrClosed) {
		t.Fatalf("second answer: %v", err)
	}
	if _, err := mustParse(t, "question", "--ref", "q1", "reworded").Merge(a, t0); !errors.Is(err, ErrClosed) {
		t.Fatalf("agent rewrote an answered question: %v", err)
	}

	p := mustNew(t, "proposal", "Rotate", "--command", "logrotate")
	d, err := Decide(p, true, "b9", "", t0)
	if err != nil || d.State != ProposalAccepted || d.Outcome.Block != "b9" {
		t.Fatalf("Decide: %+v %v", d, err)
	}
	if _, err := Decide(d, false, "", "no", t0); !errors.Is(err, ErrClosed) {
		t.Fatalf("second decision: %v", err)
	}
	if _, err := Answer(p, "x", t0); err == nil {
		t.Fatal("answered a proposal")
	}
	if _, err := Decide(q, true, "", "", t0); err == nil {
		t.Fatal("decided a question")
	}
}

func TestValidateCommandRefusesRatherThanCleans(t *testing.T) {
	good := []string{"uptime", "df -h\ndu -sh /var/*", "echo 'héllo'\tworld", "grep '日本語' f"}
	for _, c := range good {
		if err := ValidateCommand(c); err != nil {
			t.Errorf("%q: %v", c, err)
		}
	}
	bad := []string{"", "   ", "rm\x1b[2K -rf /", "ls\rrm -rf /", "ls\x00", "echo \u202e evil", "a\u2028b", "\xff\xfe", strings.Repeat("x", MaxCommandBytes+1)}
	for _, c := range bad {
		if err := ValidateCommand(c); err == nil {
			t.Errorf("%q accepted", c)
		}
	}
	if err := ValidateCwd("/var/log"); err != nil {
		t.Error(err)
	}
	if err := ValidateCwd(""); err != nil {
		t.Error(err)
	}
	if err := ValidateCwd("/a\nb"); err == nil {
		t.Error("newline in cwd accepted")
	}
	// A proposal's command goes through the same gate, unmodified.
	if _, err := Parse([]string{"proposal", "x", "--command", "ls\x1b[0m"}); !errors.Is(err, ErrUndisplayable) && err == nil {
		t.Fatal("undisplayable proposal accepted")
	}
	if got := mustNew(t, "proposal", "x", "--command", "df -h\n  uptime"); got.Command != "df -h\n  uptime" {
		t.Fatalf("command was rewritten: %q", got.Command)
	}
}

func TestCleaner(t *testing.T) {
	cases := []struct{ name, in, want string }{
		{"plain", "hello\tworld\n", "hello\tworld\n"},
		{"csi", "\x1b[1;31mred\x1b[0m", "red"},
		{"osc bel", "\x1b]0;title\x07x", "x"},
		{"osc st", "\x1b]8;;http://evil\x1b\\link", "link"},
		{"dcs", "\x1bPqpayload\x1b\\ok", "ok"},
		{"charset", "\x1b(Bok", "ok"},
		{"crlf", "a\r\nb", "a\nb"},
		{"lone cr", "10%\r20%", "10%\n20%"},
		{"double cr", "a\r\r\nb", "a\nb"},
		{"c0", "a\x07b\x08c\x7f", "abc"},
		{"c1 rune", "a\u009b31mb", "a31mb"},
		{"bidi", "a\u202eb", "ab"},
		{"utf8", "日本語\n", "日本語\n"},
		{"invalid", "a\xffb", "a\uFFFDb"},
		{"trailing cr", "a\r", "a\n"},
		{"partial at end", "a\xe6\x97", "a\uFFFD"},
	}
	for _, c := range cases {
		var cl Cleaner
		got := string(cl.Write([]byte(c.in))) + string(cl.Flush())
		if got != c.want {
			t.Errorf("%s: whole: %q, want %q", c.name, got, c.want)
		}
		// One byte at a time must not change a thing: chunk boundaries are
		// arbitrary on a pipe.
		var ch Cleaner
		var b strings.Builder
		for i := 0; i < len(c.in); i++ {
			b.Write(ch.Write([]byte{c.in[i]}))
		}
		b.Write(ch.Flush())
		if b.String() != c.want {
			t.Errorf("%s: bytewise: %q, want %q", c.name, b.String(), c.want)
		}
	}
}

func TestCleanerBinaryAndRunawaySequences(t *testing.T) {
	var c Cleaner
	if got := string(c.Write([]byte("a\x00b"))); got != "ab" || !c.Binary {
		t.Fatalf("nul: %q binary=%v", got, c.Binary)
	}
	// An unterminated OSC must not swallow the rest of the stream forever.
	var r Cleaner
	in := "\x1b]" + strings.Repeat("x", maxSequenceBytes+1) + "visible"
	if got := string(r.Write([]byte(in))); !strings.HasSuffix(got, "visible") {
		t.Fatalf("runaway OSC swallowed output: %q", got)
	}
}

// jsonFields lists a struct's JSON keys with the kind of each field.
func jsonFields(t reflect.Type) map[string]reflect.Kind {
	out := map[string]reflect.Kind{}
	for i := range t.NumField() {
		f := t.Field(i)
		name, _, _ := strings.Cut(f.Tag.Get("json"), ",")
		if name == "" || name == "-" {
			continue
		}
		k := f.Type.Kind()
		// Named strings (Kind) and plain strings are the same on the wire.
		out[name] = k
	}
	return out
}

// protocol is dependency-free, so it mirrors these shapes rather than importing
// them. This pins the mirror, so the daemon's copy between them cannot silently
// drop a field.
func TestProtocolMirrorsTheElementShape(t *testing.T) {
	pairs := []struct{ leaf, wire reflect.Type }{
		{reflect.TypeOf(Element{}), reflect.TypeOf(protocol.SSHElement{})},
		{reflect.TypeOf(Step{}), reflect.TypeOf(protocol.SSHElementStep{})},
		{reflect.TypeOf(Metric{}), reflect.TypeOf(protocol.SSHElementMetric{})},
		{reflect.TypeOf(Evidence{}), reflect.TypeOf(protocol.SSHElementEvidence{})},
		{reflect.TypeOf(Outcome{}), reflect.TypeOf(protocol.SSHElementOutcome{})},
	}
	for _, p := range pairs {
		if l, w := jsonFields(p.leaf), jsonFields(p.wire); !reflect.DeepEqual(l, w) {
			t.Errorf("%s vs %s:\n leaf %v\n wire %v", p.leaf, p.wire, l, w)
		}
	}
	// The closed vocabularies UIs draw from must match too.
	var kinds []string
	for _, k := range Kinds {
		kinds = append(kinds, string(k))
	}
	if !reflect.DeepEqual(kinds, protocol.SSHElementKinds) {
		t.Errorf("kinds: %v vs %v", kinds, protocol.SSHElementKinds)
	}
	if !reflect.DeepEqual(Severities, protocol.SSHSeverities) {
		t.Errorf("severities: %v vs %v", Severities, protocol.SSHSeverities)
	}
	if !reflect.DeepEqual(StepStates, protocol.SSHStepStates) {
		t.Errorf("step states: %v vs %v", StepStates, protocol.SSHStepStates)
	}
}

func TestWidgetPost(t *testing.T) {
	p, err := Parse([]string{"widget", "Disk", "--html", "<b>hi</b>", "--data", `{"a":1}`, "--height", "9999", "--ref", "d"})
	if err != nil {
		t.Fatal(err)
	}
	if p.Element.HTML != "<b>hi</b>" || p.Element.Data != `{"a":1}` || p.Element.Height != MaxWidgetHeight {
		t.Fatalf("%+v", p.Element)
	}
	for name, argv := range map[string][]string{
		"no html":   {"widget", "Disk"},
		"bad json":  {"widget", "Disk", "--html", "x", "--data", "{"},
		"big html":  {"widget", "Disk", "--html", strings.Repeat("x", MaxWidgetHTML+1)},
		"big data":  {"widget", "Disk", "--html", "x", "--data", `"` + strings.Repeat("x", MaxWidgetData) + `"`},
		"height":    {"widget", "Disk", "--html", "x", "--height", "tall"},
		"on a note": {"narration", "x", "--html", "<b>"},
	} {
		if _, err := Parse(argv); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	// An update of the data alone keeps the page.
	first, _ := p.New("e1", time.Now())
	upd, err := Parse([]string{"widget", "--ref", "d", "--data", `{"a":2}`})
	if err != nil {
		t.Fatal(err)
	}
	got, err := upd.Merge(first, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if got.HTML != "<b>hi</b>" || got.Data != `{"a":2}` || got.Revision != 2 {
		t.Fatalf("%+v", got)
	}
}

func TestGuidesAreEmbedded(t *testing.T) {
	if !strings.Contains(GuideAnswers, "[!OK]") || !strings.Contains(GuideWidgets, "lola.onData") {
		t.Fatal("a guide is missing")
	}
}

func TestABlobIsRefusedWithTheFormat(t *testing.T) {
	blob := strings.Repeat("All sixteen services run and nothing is wrong. ", 8)
	for _, kind := range []string{"narration", "summary"} {
		_, err := Parse([]string{kind, blob})
		if err == nil || !strings.Contains(err.Error(), "[!NOTE]") {
			t.Errorf("%s: %v", kind, err)
		}
		if _, err := Parse([]string{kind, "Healthy\n" + blob}); err != nil {
			t.Errorf("%s with a line break: %v", kind, err)
		}
		if _, err := Parse([]string{kind, "Short and fine."}); err != nil {
			t.Errorf("%s short: %v", kind, err)
		}
	}
	if _, err := Parse([]string{"recommendation", blob}); err != nil {
		t.Errorf("recommendation: %v", err)
	}
}
