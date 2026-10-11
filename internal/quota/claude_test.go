package quota

import (
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestClaudeUsageResponse(t *testing.T) {
	now := time.Date(2026, 10, 11, 12, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name, body string
		count      int
		used       float64
	}{
		{"real response", `{"five_hour":{"utilization":2,"resets_at":"2026-10-11T19:40:00.447604+00:00"},"seven_day":{"utilization":25,"resets_at":"2026-10-12T12:00:00.447633+00:00"}}`, 2, 2},
		{"zero is known", `{"five_hour":{"utilization":0,"resets_at":"2026-10-11T19:40:00Z"}}`, 1, 0},
		{"absent is unknown", `{"five_hour":null,"seven_day":null}`, 0, 0},
		{"missing percentage", `{"five_hour":{"resets_at":"2026-10-11T19:40:00Z"}}`, 0, 0},
		{"expired", `{"five_hour":{"utilization":2,"resets_at":"2026-10-10T19:40:00Z"}}`, 0, 0},
		{"missing reset", `{"five_hour":{"utilization":2}}`, 0, 0},
		{"bad reset", `{"five_hour":{"utilization":2,"resets_at":"tomorrow"}}`, 0, 0},
		{"malformed", `secret not json`, 0, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, err := parseClaudeUsage([]byte(tc.body), now)
			if len(s.Windows) != tc.count || (err != nil) != (tc.count == 0) {
				t.Fatalf("snapshot=%+v error=%v", s, err)
			}
			if tc.count > 0 && s.Windows[0].UsedPercent != tc.used {
				t.Fatalf("percentage=%v", s.Windows[0].UsedPercent)
			}
		})
	}
}

type quotaTransport func(*http.Request) (*http.Response, error)

