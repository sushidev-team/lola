package hook

import (
	"bufio"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"testing"

	"github.com/sushidev-team/lola/internal/protocol"
)

func TestTokenIsPerSessionAndVerifies(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state", "hook-secret")
	secret, err := LoadOrCreateSecret(path)
	if err != nil {
		t.Fatal(err)
	}
	if fi, err := os.Stat(path); err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("secret file mode: %v %v", fi, err)
	}
	again, err := LoadOrCreateSecret(path)
	if err != nil || string(again) != string(secret) {
		t.Fatalf("secret not stable: %v", err)
	}
	a, b := TokenFor(secret, "lola-p-a"), TokenFor(secret, "lola-p-b")
	if a == b || !ValidToken(secret, "lola-p-a", a) || ValidToken(secret, "lola-p-b", a) || ValidToken(secret, "lola-p-a", "") || ValidToken(nil, "lola-p-a", a) {
		t.Fatal("token is not bound to its session")
	}
	if err := os.WriteFile(path, []byte("short"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadOrCreateSecret(path); err == nil {
		t.Fatal("a corrupt secret must not be silently regenerated")
	}
}

// Inside a sandbox container the hook goes to the TCP bridge, token attached.
func TestPostUsesTheBridgeWhenSandboxed(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	got := make(chan protocol.Request, 1)
	go func() {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		defer c.Close()
		line, _ := bufio.NewReader(c).ReadBytes('\n')
		var req protocol.Request
		_ = json.Unmarshal(line, &req)
		got <- req
		_, _ = c.Write([]byte(`{"ok":true}` + "\n"))
	}()
	t.Setenv("LOLA_HOME", t.TempDir()) // no unix socket there
	t.Setenv("LOLA_SESSION", "lola-p-a")
	t.Setenv("LOLA_HOOK_ADDR", ln.Addr().String())
	t.Setenv("LOLA_HOOK_TOKEN", "tok")
	if err := Post("stop", protocol.HookPayload{}); err != nil {
		t.Fatalf("Post: %v", err)
	}
	req := <-got
	if req.Cmd != "hookEvent" || req.Session != "lola-p-a" || req.HookToken != "tok" {
		t.Fatalf("bridge got %+v", req)
	}
}
