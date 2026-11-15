package protocol

import (
	"bytes"
	"encoding/json"
	"math"
	"strings"
	"testing"
)

func TestParseTopic(t *testing.T) {
	long := strings.Repeat("a", 128)
	cases := []struct {
		topic, kind, session string
		ok                   bool
	}{
		{"sessions", "sessions", "", true},
		{"projects", "projects", "", true},
		{"status", "status", "", true},
		{"talk:lola-fe-42", "talk", "lola-fe-42", true},
		{"feed:lola-fe-42", "feed", "lola-fe-42", true},
		{"timeline:lola-fe-42", "timeline", "lola-fe-42", true},
		{"diff:lola-fe-42", "diff", "lola-fe-42", true},
		{"talk:" + long, "talk", long, true},
		{"talk:lola-fe-42~web", "", "", false}, // lane id
		{"talk:..", "", "", false},
		{"talk:.hidden", "", "", false},
		{"talk:a/b", "", "", false},
		{`talk:a\b`, "", "", false},
		{"talk:", "", "", false}, // trailing colon, empty id
		{"talk:" + long + "a", "", "", false},
		{"Talk:lola-fe-42", "", "", false}, // uppercase kind
		{"sessions:x", "", "", false},
		{"nope:x", "", "", false},
		{"talk", "", "", false},
		{"", "", "", false},
		{"talk:a\nb", "", "", false},
		{"talk:" + strings.Repeat("a", MaxTopicBytes), "", "", false},
	}
	for _, tc := range cases {
		k, s, ok := ParseTopic(tc.topic)
		if k != tc.kind || s != tc.session || ok != tc.ok {
			t.Errorf("ParseTopic(%q) = %q, %q, %v; want %q, %q, %v", tc.topic, k, s, ok, tc.kind, tc.session, tc.ok)
		}
	}
}

func TestFrameEventDirection(t *testing.T) {
	if FrameDirections(FrameEvent) != DirToClient || FrameDirections(FrameReady) != DirToClient {
		t.Fatal("event and ready travel to the client only")
	}
	for _, typ := range []string{FrameEvent, FrameReady} {
		if DaemonAcceptsFrame(typ) {
			t.Errorf("the daemon must refuse an inbound %q", typ)
		}
		if !ClientAcceptsFrame(typ) || !KnownFrameType(typ) {
			t.Errorf("%q must be a known client-bound type", typ)
		}
	}
	if FrameVersionCurrent != 2 || FrameVersionMin != 2 {
		t.Fatalf("envelope is v2 only, got %d..%d", FrameVersionMin, FrameVersionCurrent)
	}
}

// An event frame at every bound still fits one frame.
func TestEventBudgetFitsFrame(t *testing.T) {
	if EventMaxBytes+EnvelopeMaxBytes+FrameHeaderBytes > MaxFrameBytes {
		t.Fatalf("EventMaxBytes %d + envelope %d + prefix does not fit MaxFrameBytes %d", EventMaxBytes, EnvelopeMaxBytes, MaxFrameBytes)
	}
	// '<' escapes to six bytes; size the text so the ENCODED payload is exactly
	// EventMaxBytes.
	wrapper := len(`{"text":""}`)
	n := (EventMaxBytes - wrapper) / 6
	payload, err := json.Marshal(map[string]string{"text": strings.Repeat("<", n)})
	if err != nil {
		t.Fatal(err)
	}
	if len(payload) > EventMaxBytes {
		t.Fatalf("payload %d over EventMaxBytes", len(payload))
	}
	f := Frame{
		V: FrameVersionCurrent, Type: FrameEvent,
		ID:    strings.Repeat("w", MaxWatchIDBytes),
		Topic: TopicTimeline + ":" + strings.Repeat("s", MaxTopicBytes-len(TopicTimeline)-1),
		Seq:   math.MaxUint64, Payload: payload,
	}
	var buf bytes.Buffer
	if err := NewFrameWriter(&buf).WriteFrame(&f); err != nil {
		t.Fatalf("a maximal event frame does not encode: %v", err)
	}
	if over := buf.Len() - FrameHeaderBytes - len(payload); over > EnvelopeMaxBytes {
		t.Fatalf("envelope is %d bytes, over EnvelopeMaxBytes %d", over, EnvelopeMaxBytes)
	}
	if buf.Len()-FrameHeaderBytes > MaxFrameBytes {
		t.Fatal("frame body over MaxFrameBytes")
	}
}

