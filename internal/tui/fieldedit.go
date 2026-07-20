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

// ---- paste ----------------------------------------------------------------
//
// bubbletea v2 delivers a bracketed paste as a separate tea.PasteMsg that the
// key encoder never sees, so a form that only handles tea.KeyPressMsg silently
// ignores pasting. Every text field routes its PasteMsg through the helpers
// below; see rootModel.Update for the dispatch.

// sanitizePasteLine strips control characters from one pasted line. Pasted text
// is arbitrary clipboard content and these fields end up in config.toml (and,
// for env, in a shell-sourced file), so a stray escape sequence or NUL must
// never reach them. Tabs become spaces; everything else below 0x20 and DEL goes.
func sanitizePasteLine(s string) string {
	return strings.Map(func(r rune) rune {
		switch {
		case r == '\t':
			return ' '
		case r < 0x20 || r == 0x7f:
			return -1
		}
		return r
	}, s)
}

// pasteLines splits pasted text into sanitized lines, dropping trailing blanks.
// Used by the one-entry-per-line sub-editors, where a multi-line paste is the
// whole point (several symlinks or env vars at once).
func pasteLines(s string) []string {
	raw := strings.Split(strings.ReplaceAll(s, "\r\n", "\n"), "\n")
	out := make([]string, 0, len(raw))
	for _, l := range raw {
		out = append(out, sanitizePasteLine(l))
	}
	for len(out) > 0 && strings.TrimSpace(out[len(out)-1]) == "" {
		out = out[:len(out)-1]
	}
	return out
}

// pasteInline reduces pasted text to what a single-line field can hold: the
// first non-blank line, sanitized and trimmed. Copying a path out of a terminal
// usually carries a trailing newline, which this drops.
func pasteInline(s string) string {
	for _, l := range pasteLines(s) {
		if t := strings.TrimSpace(sanitizePasteLine(l)); t != "" {
			return t
		}
	}
	return ""
}

// pasteDigits keeps only the digits of a paste, for the integer fields.
func pasteDigits(s string) string {
	var b strings.Builder
	for _, r := range pasteInline(s) {
		if r >= '0' && r <= '9' {
			b.WriteRune(r)
		}
	}
	return b.String()
}
