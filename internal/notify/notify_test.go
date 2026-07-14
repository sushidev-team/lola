package notify

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"sync"
	"testing"
)

// fakeChannel is an in-package channel used to observe routing without any
// real desktop/network delivery.
type fakeChannel struct {
	n  string
	mu sync.Mutex
	rx []Note
}

func (f *fakeChannel) name() string { return f.n }

func (f *fakeChannel) send(_ context.Context, note Note) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.rx = append(f.rx, note)
	return nil
}

func (f *fakeChannel) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.rx)
}

func TestPriorityString(t *testing.T) {
	cases := map[Priority]string{
		Urgent:       "urgent",
		Action:       "action",
		Info:         "info",
		Priority(99): "Priority(99)",
	}
	for p, want := range cases {
		if got := p.String(); got != want {
			t.Errorf("Priority(%d).String() = %q, want %q", int(p), got, want)
		}
	}
}

// TestNotifyRouting verifies priority routing: Urgent fans out to both
// channels, Info reaches Slack only, and an unrouted priority reaches none.
func TestNotifyRouting(t *testing.T) {
	desk := &fakeChannel{n: ChannelDesktop}
	slack := &fakeChannel{n: ChannelSlack}
	o := &notifier{
		byName: map[string]channel{ChannelDesktop: desk, ChannelSlack: slack},
		routing: map[Priority][]string{
			Urgent: {ChannelDesktop, ChannelSlack},
			Info:   {ChannelSlack},
		},
	}

	o.Notify(context.Background(), Note{Priority: Urgent, Title: "u"})
	if desk.count() != 1 || slack.count() != 1 {
		t.Fatalf("after Urgent: desk=%d slack=%d, want 1 and 1", desk.count(), slack.count())
	}

	o.Notify(context.Background(), Note{Priority: Info, Title: "i"})
	if desk.count() != 1 || slack.count() != 2 {
		t.Fatalf("after Info: desk=%d slack=%d, want 1 and 2", desk.count(), slack.count())
	}

	o.Notify(context.Background(), Note{Priority: Action, Title: "a"})
	if desk.count() != 1 || slack.count() != 2 {
		t.Fatalf("after unrouted Action: desk=%d slack=%d, want unchanged 1 and 2", desk.count(), slack.count())
	}
}

// TestNotifyRoutingDedupsChannels ensures a channel named twice for one
// priority delivers a single time.
func TestNotifyRoutingDedupsChannels(t *testing.T) {
	slack := &fakeChannel{n: ChannelSlack}
	o := &notifier{
		byName:  map[string]channel{ChannelSlack: slack},
		routing: map[Priority][]string{Urgent: {ChannelSlack, ChannelSlack}},
	}
	o.Notify(context.Background(), Note{Priority: Urgent, Title: "u"})
	if slack.count() != 1 {
		t.Fatalf("slack delivered %d times, want 1", slack.count())
	}
}

// TestNewEmptyConfigIsNoop verifies an unconfigured notifier delivers nothing
// and does not panic.
func TestNewEmptyConfigIsNoop(t *testing.T) {
	n := New(NotifyConfig{})
	no, ok := n.(*notifier)
	if !ok {
		t.Fatalf("New returned %T, want *notifier", n)
	}
	if len(no.byName) != 0 {
		t.Fatalf("empty config built channels: %v", no.byName)
	}
	// Must not panic even with a routing that names channels.
	n2 := New(NotifyConfig{Routing: DefaultRouting()})
	n2.Notify(context.Background(), Note{Priority: Urgent, Title: "x"})
}

// TestNewWiresConfiguredChannels checks New enables Slack when a webhook is
// present and desktop only on darwin.
func TestNewWiresConfiguredChannels(t *testing.T) {
	n := New(NotifyConfig{Desktop: true, SlackWebhook: "https://hooks.example/T/B/X", Routing: DefaultRouting()})
	no := n.(*notifier)
	if _, ok := no.byName[ChannelSlack]; !ok {
		t.Errorf("slack channel not wired when webhook set")
	}
	_, hasDesktop := no.byName[ChannelDesktop]
	if runtime.GOOS == "darwin" && !hasDesktop {
		t.Errorf("desktop channel not wired on darwin")
	}
	if runtime.GOOS != "darwin" && hasDesktop {
		t.Errorf("desktop channel wired off darwin (should be no-op)")
	}
}

