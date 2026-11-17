package agent

import "testing"

func TestSettingCommand(t *testing.T) {
	for _, tt := range []struct {
		kind                 Kind
		setting, value, want string
	}{
		{Claude, "model", "opus", "/model opus"},
		{Claude, "effort", "auto", "/effort auto"},
		{Claude, "effort", "max", "/effort max"},
		{Codex, "model", "gpt-6.1-sol", "/model gpt-6.1-sol"},
		{Codex, "effort", "ultra", "/model --reasoning=ultra"},
		{Codex, "speed", "fast", "/model --speed=fast"},
		{OpenCode, "model", "picker", "/models"},
		{OpenCode, "variant", "picker", "/variants"},
	} {
		got, err := SettingCommand(tt.kind, tt.setting, tt.value)
		if err != nil || got != tt.want {
			t.Errorf("%+v: got %q, %v", tt, got, err)
		}
	}
	for _, tt := range []struct {
		kind           Kind
		setting, value string
	}{
		{Claude, "model", ""}, {Claude, "model", "opus\n/clear"},
		{Claude, "model", "--help"}, {Claude, "effort", "ultra"},
		{Claude, "speed", "fast"}, {Codex, "effort", "auto"},
		{OpenCode, "model", "openai/gpt-6.1-sol"}, {OpenCode, "variant", "high"},
		{Kind("unknown"), "model", "opus"}, {Claude, "permissions", "auto"},
	} {
		if got, err := SettingCommand(tt.kind, tt.setting, tt.value); err == nil || got != "" {
			t.Errorf("accepted %+v: %q", tt, got)
		}
	}
}
