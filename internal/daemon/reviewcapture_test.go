package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sushidev-team/lola/internal/linear"
	"github.com/sushidev-team/lola/internal/protocol"
	"github.com/sushidev-team/lola/internal/remote"
	"github.com/sushidev-team/lola/internal/reviewcapture"
	"github.com/sushidev-team/lola/internal/reviewpack"
	"github.com/sushidev-team/lola/internal/session"
)

func reviewCaptureFixture(t *testing.T) (*Daemon, session.Session, reviewpack.Package, protocol.ReviewCaptureArgs) {
	t.Helper()
	d := newTestDaemon(t, nativeTestConfig(nativePoll("p1")), &linear.Fake{}, &fakeNative{})
	s := nativeSess("FE-9", "working")
	s.Worktree, s.Repo = t.TempDir(), "acme/app"
	goalFixtureGit(t, s.Worktree, "init", "-b", "main")
	goalFixtureGit(t, s.Worktree, "commit", "--allow-empty", "-m", "initial")
	head := goalFixtureGit(t, s.Worktree, "rev-parse", "HEAD")
	d.sessions.Upsert(s)
	f, err := os.Create(filepath.Join(s.Worktree, "before.png"))
	if err != nil {
		t.Fatal(err)
	}
	if err := png.Encode(f, image.NewRGBA(image.Rect(0, 0, 10, 10))); err != nil {
		t.Fatal(err)
	}
	f.Close()
	p, err := reviewpack.Publish(d.home, s.ID, s.Worktree, s.Repo, 0, reviewpack.Manifest{Version: 1, Title: "Review", Body: "Details", Base: head, Head: head, Screens: []reviewpack.Screen{{Title: "Settings", Before: "before.png", After: "before.png"}}})
	if err != nil {
		t.Fatal(err)
	}
	return d, s, p, protocol.ReviewCaptureArgs{Package: p.ID, Screen: -1, URL: "http://localhost:3000/settings", Title: "Settings capture", Selector: "main", Width: 1440, Height: 900, Channel: "chrome"}
}

func fakeReviewCapture(t *testing.T, worktree string) reviewcapture.Run {
	return func(_ context.Context, dir, script string, input []byte) ([]byte, error) {
		if dir != worktree || script == "" {
			t.Fatal("capture missing source context")
		}
		var o reviewcapture.Options
		if err := json.Unmarshal(input, &o); err != nil {
			t.Fatal(err)
		}
		f, err := os.Create(o.Destination)
		if err != nil {
			return nil, err
		}
		defer f.Close()
		img := image.NewRGBA(image.Rect(0, 0, 10, 10))
		img.Set(0, 0, color.White)
		if err := png.Encode(f, img); err != nil {
			return nil, err
		}
		return []byte(`{"url":"http://localhost:3000/settings?ready=1"}`), nil
	}
}

func TestReviewCaptureRetainsNewVersionAndOldEvidence(t *testing.T) {
	for _, index := range []int{-1, 0} {
		t.Run(map[int]string{-1: "add", 0: "update"}[index], func(t *testing.T) {
			d, s, p, a := reviewCaptureFixture(t)
			a.Screen = index
			d.reviewCaptureRun = fakeReviewCapture(t, s.Worktree)
			raw, _ := json.Marshal(a)
			r := d.handle(context.Background(), protocol.Request{Cmd: protocol.CmdReviewCapture, Session: s.ID, Args: raw})
			if !r.OK {
				t.Fatal(r.Error)
			}
			var got reviewpack.Package
			if err := json.Unmarshal(r.Data, &got); err != nil {
				t.Fatal(err)
			}
			want := 2
			if index == 0 {
				want = 1
			}
			screen := got.Screens[len(got.Screens)-1]
			if got.ID == p.ID || got.Head != p.Head || got.Body != p.Body || got.Session != p.Session || len(got.Screens) != want || screen.Selector != "main" || screen.URL != a.URL+"?ready=1" || !strings.Contains(screen.Caption, "not independently verified") {
				t.Fatalf("capture: %+v", got)
			}
			if index == 0 && screen.Before != p.Screens[0].Before {
				t.Fatal("discarded baseline")
			}
			old, err := reviewpack.Read(d.home, s.ID, p.ID)
			if err != nil || old.Screens[0].After != p.Screens[0].After {
				t.Fatal("rewrote retained evidence")
			}
			if _, _, err := reviewpack.Image(d.home, s.ID, got.ID, screen.After); err != nil {
				t.Fatal(err)
			}
			if _, err := os.Stat(filepath.Join(s.Worktree, "capture.png")); !os.IsNotExist(err) {
				t.Fatal("wrote capture into agent checkout")
			}
		})
	}
}

