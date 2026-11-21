package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestPrintDevNamesTheAddressesOnlyWhileRunning(t *testing.T) {
	var b bytes.Buffer
	if err := printDev(&b, false, []string{"http://localhost:3000"}); err != nil || strings.Contains(b.String(), "3000") {
		t.Fatalf("a stopped server printed an address: %q, %v", b.String(), err)
	}
	b.Reset()
	_ = printDev(&b, true, []string{"http://localhost:3000", "http://localhost:5173"})
	if got := b.String(); !strings.Contains(got, "running\nhttp://localhost:3000\nhttp://localhost:5173\n") {
		t.Fatalf("addresses = %q", got)
	}
}

func TestDevCommandNeedsALolaSession(t *testing.T) {
	t.Setenv("LOLA_SESSION", "")
	c := devCmd()
	c.SetArgs([]string{"status"})
	c.SilenceUsage, c.SilenceErrors = true, true
	if err := c.Execute(); err == nil {
		t.Fatal("status outside a lola session succeeded")
	}
}
