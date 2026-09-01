# Hand-off — the lola mobile app

Written at the end of the session that built milestone 1. It records the things
that are true about this branch, the traps that each cost a debugging session,
and what is genuinely unverified. `mobile/PLAN.md` is the specification and
holds the reasoning behind every settled decision; this file is the shorter
thing you read first.

## Where the work is

Branch `feature/mobile-m1-byte-pipe`, unpushed. Milestone 1 works end to end: a
phone connects over TLS, renders the live session list, and drives terminals.

The daemon half lives in `internal/remote` (TLS listener, frame loop,
authorization boundary), `internal/panebus` (one tmux attach per pane, fanned
out to N subscribers), `internal/protocol` (the wire types) and
`internal/daemon/panes.go`, `remotewire.go`, `pairbegin.go`. The client is
`mobile/` — Capacitor 8 hosting the desktop's Svelte components behind a shim in
`mobile/src/wailsshim/`, with a native transport plugin in
`mobile/plugins/lola-transport/`.

## Environment traps

**A second agent was editing this working tree, and one commit paid for it.**
While milestone 1 was being built, another agent refactored `internal/state`,
`internal/tui` and `desktop/frontend/src/lib` and added `internal/agentlog`,
`internal/state/display.go` and the doctor's breaker report. That work has since
been committed and the tree is one agent's again — but `c96b62f` was made while
it was not, and it swept in a `internal/daemon/server.go` that wrote a
`session.Nudged` field the same commit did not carry. HEAD stopped compiling and
nobody noticed for two commits, because the WORKING tree had the field and every
local build was green.

Two habits follow from that, and they are cheap:

- Stage explicit paths, never `git add -A`, whenever anyone else may be working
  here.
- Check that the COMMIT compiles, not just the tree:
  `T=$(mktemp -d) && git archive HEAD | tar -x -C "$T" && go build -C "$T" ./...`

**The daemon binary must be built with `-tags lola_insecure`.** Without the tag
there is no phone listener at all, and the failure is quiet: the desktop's
Remote tab reports "this binary cannot hand out a connect code" and nothing
listens on 7717. Worse, `make build` alone never reaches the running daemon —
the TUI's `^r`, the desktop app's restart button and a hand-started `lola run`
all resolve `lola` from `PATH`, normally `$GOPATH/bin/lola`. Use:

    make daemon          # installs the tagged build of committed HEAD, LAN-reachable
    make mobile-lan      # the same, but from the WORKING TREE
    make mobile-info     # host, port, key and SPKI pin of the RUNNING listener

Use `make daemon` unless the change you are testing is uncommitted: it builds
`git archive HEAD`, so nothing half-finished beside it reaches the operator's
daemon. `make mobile-lan` installs the working tree, which is what you want
while iterating on your own change and nothing else.

`make mobile-dev --lan` does not work and never did: `make` claims a leading
`--` for its own options. The script's flags do combine, so
`contrib/lola-mobile-dev.sh --lan --head` is what `make daemon` runs.

If the installed binary is ever replaced by an untagged one, rebuild from
**committed HEAD** rather than the working tree, so you do not ship another
agent's in-progress work into the operator's daemon:

    T=$(mktemp -d) && git archive HEAD | tar -x -C "$T" && cd "$T" && \
      GOCACHE=<repo>/.gocache GOFLAGS='-mod=mod -buildvcs=false' \
      go build -tags lola_insecure -o "$T/lola" .

**Reaching the daemon from a phone needs two config keys, not one.**
`[remote].bind = "lan"` and `[remote].insecure_lan = true`. Either alone binds
loopback. The pairing was originally an environment variable and that was wrong:
the daemon is normally started by the TUI or the desktop app, neither of which
can set one, so the opt-in was lost on every restart and the listener silently
fell back to loopback.

## How to verify a mobile change

**Never use synthetic OS input.** No CGEvent posting, no `osascript` clicks or
keystrokes, no `cliclick`, no Accessibility driving, nothing that moves the real
pointer or focuses a window. `simctl` has no gesture API and that is not a
problem to route around: the operator works on this machine, and doing it stole
their keyboard focus mid-typing and leaked a click into an unrelated
application's window. It is also unreliable — CGEvents are dropped without a TCC
grant, so the usual outcome is disruption and no test. `CLAUDE.md` carries the
rule and the four alternatives:

- **Component tests** — `mobile/` runs vitest with @testing-library. `fireEvent`
  drives a real interaction with no device and no pointer. Behaviour goes here.
- **`xcrun simctl io <udid> screenshot`** for appearance. Read-only.
- **Launch-environment deep links** to REACH a screen instead of tapping to it:
  `mobile/scripts/dev-link.sh`, which carries an optional pane target and a
  sheet target for exactly this purpose. If a screen is unreachable by link, add
  a link target — never add a tap.
- **A browser harness** at phone viewport when a gesture must genuinely be
  exercised.

If none of those can verify something, say so and leave it for a human. An
unverified claim is far cheaper than a hijacked machine.

