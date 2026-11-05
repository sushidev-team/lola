package sshterm

import (
	"bytes"
	"fmt"
	"strings"
	"testing"
)

const nonce = "abc123"

func marker(kind, rest string) string { return "\x1b]6973;" + nonce + ";" + kind + ";" + rest + "\x07" }
func hexOf(s string) string           { return fmt.Sprintf("%x", s) }

type rec struct {
	log    []string
	output bytes.Buffer
}

func parser(r *rec) *Parser {
	return NewParser(nonce, Events{
		Start:  func(c string) { r.log = append(r.log, "start "+c) },
		Output: func(p []byte) { r.output.Write(p) },
		End: func(exit int, cwd string, command, full bool) {
			r.log = append(r.log, fmt.Sprintf("end %d %s %v %v", exit, cwd, command, full))
		},
	})
}

func TestParserInEveryChunking(t *testing.T) {
	stream := "motd\r\n" + marker("P", "0;"+hexOf("/home/m")) + "$ uptime\r\n" +
		marker("C", hexOf("uptime")) + "up 3 days\r\n" + marker("P", "3;"+hexOf("/srv")) + "$ "
	for size := 1; size <= len(stream); size++ {
		r := &rec{}
		p := parser(r)
		for i := 0; i < len(stream); i += size {
			p.Feed([]byte(stream[i:min(i+size, len(stream))]))
		}
		want := "end 0 /home/m false false|start uptime|end 3 /srv true false"
		if got := strings.Join(r.log, "|"); got != want {
			t.Fatalf("size %d: %q", size, got)
		}
		if r.output.String() != "up 3 days\r\n" {
			t.Fatalf("size %d: output %q", size, r.output.String())
		}
	}
}

func TestParserIgnoresAnotherNonce(t *testing.T) {
	r := &rec{}
	p := parser(r)
	p.Feed([]byte(marker("C", hexOf("x"))))
	p.Feed([]byte("\x1b]6973;forged;P;0;" + hexOf("/evil") + "\x07tail"))
	p.Feed([]byte(marker("P", "0;"+hexOf("/ok"))))
	if got := strings.Join(r.log, "|"); got != "start x|end 0 /ok true false" {
		t.Fatal(got)
	}
}

func TestParserDropsAFullScreenProgram(t *testing.T) {
	r := &rec{}
	p := parser(r)
	p.Feed([]byte(marker("C", hexOf("vim x")) + "\x1b[?1049hSECRET FILE" + marker("P", "0;"+hexOf("/"))))
	if strings.Contains(r.output.String(), "SECRET") {
		t.Fatalf("output %q", r.output.String())
	}
	if !strings.HasSuffix(r.log[1], "true true") {
		t.Fatal(r.log)
	}
}

func TestCwdFilterInEveryChunking(t *testing.T) {
	stream := "warn: a\n" + cwdPrefix(nonce) + "/var/log\n" + "after"
	for size := 1; size <= len(stream); size++ {
		var out bytes.Buffer
		f := NewCwdFilter(nonce, &out)
		for i := 0; i < len(stream); i += size {
			_, _ = f.Write([]byte(stream[i:min(i+size, len(stream))]))
		}
		if cwd := f.Close(); cwd != "/var/log" {
			t.Fatalf("size %d: cwd %q", size, cwd)
		}
		if out.String() != "warn: a\nafter" {
			t.Fatalf("size %d: %q", size, out.String())
		}
	}
}

func TestElevation(t *testing.T) {
	for cmd, want := range map[string][2]bool{
		"sudo systemctl restart api":  {true, false},
		"sudo -u postgres psql":       {true, false},
		"cd /x && sudo ls":            {true, false},
		"ls | xargs sudo rm":          {true, false},
		"sudo -i":                     {true, true},
		"sudo -s":                     {true, true},
		"sudo su":                     {true, true},
		"sudo su -":                   {true, true},
		"sudo bash":                   {true, true},
		"su":                          {true, true},
		"su - postgres":               {true, true},
		"doas -s":                     {true, true},
		"grep sudo /var/log/auth.log": {false, false},
		"ls /usr/bin/sudo":            {false, false},
		"echo pseudo":                 {false, false},
		"subl x":                      {false, false},
	} {
		if e, s := Elevated(cmd), OpensShell(cmd); e != want[0] || s != want[1] {
			t.Errorf("%q: elevated=%v shell=%v, want %v", cmd, e, s, want)
		}
	}
}
