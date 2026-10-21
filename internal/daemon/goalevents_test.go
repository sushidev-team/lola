package daemon

import (
	"encoding/json"
	"github.com/sushidev-team/lola/internal/agentoutput"
	"strings"
	"time"
)

func fixtureAgentEvents(args []string, body []byte) []byte {
	kind := "codex"
	inner := args[len(args)-1]
	if strings.Contains(inner, "'claude'") {
		kind = "claude"
	} else if strings.Contains(inner, "'opencode'") {
		kind = "opencode"
	}
	var events []any
	switch kind {
	case "claude":
		events = []any{map[string]any{"type": "result", "subtype": "success", "is_error": false, "result": string(body), "usage": map[string]int{"input_tokens": 100, "output_tokens": 20}, "total_cost_usd": 0.001}}
	case "codex":
		events = []any{map[string]any{"type": "item.completed", "item": map[string]string{"type": "agent_message", "text": string(body)}}, map[string]any{"type": "turn.completed", "usage": map[string]int{"input_tokens": 100, "cached_input_tokens": 20, "output_tokens": 20}}}
	case "opencode":
		events = []any{map[string]any{"type": "text", "part": map[string]string{"type": "text", "id": "t1", "messageID": "m1", "text": string(body)}}, map[string]any{"type": "step_finish", "part": map[string]any{"type": "step-finish", "messageID": "m1", "id": "s1", "reason": "stop", "cost": 0.001, "tokens": map[string]int{"input": 100, "output": 20}}}}
	}
	var out []byte
	for _, e := range events {
		b, _ := json.Marshal(e)
		out = append(out, append(b, '\n')...)
	}
	return out
}

func fixtureAgentText(args []string, body []byte) []byte {
	kind := "codex"
	inner := args[len(args)-1]
	if strings.Contains(inner, "'claude'") {
		kind = "claude"
	} else if strings.Contains(inner, "'opencode'") {
		kind = "opencode"
	}
	text, _, _ := agentoutput.Decode(kind, body, time.Now())
	return text
}
