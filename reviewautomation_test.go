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
)

func TestReviewAutomationUsesOneLocalRequest(t *testing.T) {
	for _, tc := range []struct {
		args    []string
		command string
	}{
		{[]string{"list", "worker"}, protocol.CmdReviewPackages},
		{[]string{"checks", "worker", "package"}, protocol.CmdReviewChecks},
		{[]string{"mark", "worker", "package", "--previous", "old"}, protocol.CmdReviewMark},
		{[]string{"feedback", "worker", "package", "--section", "Settings"}, protocol.CmdReviewFeedback},
		{[]string{"recover", "worker", "package", "request"}, protocol.CmdReviewRecover},
		{[]string{"verify", "worker", "package", "--request-id", strings.Repeat("1", 32), "--command", "npm test", "--image", "sha256:" + strings.Repeat("a", 64)}, protocol.CmdReviewVerify},
	} {
		t.Run(tc.args[0], func(t *testing.T) {
			home, err := os.MkdirTemp("/tmp", "lola-review-api-")
			if err != nil {
				t.Fatal(err)
			}
			defer os.RemoveAll(home)
			t.Setenv("LOLA_HOME", home)
			t.Setenv("LOLA_HOOK_ADDR", "invalid-agent-bridge")
			ln, err := net.Listen("unix", filepath.Join(home, "lola.sock"))
			if err != nil {
				t.Skip(err)
			}
			defer ln.Close()
			received := make(chan protocol.Request, 1)
			go func() {
				conn, err := ln.Accept()
				if err != nil {
					return
				}
				defer conn.Close()
				raw, _ := bufio.NewReader(conn).ReadBytes('\n')
				var req protocol.Request
				json.Unmarshal(raw, &req)
				received <- req
				json.NewEncoder(conn).Encode(protocol.Response{OK: true, Data: json.RawMessage(`{"accepted":true}`)})
			}()
			cmd := reviewPackageCmd()
			var out bytes.Buffer
			cmd.SetOut(&out)
			cmd.SetIn(strings.NewReader("Check keyboard focus."))
			cmd.SetArgs(tc.args)
			if err := cmd.Execute(); err != nil {
				t.Fatal(err)
			}
			req := <-received
			if req.Cmd != tc.command || req.Session != "worker" || req.HookToken != "" || strings.TrimSpace(out.String()) != `{"accepted":true}` {
				t.Fatalf("request %+v output %s", req, out.String())
			}
			if tc.command == protocol.CmdReviewFeedback {
				var a protocol.ReviewFeedbackArgs
				json.Unmarshal(req.Args, &a)
				if a.Package != "package" || a.Section != "Settings" || a.Note != "Check keyboard focus." {
					t.Fatalf("lost feedback context %+v", a)
				}
			}
		})
	}
}