func TestSizeOfIsEncodedLength(t *testing.T) {
	evs := []TopicEvent{
		&FeedEvent{Session: "s", Epoch: "e", Health: "ok", Items: []AgentFeedItem{
			{ID: "a", Kind: "assistant", Text: "<b>&amp; \x00\x1b[31m tab\t   end</b>"},
		}},
		&DiffEvent{Rev: 4, Cause: DiffCauseCheckpoint},
		&TalkEvent{TalkStateData: TalkStateData{Session: "s<", Composer: "gone", Health: "unsupported"}},
		&SessionsEvent{Remove: []string{"a&b"}},
	}
	for _, ev := range evs {
		ev = sizeOfAny(ev)
		f := Frame{V: FrameVersionCurrent, Type: FrameEvent, ID: "w1", Topic: "x", Seq: 1}
		if err := f.SetPayload(ev); err != nil {
			t.Fatal(err)
		}
		var buf bytes.Buffer
		if err := NewFrameWriter(&buf).WriteFrame(&f); err != nil {
			t.Fatal(err)
		}
		var back Frame
		if err := NewFrameReader(&buf).ReadFrame(&back); err != nil {
			t.Fatal(err)
		}
		if ev.Size() != len(back.Payload) {
			t.Errorf("%T: Size %d, payload bytes on the wire %d", ev, ev.Size(), len(back.Payload))
		}
	}
}

func sizeOfAny(ev TopicEvent) TopicEvent {
	switch e := ev.(type) {
	case *FeedEvent:
		return SizeOf(e)
	case *DiffEvent:
		return SizeOf(e)
	case *TalkEvent:
		return SizeOf(e)
	case *SessionsEvent:
		return SizeOf(e)
	}
	return ev
}

func row(id, head string) SessionInfo { return SessionInfo{ID: id, Headline: head} }

func ids(rows []SessionInfo) []string {
	var out []string
	for _, r := range rows {
		out = append(out, r.ID)
	}
	return out
}

func TestSessionsEventMerge(t *testing.T) {
	t.Run("later upsert wins and keeps position", func(t *testing.T) {
		a := SizeOf(&SessionsEvent{Upsert: []SessionInfo{row("a", "1"), row("b", "1")}})
		b := SizeOf(&SessionsEvent{Upsert: []SessionInfo{row("b", "2"), row("c", "2")}})
		m, ok := a.Merge(b)
		if !ok {
			t.Fatal("deltas must merge")
		}
		got := m.(*SessionsEvent)
		if strings.Join(ids(got.Upsert), ",") != "a,b,c" || got.Upsert[1].Headline != "2" {
			t.Fatalf("upsert = %+v", got.Upsert)
		}
		if len(a.Upsert) != 2 || a.Upsert[1].Headline != "1" {
			t.Fatal("Merge modified its receiver")
		}
	})
	t.Run("remove cancels pending upsert, upsert cancels remove", func(t *testing.T) {
		a := SizeOf(&SessionsEvent{Upsert: []SessionInfo{row("a", "1")}, Remove: []string{"x"}})
		b := SizeOf(&SessionsEvent{Upsert: []SessionInfo{row("x", "2")}, Remove: []string{"a"}})
		m, _ := a.Merge(b)
		got := m.(*SessionsEvent)
		if strings.Join(ids(got.Upsert), ",") != "x" || strings.Join(got.Remove, ",") != "a" {
			t.Fatalf("upsert %v remove %v", ids(got.Upsert), got.Remove)
		}
	})
	t.Run("events and omitted", func(t *testing.T) {
		a := SizeOf(&SessionsEvent{Events: []Event{{ID: "k"}}, Omitted: []string{"big"}})
		b := SizeOf(&SessionsEvent{Upsert: []SessionInfo{row("big", "now small")}})
		m, _ := a.Merge(b)
		got := m.(*SessionsEvent)
		if len(got.Events) != 1 {
			t.Fatal("an absent ring means unchanged: keep the earlier one")
		}
		if len(got.Omitted) != 0 {
			t.Fatalf("an id upserted later is no longer omitted: %v", got.Omitted)
		}
	})
	t.Run("parts never merge", func(t *testing.T) {
		delta := SizeOf(&SessionsEvent{Upsert: []SessionInfo{row("a", "1")}})
		for _, part := range []*SessionsEvent{{Reset: true}, {More: true}} {
			if _, ok := delta.Merge(part); ok {
				t.Error("merged a part into a delta")
			}
			if _, ok := part.Merge(delta); ok {
				t.Error("merged a delta into a part")
			}
		}
	})
	t.Run("kind mismatch and size bound", func(t *testing.T) {
		delta := &SessionsEvent{}
		if _, ok := delta.Merge(&DiffEvent{Rev: 1}); ok {
			t.Error("merged across kinds")
		}
		big := strings.Repeat("x", EventMaxBytes/2+1024)
		a := SizeOf(&SessionsEvent{Upsert: []SessionInfo{row("a", big)}})
		b := SizeOf(&SessionsEvent{Upsert: []SessionInfo{row("b", big)}})
		if _, ok := a.Merge(b); ok {
			t.Error("merged past EventMaxBytes")
		}
	})
}

func fitem(id string, seq int64, text string) AgentFeedItem {
	return AgentFeedItem{ID: id, Kind: "assistant", Seq: seq, Text: text}
}

