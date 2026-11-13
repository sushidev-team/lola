package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sushidev-team/lola/internal/protocol"
)

func noTTY() (uintptr, bool) { return 0, false }

func TestHubJoinTokenSources(t *testing.T) {
	var errb bytes.Buffer
	tok, err := hubJoinToken([]string{"https://hub"}, "", strings.NewReader("tok-123\n"), &errb, noTTY)
	if err != nil || tok != "tok-123" || errb.Len() != 0 {
		t.Fatalf("stdin: %q %v %q", tok, err, errb.String())
	}

	f := filepath.Join(t.TempDir(), "tok")
	if err := os.WriteFile(f, []byte("  tok-file\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if tok, err := hubJoinToken([]string{"https://hub"}, f, strings.NewReader("ignored"), &errb, noTTY); err != nil || tok != "tok-file" {
		t.Fatalf("file: %q %v", tok, err)
	}
	if tok, err := hubJoinToken([]string{"https://hub"}, "-", strings.NewReader("tok-dash"), &errb, noTTY); err != nil || tok != "tok-dash" {
		t.Fatalf("dash: %q %v", tok, err)
	}

	errb.Reset()
	tok, err = hubJoinToken([]string{"https://hub", "tok-argv"}, "", strings.NewReader(""), &errb, noTTY)
	if err != nil || tok != "tok-argv" || !strings.Contains(errb.String(), "visible in ps") {
		t.Fatalf("argv: %q %v %q", tok, err, errb.String())
	}

	for name, in := range map[string]string{"empty": "", "spaces": "a b", "two lines": "a\nb"} {
		if _, err := hubJoinToken([]string{"https://hub"}, "", strings.NewReader(in), &errb, noTTY); err == nil {
			t.Errorf("%s: want an error", name)
		}
	}
	if _, err := hubJoinToken([]string{"https://hub", "x"}, f, strings.NewReader(""), &errb, noTTY); err == nil {
		t.Error("argument plus --token-file must be refused")
	}
}

func TestHubCLIUsesTheDaemon(t *testing.T) {
	var sent []protocol.Request
	reply := map[string]protocol.Response{}
	old := hubSend
	t.Cleanup(func() { hubSend = old })
	hubSend = func(req protocol.Request) (*protocol.Response, error) {
		sent = append(sent, req)
		r, ok := reply[req.Cmd]
		if !ok {
			return nil, errors.New("dial unix: no such file")
		}
		return &r, nil
	}
	data := func(v any) json.RawMessage { b, _ := json.Marshal(v); return b }

	reply[protocol.CmdHubClaim] = protocol.Response{OK: true, Data: data(protocol.HubClaimData{Session: "lola-web-x", Project: "web", ItemID: "it1"})}
	reply[protocol.CmdHubBoard] = protocol.Response{OK: true, Data: data(protocol.HubBoardData{Items: []protocol.HubBoardItem{
		{ID: "it1", State: "open", Label: "GHSA-x · lodash", Repo: "acme/web", Local: protocol.HubLocalInfo{Claimable: true, Projects: []string{"a", "b"}}},
	}})}
	reply[protocol.CmdHubRelease] = protocol.Response{OK: false, Error: "hub: not joined"}

	run := func(args ...string) (string, error) {
		cmd := hubCmd()
		var out bytes.Buffer
		cmd.SetOut(&out)
		cmd.SetErr(&out)
		cmd.SetArgs(args)
		err := cmd.Execute()
		return out.String(), err
	}
	if out, err := run("claim", "it1", "--project", "web"); err != nil || !strings.Contains(out, "session lola-web-x") {
		t.Fatalf("claim: %q %v", out, err)
	}
	var ca protocol.HubClaimArgs
	if err := json.Unmarshal(sent[len(sent)-1].Args, &ca); err != nil || ca.ItemID != "it1" || ca.Project != "web" {
		t.Fatalf("claim args = %+v %v", ca, err)
	}
	if out, err := run("board"); err != nil || !strings.Contains(out, "--project a|b") {
		t.Fatalf("board: %q %v", out, err)
	}
	if _, err := run("release", "it1"); err == nil || !strings.Contains(err.Error(), "not joined") {
		t.Fatalf("release: %v", err)
	}
	if _, err := run("sync"); err == nil || !strings.Contains(err.Error(), "not running") {
		t.Fatalf("sync without a daemon: %v", err)
	}
}
