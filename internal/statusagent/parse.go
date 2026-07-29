package statusagent

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
)

// ansiEscapeRe matches CSI and OSC escape sequences (same pattern the reaction
// sanitizer uses) so model-relayed terminal text strips clean.
var ansiEscapeRe = regexp.MustCompile("\x1b\\[[0-9;?:<>=]*[ -/]*[@-~]|\x1b\\][^\x07\x1b]*(?:\x07|\x1b\\\\)")

// Text caps for the sanitized display fields.
const (
	maxHeadlineRunes  = 120
	maxWaitingOnRunes = 200
)

// AgentStates is the closed vocabulary Parse accepts for agent_state.
// working/waiting_input/idle overlay the deterministic agent axis
// (internal/state.AgentState words); "stuck" is interpreter-ONLY display
// vocabulary (a wedged agent the deterministic pipeline cannot see); "unknown"
// means the interpreter could not tell (callers drop the overlay).
var AgentStates = []string{"working", "waiting_input", "idle", "stuck", "unknown"}

// Interpretation is one parsed, clamped, sanitized interpreter judgement.
// These are the ONLY values that may leave this package for display; the raw
// model output never does.
type Interpretation struct {
	AgentState string  // one of AgentStates
	Headline   string  // one sanitized line, ≤120 runes; may be "" only for "unknown"
	WaitingOn  string  // "" unless blocked; sanitized, ≤200 runes
	Confidence float64 // clamped to [0,1]
}

// ErrNoJSON: the output contained no parsable JSON object.
var ErrNoJSON = errors.New("statusagent: no JSON object in interpreter output")

// Parse extracts the FIRST balanced JSON object from raw (tolerating code
// fences and stray prose around it), validates the state word against the
// whitelist, clamps the confidence, and sanitizes/caps the text fields.
// An unlisted state, unparsable output, or an empty headline on a non-unknown
// state is an error — never coerced, never displayed.
func Parse(raw string) (Interpretation, error) {
	obj, ok := firstJSONObject(raw)
	if !ok {
		return Interpretation{}, ErrNoJSON
	}
	var p struct {
		AgentState string   `json:"agent_state"`
		Headline   string   `json:"headline"`
		WaitingOn  string   `json:"waiting_on"`
		Confidence *float64 `json:"confidence"`
	}
	if err := json.Unmarshal([]byte(obj), &p); err != nil {
		return Interpretation{}, fmt.Errorf("statusagent: bad interpreter JSON: %w", err)
	}
	state := strings.TrimSpace(p.AgentState)
	if !allowedState(state) {
		return Interpretation{}, fmt.Errorf("statusagent: agent_state %q not in the whitelist", state)
	}
	out := Interpretation{
		AgentState: state,
		Headline:   sanitizeLine(p.Headline, maxHeadlineRunes),
		WaitingOn:  sanitizeLine(p.WaitingOn, maxWaitingOnRunes),
	}
	if p.Confidence != nil {
		out.Confidence = clamp01(*p.Confidence)
	}
	if out.Headline == "" && state != "unknown" {
		return Interpretation{}, errors.New("statusagent: empty headline")
	}
	return out, nil
}

func allowedState(s string) bool { return slices.Contains(AgentStates, s) }

func clamp01(f float64) float64 {
	switch {
	case f != f: // NaN
		return 0
	case f < 0:
		return 0
	case f > 1:
		return 1
	}
	return f
}

// firstJSONObject scans raw for the first balanced {...} object, respecting
// JSON string literals and escapes so a brace inside a quoted headline cannot
// truncate the object.
func firstJSONObject(raw string) (string, bool) {
	start := strings.IndexByte(raw, '{')
	if start < 0 {
		return "", false
	}
	depth := 0
	inStr := false
	escaped := false
	for i := start; i < len(raw); i++ {
		c := raw[i]
		switch {
		case escaped:
			escaped = false
		case inStr && c == '\\':
			escaped = true
		case c == '"':
			inStr = !inStr
		case inStr:
			// nothing
		case c == '{':
			depth++
		case c == '}':
			depth--
			if depth == 0 {
				return raw[start : i+1], true
			}
		}
	}
	return "", false
}

// sanitizeLine makes model-authored text safe to display: ANSI escapes and
// control characters are stripped, newlines collapse to single spaces, runs of
// whitespace collapse, and the result is capped at max runes with an ellipsis.
// It is DISPLAY sanitation only — the text still must never reach send-keys.
func sanitizeLine(s string, max int) string {
	s = ansiEscapeRe.ReplaceAllString(s, "")
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		switch {
		case r == '\n' || r == '\t':
			b.WriteRune(' ')
		case r < 0x20 || r == 0x7f || (r >= 0x80 && r <= 0x9f):
			// C0 controls, DEL, C1 controls: drop.
		default:
			b.WriteRune(r)
		}
	}
	out := strings.Join(strings.Fields(b.String()), " ")
	runes := []rune(out)
	if len(runes) > max {
		out = string(runes[:max-1]) + "…"
	}
	return out
}
