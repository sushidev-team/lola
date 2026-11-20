package lolaenv

import (
	"regexp"
	"strings"
)

var (
	portVariable    = regexp.MustCompile(`\$(?:LOLA_PORT(?:_[0-9]+)?\b|\{LOLA_PORT(?:_[0-9]+)?\})`)
	portPlaceholder = regexp.MustCompile(`\{\{\.Port([0-9]*)\}\}([a-zA-Z0-9_]?)`)
)

// PortTemplates converts only leased port references to Lola's original env
// placeholders. No other shell variables or shell expressions are evaluated.
func PortTemplates(value string) string {
	return portVariable.ReplaceAllStringFunc(value, func(ref string) string {
		name := strings.Trim(ref, "${}")
		index := strings.TrimPrefix(strings.TrimPrefix(name, "LOLA_PORT"), "_")
		return "{{.Port" + index + "}}"
	})
}

// DisplayPortRefs uses the same port names as dev commands. Delimit a name
// when the next character would otherwise become part of the variable.
func DisplayPortRefs(value string) string {
	return portPlaceholder.ReplaceAllStringFunc(value, func(ref string) string {
		index, suffix, _ := strings.Cut(strings.TrimPrefix(ref, "{{.Port"), "}}")
		name := "LOLA_PORT"
		if index != "" {
			name += "_" + index
		}
		if suffix != "" {
			return "${" + name + "}" + suffix
		}
		return "$" + name
	})
}
