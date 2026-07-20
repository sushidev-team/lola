// Shared field-editing helpers for the config forms: the one-entry-per-line
// list/env conversions and the per-project agent cycle order. Extracted from
// the old standalone project editor when it merged into the tabbed project form
// (form.go), so the settings editor can reuse them too.
package tui

import (
	"sort"
	"strings"

	"github.com/sushidev-team/lola/internal/agent"
)

// projAgentOptions is the per-project override picker's cycle order: an empty
// value (inherit [defaults].agent) followed by each concrete kind, so a project
// can inherit the global default or pin its own agent.
func projAgentOptions() []string {
	out := make([]string, 0, len(agent.Kinds)+1)
	out = append(out, "") // inherit the global default
	for _, k := range agent.Kinds {
		out = append(out, k.String())
	}
	return out
}

// envLines renders an env map as sorted "KEY=value" lines, so the editor's
// line order is stable across opens (Go map iteration is not).
func envLines(env map[string]string) []string {
	lines := make([]string, 0, len(env))
	for k, v := range env {
		lines = append(lines, k+"="+v)
	}
	sort.Strings(lines)
	return lines
}

// trimDropEmpty trims each entry and drops blanks — nil when nothing remains.
func trimDropEmpty(in []string) []string {
	var out []string
	for _, e := range in {
		if t := strings.TrimSpace(e); t != "" {
			out = append(out, t)
		}
	}
	return out
}

// parseEnvLines turns "KEY=value" entries into a map (later keys win); nil when
// empty. An entry without '=' is ignored.
func parseEnvLines(lines []string) map[string]string {
	out := map[string]string{}
	for _, l := range lines {
		k, v, ok := strings.Cut(l, "=")
		k = strings.TrimSpace(k)
		if !ok || k == "" {
			continue
		}
		out[k] = strings.TrimSpace(v)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// dropLastRune removes the final rune, for backspace in an inline editor.
func dropLastRune(s string) string {
	if r := []rune(s); len(r) > 0 {
		return string(r[:len(r)-1])
	}
	return s
}
