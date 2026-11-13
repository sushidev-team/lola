package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/sushidev-team/lola/internal/taskboard"
)

func TestReportTaskHelpWithoutDaemon(t *testing.T) {
	t.Setenv("LOLA_HOME", t.TempDir())
	for _, flag := range []string{"help", "--help"} {
		cmd := reportCmd()
		var output bytes.Buffer
		cmd.SetOut(&output)
		cmd.SetArgs([]string{"task", flag})
		if err := cmd.Execute(); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(output.String(), taskboard.Briefing("lola")) {
			t.Fatal("task help omitted organization contract")
		}
	}
}
