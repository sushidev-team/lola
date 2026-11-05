package sshconn

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestConfigAliases(t *testing.T) {
	home := t.TempDir()
	write := func(name, body string) {
		t.Helper()
		path := filepath.Join(home, ".ssh", name)
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
	}
	write("config", `# comment
Host prod prod-alt *.internal !excluded
  HostName 10.0.0.1
  IdentityFile ~/.ssh/secret
Include "parts/*.conf"
Include "with space"
Host = "dev" # comment
Match exec "touch must-never-run"
Include ~/other-missing
`)
	write("parts/a.conf", "hOsT stage prod\nInclude config\n")
	write("parts/b.conf", "Host \"quoted\" -oBad user@host\n")
	write("with space", "Host extra\n")
	got, err := ConfigAliases(home)
	want := []string{"dev", "extra", "prod", "prod-alt", "quoted", "stage"}
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, %v; want %v", got, err, want)
	}
}

func TestConfigAliasesMissingAndMalformed(t *testing.T) {
	home := t.TempDir()
	if got, err := ConfigAliases(home); err != nil || len(got) != 0 {
		t.Fatalf("missing: %v %v", got, err)
	}
	if err := os.Mkdir(filepath.Join(home, ".ssh"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".ssh", "config"), []byte("Include \"unfinished\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := ConfigAliases(home); err == nil {
		t.Fatal("malformed include was silently ignored")
	}
}

func TestCustomTargetArguments(t *testing.T) {
	target := Target{Sock: "/tmp/socket", Alias: "2001:db8::1", User: "deploy", Port: 2222, IdentityFile: "/tmp/key with spaces"}
	if err := target.Valid(); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{MasterArgs(target, 60), ReopenArgs(target), CommandArgs(target, "true")} {
		for _, pair := range [][2]string{{"-p", "2222"}, {"-l", "deploy"}, {"-i", "/tmp/key with spaces"}} {
			found := false
			for i := 0; i+1 < len(args); i++ {
				if args[i] == pair[0] && args[i+1] == pair[1] {
					found = true
				}
			}
			if !found {
				t.Fatalf("missing %v in %v", pair, args)
			}
		}
	}
	for _, bad := range []Target{{Alias: "x", Port: 65536}, {Alias: "x", Port: -1}, {Alias: "x", IdentityFile: "-oProxyCommand=x"}, {Alias: "x", IdentityFile: "/tmp/key\n"}, {Alias: "x;cmd"}} {
		if err := bad.Valid(); err == nil {
			t.Errorf("accepted %+v", bad)
		}
	}
}
