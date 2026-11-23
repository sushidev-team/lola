package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/sushidev-team/lola/internal/protocol"
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

func TestSandboxedDevStatusAndStopRefuseInsteadOfTouchingTheHost(t *testing.T) {
	t.Setenv("LOLA_SANDBOX", "docker")
	if _, _, err := devStatus(); err == nil || !strings.Contains(err.Error(), "sandboxed") {
		t.Fatalf("status err = %v", err)
	}
	cmd := devCmd()
	cmd.SetArgs([]string{"stop"})
	cmd.SilenceUsage, cmd.SilenceErrors = true, true
	if err := cmd.Execute(); err == nil || !strings.Contains(err.Error(), "sandboxed") {
		t.Fatalf("stop err = %v", err)
	}
}

func TestAgentLoginProjectRespectsSessionAndMember(t *testing.T) {
	s := protocol.SessionInfo{Project: "web"}
	if p, e := loginProject(s, ""); e != nil || p != "web" {
		t.Fatal(p, e)
	}
	if _, e := loginProject(s, "other"); e == nil {
		t.Fatal("unrelated project")
	}
	s.Stack = true
	s.Apps = []protocol.SessionApp{{Name: "api"}}
	if _, e := loginProject(s, ""); e == nil {
		t.Fatal("implicit stack member")
	}
	if p, e := loginProject(s, "api"); e != nil || p != "api" {
		t.Fatal(p, e)
	}
	s.Sandbox = "docker"
	if _, e := loginProject(s, "api"); e == nil {
		t.Fatal("sandbox accepted")
	}
}
