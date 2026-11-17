package agent

import "strconv"

func ValidEffort(kind Kind, effort string) bool {
	if effort == "" {
		return true
	}
	switch Parse(string(kind)) {
	case Claude:
		return effort == "low" || effort == "medium" || effort == "high" || effort == "max"
	case Codex:
		return effort == "minimal" || effort == "low" || effort == "medium" || effort == "high" || effort == "xhigh"
	case OpenCode:
		return ValidModel(effort) // provider-specific variant name
	}
	return false
}

// EffortArgs is prepended before the positional prompt, never shell-interpolated.
func EffortArgs(kind Kind, effort string) []string {
	if effort == "" || !ValidEffort(kind, effort) {
		return nil
	}
	switch Parse(string(kind)) {
	case Codex:
		return []string{"-c", "model_reasoning_effort=" + strconv.Quote(effort)}
	case OpenCode:
		return []string{"--variant", effort}
	default:
		return []string{"--effort", effort}
	}
}
