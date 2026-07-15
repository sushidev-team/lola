package config

import "github.com/sushidev-team/lola/internal/tmux"

// The [tmux] table tunes the ATTACH UX for the isolated tmux server lola runs
// its agent sessions on. It is entirely optional and every field has a safe
// default, so an absent table means zero behavior change: lola runs its own
// tmux server on the "lola" socket, keeps tmux's built-in Ctrl-b d detach, uses
// its built-in branded status bar, and leaves the mouse off.
//
// Isolation is the point: lola always talks to tmux with `-L <socket_name>`, a
// SEPARATE server from your personal default tmux server. Attaching to (or
// detaching from) a lola session therefore never touches your own tmux — a
// different socket, a different set of sessions, its own options.
//
// Like [reactions]/[brain], the table uses a pointer-per-field on-disk mirror so
// load can tell an ABSENT key (nil → take the default) from an explicit value
// the operator wants preserved, and a fresh Config persists no [tmux] table.

const (
	// DefaultTmuxSocketName is the tmux server socket name lola runs its
	// sessions on when [tmux].socket_name is unset. Lola always passes this to
	// tmux as `-L <name>`, so its sessions live on an isolated server that never
	// collides with the operator's default tmux server.
	DefaultTmuxSocketName = "lola"

	// DefaultDetachHint is the human-facing detach hint when no custom
	// [tmux].detach_key is bound: tmux's built-in two-key detach (prefix then d).
	DefaultDetachHint = "Ctrl-b d"

	// TmuxBrand is the fixed brand shown in every lola session's status bar. The
	// per-session label (the Linear issue) is composed with it by the consumer
	// (see SessionChrome).
	TmuxBrand = "LOLA"
)

// TmuxConfig is the [tmux] table.
//
//   - SocketName is the isolated tmux server socket (tmux `-L`); "" resolves to
//     DefaultTmuxSocketName.
//   - DetachKey is an OPT-IN single key (e.g. "F12") bound to detach-client for
//     a friendlier one-press detach. "" keeps tmux's default Ctrl-b d —
//     remapping is a taste choice, so it stays off unless the operator asks.
//   - StatusRight overrides the status bar's right side (a raw tmux status-right
//     format string). "" uses lola's built-in branded format.
//   - Mouse enables tmux mouse mode inside the session. Off by default.
type TmuxConfig struct {
	SocketName  string `toml:"socket_name"`
	DetachKey   string `toml:"detach_key"`
	StatusRight string `toml:"status_right"`
	Mouse       bool   `toml:"mouse"`
}

// DetachHint returns the human-facing key hint for detaching from an attached
// session. When DetachKey is set (a single key like "F12" the operator bound to
// detach-client) that key IS the hint; otherwise it is tmux's default two-key
// sequence, DefaultDetachHint ("Ctrl-b d"). The hint text therefore always
// matches whatever key actually detaches.
func (t TmuxConfig) DetachHint() string {
	if t.DetachKey != "" {
		return t.DetachKey
	}
	return DefaultDetachHint
}

// TmuxSocketName returns the tmux server socket name lola runs its sessions on:
// [tmux].socket_name when set, else DefaultTmuxSocketName. Callers pass this to
// the tmux client's SocketName so lola's sessions stay on their own isolated
// `-L` server. It never returns "".
func (c *Config) TmuxSocketName() string {
	if c.Tmux.SocketName != "" {
		return c.Tmux.SocketName
	}
	return DefaultTmuxSocketName
}

// SessionChrome projects the resolved [tmux] config into the tmux.SessionChrome
// the runtime hands to (*tmux.Client).ConfigureSession — the analog of
// ResolveNotify projecting [notify] into notify.NotifyConfig. It stamps the
// fixed TmuxBrand, the caller-supplied label (the Linear issue identifier/title;
// pass "" when none is known), the operator's status-right override and mouse
// preference, and the opt-in detach key. The tmux layer renders the detach hint
// from DetachKey; DetachHint below is the human-facing equivalent for non-tmux
// surfaces (TUI/notify), kept correct for whatever key resolves.
func (c *Config) SessionChrome(label string) tmux.SessionChrome {
	return tmux.SessionChrome{
		Brand:       TmuxBrand,
		Label:       label,
		StatusRight: c.Tmux.StatusRight,
		DetachKey:   c.Tmux.DetachKey,
		Mouse:       c.Tmux.Mouse,
	}
}

// --- on-disk mirror --------------------------------------------------------

type fileTmuxConfig struct {
	SocketName  *string `toml:"socket_name,omitempty"`
	DetachKey   *string `toml:"detach_key,omitempty"`
	StatusRight *string `toml:"status_right,omitempty"`
	Mouse       *bool   `toml:"mouse,omitempty"`
}

// defaultTmux is the [tmux] default: the isolated "lola" socket, tmux's default
// detach, the built-in branded status bar, mouse off.
func defaultTmux() TmuxConfig {
	return TmuxConfig{SocketName: DefaultTmuxSocketName}
}

// resolveTmux materializes the [tmux] table. A nil (absent) mirror yields the
// defaults (socket "lola", everything else off), so a config with no [tmux]
// table behaves exactly as before. A present table overlays each explicitly-set
// field onto the defaults.
func resolveTmux(ft *fileTmuxConfig) TmuxConfig {
	d := defaultTmux()
	if ft == nil {
		return d
	}
	if ft.SocketName != nil {
		d.SocketName = *ft.SocketName
	}
	if ft.DetachKey != nil {
		d.DetachKey = *ft.DetachKey
	}
	if ft.StatusRight != nil {
		d.StatusRight = *ft.StatusRight
	}
	if ft.Mouse != nil {
		d.Mouse = *ft.Mouse
	}
	return d
}

// tmuxFile builds the on-disk mirror for Save. A zero (unconfigured) table —
// the value a fresh &Config{} carries before load materializes the default —
// returns nil so [tmux] is omitted entirely; otherwise every field is written
// explicitly so the round-trip is exact and an operator's explicit ""/false
// survives.
func tmuxFile(t TmuxConfig) *fileTmuxConfig {
	if t == (TmuxConfig{}) {
		return nil
	}
	return &fileTmuxConfig{
		SocketName:  &t.SocketName,
		DetachKey:   &t.DetachKey,
		StatusRight: &t.StatusRight,
		Mouse:       &t.Mouse,
	}
}
