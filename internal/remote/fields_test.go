package remote

import (
	"encoding/json"
	"reflect"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/sushidev-team/lola/internal/protocol"
)

func hasTiers(tiers ...string) func(string) bool {
	return func(t string) bool { return slices.Contains(tiers, t) }
}

func rawArgs(t *testing.T, v any) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// argsFieldNames lists the JSON names of every leaf of an Args type, nested
// structs flattened with ".", sorted.
func argsFieldNames(typ reflect.Type) []string {
	var out []string
	var walk func(t reflect.Type, prefix string, depth int)
	walk = func(t reflect.Type, prefix string, depth int) {
		for t.Kind() == reflect.Pointer || t.Kind() == reflect.Slice || t.Kind() == reflect.Array || t.Kind() == reflect.Map {
			t = t.Elem()
		}
		if t.Kind() != reflect.Struct || t.PkgPath() == "time" || depth > 6 {
			out = append(out, strings.TrimSuffix(prefix, "."))
			return
		}
		for i := 0; i < t.NumField(); i++ {
			f := t.Field(i)
			if !f.IsExported() {
				continue
			}
			name, _, _ := strings.Cut(f.Tag.Get("json"), ",")
			if name == "-" {
				continue
			}
			if name == "" {
				name = f.Name
			}
			walk(f.Type, prefix+name+".", depth+1)
		}
	}
	walk(typ, "", 0)
	sort.Strings(out)
	return out
}

func TestEveryTieredCommandHasAFieldSpec(t *testing.T) {
	for cmd, tier := range CommandTiers() {
		_, has := requestFields[cmd]
		switch {
		case tier == protocol.TierNever && has:
			t.Errorf("%s is never but has a field spec", cmd)
		case tier != protocol.TierNever && !has:
			t.Errorf("%s is %s but has no field spec", cmd, tier)
		}
	}
	for cmd := range requestFields {
		if _, ok := CommandTiers()[cmd]; !ok {
			t.Errorf("field spec for %s, which is not in the tier table", cmd)
		}
	}
	for _, cmd := range TransportCommands {
		if _, ok := requestFields[cmd]; !ok {
			t.Errorf("transport command %s has no field spec", cmd)
		}
	}
}

func TestFieldSpecsAreConsistent(t *testing.T) {
	known := map[string]bool{}
	for _, n := range flatNames {
		known[n] = true
	}
	for cmd, spec := range requestFields {
		if (spec.fields&fArgs != 0) != (spec.args != nil) {
			t.Errorf("%s: fArgs and the Args type disagree", cmd)
		}
		var argNames []string
		if spec.args != nil {
			argNames = argsFieldNames(spec.args)
		}
		for _, a := range spec.audit {
			if !known[a] && !slices.Contains(argNames, a) {
				t.Errorf("%s: audit field %q is neither a flat field nor an Args field", cmd, a)
			}
		}
	}
}

