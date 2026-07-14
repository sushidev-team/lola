package hook

import (
	"bufio"
	"encoding/json"
	"errors"
	"net"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/sushidev-team/lola/internal/protocol"
)

const settingsGolden = `{
  "hooks": {
    "Stop": [
      {
        "matcher": "",
        "hooks": [
          {
            "type": "command",
            "command": "/usr/local/bin/lola hook stop",
            "timeout": 10
          }
        ]
      }
    ],
    "Notification": [
      {
        "matcher": "",
        "hooks": [
          {
            "type": "command",
            "command": "/usr/local/bin/lola hook notification",
            "timeout": 10
          }
        ]
      }
    ],
    "SessionEnd": [
      {
        "matcher": "",
        "hooks": [
          {
            "type": "command",
            "command": "/usr/local/bin/lola hook session_end",
            "timeout": 10
          }
        ]
      }
    ],
    "PostToolUse": [
      {
        "matcher": "",
        "hooks": [
          {
            "type": "command",
            "command": "/usr/local/bin/lola hook tool_use",
            "timeout": 10,
            "async": true
          }
        ]
      }
    ],
    "UserPromptSubmit": [
      {
        "matcher": "",
        "hooks": [
          {
            "type": "command",
            "command": "/usr/local/bin/lola hook user_prompt",
            "timeout": 10
          }
        ]
      }
    ]
  }
}
`

func TestSettingsJSONGolden(t *testing.T) {
	got := SettingsJSON("/usr/local/bin/lola")
	if string(got) != settingsGolden {
		t.Errorf("SettingsJSON mismatch:\n--- got ---\n%s\n--- want ---\n%s", got, settingsGolden)
	}
}

// TestSettingsJSONShape re-parses the output so the golden can't silently
// bless invalid JSON, and asserts the load-bearing bits event by event.
func TestSettingsJSONShape(t *testing.T) {
	raw := SettingsJSON("/opt/lola")

	var parsed struct {
		Hooks map[string][]struct {
			Matcher string `json:"matcher"`
			Hooks   []struct {
				Type    string `json:"type"`
				Command string `json:"command"`
				Timeout int    `json:"timeout"`
				Async   bool   `json:"async"`
			} `json:"hooks"`
		} `json:"hooks"`
	}
	if err := json.Unmarshal(raw, &parsed); err != nil {
		t.Fatalf("SettingsJSON is not valid JSON: %v\n%s", err, raw)
	}

	want := map[string]struct {
		cmd   string
		async bool
	}{
		"Stop":             {"/opt/lola hook stop", false},
		"Notification":     {"/opt/lola hook notification", false},
		"SessionEnd":       {"/opt/lola hook session_end", false},
		"PostToolUse":      {"/opt/lola hook tool_use", true},
		"UserPromptSubmit": {"/opt/lola hook user_prompt", false},
	}
	if len(parsed.Hooks) != len(want) {
		t.Errorf("got %d hook events, want %d: %v", len(parsed.Hooks), len(want), parsed.Hooks)
	}
	for event, w := range want {
		entries := parsed.Hooks[event]
		if len(entries) != 1 || len(entries[0].Hooks) != 1 {
			t.Errorf("%s: want exactly one matcher entry with one hook, got %+v", event, entries)
			continue
		}
		h := entries[0].Hooks[0]
		if h.Type != "command" || h.Command != w.cmd || h.Timeout != 10 || h.Async != w.async {
			t.Errorf("%s hook = %+v, want command=%q timeout=10 async=%v", event, h, w.cmd, w.async)
		}
	}
}

func TestSettingsJSONQuotesUnsafeBin(t *testing.T) {
	raw := SettingsJSON("/Users/me/My Tools/lola")
	if !strings.Contains(string(raw), `'/Users/me/My Tools/lola' hook stop`) {
		t.Errorf("binary path with spaces not shell-quoted:\n%s", raw)
	}
}

func TestPostNoSession(t *testing.T) {
	t.Setenv("LOLA_SESSION", "")
	t.Setenv("LOLA_HOME", t.TempDir())

	err := Post("stop", "")
	if err == nil || !strings.Contains(err.Error(), "not a lola session") {
		t.Fatalf("Post without LOLA_SESSION = %v, want 'not a lola session' error", err)
	}
}

func TestPostDaemonDown(t *testing.T) {
	t.Setenv("LOLA_SESSION", "sess-1")
	t.Setenv("LOLA_HOME", t.TempDir()) // no socket

	start := time.Now()
	if err := Post("stop", ""); err == nil {
		t.Fatal("Post with no socket = nil, want dial error")
	}
	if d := time.Since(start); d > postTimeout {
		t.Errorf("Post took %v, must return within the %v budget", d, postTimeout)
	}
}

func TestPostSendsHookEvent(t *testing.T) {
	home := t.TempDir()
	t.Setenv("LOLA_HOME", home)
	t.Setenv("LOLA_SESSION", "sess-42")

	ln, err := net.Listen("unix", filepath.Join(home, "lola.sock"))
	if errors.Is(err, syscall.EPERM) {
		t.Skip("sandbox forbids unix socket bind")
	}
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })

	got := make(chan protocol.Request, 1)
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		line, err := bufio.NewReader(conn).ReadString('\n')
		if err != nil {
			return
		}
		var req protocol.Request
		if json.Unmarshal([]byte(line), &req) == nil {
			got <- req
		}
		out, _ := json.Marshal(protocol.Response{OK: true})
		conn.Write(append(out, '\n'))
	}()

	if err := Post("notification", "permission_request"); err != nil {
		t.Fatalf("Post = %v, want nil", err)
	}
	select {
	case req := <-got:
		want := protocol.Request{Cmd: "hookEvent", Session: "sess-42", Event: "notification", Detail: "permission_request"}
		if req != want {
			t.Errorf("daemon received %+v, want %+v", req, want)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("daemon never received the request")
	}
}

// TestPostSurvivesSilentDaemon: a daemon that accepts but never replies must
// not hang Post past its deadline — best-effort read, bounded by postTimeout.
func TestPostSurvivesSilentDaemon(t *testing.T) {
	home := t.TempDir()
	t.Setenv("LOLA_HOME", home)
	t.Setenv("LOLA_SESSION", "sess-1")

	ln, err := net.Listen("unix", filepath.Join(home, "lola.sock"))
	if errors.Is(err, syscall.EPERM) {
		t.Skip("sandbox forbids unix socket bind")
	}
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		time.Sleep(5 * time.Second) // never reply
	}()

	old := postTimeout
	postTimeout = 300 * time.Millisecond
	t.Cleanup(func() { postTimeout = old })

	start := time.Now()
	if err := Post("tool_use", ""); err != nil {
		t.Fatalf("Post = %v, want nil (reply is best-effort)", err)
	}
	if d := time.Since(start); d > time.Second {
		t.Errorf("Post took %v, want return at the ~%v deadline", d, postTimeout)
	}
}