// TestNewRoutingCopyIsIsolated verifies New snapshots the routing map so a
// caller mutating its own map afterwards cannot change delivery.
func TestNewRoutingCopyIsIsolated(t *testing.T) {
	routing := map[Priority][]string{Urgent: {ChannelSlack}}
	built := New(NotifyConfig{SlackWebhook: "https://hooks.example/x", Routing: routing})
	routing[Urgent] = nil                    // caller mutation post-build
	routing[Info] = []string{ChannelDesktop} // add a new key too
	bn := built.(*notifier)
	if got := bn.routing[Urgent]; len(got) != 1 || got[0] != ChannelSlack {
		t.Fatalf("routing not copied; Urgent = %v after caller mutation, want [slack]", got)
	}
	if got := bn.routing[Info]; got != nil {
		t.Fatalf("routing not copied; Info = %v after caller added a key, want none", got)
	}
}

func TestSlackText(t *testing.T) {
	cases := []struct {
		n    Note
		want string
	}{
		{Note{Title: "T"}, "T"},
		{Note{Title: "T", Body: "B"}, "T\nB"},
		{Note{Title: "T", Body: "B", URL: "http://x"}, "T\nB\nhttp://x"},
		{Note{Title: "T", URL: "http://x"}, "T\nhttp://x"},
	}
	for _, c := range cases {
		if got := slackText(c.n); got != c.want {
			t.Errorf("slackText(%+v) = %q, want %q", c.n, got, c.want)
		}
	}
}

// TestSlackSendPostsJSON verifies the channel POSTs the expected JSON body and
// content type to the webhook.
func TestSlackSendPostsJSON(t *testing.T) {
	var (
		gotBody   []byte
		gotCT     string
		gotMethod string
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotCT = r.Header.Get("Content-Type")
		gotBody, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, "ok")
	}))
	defer srv.Close()

	s := newSlackChannel(srv.URL)
	if err := s.send(context.Background(), Note{Title: "Approved", Body: "PR #7 is green", URL: "http://pr/7"}); err != nil {
		t.Fatalf("send: %v", err)
	}
	if gotMethod != http.MethodPost {
		t.Errorf("method = %q, want POST", gotMethod)
	}
	if gotCT != "application/json" {
		t.Errorf("content-type = %q, want application/json", gotCT)
	}
	var payload map[string]string
	if err := json.Unmarshal(gotBody, &payload); err != nil {
		t.Fatalf("body not JSON: %v (%q)", err, gotBody)
	}
	if want := "Approved\nPR #7 is green\nhttp://pr/7"; payload["text"] != want {
		t.Errorf("text = %q, want %q", payload["text"], want)
	}
	if len(payload) != 1 {
		t.Errorf("payload has extra keys: %v", payload)
	}
}

// TestNewSlackNotifyDelivers checks the standalone Notifier from NewSlack.
func TestNewSlackNotifyDelivers(t *testing.T) {
	var hits int
	var mu sync.Mutex
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		hits++
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	NewSlack(srv.URL).Notify(context.Background(), Note{Title: "hi"})
	mu.Lock()
	defer mu.Unlock()
	if hits != 1 {
		t.Fatalf("server hit %d times, want 1", hits)
	}
}

// TestSlackErrorsNeverLeakURL is the secret-safety guard: neither a non-2xx
// response nor a transport failure may surface the webhook URL in the error.
func TestSlackErrorsNeverLeakURL(t *testing.T) {
	// Non-2xx status.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	secretURL := srv.URL
	s := newSlackChannel(secretURL)
	err := s.send(context.Background(), Note{Title: "x"})
	if err == nil {
		t.Fatal("want error on 500 response")
	}
	if strings.Contains(err.Error(), secretURL) {
		t.Fatalf("error leaks webhook URL: %q", err.Error())
	}
	srv.Close()

	// Transport failure (server now closed) — net/http returns a *url.Error
	// whose default string embeds the URL; the channel must sanitize it.
	err = s.send(context.Background(), Note{Title: "x"})
	if err == nil {
		t.Fatal("want error on transport failure")
	}
	if strings.Contains(err.Error(), secretURL) {
		t.Fatalf("transport error leaks webhook URL: %q", err.Error())
	}
}

func TestResolveWebhook(t *testing.T) {
	if got := ResolveWebhook(""); got != "" {
		t.Errorf("empty env name = %q, want \"\"", got)
	}
	if got := ResolveWebhook("LOLA_TEST_WEBHOOK_UNSET_XYZ"); got != "" {
		t.Errorf("unset env = %q, want \"\"", got)
	}
	t.Setenv("LOLA_TEST_WEBHOOK", "https://hooks.example/secret")
	if got := ResolveWebhook("LOLA_TEST_WEBHOOK"); got != "https://hooks.example/secret" {
		t.Errorf("resolve = %q, want the secret value", got)
	}
}
