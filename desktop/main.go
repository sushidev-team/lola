// Command Lola is the native macOS companion to the lola TUI. It is a
// Wails 3 app: a Go backend that speaks the daemon's unix-socket protocol and a
// Svelte frontend that renders the same flight-deck the TUI does, with live
// terminal tiles. The backend is a *client* of the daemon — it never embeds the
// daemon — so the TUI and this app observe and drive the exact same sessions.
package main

import (
	"context"
	"embed"
	"log"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/sushidev-team/lola/internal/config"
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

// version is the compiled-in app version, injected at build time via
// -ldflags "-X main.version=<tag>" (see build/darwin/Taskfile.yml and the
// release workflow). The "dev" default marks an un-tagged local build; the
// update checker treats a non-semver version as "always offer the release".
var version = "dev"

// canvasByTheme is the native-window twin of the frontend's --color-canvas: for
// every [ui].theme id, the Catppuccin `mantle` that catppuccin.ts maps that
// token to. It is a MIRROR of TypeScript data, which is normally the wrong
// shape — but the window is built here in main(), long before a webview exists
// to ask, so Go cannot read the palette from its real home. What makes the
// mirror safe is TestCanvasMatchesFrontendPalette: it parses catppuccin.ts and
// fails if either side moves. Do not add an id here without adding it to
// config.UIThemes; the same test pins the two sets equal.
var canvasByTheme = map[string]application.RGBA{
	"catppuccin-latte":     application.NewRGB(0xe6, 0xe9, 0xef),
	"catppuccin-frappe":    application.NewRGB(0x29, 0x2c, 0x3c),
	"catppuccin-macchiato": application.NewRGB(0x1e, 0x20, 0x30),
	"catppuccin-mocha":     application.NewRGB(0x18, 0x18, 0x25),
}

// canvasFor maps a [ui].theme id to its native-window background. An unknown or
// empty id falls back to config.DefaultUITheme's canvas, never the zero RGBA:
// zero is transparent-black, the one outcome worse than the wrong flavor. The
// miss covers both an id that config.Load accepted but Validate would reject and
// a flavor added on the frontend but not here (TestCanvasMatchesFrontendPalette
// keeps the two sets in step).
func canvasFor(themeID string) application.RGBA {
	if c, ok := canvasByTheme[themeID]; ok {
		return c
	}
	return canvasByTheme[config.DefaultUITheme]
}

// windowCanvas resolves the NSWindow background from the CONFIGURED flavor at
// startup, so the native chrome and the page agree in all four — a fixed literal
// put a near-black rectangle behind Latte's near-white page. It reads the theme
// through ConfigService.GetTheme, the same accessor the frontend calls, so there
// is one resolution rule rather than a second copy of the "" → default fallback.
func windowCanvas() application.RGBA {
	return canvasFor((&ConfigService{}).GetTheme())
}

// repaintWindowCanvas tracks a runtime flavor switch on the native window:
// ConfigService.SetTheme calls it after persisting, because BackgroundColour is
// set once at window construction and would otherwise show the old canvas — a
// seam at the title bar and during resize — until the next launch, even though
// the webview itself repaints immediately via the frontend applier. Best-effort:
// a no-op before the app or its window exists (unit tests, early startup), where
// windowCanvas has already painted the correct colour anyway. SetBackgroundColour
// marshals to the main thread itself (InvokeSync), so this is safe from the
// bound-service goroutine SetTheme runs on.
func repaintWindowCanvas(themeID string) {
	app := application.Get()
	if app == nil {
		return
	}
	if win := app.Window.Current(); win != nil {
		win.SetBackgroundColour(canvasFor(themeID))
	}
}

func main() {
	ensurePATH()

	daemon := &DaemonService{}
	term := NewTermService()
	updater := NewUpdateService()

	app := application.New(application.Options{
		// Name drives the app menu's Hide/Quit/About role labels ("Quit Lola").
		// The Dock/Finder/Cmd-Tab label comes from the .app bundle DIRECTORY
		// name instead — see desktop/Taskfile.yml's APP_NAME.
		Name:        "Lola",
		Description: "Native cockpit for the lola coding-agent orchestrator",
		Services: []application.Service{
			application.NewService(daemon),
			application.NewService(term),
			application.NewService(&ConfigService{}),
			application.NewService(&DoctorService{}),
			application.NewService(NewLinearService()),
			application.NewService(updater),
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
	// The updater emits download-progress events and quits for the install swap.
	updater.SetApp(app)

	win := app.Window.NewWithOptions(application.WebviewWindowOptions{
		Title:  "Lola",
		Width:  1280,
		Height: 832,
		// 1000, not 920: the kanban lens has to keep fitting next to the 248px
		// sidebar.
		MinWidth:         1000,
		MinHeight:        560,
		BackgroundColour: windowCanvas(),
		Mac: application.MacWindow{
			// The whole top strip is draggable (the frontend paints a .drag band
			// across it — the sidebar's brand row plus the main top bar).
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

	// Install the app menu, repointing its Zoom In/Out/Actual Size items to
	// reflowing page zoom (see installAppMenu / zoomController).
	installAppMenu(app, newZoomController(win))

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
	tray.SetLabel("Lola")
	tray.SetTooltip("Lola — coding-agent orchestrator")

	menu := app.Menu.New()
	menu.Add("Open Lola").OnClick(func(*application.Context) {
		win.Show()
		win.Focus()
	})
	menu.Add("Settings…").OnClick(func(*application.Context) {
		win.Show()
		win.Focus()
		app.Event.Emit(evtOpenSettings, struct{}{})
	})
	menu.Add("Check for Updates…").OnClick(func(*application.Context) {
		win.Show()
		win.Focus()
		app.Event.Emit(evtOpenUpdate, struct{}{})
	})
	menu.AddSeparator()
	menu.Add("Quit Lola").OnClick(func(*application.Context) { app.Quit() })

	tray.SetMenu(menu)
}

// Page-zoom bounds. Steps of 0.1 land on 1.0 exactly, and the range matches a
// browser's usual zoom span.
const (
	zoomMin  = 0.5
	zoomMax  = 3.0
	zoomStep = 0.1
)

// zoomController drives WKWebView page zoom in response to the View-menu Zoom
// items. It exists because Wails' built-in Zoom roles call
// -[WKWebView setMagnification:], which visually scales the surface WITHOUT
// reflowing — so past 100% the fixed-origin content spills off the right edge
// of our frameless, scrollbar-less window. setPageZoom shrinks the layout
// viewport by 1/factor and repaints, so the responsive flight-deck reflows and
// keeps fitting the width. Menu clicks are serialized on the main thread, but a
// mutex guards `factor` cheaply since setPageZoom itself may hop threads.
type zoomController struct {
	win    *application.WebviewWindow
	mu     sync.Mutex
	factor float64
}

func newZoomController(win *application.WebviewWindow) *zoomController {
	return &zoomController{win: win, factor: 1.0}
}

func (z *zoomController) set(f float64) {
	f = math.Round(f*10) / 10 // snap to a clean 0.1 step so presses hit 1.0
	if f < zoomMin {
		f = zoomMin
	}
	if f > zoomMax {
		f = zoomMax
	}
	z.mu.Lock()
	z.factor = f
	z.mu.Unlock()
	setPageZoom(z.win.NativeWindow(), f)
}

func (z *zoomController) in() {
	z.mu.Lock()
	f := z.factor
	z.mu.Unlock()
	z.set(f + zoomStep)
}

func (z *zoomController) out() {
	z.mu.Lock()
	f := z.factor
	z.mu.Unlock()
	z.set(f - zoomStep)
}

func (z *zoomController) reset() { z.set(1.0) }

// installAppMenu builds the macOS app menu. It is DefaultApplicationMenu's role
// list, assembled by hand only so the "Session" submenu lands in its
// conventional slot (before Window/Help) — Menu has no insert-at API — and
// swaps the three View-menu Zoom items from Wails' magnification handlers to
// reflowing page zoom. Repointing keeps the role items' labels and their
// Cmd+/-/0 accelerators, so that part reads exactly like the default.
//
// The Session items are the app's ONE set of modifier shortcuts, and they live
// in the menu rather than in the frontend's key handler on purpose:
//   - AppKit dispatches a menu key equivalent before the WKWebView sees it, so
//     ⌘⇧R works while a live terminal holds the keyboard — a JS handler there
//     never fires, because xterm's hidden textarea counts as "typing".
//   - the menu is where a macOS user looks for what a chord does, and AppKit
//     refuses a duplicate accelerator loudly instead of two silent handlers.
//
// Every accelerator here is Cmd-based and none is a system or Edit-menu one
// (⌘C/⌘V/⌘X/⌘A/⌘Z/⌘W/⌘Q, and ⌘⌫ which deletes to line start in a text field).
// Cmd chords are also the one class tmux/zellij inside a pane never receive, so
// these can't collide with a shell binding either.
func installAppMenu(app *application.App, zoom *zoomController) {
	menu := application.NewMenu()
	menu.AddRole(application.AppMenu)
	menu.AddRole(application.FileMenu)
	menu.AddRole(application.EditMenu)
	menu.AddRole(application.ViewMenu)
	newSessionMenu(app, menu.AddSubmenu("Session"))
	menu.AddRole(application.WindowMenu)
	menu.AddRole(application.HelpMenu)

	if it := menu.FindByRole(application.ZoomIn); it != nil {
		it.OnClick(func(*application.Context) { zoom.in() })
	}
	if it := menu.FindByRole(application.ZoomOut); it != nil {
		it.OnClick(func(*application.Context) { zoom.out() })
	}
	if it := menu.FindByRole(application.ResetZoom); it != nil {
		it.OnClick(func(*application.Context) { zoom.reset() })
	}
	// Force Reload ships on ⌘⇧R, which Session > Trigger Review now owns — a
	// duplicate accelerator would go to whichever item AppKit finds first. The
	// item stays (it is a genuine escape hatch for a wedged webview), moved to
	// ⌥⌘R next to Reload's ⌘R.
	if it := menu.FindByRole(application.ForceReload); it != nil {
		it.SetAccelerator("CmdOrCtrl+OptionOrAlt+r")
	}
	app.Menu.SetApplicationMenu(menu)
}

// newSessionMenu fills the Session submenu. Each item only ASKS the frontend to
// act (evtSessionAction): the target is the cockpit's current selection, which
// is frontend nav state the backend has no view of — the same reason the
// status-bar menu emits for Settings instead of opening it.
func newSessionMenu(app *application.App, menu *application.Menu) {
	ask := func(action string) func(*application.Context) {
		return func(*application.Context) { app.Event.Emit(evtSessionAction, action) }
	}
	menu.Add("Trigger Review").
		SetTooltip("run the configured QA review provider on the selected session").
		SetAccelerator("CmdOrCtrl+Shift+r").
		OnClick(ask(actionReview))
	menu.Add("Open PR in Browser").
		SetAccelerator("CmdOrCtrl+Shift+o").
		OnClick(ask(actionOpenPR))
	menu.Add("New Worktree Shell").
		SetAccelerator("CmdOrCtrl+t").
		OnClick(ask(actionNewShell))
	menu.AddSeparator()
	menu.Add("Kill Session…").
		SetAccelerator("CmdOrCtrl+Shift+k").
		OnClick(ask(actionKill))
}

// pathProbeTimeout bounds the login-shell PATH probe. A shell with a heavy rc
// can take a moment; anything past this is a wedged profile we must not block
// app startup on, and the static fallbacks below still apply.
const pathProbeTimeout = 3 * time.Second

// pathSentinel brackets the PATH the probe asks for. Login rc files print
// banners, version notices and `motd` to stdout, so reading "the output" would
// hand exec a directory list made of someone's shell greeting. The sentinel
// makes the answer findable regardless of what surrounds it.
const pathSentinel = "__LOLA_PATH__"

// staticPATHDirs are the directories added when (or in addition to) the probe.
// The Homebrew prefixes are where tmux/gh/git live; the Go and local bins are
// where a `go install`ed lola lands, which is the single most common way to
// have a CLI the app could not see.
var staticPATHDirs = []string{
	"/opt/homebrew/bin",
	"/opt/homebrew/sbin",
	"/usr/local/bin",
	"~/go/bin",
	"~/.local/bin",
	"~/bin",
}

// loginShellPATH is the exec seam over the probe, so tests can drive ensurePATH
// without a shell. It returns "" whenever it cannot answer — every caller
// treats that as "use the static list", never as an error.
var loginShellPATH = func() string {
	sh := strings.TrimSpace(os.Getenv("SHELL"))
	if sh == "" {
		return ""
	}
	ctx, cancel := context.WithTimeout(context.Background(), pathProbeTimeout)
	defer cancel()
	// -l (login) but NOT -i (interactive): the login profile is where PATH is
	// built, and an interactive shell additionally loads prompt/plugin machinery
	// that is slow and can block on a tty we do not have.
	out, err := exec.CommandContext(ctx, sh, "-l", "-c",
		"printf '\\n%s%s\\n' "+pathSentinel+" \"$PATH\"").Output()
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(out), "\n") {
		if v, ok := strings.CutPrefix(strings.TrimSpace(line), pathSentinel); ok {
			return v
		}
	}
	return ""
}

// ensurePATH rebuilds the process PATH so child execs can find the user's
// tools. A .app launched from Finder inherits a MINIMAL PATH (/usr/bin:/bin:
// /usr/sbin:/sbin) — not the login shell's — so tmux/git/gh/claude/lola are all
// "command not found" no matter where they are installed. Every child exec
// (tmux capture/attach, `lola run`, the doctor's LookPath probes) inherits this,
// so it is fixed once at startup.
//
// Two sources, in this order:
//
//  1. the LOGIN SHELL's PATH, which is the only thing that knows about version
//     managers (mise, asdf, fnm, volta) — and `claude` is very often installed
//     through one of those, so the old two-directory list could not find it;
//  2. a static list of well-known directories, as the floor when the probe
//     fails (no $SHELL, a wedged profile, a sandbox).
//
// Both go AHEAD of the inherited entries, matching the previous behaviour where
// a Homebrew tool won over a system one — a user who installed a newer git
// meant to use it. Order within each source is preserved and duplicates are
// dropped, so the result stays the user's own precedence.
func ensurePATH() {
	var ordered []string
	seen := map[string]bool{}
	add := func(dirs []string) {
		for _, d := range dirs {
			d = expandHome(strings.TrimSpace(d))
			if d == "" || seen[d] {
				continue
			}
			seen[d] = true
			ordered = append(ordered, d)
		}
	}
	add(filepath.SplitList(loginShellPATH()))
	add(staticPATHDirs)
	add(filepath.SplitList(os.Getenv("PATH")))
	_ = os.Setenv("PATH", strings.Join(ordered, string(os.PathListSeparator)))
}

// Event names the frontend subscribes to. Kept in one place so the binding
// generator and the Svelte store agree on the strings.
const (
	evtAlive    = "daemon:alive"    // bool
	evtSessions = "daemon:sessions" // protocol.SessionsData
	evtProjects = "daemon:projects" // protocol.ProjectsData
	evtStatus   = "daemon:status"   // protocol.StatusData
	// evtPushErr carries a push-loop command failure so the frontend can explain
	// a blanked read (an out-of-date daemon answering `unknown cmd`) instead of
	// swallowing it. A non-empty Msg means failure; "" means the command recovered.
	evtPushErr = "daemon:pusherr" // PushErrDTO

	// evtOpenSettings is fired by the status-bar menu. The overlay lives in the
	// frontend's nav state, so the menu cannot open it directly — it asks.
	evtOpenSettings = "app:open-settings" // no payload
	// evtOpenUpdate opens the software-update overlay from the status-bar menu.
	evtOpenUpdate = "app:open-update" // no payload
	// evtSessionAction carries one of the action* names below from the Session
	// menu (and its accelerator) to the frontend, which applies it to the
	// cockpit's selected session. See newSessionMenu.
	evtSessionAction = "app:session-action" // string
)

// The evtSessionAction payloads. Mirrored by the switch in App.svelte's
// sessionAction — keep the two lists in step.
const (
	actionReview   = "review"
	actionOpenPR   = "open-pr"
	actionNewShell = "new-shell"
	actionKill     = "kill"
)

// PushErrDTO is the payload of evtPushErr: which push-loop command failed and
// the daemon's error text. Emitted only on a change (see pushLoop) so a
// persistent failure is announced once, not every 2s.
type PushErrDTO struct {
	Cmd string `json:"cmd"`
	Msg string `json:"msg"`
}

func init() {
	application.RegisterEvent[bool](evtAlive)
	application.RegisterEvent[struct{}](evtOpenSettings)
	application.RegisterEvent[struct{}](evtOpenUpdate)
	application.RegisterEvent[string](evtSessionAction)
	application.RegisterEvent[protocol.SessionsData](evtSessions)
	application.RegisterEvent[protocol.ProjectsData](evtProjects)
	application.RegisterEvent[protocol.StatusData](evtStatus)
	application.RegisterEvent[PushErrDTO](evtPushErr)
	application.RegisterEvent[UpdateProgressDTO](evtUpdateProgress)
}

func pushLoop(app *application.App, d *DaemonService) {
	const fast = 2 * time.Second // sessions cadence; projects/status every other tick
	tick := time.NewTicker(fast)
	defer tick.Stop()

	// Last push error surfaced per command, so a persistent failure (an
	// out-of-date daemon) is announced once rather than every 2s — the banner is
	// dismissible and re-emitting would resurrect it. emitPushErr diffs against
	// this and fires evtPushErr only on a change (including recovery → "").
	lastErr := map[string]string{}
	emitPushErr := func(cmd string, err error) {
		msg := ""
		if err != nil {
			msg = err.Error()
		}
		if lastErr[cmd] == msg {
			return
		}
		lastErr[cmd] = msg
		app.Event.Emit(evtPushErr, PushErrDTO{Cmd: cmd, Msg: msg})
	}

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
			// A down daemon isn't "out of date" — the frontend's offline state
			// covers it. Reset the dedup so a still-out-of-date daemon re-announces
			// its errors when it comes back up.
			clear(lastErr)
			i++
			continue
		}
		if sd, err := d.Sessions(); err != nil {
			emitPushErr("sessions", err)
		} else {
			app.Event.Emit(evtSessions, sd)
			emitPushErr("sessions", nil)
		}
		if i%2 == 0 {
			if pd, err := d.Projects(); err != nil {
				emitPushErr("projects", err)
			} else {
				app.Event.Emit(evtProjects, pd)
				emitPushErr("projects", nil)
			}
			if st, err := d.Status(); err != nil {
				emitPushErr("status", err)
			} else {
				app.Event.Emit(evtStatus, st)
				emitPushErr("status", nil)
			}
		}
		i++
	}
}
