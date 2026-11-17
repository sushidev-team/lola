package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSSHCommandText(t *testing.T) {
	for _, c := range []struct {
		words []string
		want  string
	}{
		{[]string{"docker", "ps"}, "docker ps"},
		{[]string{"ps aux | grep nginx"}, "ps aux | grep nginx"},
		{[]string{"docker", "ps", "--format", "table {{.Names}}\t{{.Image}}"}, "docker ps --format 'table {{.Names}}\t{{.Image}}'"},
		{[]string{"echo", "it's"}, `echo 'it'\''s'`},
		{[]string{"echo", ""}, "echo ''"},
		{[]string{"ls", ";", "pwd"}, "ls ';' pwd"},
		{[]string{"ls", "/var/log", "--color=auto"}, "ls /var/log --color=auto"},
	} {
		if got := sshCommandText(c.words); got != c.want {
			t.Errorf("%q: got %q, want %q", c.words, got, c.want)
		}
	}
}

func TestPostFilesAreReadOnTheAgentsSide(t *testing.T) {
	p := filepath.Join(t.TempDir(), "w.html")
	if err := os.WriteFile(p, []byte("<b>hi</b>"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := sshExpandPostFiles([]string{"widget", "T", "--file", p, "--ref", "d"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(got, "|") != "widget|T|--html|<b>hi</b>|--ref|d" {
		t.Fatal(got)
	}
	if _, err := sshExpandPostFiles([]string{"widget", "--file", "/no/such"}); err == nil {
		t.Fatal("a missing file passed")
	}
}
