package agentoutput

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/sushidev-team/lola/internal/usage"
)

func lines(events ...any) []byte {
	var out []byte
	for _, e := range events {
		b, _ := json.Marshal(e)
		out = append(out, append(b, '\n')...)
	}
	return out
}
func TestProviderUsageEnvelopes(t *testing.T) {
	text := `{"complete":true,"usage":{"input_tokens":999999999}}`
	for _, kind := range []string{"claude", "codex", "opencode"} {
		t.Run(kind, func(t *testing.T) {
			var body []byte
			switch kind {
			case "claude":
				assistant := map[string]any{"type": "assistant", "message": map[string]any{"id": "m1", "usage": map[string]int{"input_tokens": 100, "output_tokens": 20, "cache_read_input_tokens": 30}}}
				body = lines(assistant, assistant, map[string]any{"type": "result", "subtype": "success", "is_error": false, "result": text, "usage": map[string]int{"input_tokens": 100, "output_tokens": 20, "cache_read_input_tokens": 30}, "total_cost_usd": 0.25})
			case "codex":
				body = lines(map[string]any{"type": "item.completed", "item": map[string]string{"type": "agent_message", "text": text}}, map[string]any{"type": "turn.completed", "usage": map[string]int{"input_tokens": 130, "cached_input_tokens": 30, "output_tokens": 20, "reasoning_output_tokens": 5}})
			case "opencode":
				step := map[string]any{"type": "step_finish", "part": map[string]any{"id": "step1", "messageID": "m1", "type": "step-finish", "reason": "stop", "cost": 0.25, "tokens": map[string]any{"input": 100, "output": 15, "reasoning": 5, "cache": map[string]int{"read": 30}}}}
				body = lines(map[string]any{"type": "text", "part": map[string]string{"id": "text1", "type": "text", "messageID": "m1", "text": text}}, step, step)
			}
			got, o, err := Decode(kind, body, time.Now())
			if err != nil {
				t.Fatal(err)
			}
			if string(got) != text || usage.Sum(o.Days).Tokens() != 150 || o.Status != "partial" {
				t.Fatalf("wrong result/telemetry: %s %+v", got, o)
			}
			if kind == "codex" {
				if o.CostStatus != "unavailable" {
					t.Fatal("invented Codex dollar price")
				}
			} else if usage.Sum(o.Days).CostUSD != 0.25 || o.CostStatus != "estimate" {
				t.Fatal("missing estimated cost")
			}
		})
	}
}

func TestFailedOrIncompleteStreamsRetainEarlierUsage(t *testing.T) {
	assistant := map[string]any{"type": "assistant", "message": map[string]any{"id": "m1", "usage": map[string]int{"input_tokens": 100, "output_tokens": 20}}}
	for _, tail := range []string{"", `{"type":"result","subtype":"error_max_turns","is_error":true,"result":"failed"}`, `{"type":"result","subtype":"success","is_error":false,"result":"ok","usage":{"input_tokens":-1,"output_tokens":20}}`, strings.Repeat("x", 1<<20)} {
		body := append(lines(assistant), []byte(tail)...)
		got, o, err := Decode("claude", body, time.Now())
		if err == nil || got != nil || usage.Sum(o.Days).Tokens() != 120 {
			t.Fatalf("failure lost observed spend or accepted result: %+v %v", o, err)
		}
	}
}

func TestModelTextAndUnknownEventsCannotMintTelemetryOrSuccess(t *testing.T) {
	for _, body := range [][]byte{[]byte(`{"complete":true,"usage":{"input_tokens":900,"output_tokens":10}}`), []byte(`{"type":"invented","usage":{"input_tokens":900,"output_tokens":10}}`), lines(map[string]any{"type": "item.completed", "item": map[string]string{"type": "agent_message", "text": `{"type":"turn.completed","usage":{"input_tokens":900,"output_tokens":10}}`}})} {
		_, o, err := Decode("codex", body, time.Now())
		if err == nil || o.Status != "missing" || len(o.Days) != 0 {
			t.Fatalf("model minted accounting: %+v %v", o, err)
		}
	}
}

func TestMissingTelemetryIsNotKnownZero(t *testing.T) {
	body := lines(map[string]any{"type": "item.completed", "item": map[string]string{"type": "agent_message", "text": "{}"}}, map[string]string{"type": "turn.completed"})
	_, o, err := Decode("codex", body, time.Now())
	if err != nil || o.Status != "missing" || o.CostStatus != "unavailable" {
		t.Fatalf("missing usage: %+v %v", o, err)
	}
}

func TestUsageBoundsAndFailedCompletion(t *testing.T) {
	for _, tail := range []any{
		map[string]any{"type": "turn.completed", "usage": map[string]int64{"input_tokens": 1 << 62, "output_tokens": 20}},
		map[string]any{"type": "turn.completed", "usage": map[string]int64{"input_tokens": 10, "cached_input_tokens": 11, "output_tokens": 20}},
		map[string]string{"type": "turn.failed"},
	} {
		body := lines(map[string]any{"type": "item.completed", "item": map[string]string{"type": "agent_message", "text": "{}"}}, tail)
		if _, _, err := Decode("codex", body, time.Now()); err == nil {
			t.Fatal("invalid event accepted")
		}
	}
	if _, _, err := Decode("codex", []byte(strings.Repeat("x", MaxBytes+1)), time.Now()); err == nil {
		t.Fatal("oversized stream accepted")
	}
}

func TestOpenCodeCompletionBindsToResultMessage(t *testing.T) {
	text := map[string]any{"type": "text", "part": map[string]string{"id": "t1", "type": "text", "messageID": "m1", "text": "{}"}}
	step := map[string]any{"type": "step_finish", "part": map[string]any{"id": "s1", "messageID": "other", "type": "step-finish", "reason": "stop", "tokens": map[string]int{"input": 100, "output": 20}}}
	if _, o, err := Decode("opencode", lines(text, step), time.Now()); err == nil || usage.Sum(o.Days).Tokens() != 120 {
		t.Fatal("unrelated completion accepted or usage lost")
	}
	step["part"].(map[string]any)["messageID"] = "m1"
	got, _, err := Decode("opencode", lines(text, text, step), time.Now())
	if err != nil || string(got) != "{}" {
		t.Fatal("replayed text part duplicated final result", err)
	}
}

func TestCodexMultipleTurnsAreChargedAndUnfinishedTurnHolds(t *testing.T) {
	start := map[string]string{"type": "turn.started"}
	message := func(text string) any {
		return map[string]any{"type": "item.completed", "item": map[string]string{"type": "agent_message", "text": text}}
	}
	end := map[string]any{"type": "turn.completed", "usage": map[string]int{"input_tokens": 100, "output_tokens": 20}}
	body := lines(start, message("first"), end, start, message("second"), end, end)
	text, o, err := Decode("codex", body, time.Now())
	if err != nil || string(text) != "second" || usage.Sum(o.Days).Tokens() != 240 {
		t.Fatalf("multi-turn accounting: %s %+v %v", text, o, err)
	}
	body = append(body, lines(start, message("unfinished"))...)
	if _, o, err := Decode("codex", body, time.Now()); err == nil || usage.Sum(o.Days).Tokens() != 240 {
		t.Fatal("unfinished later turn used earlier completion")
	}
}