// reviewedArgsFields is the golden list of JSON fields each remotely reachable
// Args type carries. A field added to one of those types fails
// TestRemoteArgsFieldsAreReviewed until a human reads what the handler does with it
// and adds the name here.
var reviewedArgsFields = map[string][]string{
	"addressPRFeedback": {"confirmUnready", "head", "method", "number", "repo"},
	"agentChoose":       {"hash", "key", "session"},
	"agentFeed":         {"after", "before", "epoch", "limit", "parent", "session"},
	"agentFeedOutput":   {"item", "session"},
	"agentQueueRelease": {"session"},
	"agentSay":          {"conn", "session", "stopFirst", "text"},
	"agentSetting":      {"agent", "session", "setting", "value"},
	"agentStop":         {"session"},
	"attachImage":       {"data", "last", "mime", "name", "part", "session"},
	"attachmentImage":   {"offset", "path", "session"},
	"bestOf":            {"agents", "issue", "project"},
	"bestOfCompare":     {"group", "issue", "session"},
	"bestOfPick":        {"session"},
	"broadcast":         {"message", "project"},
	"checkpointDiff":    {"agent", "seq", "session"},
	"dev":               {"on", "session"},
	"devFreePort":       {"pid", "port", "session"},
	"feedback":          {"comments.body", "comments.endLine", "comments.line", "comments.path", "comments.quote", "comments.side", "note", "session"},
	"forkCheckpoint":    {"agent", "seq", "session"},
	"mergePR":           {"confirmUnready", "head", "method", "number", "repo"},
	"openExisting":      {"agent", "agentKind", "aiAction", "base", "branch", "existing", "location", "name", "path", "project", "prompt"},
	"openManual":        {"agent", "agentKind", "aiAction", "base", "branch", "existing", "location", "name", "path", "project", "prompt"},
	"openPr":            {"branch", "isFork", "number", "project"},
	"openTicket":        {"agentKind", "branch", "identifier", "project", "title", "uuid"},
	"paneClose":         {"pane", "session"},
	"paneResize":        {"cols", "pane", "rows", "session"},
	"prReadiness":       {"confirmUnready", "head", "method", "number", "repo"},

	"prs":                   {"project", "refresh"},
	"projectGit":            {"area", "checkout", "from", "head", "limit", "message", "name", "op", "path", "paths", "pop", "project", "ref", "sha", "to", "token", "worktree"},
	"projectGitAct":         {"area", "checkout", "from", "head", "limit", "message", "name", "op", "path", "paths", "pop", "project", "ref", "sha", "to", "token", "worktree"},
	"projectGitDiscard":     {"area", "checkout", "from", "head", "limit", "message", "name", "op", "path", "paths", "pop", "project", "ref", "sha", "to", "token", "worktree"},
	"relayCred":             {"holds"},
	"restoreCheckpoint":     {"agent", "seq", "session"},
	"reviewPackageCapture":  {"channel", "height", "package", "screen", "selector", "title", "url", "width"},
	"reviewPackageChecks":   {"id", "package", "spec.artifacts.id", "spec.artifacts.kind", "spec.artifacts.path", "spec.command", "spec.id", "spec.image", "spec.member", "spec.memory_mib", "spec.timeout_seconds"},
	"reviewPackageDiff":     {"from", "to"},
	"reviewPackageFeedback": {"note", "package", "section"},
	"reviewPackageImage":    {"artifact", "chunked", "offset", "package"},
	"reviewPackageMark":     {"package", "previous"},
	"reviewPackageRecover":  {"id", "package", "spec.artifacts.id", "spec.artifacts.kind", "spec.artifacts.path", "spec.command", "spec.id", "spec.image", "spec.member", "spec.memory_mib", "spec.timeout_seconds"},
	"reviewPackageVerify":   {"id", "package", "spec.artifacts.id", "spec.artifacts.kind", "spec.artifacts.path", "spec.command", "spec.id", "spec.image", "spec.member", "spec.memory_mib", "spec.timeout_seconds"},
	"sessionTask":           {"argv"},
	"sessionTimeline":       {"after", "conn", "limit", "session"},
	"sshAnswer":             {"element", "revision", "session", "text"},
	"sshRecall":             {"item", "session"},
	"switchAgent":           {"agent", "session"},
	"talkState":             {"session"},
	"tickets":               {"project", "scope"},
	"unwatch":               {"all", "topics"},
	"watch":                 {"topics"},
}

func TestRemoteArgsFieldsAreReviewed(t *testing.T) {
	seen := map[string]bool{}
	for cmd, spec := range requestFields {
		if spec.args == nil {
			continue
		}
		seen[cmd] = true
		want, ok := reviewedArgsFields[cmd]
		if !ok {
			t.Errorf("%s: Args type %s has no reviewed field list", cmd, spec.args)
			continue
		}
		if got := argsFieldNames(spec.args); !slices.Equal(got, want) {
			t.Errorf("%s: %s fields are %v, reviewed list is %v: read the handler, then update the list", cmd, spec.args.Name(), got, want)
		}
	}
	for cmd := range reviewedArgsFields {
		if !seen[cmd] {
			t.Errorf("reviewed list names %s, which has no Args type", cmd)
		}
	}
}

func TestRebuildNeverCarriesForceOrHookFields(t *testing.T) {
	in := protocol.Request{
		Cmd: "kill", Session: "s", KeepWorktree: true, Force: true, Event: "stop", Detail: "d",
		Hook: &protocol.HookPayload{Message: "m"}, HookToken: "tok",
	}
	out, _, err := rebuild("kill", in, hasTiers())
	if err != nil {
		t.Fatal(err)
	}
	if out.Force || out.Event != "" || out.Detail != "" || out.Hook != nil || out.HookToken != "" {
		t.Fatalf("rebuilt request carries a never-field: %+v", out)
	}
	if out.Session != "s" || !out.KeepWorktree {
		t.Fatalf("kill lost its own fields: %+v", out)
	}
	// And for every spec, whatever its mask.
	for cmd := range requestFields {
		o, _, _ := rebuild(cmd, protocol.Request{Force: true, Event: "e", Detail: "d", Hook: &protocol.HookPayload{}, HookToken: "t"}, hasTiers(protocol.TierShell))
		if o.Force || o.Event != "" || o.Detail != "" || o.Hook != nil || o.HookToken != "" {
			t.Errorf("%s: rebuild carried a never-field", cmd)
		}
	}
}

