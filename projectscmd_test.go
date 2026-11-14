package main

import "testing"

func TestProjectsBulkRejectsSetAndClear(t *testing.T) {
	for _, args := range [][]string{
		{"bulk", "--blocked-label", "B", "--clear-blocked-label"},
		{"bulk", "--on-sent-label", "S", "--clear-on-sent-label"},
		{"bulk", "--match-label", "m", "--clear-match-labels"},
	} {
		c := projectsCmd()
		c.SetArgs(args)
		c.SilenceUsage, c.SilenceErrors = true, true
		if err := c.Execute(); err == nil {
			t.Errorf("%v accepted", args)
		}
	}
}
