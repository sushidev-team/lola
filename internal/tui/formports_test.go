package tui

import (
	"slices"
	"strings"
	"testing"

	"github.com/sushidev-team/lola/internal/config"
)

// The port lease and the teardown hook round-trip through the project form.
func TestFormSavesPortLeaseAndPreRemove(t *testing.T) {
	f, path := newFormOn(t, []config.Project{
		{Name: "web", Path: "/tmp/web", Repo: "acme/web", PortRange: "4100-4199", PortsPerSession: 2},
	}, "web")
	if f.portsBuf != "2" {
		t.Fatalf("portsBuf = %q, want the saved 2", f.portsBuf)
	}
	if !slices.Contains(f.fields(), fPortRange) || !slices.Contains(f.fields(), fPreRemove) {
		t.Fatalf("Repo tab is missing the new fields: %v", f.fields())
	}

	f.poll.PortRange = " 5000-5099 "
	f.portsBuf = "3"
	f.preRemove = []string{"dropdb --if-exists \"$LOLA_DB_NAME\"", "  "}
	if _, ev := f.save(); ev != formSaved {
		t.Fatalf("save failed: %v", f.errs)
	}
	got, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	p := got.ProjectByName("web")
	if p.PortRange != "5000-5099" || p.PortsPerSession != 3 {
		t.Errorf("saved port_range %q ports_per_session %d", p.PortRange, p.PortsPerSession)
	}
	if !slices.Equal(p.PreRemove, []string{"dropdb --if-exists \"$LOLA_DB_NAME\""}) {
		t.Errorf("saved pre_remove %q", p.PreRemove)
	}
}

func TestFormRejectsAMalformedPortRange(t *testing.T) {
	f, _ := newFormOn(t, []config.Project{{Name: "web", Path: "/tmp/web"}}, "web")
	f.poll.PortRange = "80-90"
	if _, ev := f.save(); ev == formSaved {
		t.Fatal("a privileged port range must not save")
	}
	if !strings.Contains(strings.Join(f.errs, " "), "port_range") {
		t.Errorf("errors do not name port_range: %v", f.errs)
	}
}
