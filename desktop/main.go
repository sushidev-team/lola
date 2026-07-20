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
	"github.com/wailsapp/wails/v3/pkg/events"
)

// fixHiDPIOnReady re-applies the WKWebView device-scale override each time the
// window becomes main (idempotent), so Retina text stays crisp. The platform
// work is in fixHiDPI (darwin: hidpi_darwin.go; no-op elsewhere).
func fixHiDPIOnReady(win *application.WebviewWindow) {
	win.OnWindowEvent(events.Mac.WindowDidBecomeMain, func(*application.WindowEvent) {
		fixHiDPI(win.NativeWindow())
	})
}

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

	win := app.Window.NewWithOptions(application.WebviewWindowOptions{
		Title:            "lola",
		Width:            1280,
		Height:           832,
		MinWidth:         920,
		MinHeight:        560,
		BackgroundColour: canvas,
		Mac: application.MacWindow{
			// The whole top strip (the vitals bar) is draggable.
			InvisibleTitleBarHeight: 36,
			// Opaque, not vibrancy: the TUI theme is deliberately one cohesive
			// opaque canvas, so we match it rather than letting the desktop bleed
			// through.
			Backdrop: application.MacBackdropNormal,
			// Hidden (not HiddenInset): HiddenInset adds a toolbar that insets the
			// traffic lights downward, leaving too much space above them. Hidden
			// keeps them at the standard top-left position, like Ghostty/Terminal.
			TitleBar: application.MacTitleBarHidden,
		},
		URL: "/",
	})

	newStatusBarMenu(app, win)

	// Force the WKWebView to report the screen's real backing scale factor once
	// the window is up, so Retina renders crisply (see hidpi_darwin.go). Runs on
	// every focus but is idempotent.
	fixHiDPIOnReady(win)

	// Live push loop: the daemon has no push channel, so the desktop backend
	// polls its cheap in-memory caches and emits typed events the frontend
	// subscribes to. When the daemon is down we emit only the liveness flag so
	// the UI can show its "start daemon" banner without spamming dial errors.
	go pushLoop(app, daemon)

	if err := app.Run(); err != nil {
		log.Fatal(err)
	}
}

// newStatusBarMenu puts lola in the macOS menu bar, so the cockpit is reachable
// (and Settings openable) without hunting for the window — the app keeps running
// with its window closed, and this is the way back to it.
//
// Settings is emitted rather than opened directly: the overlay is frontend nav
// state, and showing the window first means the overlay is not opened behind a
// hidden window.
func newStatusBarMenu(app *application.App, win application.Window) {
	tray := app.SystemTray.New()
	tray.SetLabel("lola")
	tray.SetTooltip("lola — coding-agent orchestrator")

	menu := app.Menu.New()
	menu.Add("Open lola").OnClick(func(*application.Context) {
		win.Show()
		win.Focus()
	})
	menu.Add("Settings…").OnClick(func(*application.Context) {
		win.Show()
		win.Focus()
		app.Event.Emit(evtOpenSettings, struct{}{})
	})
	menu.AddSeparator()
	menu.Add("Quit lola").OnClick(func(*application.Context) { app.Quit() })

	tray.SetMenu(menu)
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

	// evtOpenSettings is fired by the status-bar menu. The overlay lives in the
	// frontend's nav state, so the menu cannot open it directly — it asks.
	evtOpenSettings = "app:open-settings" // no payload
)

func init() {
	application.RegisterEvent[bool](evtAlive)
	application.RegisterEvent[struct{}](evtOpenSettings)
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
