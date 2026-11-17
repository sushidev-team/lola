import { TermService } from "@bindings/desktop";

// Drop every tmux attachment the PREVIOUS page left open, once per page load.
//
// A web-view reload (⌥⌘R, or every HMR reload under `wails3 dev`) never runs
// LiveTerminal's onDestroy, so the Go side kept those `tmux attach` clients
// alive. The daemon reads any attached client as "a human is at this
// terminal" and defers every automatic prompt to that session (conflict
// fixes, review hand-offs, queued feedback; internal/daemon/autoprompt.go),
// so one reload could silently stop automation for every session ever opened.
//
// It is a shared promise rather than a fire-and-forget call because the
// backend runs each bridge call on its own goroutine: an un-awaited DetachAll
// could land AFTER the first Attach and kill the terminal just opened.
// LiveTerminal awaits this before attaching. A failure is swallowed; Attach is
// exclusive on the Go side anyway, so the worst case is orphans lasting until
// the first terminal opens.
let booted: Promise<void> | undefined;

export function termBoot(): Promise<void> {
  // Inside a .then so even a synchronous throw (a binding that is missing,
  // as in a test mock) lands in the swallow instead of escaping.
  booted ??= Promise.resolve()
    .then(() => TermService.DetachAll())
    .then(
      () => undefined,
      () => undefined,
    );
  return booted;
}
