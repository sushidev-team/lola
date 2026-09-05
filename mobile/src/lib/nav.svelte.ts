// Where the app is. Three screens, four tabs and one selection, which is the
// whole of the navigation.
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

/**
 * A SCREEN is what fills the window; a TAB is which of the four destinations
 * the ordinary screen is showing.
 *
 * The terminal is deliberately NOT a tab. It is a screen PUSHED OVER whichever
 * tab you were on — it is full-screen (its own accessory bar takes the space a
 * tab bar would want, and it owns every touch inside the pane), it belongs to
 * one session rather than to a section of the app, and leaving it has to put
 * you back where you came from. Modelling it as a fifth tab would mean either
 * drawing a tab bar the terminal has no room for, or a tab that silently swaps
 * itself for the sessions list when nothing is attached.
 */
export type Screen = "connect" | "sessions" | "terminal";

/**
 * The bottom bar's four destinations, in the order they are drawn.
 *
 * THE VOCABULARY LIVES HERE for the same reason `sheets.ts` holds the sheet
 * names: a tab is a place the app can BE, so a development link must be able to
 * name one (`?tab=activity`), and the validator and the state have to agree
 * about the spelling. Sheets got their own plain module because `devlink`
 * validates against it and neither module should have to import the other; this
 * list sits in nav instead because nav is where the tab actually lives and a
 * second file for four strings is its own kind of drift. If devlink ever grows
 * a `tab` field it should import `isTab` from here — a plain array and a type
 * guard cost nothing to import, even out of a runes module.
 */
export const TABS = ["sessions", "activity", "projects", "settings"] as const;

/** One of the bottom bar's destinations. */
export type Tab = (typeof TABS)[number];

/**
 * Narrow a string to a tab name, FAILING CLOSED.
 *
 * Anything unrecognised — a typo, a tab a later build added, a value from a link
 * written against a different version of the app — is not a tab, and the caller
 * leaves the current one alone rather than landing somewhere unexpected. Same
 * rule, and the same reasoning, as `isSheetName`.
 */
export function isTab(v: string): v is Tab {
  return (TABS as readonly string[]).includes(v);
}

/**
 * The pickers a project's detail can open over itself.
 *
 * A closed vocabulary, validated the same way the tabs and the sheets are, so a
 * development link naming one that this build has never heard of opens nothing
 * rather than something unexpected.
 */
export const PROJECT_PICKS = ["prs", "tickets"] as const;

/** A picker, or "" for none. */
export type ProjectPick = (typeof PROJECT_PICKS)[number] | "";

/** Narrow a string to a picker name, FAILING CLOSED. */
export function isProjectPick(v: string): v is Exclude<ProjectPick, ""> {
  return (PROJECT_PICKS as readonly string[]).includes(v);
}

class Nav {
  screen = $state<Screen>("connect");

  /**
   * Which of the four bottom-bar destinations the `sessions` screen is showing.
   *
   * It is a SEPARATE axis from `screen` rather than four more members of that
   * union, because the terminal has to be able to sit on top of any of them and
   * hand the same one back on the way out. Folding the two together would make
   * "which tab was I on" a thing `back()` has to remember, and a remembered
   * value is exactly what goes stale.
   */
  tab = $state<Tab>("sessions");

  /**
   * Which project the PROJECTS TAB is drilled into, or "" for its list.
   *
   * A THIRD AXIS RATHER THAN A FOURTH SCREEN, and the reason is the tab bar. A
   * project's detail is a place INSIDE the Projects tab — the bar stays drawn,
   * the tab stays lit, and switching to Sessions and back should return here
   * rather than to the list. A `Screen` member would have taken the bar away
   * (the terminal is a screen precisely because it needs the space) and made
   * "which tab was I on" something `back()` has to remember, which is the
   * staleness this file already refuses for the tab itself.
   *
   * It is a project NAME, not an index or a reference: `Name` is identity in
   * this repository — the path segment, the session id prefix, the thing every
   * session carries in its `project` field — so a name that no longer resolves
   * (a project removed on the Mac between two pushes) renders the list's own
   * "not found" rather than a stale object nothing can refresh.
   */
  project = $state("");

  /**
   * The picker open over a project's detail, or "".
   *
   * Two of the detail's actions are "choose one of a list the daemon fetches"
   * — an open pull request, a Linear issue — and each is a full screen's worth
   * of rows on a phone. They are named here for the same reason `sheet` is:
   * a screen only a tap can reach is a screen no script can photograph, and the
   * Simulator has no gesture API. `?tab=projects&project=nori-app&pick=prs`
   * lands on one.
   */
  pick = $state<ProjectPick>("");

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

  /**
   * Show the tab shell — the ordinary, non-terminal screen.
   *
   * IT DOES NOT RESET THE TAB, and that is the whole of the terminal's back
   * behaviour: `back()` and the terminal's own back button both land here, and
   * a reset would drop somebody who opened a pane from the Projects tab onto
   * Sessions instead of returning them where they were. The name is historical
   * — it predates the tabs, it is called from four places, and "toSessions"
   * describes what the user sees on a fresh boot because `tab` starts on
   * `sessions`. Use `toTab("sessions")` when the sessions LIST is what is
   * actually wanted.
   */
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
   * Switch the bottom bar to a tab, leaving whatever is behind it alone.
   *
   * It sets the screen too, so a caller never has to pair two calls: every
   * present way to reach a tab is the bar itself, which is only drawn on the
   * tab shell, but a project row that filters the list and then switches tabs
   * is one call rather than two, and a link that names a tab lands on it from a
   * cold start.
   *
   * The sheet is closed for the same reason `toSessions` closes it: a sheet
   * belongs to the screen it was opened over, and only that screen can close
   * it. The terminal's attachment (`paneSession`/`pane`) is deliberately NOT
   * cleared — moving between tabs is not leaving the terminal, and the pane the
   * user was in is still theirs to come back to.
   */
  toTab(tab: Tab): void {
    this.tab = tab;
    this.screen = "sessions";
    this.sheet = "";
  }

  /**
   * Drill the Projects tab into one project, or back out of it.
   *
   * Passing "" is the way back to the list, and it clears the picker with it —
   * a picker belongs to the project it was opened over, exactly as a sheet
   * belongs to its screen.
   */
  toProject(name: string): void {
    this.project = name;
    this.pick = "";
    this.tab = "projects";
    this.screen = "sessions";
    this.sheet = "";
  }

  /** Open one of a project's pickers, or close it with "". */
  toPick(pick: ProjectPick): void {
    this.pick = pick;
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

  /**
   * The one back action. Every place except `connect` has exactly one parent.
   *
   * ORDERED FROM THE DEEPEST OUT, because the Projects tab now stacks: a picker
   * sits over a project's detail, which sits over the project list, which is a
   * tab. A terminal sits over all of it and is checked first — it is a SCREEN,
   * so it covers whatever tab state is underneath and has to hand it back
   * untouched, which is why this branch does not clear `project` or `pick`.
   */
  back(): void {
    if (this.screen === "terminal") this.toSessions();
    else if (this.pick !== "") this.pick = "";
    else if (this.project !== "") this.project = "";
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
