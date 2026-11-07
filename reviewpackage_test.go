package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sushidev-team/lola/internal/protocol"
	"github.com/sushidev-team/lola/internal/reviewpack"
)

func TestReviewPackageCLIUsesSessionTransportAndReturnsReceipt(t *testing.T) {
	home, err := os.MkdirTemp("/tmp", "lola-review-cli-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(home) })
	t.Setenv("LOLA_HOME", home)
	t.Setenv("LOLA_SESSION", "worker")
	t.Setenv("LOLA_HOOK_ADDR", "")
	ln, err := net.Listen("unix", filepath.Join(home, "lola.sock"))
	if err != nil {
		t.Skipf("socket unavailable: %v", err)
	}
	defer ln.Close()
	received := make(chan protocol.Request, 1)
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		line, _ := bufio.NewReader(conn).ReadBytes('\n')
		var req protocol.Request
		json.Unmarshal(line, &req)
		received <- req
		json.NewEncoder(conn).Encode(protocol.Response{OK: true, Data: json.RawMessage(`{"id":"receipt"}`)})
	}()
	m := reviewpack.Manifest{Version: 1, Title: "Review", Body: "Explain the change", Base: strings.Repeat("a", 40), Head: strings.Repeat("b", 40)}
	raw, _ := json.Marshal(m)
	path := filepath.Join(home, "manifest.json")
	if err := os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	c := reviewPackageCmd()
	var out bytes.Buffer
	c.SetOut(&out)
	c.SetArgs([]string{"publish", path, "--app", "web"})
	if err := c.Execute(); err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(out.String()) != `{"id":"receipt"}` {
		t.Fatalf("receipt: %s", out.String())
	}
	req := <-received
	var args protocol.ReviewPublishArgs
	json.Unmarshal(req.Args, &args)
	var sent reviewpack.Manifest
	json.Unmarshal(args.Manifest, &sent)
	if req.Cmd != protocol.CmdReviewPublish || req.Session != "worker" || args.App != "web" || sent.Head != m.Head {
		t.Fatalf("request: %+v %+v", req, args)
	}
}

func TestReviewPackageCLIRejectsInvalidManifestBeforeSending(t *testing.T) {
	path := filepath.Join(t.TempDir(), "manifest.json")
	os.WriteFile(path, []byte(`{"version":9}`), 0600)
	c := reviewPackageCmd()
	c.SilenceErrors = true
	c.SilenceUsage = true
	c.SetArgs([]string{"publish", path})
	if err := c.Execute(); err == nil || !strings.Contains(err.Error(), "version 1") {
		t.Fatalf("unexpected result: %v", err)
	}
}
