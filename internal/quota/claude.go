package quota

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"time"
)

const ClaudeRefreshInterval = 5 * time.Minute

// ClaudeFetch is the network/credential seam. Errors must be safe to display:
// never include credentials, response bodies, or command output.
type ClaudeFetch func(context.Context, time.Time) (Snapshot, error)

type claudeFetchError struct {
	message    string
	retryAfter time.Duration
}

func (e *claudeFetchError) Error() string { return e.message }

// ClaudePoller belongs to the daemon's single usage worker. API snapshots have
// their own file so an in-flight fetch cannot overwrite a newer status-line hook.
// Only usage figures are persisted; Claude owns login and token refresh.
type ClaudePoller struct {
	fetch   ClaudeFetch
	path    string
	cached  Snapshot
	next    time.Time
	delay   time.Duration
	problem string
}

func NewClaudePoller(path string, fetch ClaudeFetch) *ClaudePoller {
	if fetch == nil {
		fetch = fetchClaude
	}
	s, _ := ReadClaude(path)
	return &ClaudePoller{path: path, fetch: fetch, cached: s}
}

// Refresh prefers the newest hook/API reading, polling only when it is stale
// or a window has expired. A failed fetch keeps live cached windows with their
// original timestamp. Missing data is never manufactured as zero percent.
func (p *ClaudePoller) Refresh(ctx context.Context, hook Snapshot, now time.Time) (Snapshot, bool, string) {
	if hook.At.After(p.cached.At) {
		p.cached = hook
	}
	live, ok := p.cached.Live(now)
	fresh := ok && len(live.Windows) == len(p.cached.Windows) && !live.At.IsZero() && now.Sub(live.At) < ClaudeRefreshInterval
	if fresh {
		return live, false, ""
	}
	if !now.Before(p.next) {
		ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
		s, err := p.fetch(ctx, now)
		cancel()
		if err == nil {
			if _, valid := s.Live(now); !valid {
				err = errors.New("Claude returned no current usage limits")
			}
		}
		if err == nil {
			p.cached, p.problem, p.delay = s, "", 0
			p.next = now.Add(ClaudeRefreshInterval)
			if p.path != "" {
				_ = RecordClaude(p.path, s)
			}
			live, _ = s.Live(now)
			return live, false, ""
		}
		p.problem = err.Error()
		p.delay = min(max(ClaudeRefreshInterval, p.delay*2), time.Hour)
		wait := p.delay
		var failure *claudeFetchError
		if errors.As(err, &failure) {
			wait = max(wait, failure.retryAfter)
		}
		p.next = now.Add(wait)
	}
	if p.problem == "" {
		return live, true, "Waiting for the next usage refresh"
	}
	return live, true, p.problem
}

type claudeCredentials struct {
	OAuth struct {
		AccessToken string `json:"accessToken"`
		ExpiresAt   int64  `json:"expiresAt"`
		Plan        string `json:"subscriptionType"`
	} `json:"claudeAiOauth"`
}

// readClaudeCredentials never writes or refreshes credentials. Custom config
// directories only use their own credentials file, never another account's
// default keychain entry.
func readClaudeCredentials(ctx context.Context, now time.Time) (claudeCredentials, error) {
	var credentials claudeCredentials
	dir := os.Getenv("CLAUDE_CONFIG_DIR")
	var raw []byte
	if runtime.GOOS == "darwin" && dir == "" {
		cmd := exec.CommandContext(ctx, "/usr/bin/security", "find-generic-password", "-s", "Claude Code-credentials", "-w")
		raw, _ = cmd.Output()
	}
	if len(raw) == 0 {
		if dir == "" {
			home, _ := os.UserHomeDir()
			if home != "" {
				dir = filepath.Join(home, ".claude")
			}
		}
		if dir != "" {
			raw, _ = os.ReadFile(filepath.Join(dir, ".credentials.json"))
		}
	}
	if json.Unmarshal(raw, &credentials) != nil || credentials.OAuth.AccessToken == "" {
		return claudeCredentials{}, errors.New("Claude login unavailable; open Claude Code and sign in")
	}
	if credentials.OAuth.ExpiresAt > 0 && credentials.OAuth.ExpiresAt <= now.UnixMilli() {
		return claudeCredentials{}, errors.New("Claude login expired; open Claude Code to refresh it")
	}
	return credentials, nil
}

func fetchClaude(ctx context.Context, now time.Time) (Snapshot, error) {
	credentials, err := readClaudeCredentials(ctx, now)
	if err != nil {
		return Snapshot{}, err
	}
	// No redirects: the bearer credential must only go to this exact endpoint.
	client := &http.Client{Timeout: 8 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	s, err := requestClaude(ctx, client, credentials.OAuth.AccessToken, now)
	if err == nil {
		switch credentials.OAuth.Plan {
		case "pro", "max", "team", "enterprise":
			s.Plan = credentials.OAuth.Plan
		}
	}
	return s, err
}

func requestClaude(ctx context.Context, client *http.Client, token string, now time.Time) (Snapshot, error) {
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, "https://api.anthropic.com/api/oauth/usage", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("anthropic-beta", "oauth-2025-04-20")
	req.Header.Set("User-Agent", "lola-usage")
	resp, err := client.Do(req)
	if err != nil {
		return Snapshot{}, errors.New("Cannot reach Claude usage; will retry automatically")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		failure := &claudeFetchError{message: fmt.Sprintf("Claude usage unavailable (HTTP %d); will retry automatically", resp.StatusCode)}
		switch resp.StatusCode {
		case 401, 403:
			failure.message = "Claude usage access denied; open Claude Code to check your login"
		case 429:
			failure.message = "Claude usage requests are rate limited; will retry automatically"
			if seconds, err := strconv.ParseInt(resp.Header.Get("Retry-After"), 10, 64); err == nil && seconds > 0 {
				failure.retryAfter = time.Duration(min(seconds, 86400)) * time.Second
			} else if at, err := http.ParseTime(resp.Header.Get("Retry-After")); err == nil {
				failure.retryAfter = min(max(at.Sub(now), 0), 24*time.Hour)
			}
		}
		return Snapshot{}, failure
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, (1<<20)+1))
	if err != nil || len(data) > 1<<20 {
		return Snapshot{}, errors.New("Cannot read Claude usage response")
	}
	return parseClaudeUsage(data, now)
}

func parseClaudeUsage(data []byte, now time.Time) (Snapshot, error) {
	type window struct {
		Used  *float64  `json:"utilization"`
		Reset time.Time `json:"resets_at"`
	}
	var body struct {
		FiveHour *window `json:"five_hour"`
		SevenDay *window `json:"seven_day"`
	}
	if json.Unmarshal(data, &body) != nil {
		return Snapshot{}, errors.New("Claude returned an invalid usage response")
	}
	s := Snapshot{At: now}
	for i, w := range []*window{body.FiveHour, body.SevenDay} {
		if w == nil || w.Used == nil || !w.Reset.After(now) {
			continue
		}
		s.Windows = append(s.Windows, Window{Label: []string{"5h", "7d"}[i], UsedPercent: clampPercent(*w.Used), ResetsAt: w.Reset})
	}
	if len(s.Windows) == 0 {
		return Snapshot{}, errors.New("Claude returned no current usage limits")
	}
	return s, nil
}
