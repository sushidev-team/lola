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

## Project label vs id

The Repo tab of the project overlay has two name fields, and they behave very
differently.

- **Label** — free text (`Nori App`), shown everywhere in the app. Nothing keys
  by it, so renaming it is an ordinary `ConfigService.SaveProject`, safe at any
  time. `displayName()` in `lib/slug.ts` applies the "label, else id" fallback;
  `store.displayNameFor(id)` does the same from an id.
- **ID** — the `[[project]].name`: a path segment (`worktrees/<id>/`,
  `state/<id>.seen`) and the prefix of every session and tmux name. It is slugged
  as you type (`slugTyping`, which deliberately does not trim so a hyphen can be
  entered), and on a NEW project it is derived from the label until you type an
  id yourself.

Changing the ID is a **rename**, not a field edit. The form calls
`DaemonService.RenameProject(from, to)` *before* `SaveProject`, because the
daemon is the only thing that knows whether a session still holds the old id — it
refuses while any session or leftover worktree does, then migrates the seen file
and reloads. A refusal aborts the whole save; the fields are never written
against a stale id.

`lib/slug.ts` is a deliberate duplicate of Go's `config.Slug`/`SlugTyping` (a
Wails round-trip per keystroke would make the field feel dead). Go stays the
authority — `SaveProject` re-slugs whatever arrives — so drift can only make the
preview disagree, never write an unsafe name. `slug.test.ts` mirrors the Go test
case for case; change both together.
