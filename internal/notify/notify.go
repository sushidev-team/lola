// Package notify delivers best-effort operator notifications (PLAN P3.20):
// desktop banners on macOS and/or a Slack incoming webhook, routed by
// priority. It is deliberately fire-and-forget — Notify never returns an
// error and never panics, so a reaction path can call it without guarding.
//
// Secret handling: the Slack webhook URL is a secret. It is resolved from an
// environment variable name (never from config.toml), stored only in the
// unexported slackChannel, and never placed in argv, a log line, or any
// returned error — the net/http *url.Error (which embeds the request URL) is
// deliberately replaced with a sanitized message before it can surface.
package notify

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"
)

// Priority selects which channels a Note is routed to (see New / Routing).
type Priority int

const (
	// Urgent is for things a human must act on now (needs_input, escalations).
	Urgent Priority = iota
	// Action is for things a human should look at soon (changes-requested,
	// CI failed after retries).
	Action
	// Info is for FYI events (approved+parked, merged+cleaned).
	Info
)

func (p Priority) String() string {
	switch p {
	case Urgent:
		return "urgent"
	case Action:
		return "action"
	case Info:
		return "info"
	default:
		return fmt.Sprintf("Priority(%d)", int(p))
	}
}

// Channel names, used as keys in Routing and in the internal registry.
const (
	ChannelDesktop = "desktop"
	ChannelSlack   = "slack"
)

// Note is a single notification. Body and URL may be empty.
type Note struct {
	Title    string
	Body     string
	Priority Priority
	URL      string
}

// Notifier delivers a Note. It is best-effort: it never returns an error and
// never panics. It may block up to a bounded per-channel timeout while
// channels run concurrently.
type Notifier interface {
	Notify(ctx context.Context, n Note)
}

// NotifyConfig configures the fan-out Notifier built by New.
//
//   - Desktop enables the macOS desktop channel (a no-op off darwin).
//   - SlackWebhook is the already-resolved webhook URL value (never an env
//     var name); "" disables the Slack channel. Resolve it with
//     ResolveWebhook.
//   - Routing maps each Priority to the channel names that should receive it.
//     A priority absent from the map (or naming an unconfigured channel) is
//     silently dropped.
type NotifyConfig struct {
	Desktop      bool
	SlackWebhook string
	Routing      map[Priority][]string
}

// perChannelTimeout bounds a single channel delivery so Notify cannot block
// the caller indefinitely. It sits above the Slack HTTP client's own 10s
// timeout so that timeout (which yields a sanitized error) fires first.
var perChannelTimeout = 15 * time.Second

// channel is one delivery backend. send's error is swallowed by the fan-out;
// it exists only so channels can be unit-tested in isolation.
type channel interface {
	name() string
	send(ctx context.Context, n Note) error
}

// notifier is the fan-out Notifier returned by New.
type notifier struct {
	byName  map[string]channel
	routing map[Priority][]string
}

// New builds a fan-out Notifier from cfg. Enabled channels are constructed
// once; each Note is routed by its Priority through cfg.Routing. Unconfigured
// channels and empty routing simply yield a no-op for the affected notes. The
// returned Notifier is always non-nil.
func New(cfg NotifyConfig) Notifier {
	byName := make(map[string]channel)
	if cfg.Desktop {
		if ch := newDesktopChannel(); ch != nil {
			byName[ChannelDesktop] = ch
		}
	}
	if cfg.SlackWebhook != "" {
		byName[ChannelSlack] = newSlackChannel(cfg.SlackWebhook)
	}
	// Copy routing so later caller mutation can't change delivery.
	routing := make(map[Priority][]string, len(cfg.Routing))
	for p, names := range cfg.Routing {
		routing[p] = append([]string(nil), names...)
	}
	return &notifier{byName: byName, routing: routing}
}

