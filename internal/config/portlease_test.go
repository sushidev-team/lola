package config

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestPortLeaseKeysRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	orig := &Config{Projects: []Project{{
		Name: "app", Path: "/tmp/app",
		PortRange: "4100-4199", PortsPerSession: 2,
		PreRemove: []string{`dropdb --if-exists "$LOLA_DB_NAME"`},
	}}}
	if err := orig.Save(path); err != nil {
		t.Fatal(err)
	}
	got, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	p := got.Projects[0]
	if p.PortRange != "4100-4199" || p.PortsPerSession != 2 || len(p.PreRemove) != 1 {
		t.Fatalf("round trip = %q %d %q", p.PortRange, p.PortsPerSession, p.PreRemove)
	}
	if lo, hi, n, ok := p.PortLease(); !ok || lo != 4100 || hi != 4199 || n != 2 {
		t.Errorf("PortLease = %d %d %d %v", lo, hi, n, ok)
	}
}

func TestPortLeaseDefaultsToOnePortAndIsOffWithoutARange(t *testing.T) {
	p := Project{PortRange: " 5000 - 5009 "}
	if lo, hi, n, ok := p.PortLease(); !ok || lo != 5000 || hi != 5009 || n != 1 {
		t.Errorf("PortLease = %d %d %d %v, want one port from 5000-5009", lo, hi, n, ok)
	}
	if _, _, _, ok := (&Project{}).PortLease(); ok {
		t.Error("no port_range must lease nothing")
	}
	if _, _, _, ok := (&Project{PortRange: "nope"}).PortLease(); ok {
		t.Error("a malformed port_range must lease nothing")
	}
}

func TestValidatePortLeaseAndPreRemove(t *testing.T) {
	cases := []struct {
		p    Project
		want string
	}{
		{Project{PortRange: "80-90"}, "1024-65535"},
		{Project{PortRange: "5000-4000"}, "lo <= hi"},
		{Project{PortRange: "5000"}, `"lo-hi"`},
		{Project{PortsPerSession: 2}, "needs a port_range"},
		{Project{PortRange: "5000-5001", PortsPerSession: 3}, "fewer than ports_per_session"},
		{Project{PortRange: "5000-5999", PortsPerSession: 21}, "ports_per_session must be"},
		{Project{PreRemove: []string{"ok", " "}}, "pre_remove[1] is empty"},
	}
	for _, tc := range cases {
		tc.p.Name, tc.p.Path = "app", "/tmp/app"
		err := (&Config{Projects: []Project{tc.p}}).Validate()
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%+v: err = %v, want it to mention %q", tc.p, err, tc.want)
		}
	}
	ok := &Config{Projects: []Project{{Name: "app", Path: "/tmp/app", PortRange: "4100-4199", PortsPerSession: 2}}}
	if err := ok.Validate(); err != nil && strings.Contains(err.Error(), "port") {
		t.Errorf("a valid lease config was rejected: %v", err)
	}
}