## Invariants that are easy to get wrong

**Releasing a pinned tmux window size takes two commands.** Unsetting
`window-size` does nothing at all — tmux leaves the window at whatever it was
last told. `resize-window -A` is what makes it recompute from attached clients.
Miss the second and a phone that pinned and disconnected leaves the developer's
window squashed indefinitely. Verified on an isolated tmux 3.7 server before the
feature was written.

**A tmux session name is a pane's identity.** The daemon anchors on it
(`resolvePaneName`), parses the shell index out of it (`shellIndex`) and matches
its suffix at teardown. A pane rename is therefore a display-only label stored
on the device, never a tmux rename.

**The agent pane can be resized but never closed.** It IS the session; closing
it would end the work and leave a record pointing at nothing. Ending a session
is `cmd=kill`, which takes the worktree and branch too. Resizing it, by
contrast, is the entire point of the pin feature.

**Prefix matching must be anchored at both ends.** `lola-fe-42` is a prefix of
`lola-fe-420-shell-1`, so a loose test lets one session close, resize or adopt
another's tab.

**`PaneResizeData.pinned` is the only field that says which way a call went.**
`cols`/`rows` are `omitempty` in Go, so a release answers with both absent, and
inferring direction from their presence reads a release as a pin.

**`paneClose`/`paneResize` take a nested `args` object**; `panes`/`shellCreate`
take flat top-level fields. That follows their Go handler signatures.

**A release of a pane that no longer exists is SUCCESS.** Identity is checked by
name, not liveness, so releasing a just-closed pane reaches tmux and is refused
— which a client cannot distinguish from a release that did not land. Only
releases forgive, and only when the pane is genuinely absent, so the stuck-pin
warning keeps meaning something.

**A refused PIN, by contrast, means the window was never touched — and both
halves have to hold that up.** `SetWindowSize` sets `window-size manual` and
then resizes, and the option alone already stops tmux recomputing the window
from its clients, so a resize that fails after it leaves the window pinned with
nobody holding the pin. It therefore undoes its own option before returning the
error. That is what lets `mobile/src/lib/panepin.ts` treat a `DaemonError` — a
refusal the daemon actually answered — as proof that nothing landed and stop
believing it holds the pane. Every other failure (a timeout, a dead socket) is
still assumed to have landed, because that is the direction that costs a
redundant release rather than somebody's squashed window.

This was found the way these things are: a phone running a build newer than the
daemon it talked to. Every `cmd=paneResize` came back `unknown cmd`, so nothing
was ever pinned — and the app raised the stuck-pin banner anyway, in the one
situation where it could not be true. A daemon that predates a command answers
plainly; check what the running one actually carries before debugging the
client:

    printf '{"cmd":"paneResize","args":{"session":"x","pane":"x","cols":80,"rows":24}}\n' | nc -U ~/.lola/lola.sock

## What is verified, and what is not

Proven against a real device or the live daemon: connecting, Keychain
persistence across a process death, reconnect on foreground, walking the
daemon's offered addresses, LAN binding, and rebinding when the machine changes
network.

**Never exercised, and all three need hardware:**

- **The QR scanner.** The Simulator has no camera, so `scanCapability()`
  correctly reports `no_camera` and the connect screen hides the button. The
  whole scan path is unrun.
- **The long press on glass** — the hold, the drag-cancel against WebKit's
  scroll takeover, and the click suppression. jsdom has no `PointerEvent`.
- **The pin actually reflowing a real tmux window** and handing the size back.
  Enabling it against the operator's Mac squashes a live window to phone width,
  so no unattended run should turn it on.

## Security posture, stated plainly

Milestone 1 authenticates a phone with a single shared bearer key, generated and
persisted at `~/.lola/remote.key` (0600). There are no capability tiers: every
paired device gets everything. What contains this is the build tag — none of the
insecure path exists in a release binary, and `go tool nm` on an untagged build
finds no `insecureAuthorizer`, `handlePairBegin` or `RegenerateInsecureKey`.

`cmd=shellCreate` is reachable by any paired device and starts a shell in a
worktree, which runs as the developer with their `gh` token and SSH agent in
reach. That is arbitrary code execution on the Mac, initiated from a phone. It
was accepted deliberately (see PLAN.md, "Settled since drafting") and it is the
first command that should sit behind the `shell` capability when tiers exist.

Regenerating the key from the desktop's Remote tab is the only revocation this
milestone has, and it is blunt: every paired phone loses access at once.

## Next

**Milestone 2** replaces the shared key with per-device identities, mutual TLS,
the three-emoji short authentication string and real per-device revocation. It
deletes the entire `lola_insecure` path — the bearer key, the forced-loopback
rail, `insecure_lan`, `regenerateRemoteKey` and the dev URL scheme all go with
it. At that point binding to a LAN is unremarkable and needs no opt-in.

Read `mobile/PLAN.md` before starting it. Its "Open questions" section lists
what still needs a human decision, and its threat subsection is the baseline any
change to the pairing design has to argue against.