func TestRebuildDropsUnlistedFlatFields(t *testing.T) {
	in := protocol.Request{
		Session: "s", Text: "t", Project: "p", Ref: "r", Poll: "po", DryRun: true, Provider: "pr",
		KeepWorktree: true, Cols: 1, Rows: 2, Lines: 3, Args: json.RawMessage(`{"x":1}`),
	}
	out, _, err := rebuild("status", in, hasTiers())
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(out, protocol.Request{Cmd: "status"}) {
		t.Fatalf("status kept %+v", out)
	}
	out, _, _ = rebuild("pane", in, hasTiers())
	want := protocol.Request{Cmd: "pane", Session: "s", Lines: 3}
	if !reflect.DeepEqual(out, want) {
		t.Fatalf("pane = %+v, want %+v", out, want)
	}
	out, _, _ = rebuild("pollOnce", in, hasTiers())
	if want := (protocol.Request{Cmd: "pollOnce", Poll: "po", DryRun: true}); !reflect.DeepEqual(out, want) {
		t.Fatalf("pollOnce = %+v", out)
	}
}

func TestRebuildCopiesArgsWithoutAliasing(t *testing.T) {
	args := json.RawMessage(`{"project":"p"}`)
	out, _, err := rebuild("tickets", protocol.Request{Args: args}, hasTiers())
	if err != nil {
		t.Fatal(err)
	}
	args[2] = 'X'
	if string(out.Args) != `{"project":"p"}` {
		t.Fatalf("rebuilt Args alias the input: %s", out.Args)
	}
}

func TestRebuildUnknownCommandFails(t *testing.T) {
	for _, cmd := range []string{"stop", "", "remote.pair", "nonsense"} {
		if _, _, err := rebuild(cmd, protocol.Request{}, hasTiers()); err == nil {
			t.Errorf("rebuild(%q) succeeded", cmd)
		}
	}
}

func TestEnvelopeCmdWins(t *testing.T) {
	out, _, err := rebuild("status", protocol.Request{Cmd: "stop"}, hasTiers())
	if err != nil {
		t.Fatal(err)
	}
	if out.Cmd != "status" {
		t.Fatalf("Cmd = %q; the envelope's command must win", out.Cmd)
	}
}

func TestOpenManualLocationRefused(t *testing.T) {
	for _, cmd := range []string{"openManual", "openExisting"} {
		for _, a := range []protocol.OpenManualArgs{
			{Project: "p", Agent: true, Location: "main"},
			{Project: "p", Agent: true, Location: "temporary"},
			{Project: "p", Agent: true, Location: "checkout"},
			{Project: "p", Agent: true, Path: "/tmp"},
		} {
			_, _, err := rebuild(cmd, protocol.Request{Args: rawArgs(t, a)}, hasTiers(protocol.TierShell))
			if err == nil || err.Error() != "only a new worktree can be opened from a phone" {
				t.Errorf("%s %+v: err = %v", cmd, a, err)
			}
		}
		for _, loc := range []string{"", "worktree"} {
			a := protocol.OpenManualArgs{Project: "p", Agent: true, Location: loc}
			if _, _, err := rebuild(cmd, protocol.Request{Args: rawArgs(t, a)}, hasTiers()); err != nil {
				t.Errorf("%s location %q refused: %v", cmd, loc, err)
			}
		}
	}
}

func TestOpenManualShellNeedsShellTier(t *testing.T) {
	a := rawArgs(t, protocol.OpenManualArgs{Project: "p", Branch: "b"})
	_, _, err := rebuild("openManual", protocol.Request{Args: a}, hasTiers(protocol.TierControl))
	if err == nil || err.Error() != "opening a shell needs the shell permission" {
		t.Fatalf("err = %v", err)
	}
	if _, _, err := rebuild("openManual", protocol.Request{Args: a}, hasTiers(protocol.TierControl, protocol.TierShell)); err != nil {
		t.Fatalf("with shell: %v", err)
	}
	ag := rawArgs(t, protocol.OpenManualArgs{Project: "p", Branch: "b", Agent: true})
	if _, _, err := rebuild("openManual", protocol.Request{Args: ag}, hasTiers(protocol.TierControl)); err != nil {
		t.Fatalf("agent session without shell: %v", err)
	}
}

