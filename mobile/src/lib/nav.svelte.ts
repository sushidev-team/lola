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

import type { SheetName } from "./sheets";

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

  /**
   * Which sheet is open, or "".
   *
   * IT LIVES HERE RATHER THAN IN THE SCREEN that draws it, which is a change
   * from three separate `let open = $state(false)` locals. The reason is not
   * tidiness: a sheet only a tap can open is a sheet no script can reach, and
   * the Simulator has no gesture API — so the filter overlay, the connection
   * settings and the terminal's view settings were the three surfaces a
   * reviewer could not photograph at all. Naming the open sheet makes it a
   * place the app can be in, which a development link can then ask for (see
   * devlink.ts). The screens still own the CONTENT; they no longer own the
   * question of whether it is up.
   *
   * Only ever one at a time, which is already true of a modal.
   */
  sheet = $state<SheetName>("");

  /**
   * The pane whose long-press menu is open, for `sheet === "pane"`.
   *
   * It lives beside `sheet` rather than inside the tab strip for the reason
   * `sheet` itself does: a menu only a long press can open is a menu no script
   * can photograph, and the Simulator has no gesture API. With the pane named
   * here, `?sheet=pane` reaches it. An empty value means "the pane the
   * screen is attached to", so a link needs no second field to be useful.
   */
  menuPane = $state("");

  /** Open a sheet by name. */
  openSheet(name: SheetName): void {
    this.sheet = name;
  }

  /** Close whatever is open. Safe when nothing is. */
  closeSheet(): void {
    this.sheet = "";
  }

  toConnect(): void {
    this.screen = "connect";
    this.sheet = "";
  }

  toSessions(): void {
    this.screen = "sessions";
    this.paneSession = "";
    this.pane = "";
    this.menuPane = "";
    // A sheet belongs to the screen it was opened over. Leaving one set across
    // a navigation would pop the terminal's view settings open on the list, or
    // the list's filter sheet over a terminal — the sheets are per-screen and
    // only the screen that draws one can close it.
    this.sheet = "";
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
    this.menuPane = "";
    this.screen = "terminal";
    this.sheet = "";
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
