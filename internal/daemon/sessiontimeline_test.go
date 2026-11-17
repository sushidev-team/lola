package daemon

import (
	"context"
	"encoding/json"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/sushidev-team/lola/internal/protocol"
	"github.com/sushidev-team/lola/internal/sshstore"
)

// seedTimeline appends items to session s1's store with consecutive Seq/Order.
func seedTimeline(t *testing.T, r *sshRig, items ...protocol.SSHTimelineItem) {
	t.Helper()
	store, err := r.d.sshStore("s1", true)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Update(func(st *sshstore.State) error {
		for i := range items {
			it := items[i]
			st.Seq++
			st.Order++
			it.Seq, it.Order = st.Seq, st.Order
			st.Items = append(st.Items, &it)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestSessionTimelineStripsPrivateAndConnDetails(t *testing.T) {
	r := newSSHRig(t, hostDev)
	r.session("s1", "")
	conn := r.connected(t, "s1", "dev-box")
	exit := 3
	seedTimeline(t, r,
		protocol.SSHTimelineItem{ID: "b-private", Kind: protocol.SSHItemBlock, Block: &protocol.SSHBlock{ID: "b1", Conn: conn, Command: "cat ~/.secret", Private: true}},
		protocol.SSHTimelineItem{ID: "b-shared", Kind: protocol.SSHItemBlock, Block: &protocol.SSHBlock{ID: "b2", Conn: conn, Command: "ls", Host: "HOSTNAME", User: "USERNAME", Cwd: "/CWD", ExitCode: &exit}},
		protocol.SSHTimelineItem{ID: "w", Kind: protocol.SSHItemElement, Element: &protocol.SSHElement{ID: "e1", Kind: "widget", Title: "Disk", HTML: "<script>EVIL</script>", Data: "DATABLOB", Cwd: "/ECWD"}},
		protocol.SSHTimelineItem{ID: "h", Kind: protocol.SSHItemHuman, Human: &protocol.SSHHumanMessage{Text: "hi", Transcript: "TRANSCRIPT"}},
	)
	raw, err := r.d.handleSSHTimeline(protocol.SSHTimelineArgs{Session: "s1"})
	if err != nil {
		t.Fatal(err)
	}
	if len(raw.Items) < 4 || raw.Connections[0].Host == "" {
		t.Fatalf("fixture: the unfiltered timeline must carry everything: %+v", raw)
	}

	got, err := r.d.handleSessionTimeline(protocol.SessionTimelineArgs{Session: "s1"})
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(got)
	for _, leak := range []string{"cat ~/.secret", "HOSTNAME", "USERNAME", "/CWD", "EVIL", "DATABLOB", "/ECWD", "TRANSCRIPT", "10.1.2.3", "resolved-user", "s1-ssh-1"} {
		if strings.Contains(string(b), leak) {
			t.Errorf("%q leaked: %s", leak, b)
		}
	}
	var ids []string
	for _, it := range got.Items {
		if strings.HasPrefix(it.ID, "s") && it.Kind == protocol.SSHItemSystem {
			continue // the connect flow's own system rows
		}
		ids = append(ids, it.ID)
	}
	if want := []string{"b-shared", "w", "h"}; !slices.Equal(ids, want) {
		t.Fatalf("items = %v, want %v (private block dropped)", ids, want)
	}
	n := len(got.Items)
	got.Items = got.Items[n-3:]
	if got.LastSeq != raw.LastSeq {
		t.Errorf("LastSeq %d != %d: a dropped row must not move the cursor", got.LastSeq, raw.LastSeq)
	}
	if got.Items[0].Block.Command != "ls" || got.Items[0].Block.ExitCode == nil || *got.Items[0].Block.ExitCode != 3 {
		t.Errorf("the shared block's facts must survive: %+v", got.Items[0].Block)
	}
	if got.Items[1].Element.Title != "Disk" || got.Items[2].Human.Text != "hi" {
		t.Errorf("text fields must survive")
	}
	if c := got.Connections[0]; c.ID != conn || c.Alias == "" || c.State == "" {
		t.Errorf("connection identity must survive: %+v", c)
	}
}

func TestSessionTimelinePagingMatchesSSH(t *testing.T) {
	r := newSSHRig(t, hostDev)
	r.session("s1", "")
	var items []protocol.SSHTimelineItem
	for i := 0; i < 7; i++ {
		items = append(items, protocol.SSHTimelineItem{ID: "s" + string(rune('a'+i)), Kind: protocol.SSHItemSystem, System: &protocol.SSHSystemEvent{Kind: "note", Text: "t"}})
	}
	seedTimeline(t, r, items...)
	for _, a := range []protocol.SSHTimelineArgs{{Session: "s1"}, {Session: "s1", Limit: 3}, {Session: "s1", After: 3, Limit: 2}, {Session: "s1", After: 7}} {
		want, err := r.d.handleSSHTimeline(a)
		if err != nil {
			t.Fatal(err)
		}
		got, err := r.d.handleSessionTimeline(protocol.SessionTimelineArgs{Session: a.Session, After: a.After, Limit: a.Limit})
		if err != nil {
			t.Fatal(err)
		}
		if got.LastSeq != want.LastSeq || got.More != want.More || len(got.Items) != len(want.Items) {
			t.Errorf("%+v: got seq=%d more=%v n=%d, want seq=%d more=%v n=%d", a, got.LastSeq, got.More, len(got.Items), want.LastSeq, want.More, len(want.Items))
		}
		for i := range got.Items {
			if got.Items[i].ID != want.Items[i].ID || got.Items[i].Seq != want.Items[i].Seq {
				t.Errorf("%+v: item %d differs", a, i)
			}
		}
	}
	// An unknown session is the SSH answer, not a phone-specific one.
	if _, err := r.d.handleSessionTimeline(protocol.SessionTimelineArgs{Session: "bad/id"}); err == nil {
		t.Error("an invalid session id must error")
	}
	empty, err := r.d.handleSessionTimeline(protocol.SessionTimelineArgs{Session: "nope"})
	if err != nil || empty.Items == nil || empty.Connections == nil {
		t.Errorf("unknown session: want empty non-nil slices, got %+v %v", empty, err)
	}
}

// fillAll sets every field of the struct v points to a non-zero value.
func fillAll(v reflect.Value) {
	switch v.Kind() {
	case reflect.Ptr:
		v.Set(reflect.New(v.Type().Elem()))
		fillAll(v.Elem())
	case reflect.Struct:
		if v.Type() == reflect.TypeOf(time.Time{}) {
			v.Set(reflect.ValueOf(time.Unix(1700000000, 0)))
			return
		}
		for i := 0; i < v.NumField(); i++ {
			fillAll(v.Field(i))
		}
	case reflect.Slice:
		v.Set(reflect.MakeSlice(v.Type(), 1, 1))
		fillAll(v.Index(0))
	case reflect.String:
		v.SetString("x")
	case reflect.Bool:
		v.SetBool(true)
	case reflect.Int, reflect.Int64:
		v.SetInt(7)
	case reflect.Float64:
		v.SetFloat(7)
	}
}

func keptFields(v any) []string {
	rv := reflect.ValueOf(v)
	var out []string
	for i := 0; i < rv.NumField(); i++ {
		if !rv.Field(i).IsZero() {
			out = append(out, rv.Type().Field(i).Name)
		}
	}
	slices.Sort(out)
	return out
}

func sorted(s ...string) []string { slices.Sort(s); return s }

// allFieldsExcept lists a type's field names minus the removed ones. A field
// added to the protocol type shows up in the expectation but not in the
// allowlist copy, so the test fails until a human decides about it.
func allFieldsExcept(v any, removed ...string) []string {
	rt := reflect.TypeOf(v)
	var out []string
	for i := 0; i < rt.NumField(); i++ {
		if n := rt.Field(i).Name; !slices.Contains(removed, n) {
			out = append(out, n)
		}
	}
	slices.Sort(out)
	return out
}

func TestPublicTimelineIsAnAllowlist(t *testing.T) {
	var conn protocol.SSHConnInfo
	fillAll(reflect.ValueOf(&conn).Elem())
	var blk protocol.SSHBlock
	fillAll(reflect.ValueOf(&blk).Elem())
	var el protocol.SSHElement
	fillAll(reflect.ValueOf(&el).Elem())
	var sys protocol.SSHSystemEvent
	fillAll(reflect.ValueOf(&sys).Elem())
	var hum protocol.SSHHumanMessage
	fillAll(reflect.ValueOf(&hum).Elem())
	blk.Private = false // a private block is dropped whole; this pins the kept shape

	in := protocol.SSHTimelineData{
		Session: "s", LastSeq: 9, More: true,
		Connections: []protocol.SSHConnInfo{conn},
		Items: []protocol.SSHTimelineItem{{
			ID: "1", Kind: "x", Order: 1, Seq: 1, At: time.Unix(1, 0),
			Block: &blk, Element: &el, System: &sys, Human: &hum,
		}},
	}
	out := publicTimeline(in)
	it := out.Items[0]

	check := func(name string, got any, want []string) {
		t.Helper()
		if g := keptFields(got); !slices.Equal(g, want) {
			t.Errorf("%s keeps %v, want %v", name, g, want)
		}
	}
	check("SSHTimelineData", out, allFieldsExcept(out))
	check("SSHConnInfo", out.Connections[0], sorted("ID", "Session", "Alias", "Label", "Tier", "Entry", "Sandboxed", "State", "Wheel", "WheelSince", "Why", "Scope", "RequestedAt"))
	check("SSHTimelineItem", it, allFieldsExcept(it))
	check("SSHBlock", *it.Block, allFieldsExcept(blk, "Host", "User", "Cwd", "Private"))
	check("SSHElement", *it.Element, allFieldsExcept(el, "Cwd", "HTML", "Data"))
	check("SSHSystemEvent", *it.System, allFieldsExcept(sys))
	check("SSHHumanMessage", *it.Human, allFieldsExcept(hum, "Transcript"))
	if it.Element.HTML != "" || it.Element.Data != "" {
		t.Error("widget document must be absent")
	}

	// The copy shares no pointer or slice with its input.
	*it.Block.ExitCode = 99
	if *blk.ExitCode == 99 {
		t.Error("ExitCode aliases the input")
	}
}

// A prompt card, and the secret answered into it, never reach a phone's
// timeline: the card is memory-only, the shell block is private.
func TestSessionTimelineHasNoPromptAnswer(t *testing.T) {
	r, conn, tap, fp := promptRig(t)
	tap.start("sudo ls")
	tap.output([]byte("[sudo] password for m: "))
	tap.promptRead()
	fp.show("$ sudo ls\n[sudo] password for m: ", 3)
	q := r.wirePrompt(t, conn)
	if q == nil {
		t.Fatal("fixture: no prompt on the desktop's timeline")
	}
	if _, err := r.d.handleSSHPromptAnswer(context.Background(), protocol.SSHPromptAnswerArgs{Conn: conn, Prompt: q.ID, Action: protocol.SSHPromptSend, Secret: testSecret}); err != nil {
		t.Fatal(err)
	}
	tap.start("sudo reboot")
	tap.output([]byte("[sudo] password for m: "))
	tap.promptRead() // a second card is pending while the phone reads

	got, err := r.d.handleSessionTimeline(protocol.SessionTimelineArgs{Session: "s1"})
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(got)
	for _, leak := range []string{testSecret, "sudo ls", "sudo reboot", "password for m", `"prompt"`, "termOpenLine"} {
		if strings.Contains(string(b), leak) {
			t.Errorf("%q reached the phone timeline: %s", leak, b)
		}
	}
	for _, c := range got.Connections {
		if c.Prompt != nil {
			t.Errorf("prompt card on a phone connection: %+v", c.Prompt)
		}
	}
}