func TestOpenManualBadArgsFixedSentence(t *testing.T) {
	_, _, err := rebuild("openManual", protocol.Request{Args: json.RawMessage(`{"project":"secret-name"`)}, hasTiers(protocol.TierShell))
	if err == nil || strings.Contains(err.Error(), "secret-name") {
		t.Fatalf("err = %v", err)
	}
}

func TestAgentSayConnDropped(t *testing.T) {
	in := protocol.Request{Args: rawArgs(t, protocol.AgentSayArgs{Session: "s", Text: "hi", Conn: "host-1", StopFirst: true})}
	out, res, err := rebuild(protocol.CmdAgentSay, in, hasTiers())
	if err != nil {
		t.Fatal(err)
	}
	var a protocol.AgentSayArgs
	if err := json.Unmarshal(out.Args, &a); err != nil {
		t.Fatal(err)
	}
	if a.Conn != "" || a.Text != "hi" || a.Session != "s" || !a.StopFirst {
		t.Fatalf("args = %+v", a)
	}
	if !slices.Equal(res.dropped, []string{"conn"}) {
		t.Fatalf("dropped = %v", res.dropped)
	}
	// Nothing to drop: nothing reported.
	in = protocol.Request{Args: rawArgs(t, protocol.AgentSayArgs{Session: "s", Text: "hi"})}
	if _, res, _ = rebuild(protocol.CmdAgentSay, in, hasTiers()); len(res.dropped) != 0 {
		t.Fatalf("dropped = %v", res.dropped)
	}
}

func TestPrsRefreshDropped(t *testing.T) {
	in := protocol.Request{Args: rawArgs(t, protocol.PrsArgs{Project: "p", Refresh: true})}
	out, res, err := rebuild("prs", in, hasTiers())
	if err != nil {
		t.Fatal(err)
	}
	var a protocol.PrsArgs
	if err := json.Unmarshal(out.Args, &a); err != nil {
		t.Fatal(err)
	}
	if a.Refresh || a.Project != "p" {
		t.Fatalf("args = %+v", a)
	}
	if !slices.Equal(res.dropped, []string{"refresh"}) {
		t.Fatalf("dropped = %v", res.dropped)
	}
}

func TestAttachImageCheckReportsBytesAndLast(t *testing.T) {
	part := func(n int, last bool) protocol.Request {
		return protocol.Request{Args: rawArgs(t, protocol.AttachImageArgs{
			Session: "s", Name: "abcdefgh", Mime: "image/png", Data: make([]byte, n), Last: last,
		})}
	}
	_, res, err := rebuild(protocol.CmdAttachImage, part(1000, false), hasTiers())
	if err != nil || res.attachBytes != 1000 || res.attachLast {
		t.Fatalf("non-last: %+v %v", res, err)
	}
	_, res, err = rebuild(protocol.CmdAttachImage, part(7, true), hasTiers())
	if err != nil || res.attachBytes != 7 || !res.attachLast {
		t.Fatalf("last: %+v %v", res, err)
	}
	if _, _, err := rebuild(protocol.CmdAttachImage, protocol.Request{Args: json.RawMessage(`nope`)}, hasTiers()); err == nil {
		t.Fatal("a malformed part was accepted")
	}
}

func TestRelayCredIsReadWithFieldSpec(t *testing.T) {
	if TierFor(protocol.CmdRelayCred) != protocol.TierRead {
		t.Fatalf("tier = %s", TierFor(protocol.CmdRelayCred))
	}
	spec, ok := requestFields[protocol.CmdRelayCred]
	if !ok || spec.fields != fArgs || spec.args != reflect.TypeOf(protocol.RelayCredArgs{}) || spec.check != nil {
		t.Fatalf("spec = %+v", spec)
	}
	in := protocol.Request{Session: "other", Args: rawArgs(t, protocol.RelayCredArgs{Holds: "ab"})}
	out, _, err := rebuild(protocol.CmdRelayCred, in, hasTiers())
	if err != nil {
		t.Fatal(err)
	}
	if out.Session != "" || string(out.Args) != `{"holds":"ab"}` {
		t.Fatalf("out = %+v", out)
	}
}

func TestKillCarriesKeepWorktreeNeverForce(t *testing.T) {
	out, _, _ := rebuild("kill", protocol.Request{Session: "s", Force: true, KeepWorktree: true}, hasTiers())
	if out.Force || !out.KeepWorktree {
		t.Fatalf("%+v", out)
	}
}