func TestReviewCaptureRefusesInvalidScopeAndStaleRevisions(t *testing.T) {
	for _, mode := range []string{"foreign", "sandbox", "stale", "invalid-index", "invalid-url", "during-capture"} {
		t.Run(mode, func(t *testing.T) {
			d, s, p, a := reviewCaptureFixture(t)
			called := false
			run := fakeReviewCapture(t, s.Worktree)
			d.reviewCaptureRun = func(ctx context.Context, dir, script string, input []byte) ([]byte, error) {
				called = true
				out, err := run(ctx, dir, script, input)
				if mode == "during-capture" {
					goalFixtureGit(t, s.Worktree, "commit", "--allow-empty", "-m", "moved")
				}
				return out, err
			}
			id := s.ID
			switch mode {
			case "foreign":
				id = "foreign"
			case "sandbox":
				s.Sandbox = "docker"
				d.sessions.Upsert(s)
			case "stale":
				goalFixtureGit(t, s.Worktree, "commit", "--allow-empty", "-m", "moved")
			case "invalid-index":
				a.Screen = 8
			case "invalid-url":
				a.URL = "file:///etc/passwd"
			}
			raw, _ := json.Marshal(a)
			r := d.handle(context.Background(), protocol.Request{Cmd: protocol.CmdReviewCapture, Session: id, Args: raw})
			if r.OK || called != (mode == "during-capture") {
				t.Fatalf("unexpected capture result: %+v called %v", r, called)
			}
			all, err := reviewpack.List(d.home, p.Session)
			if err != nil || len(all) != 1 {
				t.Fatal("retained failed capture")
			}
		})
	}
	if hookBridgeCmds[protocol.CmdReviewCapture] || !remote.CommandDenied(protocol.CmdReviewCapture) {
		t.Fatal("capture must stay human-only")
	}
}

// Opt-in integration uses an existing browser/module installation, never downloads.
func TestReviewCaptureBrowserIntegration(t *testing.T) {
	modules := os.Getenv("LOLA_REVIEW_CAPTURE_MODULES")
	if modules == "" {
		t.Skip("set LOLA_REVIEW_CAPTURE_MODULES to an installed node_modules directory")
	}
	d, s, p, a := reviewCaptureFixture(t)
	if err := os.Symlink(modules, filepath.Join(s.Worktree, "node_modules")); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, `<!doctype html><title>Capture test</title><main style="padding:24px;background:#ddeeff"><h1>Independent capture fixture</h1><p>Local integration test.</p></main>`)
	}))
	defer server.Close()
	a.URL = server.URL
	a.Channel = "chrome"
	raw, _ := json.Marshal(a)
	r := d.handle(context.Background(), protocol.Request{Cmd: protocol.CmdReviewCapture, Session: s.ID, Args: raw})
	if !r.OK {
		t.Fatal(r.Error)
	}
	var got reviewpack.Package
	if err := json.Unmarshal(r.Data, &got); err != nil {
		t.Fatal(err)
	}
	if got.ID == p.ID || len(got.Screens) != 2 || got.Screens[1].Selector != "main" || got.Screens[1].URL != a.URL+"/" {
		t.Fatalf("capture metadata: %+v", got)
	}
	data, _, err := reviewpack.Image(d.home, s.ID, got.ID, got.Screens[1].After)
	if err != nil || len(data) < 100 {
		t.Fatalf("capture image: %d %v", len(data), err)
	}
}
