package agent

import "fmt"

// SettingCommand builds the CLI's model controls. OpenCode owns its native
// model/variant picker; callers must reveal it after delivery. Every caller
// must prove an idle, empty composer before sending a command.
// Contracts: Claude Code model-config docs; Codex 0.167.0's model_command.rs;
// OpenCode 1.18.22's model.list / variant.list commands in packages/tui/src/app.tsx.
func SettingCommand(k Kind, setting, value string) (string, error) {
	if k == OpenCode {
		if value == "picker" {
			switch setting {
			case "model":
				return "/models", nil
			case "variant":
				return "/variants", nil
			}
		}
		return "", fmt.Errorf("choose OpenCode models and variants in its native picker")
	}
	if k != Claude && k != Codex {
		return "", fmt.Errorf("unsupported agent")
	}
	switch setting {
	case "model":
		if ValidModel(value) {
			return "/model " + value, nil
		}
	case "effort":
		if k == Codex {
			switch value {
			case "none", "minimal", "low", "medium", "high", "xhigh", "max", "ultra":
				return "/model --reasoning=" + value, nil
			}
			break
		}
		switch value {
		case "auto", "low", "medium", "high", "xhigh", "max":
			return "/effort " + value, nil
		}
	case "speed":
		if k == Codex && (value == "standard" || value == "fast" || value == "flex") {
			return "/model --speed=" + value, nil
		}
	}
	return "", fmt.Errorf("invalid model setting")
}
