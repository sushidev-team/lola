// Where the app is. Three screens and one selection, which is the whole of M1's
// navigation.
//
// DELIBERATELY NOT the desktop's `$lib/nav.svelte`. That module models a
// two-pane cockpit — a project scope, a triage lens, a focused terminal that can
// be toggled beside a list, keyboard movement through rows — and every one of
// those concepts assumes two things visible at once. A phone shows one screen,
// and "go back" is a real, first-class operation there in a way it never is on a
// desktop. Reusing it would mean carrying five fields that are always the same
// value and re-deriving the one that matters.
//
// The triage filter IS shared, because that is a data question rather than a
// layout one: `$lib/filters`'s TRIAGE_FILTERS is derived from theme.ts's
// KANBAN_COLUMNS, which is a port of Go's state.KanbanColumns(). A second
// partition on the phone would be a third mirror of the same list.

export type Screen = "connect" | "sessions" | "terminal";

class Nav {
  screen = $state<Screen>("connect");

  /** The session whose terminal is open, or "". */
  paneSession = $state("");

  /** Which tmux pane that screen is attached to. Normally the session's own. */
  pane = $state("");

  /**
   * The triage filter, "" for everything. Values come from $lib/filters's
   * TRIAGE_FILTERS so the phone's chips and the desktop's lens are the same
   * partition by construction.
   */
  triage = $state("");

  /** Free-text filter over issue key, title and project. */
  query = $state("");

  toConnect(): void {
    this.screen = "connect";
  }

  toSessions(): void {
    this.screen = "sessions";
    this.paneSession = "";
    this.pane = "";
  }

  /**
   * Open a terminal.
   *
   * `pane` is the tmux target and defaults to the session's own. It is passed
   * explicitly because a session owns several panes — `<id>-shell-N`,
   * `<id>-review`, `<id>-dev-N` — and M3 will offer them; M1 only ever opens the
   * agent's.
   */
  toTerminal(sessionId: string, pane: string): void {
    this.paneSession = sessionId;
    this.pane = pane;
    this.screen = "terminal";
  }

  /** The one back action. Every screen except `connect` has exactly one parent. */
  back(): void {
    if (this.screen === "terminal") this.toSessions();
    else this.toConnect();
  }
}

export const nav = new Nav();

/**
 * The pane name for a session.
 *
 * Mirrors the daemon's own `paneTarget`: the tmux session name when one
 * correlates, and the session id otherwise. The daemon re-resolves this against
 * its session store before it execs anything — the name reaches a tmux argv, so
 * it is never trusted from a client — but sending the right one means the
 * difference between a stream and an `unknown_pane` refusal.
 */
export function paneNameFor(s: { id: string; tmuxName?: string }): string {
  return s.tmuxName || s.id;
}
