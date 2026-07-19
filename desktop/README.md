# lola-desktop

A native macOS companion to the lola TUI, built with **Wails 3 + Svelte 5 +
Tailwind v4 + xterm.js**. It renders the same flight-deck the TUI does — projects,
sessions, triage, activity, PR/ticket pickers, config editors — plus a
**live-terminal overview grid** that shows every agent session as a small,
refreshing terminal at a glance, and lets you expand any one into a full,
interactive xterm.

It is a **client of the daemon**, exactly like the TUI: the Go backend speaks the
same `~/.lola/lola.sock` protocol (`internal/protocol`) and drives tmux
(`tmux -L lola`) directly for terminal streaming. Run it alongside the TUI or on
its own — they observe and drive the same sessions.

## Layout

```
desktop/
├── main.go            # Wails app: window, services, live push loop
├── client.go          # daemon socket round-trip (internal/protocol)
├── daemonsvc.go       # DaemonService — every daemon command, + daemon lifecycle
├── termsvc.go         # TermService — tmux snapshots (grid) + live PTY attach (focus)
├── configsvc.go       # ConfigService — read/write config.toml (settings/project/poll)
├── doctorsvc.go       # DoctorService — internal/doctor health checks
├── *_test.go          # Go backend tests (fake-socket daemon, config helpers)
└── frontend/
    ├── src/
    │   ├── App.svelte           # shell: title bar, view router, overlays, global keys
    │   ├── lib/
    │   │   ├── store.svelte.ts  # reactive state fed by push events + action wrappers
    │   │   ├── nav.svelte.ts     # view / overlay / selection / lens state
    │   │   ├── theme.ts          # status → color/pill/badge (ported from theme.go)
    │   │   ├── ansi.ts           # ANSI-SGR → HTML for the snapshot tiles
    │   │   ├── components/       # Panel, Modal, StatusPill, Meter, terminals, …
    │   │   └── views/            # Cockpit, Home, ProjectDetail, PRPicker, …
    │   └── app.css               # Tailwind v4 + theme tokens
    └── bindings/                 # generated TS bindings (wails3 generate bindings)
```

The app is a package **inside** the lola Go module (not a separate module) so it
can reuse `internal/protocol`, `internal/config`, `internal/doctor`, etc.

## Develop

```sh
cd desktop
wails3 dev            # Vite HMR + Go rebuild/live-reload
```

`wails3 dev` runs from your shell, so `tmux`/`git`/`gh`/`lola` resolve from your
normal PATH. (The bundled `.app` inherits a minimal PATH from Finder; `main.go`'s
`ensurePATH` prepends `/opt/homebrew/bin` and `/usr/local/bin` to fix that.)

Regenerate the TS bindings after changing a Go service signature:

```sh
cd desktop && wails3 generate bindings -ts -d frontend/bindings
```

## Build

```sh
cd desktop
wails3 task build      # compiles the binary
wails3 task package    # → bin/lola.app  (ad-hoc signed, fine for local use)
```

## Test

```sh
# Go backend (from the repo root, Makefile env)
GOCACHE=$PWD/.gocache GOFLAGS='-mod=mod -buildvcs=false' go test ./desktop/

# Frontend
cd desktop/frontend && npm test        # vitest
npm run check                          # svelte-check
```

## Terminal rendering — why two paths

Browsers cap live WebGL contexts at ~16 per page, so we cannot give 40 sessions a
WebGL terminal. The app tiers it:

- **Overview grid** — read-only `tmux capture-pane -e` snapshots (`TermService.
  CaptureMany`), rendered as styled DOM via `ansi.ts`. Cheap; scales to dozens.
- **Focused terminal** — a real interactive `tmux attach` in a PTY, streamed as
  coalesced base64 chunks on `pty:<id>` into a single xterm.js **WebGL** instance.

Snapshots never attach a tmux client, so they can't resize or disturb a running
agent; only the focused terminal does.

## Known gaps

- The **poll editor** takes raw Linear UUIDs (paste from Linear) rather than the
  TUI's live cascading team→project→cycle→state→label pickers. Those pickers need
  a Linear-metadata service and are a follow-up.
- No **first-run setup wizard** yet: create the config with `lola setup` (or the
  TUI) first. The app shows a daemon-down banner and empty state until then.