// Notify delivers n to every channel routed for n.Priority, concurrently,
// each bounded by perChannelTimeout and guarded against panics. It returns
// once all routed channels have finished (or timed out).
func (o *notifier) Notify(ctx context.Context, n Note) {
	if o == nil {
		return
	}
	if ctx == nil {
		ctx = context.Background()
	}
	var wg sync.WaitGroup
	seen := make(map[string]bool)
	for _, name := range o.routing[n.Priority] {
		if seen[name] {
			continue
		}
		seen[name] = true
		ch := o.byName[name]
		if ch == nil {
			continue // configured in routing but not enabled: skip silently
		}
		wg.Add(1)
		go func(ch channel) {
			defer wg.Done()
			defer func() { _ = recover() }() // best-effort: never propagate a panic
			cctx, cancel := context.WithTimeout(ctx, perChannelTimeout)
			defer cancel()
			_ = ch.send(cctx, n)
		}(ch)
	}
	wg.Wait()
}

// ResolveWebhook returns the webhook URL held in the named environment
// variable, or "" if envVar is empty or unset. The value is a secret: it is
// returned to the caller and never logged here.
func ResolveWebhook(envVar string) string {
	if envVar == "" {
		return ""
	}
	return os.Getenv(envVar)
}

// DefaultRouting is a sensible priority→channels default the daemon may use
// when the operator has not configured routing explicitly: Urgent is loud
// (desktop + Slack), lower priorities go to Slack only. Channels not enabled
// in NotifyConfig are skipped at delivery time.
func DefaultRouting() map[Priority][]string {
	return map[Priority][]string{
		Urgent: {ChannelDesktop, ChannelSlack},
		Action: {ChannelSlack},
		Info:   {ChannelSlack},
	}
}

// slackChannel posts to a Slack incoming webhook. The webhook URL is a secret
// (see package doc) and is never logged or embedded in an error.
type slackChannel struct {
	webhookURL string
	http       *http.Client
}

func newSlackChannel(webhookURL string) *slackChannel {
	return &slackChannel{
		webhookURL: webhookURL,
		http:       &http.Client{Timeout: 10 * time.Second},
	}
}

// NewSlack returns a standalone best-effort Notifier that posts every Note to
// the given (already-resolved) webhook URL, regardless of priority. The
// fan-out Notifier from New is the usual entry point; NewSlack is exposed for
// callers that want a Slack-only notifier.
func NewSlack(webhookURL string) Notifier { return newSlackChannel(webhookURL) }

func (s *slackChannel) name() string { return ChannelSlack }

func (s *slackChannel) Notify(ctx context.Context, n Note) {
	defer func() { _ = recover() }()
	if ctx == nil {
		ctx = context.Background()
	}
	_ = s.send(ctx, n) // HTTP client bounds this to 10s
}

func (s *slackChannel) send(ctx context.Context, n Note) error {
	// Errors below are intentionally URL-free: a wrapped *url.Error would leak
	// the secret webhook into any log that stringifies it.
	payload, err := json.Marshal(map[string]string{"text": slackText(n)})
	if err != nil {
		return errors.New("slack notify: marshal payload failed")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.webhookURL, bytes.NewReader(payload))
	if err != nil {
		return errors.New("slack notify: build request failed")
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := s.http.Do(req)
	if err != nil {
		return errors.New("slack notify: request failed")
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("slack notify: unexpected status %d", resp.StatusCode)
	}
	return nil
}

// slackText renders a Note as the webhook "text" field. Slack auto-links a
// bare URL, so it is appended on its own line.
func slackText(n Note) string {
	var b strings.Builder
	b.WriteString(n.Title)
	if n.Body != "" {
		if b.Len() > 0 {
			b.WriteByte('\n')
		}
		b.WriteString(n.Body)
	}
	if n.URL != "" {
		if b.Len() > 0 {
			b.WriteByte('\n')
		}
		b.WriteString(n.URL)
	}
	return b.String()
}
