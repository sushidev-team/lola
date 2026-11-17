package main

import (
	"strings"
	"testing"
)

// The setup help names the wizard's real steps. It used to promise a project
// step the wizard does not have, so a first run looked like it skipped one.
func TestSetupHelpMatchesTheWizard(t *testing.T) {
	c := setupCmd()
	for _, want := range []string{"Linear key", "agent", "theme", "poll interval", "add projects afterwards"} {
		if !strings.Contains(c.Short, want) {
			t.Errorf("Short %q does not mention %q", c.Short, want)
		}
	}
	if strings.Contains(c.Short, "Linear key, project") {
		t.Errorf("Short still promises a project step: %q", c.Short)
	}
	if !strings.Contains(c.Long, "does not add a project") {
		t.Errorf("Long does not say projects come afterwards: %q", c.Long)
	}
}