func TestFeedEventMerge(t *testing.T) {
	a := SizeOf(&FeedEvent{Session: "s", Epoch: "e1", LastSeq: 2, Health: "ok", Scanning: true,
		Items: []AgentFeedItem{fitem("a", 1, "run"), fitem("b", 2, "x")}, Omitted: []OmittedItem{{ID: "o", Seq: 1}}})
	b := SizeOf(&FeedEvent{Session: "s", Epoch: "e1", LastSeq: 5, Health: "ok",
		Items: []AgentFeedItem{fitem("a", 4, "done"), fitem("c", 5, "y")}, Omitted: []OmittedItem{{ID: "p", Seq: 3}}})
	m, ok := a.Merge(b)
	if !ok {
		t.Fatal("same-epoch deltas must merge")
	}
	got := m.(*FeedEvent)
	if len(got.Items) != 3 || got.Items[0].Text != "done" || got.LastSeq != 5 || got.Scanning {
		t.Fatalf("merged = %+v", got)
	}
	if len(got.Omitted) != 2 {
		t.Fatalf("omitted must union: %v", got.Omitted)
	}
	// A stale (lower seq) item does not overwrite.
	stale := SizeOf(&FeedEvent{Session: "s", Epoch: "e1", LastSeq: 6, Items: []AgentFeedItem{fitem("a", 2, "old")}})
	m, _ = got.Merge(stale)
	if m.(*FeedEvent).Items[0].Text != "done" {
		t.Error("a lower seq replaced a higher one")
	}
	// An empty merge keeps Items non-nil.
	e, _ := (&FeedEvent{Session: "s", Epoch: "e"}).Merge(&FeedEvent{Session: "s", Epoch: "e"})
	if e.(*FeedEvent).Items == nil {
		t.Error("Items must never be null")
	}
	if _, ok := a.Merge(&FeedEvent{Session: "s", Epoch: "e2"}); ok {
		t.Error("merged across epochs")
	}
	if _, ok := a.Merge(&FeedEvent{Session: "other", Epoch: "e1"}); ok {
		t.Error("merged across sessions")
	}
	if _, ok := a.Merge(&FeedEvent{Reset: true, Session: "s", Epoch: "e1"}); ok {
		t.Error("merged a reset")
	}
}

func TestTimelineEventMerge(t *testing.T) {
	conn := []SSHConnInfo{{ID: "c1"}}
	a := SizeOf(&TimelineEvent{Session: "s", LastSeq: 3, Connections: conn,
		Items: []SSHTimelineItem{{ID: "a", Seq: 1}, {ID: "b", Seq: 3}}})
	b := SizeOf(&TimelineEvent{Session: "s", LastSeq: 5, Items: []SSHTimelineItem{{ID: "a", Seq: 5}}})
	m, ok := a.Merge(b)
	if !ok {
		t.Fatal("deltas must merge")
	}
	got := m.(*TimelineEvent)
	if len(got.Items) != 2 || got.Items[0].Seq != 5 || got.LastSeq != 5 {
		t.Fatalf("merged = %+v", got)
	}
	if len(got.Connections) != 1 {
		t.Error("absent connections mean unchanged: keep the earlier")
	}
	c := SizeOf(&TimelineEvent{Session: "s", LastSeq: 6, Connections: []SSHConnInfo{{ID: "c2"}}, Items: []SSHTimelineItem{}})
	m, _ = got.Merge(c)
	if m.(*TimelineEvent).Connections[0].ID != "c2" {
		t.Error("later connections win")
	}
	if _, ok := a.Merge(&TimelineEvent{Session: "s", More: true}); ok {
		t.Error("merged a part")
	}
	if _, ok := a.Merge(&TimelineEvent{Session: "t"}); ok {
		t.Error("merged across sessions")
	}
}

func TestReplaceTopicsKeepTheNewest(t *testing.T) {
	a := SizeOf(&DiffEvent{Rev: 1})
	m, ok := a.Merge(&DiffEvent{Rev: 2, Cause: DiffCauseEdit})
	if !ok || m.(*DiffEvent).Rev != 2 || m.Size() == 0 {
		t.Fatalf("diff merge = %+v %v", m, ok)
	}
	ta := &TalkEvent{TalkStateData: TalkStateData{Session: "s", Composer: "idle"}}
	m, ok = ta.Merge(&TalkEvent{TalkStateData: TalkStateData{Session: "s", Composer: "busy"}})
	if !ok || m.(*TalkEvent).Composer != "busy" {
		t.Fatal("talk replace")
	}
	// The wrapper marshals as the inner value.
	b, _ := json.Marshal(ta)
	if strings.Contains(string(b), "TalkStateData") || !strings.Contains(string(b), `"composer":"idle"`) {
		t.Fatalf("wrapper encoding: %s", b)
	}
}