func (f quotaTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestClaudeRequestErrorsAndRetryAfter(t *testing.T) {
	now := time.Date(2026, 10, 11, 12, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		code  int
		retry string
		wait  time.Duration
	}{
		{401, "", 0}, {403, "", 0}, {500, "", 0}, {302, "", 0},
		{429, "0", 0}, {429, "600", 10 * time.Minute}, {429, now.Add(time.Hour).Format(http.TimeFormat), time.Hour},
	} {
		client := &http.Client{Transport: quotaTransport(func(r *http.Request) (*http.Response, error) {
			if r.URL.String() != "https://api.anthropic.com/api/oauth/usage" || r.Header.Get("Authorization") != "Bearer private-token" || r.Header.Get("anthropic-beta") != "oauth-2025-04-20" {
				t.Fatal("incorrect authenticated request")
			}
			return &http.Response{StatusCode: tc.code, Header: http.Header{"Retry-After": []string{tc.retry}}, Body: io.NopCloser(strings.NewReader("private-token server error"))}, nil
		})}
		_, err := requestClaude(context.Background(), client, "private-token", now)
		var failure *claudeFetchError
		if !errors.As(err, &failure) || failure.retryAfter != tc.wait || strings.Contains(err.Error(), "private-token") {
			t.Fatalf("error=%v", err)
		}
	}
	client := &http.Client{Transport: quotaTransport(func(*http.Request) (*http.Response, error) { return nil, errors.New("private-token transport error") })}
	_, err := requestClaude(context.Background(), client, "private-token", now)
	if err == nil || strings.Contains(err.Error(), "private-token") {
		t.Fatalf("error=%v", err)
	}
}

func TestClaudePollerCacheBackoffAndRecovery(t *testing.T) {
	now := time.Now()
	calls := 0
	var fail error
	path := filepath.Join(t.TempDir(), "quota.json")
	p := NewClaudePoller(path, func(ctx context.Context, at time.Time) (Snapshot, error) {
		calls++
		if _, ok := ctx.Deadline(); !ok {
			t.Fatal("fetch must be bounded")
		}
		return Snapshot{At: at, Windows: []Window{{Label: "5h", UsedPercent: 25, ResetsAt: now.Add(30 * time.Minute)}}}, fail
	})
	s, stale, detail := p.Refresh(context.Background(), Snapshot{}, now)
	if calls != 1 || stale || detail != "" || len(s.Windows) != 1 {
		t.Fatalf("initial fetch: %+v %v %s calls=%d", s, stale, detail, calls)
	}
	// A restart reuses the persisted reading without a request.
	restarted := NewClaudePoller(path, func(context.Context, time.Time) (Snapshot, error) {
		t.Fatal("fresh cache fetched")
		return Snapshot{}, nil
	})
	restarted.Refresh(context.Background(), Snapshot{}, now.Add(time.Minute))
	p.Refresh(context.Background(), Snapshot{}, now.Add(4*time.Minute))
	if calls != 1 {
		t.Fatal("fresh reading fetched")
	}
	fail = &claudeFetchError{message: "rate limited", retryAfter: 10 * time.Minute}
	s, stale, detail = p.Refresh(context.Background(), Snapshot{}, now.Add(5*time.Minute))
	if calls != 2 || !stale || detail != "rate limited" || !s.At.Equal(now) {
		t.Fatalf("failed refresh lost cached reading: %+v %v %s", s, stale, detail)
	}
	p.Refresh(context.Background(), Snapshot{}, now.Add(14*time.Minute))
	if calls != 2 {
		t.Fatal("ignored Retry-After")
	}
	p.Refresh(context.Background(), Snapshot{}, now.Add(15*time.Minute))
	if calls != 3 || p.delay != 10*time.Minute {
		t.Fatal("no exponential backoff")
	}
	// A new hook supplies fresh data even during network backoff.
	hook := Snapshot{At: now.Add(16 * time.Minute), Windows: []Window{{Label: "7d", UsedPercent: 26, ResetsAt: now.Add(time.Hour)}}}
	s, stale, detail = p.Refresh(context.Background(), hook, hook.At)
	if calls != 3 || stale || detail != "" || s.Windows[0].UsedPercent != 26 {
		t.Fatal("fresh hook did not recover display")
	}
	fail = nil
	p.Refresh(context.Background(), Snapshot{}, now.Add(25*time.Minute))
	if calls != 4 || p.delay != 0 {
		t.Fatal("success did not reset backoff")
	}
	fail = errors.New("offline")
	s, stale, _ = p.Refresh(context.Background(), Snapshot{}, now.Add(31*time.Minute))
	if !stale || len(s.Windows) != 0 {
		t.Fatal("expired window must not survive a failed fetch")
	}
}

func TestClaudePollerMissingDataBacksOff(t *testing.T) {
	now := time.Now()
	calls := 0
	p := NewClaudePoller("", func(context.Context, time.Time) (Snapshot, error) { calls++; return Snapshot{}, nil })
	for i := 0; i < 5; i++ {
		s, stale, detail := p.Refresh(context.Background(), Snapshot{}, now.Add(time.Duration(i)*time.Minute))
		if len(s.Windows) != 0 || !stale || detail == "" {
			t.Fatal("missing reading must have availability reason")
		}
	}
	if calls != 1 {
		t.Fatal("empty response must back off")
	}
}

func TestClaudeCredentialsReadOnly(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", dir)
	path := filepath.Join(dir, ".credentials.json")
	for _, raw := range []string{
		`{"claudeAiOauth":{"accessToken":"private-token","subscriptionType":"max"}}`,
		`{"claudeAiOauth":{"accessToken":"private-token","expiresAt":1}}`,
		`invalid private-token`,
	} {
		if err := os.WriteFile(path, []byte(raw), 0o600); err != nil {
			t.Fatal(err)
		}
		_, err := readClaudeCredentials(context.Background(), time.Now())
		if err != nil && strings.Contains(err.Error(), "private-token") {
			t.Fatal("credential leaked")
		}
		if strings.Contains(raw, "subscriptionType") != (err == nil) {
			t.Fatalf("unexpected credential result: %v", err)
		}
		after, _ := os.ReadFile(path)
		if string(after) != raw {
			t.Fatal("credentials were mutated")
		}
	}
}
