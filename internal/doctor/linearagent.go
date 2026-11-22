package doctor

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/sushidev-team/lola/internal/config"
	"github.com/sushidev-team/lola/internal/linear"
	"github.com/sushidev-team/lola/internal/secrets"
)

// checkLinearAgent names the native Linear agent row.
const checkLinearAgent = "linear agent"

// agentTokenEnv mirrors the daemon's env fallback (the daemon package is not
// importable from here).
const agentTokenEnv = "LOLA_LINEAR_AGENT_TOKEN"

// LinearAgentResult reports whether [linear_agent] can actually receive a
// mention: a stored token, the scopes Linear granted it, and how fast the loop
// hears about a new session. There is no row while the feature is off. It makes
// no network call and prints no credential, only scope words and source names.
// Never critical: the feature is optional.
func LinearAgentResult(cfg *config.Config) (Result, bool) {
	a := cfg.LinearAgent
	if !a.Enabled {
		return Result{}, false
	}
	res := Result{Name: checkLinearAgent}
	raw, err := secrets.Resolve("linear agent token", a.TokenKeychain, agentTokenEnv)
	if err != nil {
		res.Detail = "no agent token stored: run `lola linear-agent login`"
		return res, true
	}
	var tok linear.OAuthToken
	if json.Unmarshal([]byte(raw), &tok) != nil || tok.AccessToken == "" {
		tok = linear.OAuthToken{AccessToken: strings.TrimSpace(raw)} // a bare access token
	}
	var notes []string
	if missing := missingScopes(tok.Scope); len(missing) > 0 {
		res.Detail = "the token lacks scope " + strings.Join(missing, ", ") + ": run `lola linear-agent login` again and approve all of them"
		return res, true
	} else if tok.Scope == "" {
		notes = append(notes, "scopes not recorded (log in again to check them)")
	}
	if tok.RefreshToken == "" {
		if !tok.ExpiresAt.IsZero() && time.Now().After(tok.ExpiresAt) {
			res.Detail = "the stored login has expired and cannot refresh: run `lola linear-agent login` again"
			return res, true
		}
		notes = append(notes, "no refresh token: the login will expire")
	}
	switch {
	case a.WebhookListen != "":
		notes = append(notes, "webhook doorbell on "+a.WebhookListen)
	case cfg.Hub.Enabled:
		notes = append(notes, "hub doorbell (needs the org's Linear webhook secret on the hub)")
	default:
		notes = append(notes, fmt.Sprintf("polling every %s only: Linear expects a first reply within 10s, so a webhook or the hub doorbell is advised", a.PollInterval))
	}
	if a.OnlyWhenAssignedToMe {
		notes = append(notes, "acts only on sessions you own")
	} else {
		notes = append(notes, "acts on every session (set only_when_assigned_to_me when others run lola too)")
	}
	res.OK = true
	res.Detail = "token stored; " + strings.Join(notes, "; ")
	return res, true
}

// missingScopes lists the scopes lola asks for that a recorded grant lacks. An
// empty grant means "not recorded" and reports nothing.
func missingScopes(granted string) []string {
	if strings.TrimSpace(granted) == "" {
		return nil
	}
	have := map[string]bool{}
	for _, s := range strings.FieldsFunc(granted, func(r rune) bool { return r == ',' || r == ' ' }) {
		have[s] = true
	}
	var out []string
	for _, s := range strings.Split(linear.AgentScopes, ",") {
		if !have[s] {
			out = append(out, s)
		}
	}
	return out
}
