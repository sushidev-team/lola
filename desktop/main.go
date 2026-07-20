// Command lola-desktop is a native macOS companion to the lola TUI. It is a
// Wails 3 app: a Go backend that speaks the daemon's unix-socket protocol and a
// Svelte frontend that renders the same flight-deck the TUI does, with live
// terminal tiles. The backend is a *client* of the daemon — it never embeds the
// daemon — so the TUI and this app observe and drive the exact same sessions.
package main

import (
	"embed"
	"log"
	"os"
	"strings"
	"time"

	"github.com/sushidev-team/lola/internal/protocol"
	"github.com/wailsapp/wails/v3/pkg/application"
)

//go:embed all:frontend/dist
var assets embed.FS

// Canvas colour = theme.go colCanvas (#0e1420), so the native window chrome and
// the webview share one deep navy surface with no seam at the title bar.
var canvas = application.NewRGB(0x0e, 0x14, 0x20)

func main() {
	ensurePATH()

	daemon := &DaemonService{}
	term := NewTermService()

	app := application.New(application.Options{
		Name:        "lola",
		Description: "Native cockpit for the lola coding-agent orchestrator",
		Services: []application.Service{
			application.NewService(daemon),
			application.NewService(term),
			application.NewService(&ConfigService{}),
			application.NewService(&DoctorService{}),
			application.NewService(NewLinearService()),
		},
		Assets: application.AssetOptions{
			Handler: application.AssetFileServerFS(assets),
		},
		Mac: application.MacOptions{
			ApplicationShouldTerminateAfterLastWindowClosed: true,
		},
	})

	// Give the terminal service the emitter it streams PTY bytes over.
	term.SetApp(app)

	app.Window.NewWithOptions(application.WebviewWindowOptions{
		Title:            "lola",
		Width:            1280,
		Height:           832,
		MinWidth:         920,
		MinHeight:        560,
		BackgroundColour: canvas,
		Mac: application.MacWindow{
			// Match the vitals bar height (h-9 = 36px) so macOS centers the
			// traffic lights vertically on the vitals row.
			InvisibleTitleBarHeight: 36,
			// Opaque, not vibrancy: the TUI theme is deliberately one cohesive
			// opaque canvas, so we match it rather than letting the desktop bleed
			// through.
			Backdrop: application.MacBackdropNormal,
			TitleBar: application.MacTitleBarHiddenInset,
		},
		URL: "/",
	})

	// Live push loop: the daemon has no push channel, so the desktop backend
	// polls its cheap in-memory caches and emits typed events the frontend
	// subscribes to. When the daemon is down we emit only the liveness flag so
	// the UI can show its "start daemon" banner without spamming dial errors.
	go pushLoop(app, daemon)

	if err := app.Run(); err != nil {
		log.Fatal(err)
	}
}

// ensurePATH augments the process PATH with the usual Homebrew locations. A
// .app launched from Finder inherits a minimal PATH (/usr/bin:/bin:/usr/sbin:
// /sbin) — not the login shell's — so tmux/git/gh/lola under /opt/homebrew/bin
// (Apple Silicon) or /usr/local/bin (Intel) would be "command not found". Every
// child exec (tmux capture/attach, `lola run`) inherits this, so we fix it once
// at startup. Harmless under `wails3 dev`, where PATH is already rich.
func ensurePATH() {
	want := []string{"/opt/homebrew/bin", "/usr/local/bin"}
	cur := os.Getenv("PATH")
	parts := strings.Split(cur, ":")
	have := make(map[string]bool, len(parts))
	for _, p := range parts {
		have[p] = true
	}
	var prefix []string
	for _, w := range want {
		if !have[w] {
			prefix = append(prefix, w)
		}
	}
	if len(prefix) > 0 {
		_ = os.Setenv("PATH", strings.Join(prefix, ":")+":"+cur)
	}
}

// Event names the frontend subscribes to. Kept in one place so the binding
// generator and the Svelte store agree on the strings.
const (
	evtAlive    = "daemon:alive"    // bool
	evtSessions = "daemon:sessions" // protocol.SessionsData
	evtProjects = "daemon:projects" // protocol.ProjectsData
	evtStatus   = "daemon:status"   // protocol.StatusData
)

func init() {
	application.RegisterEvent[bool](evtAlive)
	application.RegisterEvent[protocol.SessionsData](evtSessions)
	application.RegisterEvent[protocol.ProjectsData](evtProjects)
	application.RegisterEvent[protocol.StatusData](evtStatus)
}

func pushLoop(app *application.App, d *DaemonService) {
	const fast = 2 * time.Second // sessions cadence; projects/status every other tick
	tick := time.NewTicker(fast)
	defer tick.Stop()

	var lastAlive bool
	var first = true
	var i int
	for range tick.C {
		alive := daemonAlive()
		if alive != lastAlive || first {
			app.Event.Emit(evtAlive, alive)
			lastAlive = alive
			first = false
		}
		if !alive {
			i++
			continue
		}
		if sd, err := d.Sessions(); err == nil {
			app.Event.Emit(evtSessions, sd)
		}
		if i%2 == 0 {
			if pd, err := d.Projects(); err == nil {
				app.Event.Emit(evtProjects, pd)
			}
			if st, err := d.Status(); err == nil {
				app.Event.Emit(evtStatus, st)
			}
		}
		i++
	}
}
