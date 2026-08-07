// Navigation state: which top-level view is showing, the project scope, the
// active modal overlay, and the selected session / terminal lens. Mirrors the
// TUI's rootModel.view + overlay precedence, extended with a "grid" lens for the
// new live-terminal overview.

export type View = "cockpit" | "home" | "detail" | "prpicker" | "ticketpicker";
// There is no separate "poll" overlay any more: a project IS the poll unit, so
// the project overlay covers repo setup, filter, labels and write-back in tabs.
export type Overlay = null | "doctor" | "settings" | "project" | "setup" | "update" | "help";
export type Lens = "list" | "kanban" | "grid";

/** Webview-local sidebar preference. Deliberately NOT in config.toml. */
const SIDEBAR_KEY = "lola.sidebar";

function readSidebar(): boolean {
  try {
    return localStorage.getItem(SIDEBAR_KEY) !== "0";
  } catch {
    return true; // storage unavailable → the sidebar is the default state
  }
}

class Nav {
  view = $state<View>("cockpit");
  /** Project name backing detail/pickers and the cockpit's project scope. */
  project = $state<string>("");
  /** Cockpit is scoped to `project` (vs global) when true. */
  scoped = $state(false);
  overlay = $state<Overlay>(null);
  /** The project whose form overlay is open. */
  overlayProject = $state<string>("");
  /** Tab the overlay should open on ("" = the overlay's own default). */
  overlayTab = $state<string>("");
  /** Selected session id (reorder-proof selection). */
  selectedId = $state<string>("");
  /** Sessions-panel lens. */
  lens = $state<Lens>("list");
  /** The session whose live terminal is expanded/focused ("" = none). */
  focusedTerm = $state<string>("");
  /**
   * Lens to return to when the focused terminal closes ("" = stay put).
   *
   * The grid lens cannot host a terminal (see setLens), so opening one from a
   * tile has to switch to the list first. Without this the trip was one-way:
   * minimizing dropped you in the list lens you never chose, and the grid you
   * were reading — the whole point of the click — was gone.
   */
  returnLens = $state<Lens | "">("");
  /**
   * Sidebar visible. Webview-local UI state — NOT a config.toml key; the daemon
   * and the TUI never learn about it. Mirrored to localStorage so the choice
   * survives a relaunch, best-effort: a webview preference is never worth
   * throwing over.
   */
  sidebarOpen = $state(readSidebar());
  /**
   * Active triage filter — "" = all. One of $lib/filters' TRIAGE_FILTERS, i.e. a
   * KANBAN_COLUMNS title, so the sidebar filter and the kanban lens partition
   * sessions the same way.
   */
  triage = $state<string>("");

  toggleSidebar() {
    this.sidebarOpen = !this.sidebarOpen;
    try {
      localStorage.setItem(SIDEBAR_KEY, this.sidebarOpen ? "1" : "0");
    } catch {
      /* private mode / disabled storage — the preference just doesn't persist */
    }
  }
  setTriage(t: string) {
    this.triage = t;
  }

  /**
   * Deliberately does NOT reset `triage`: the project scope and the triage
   * filter compose, so switching projects keeps "show me only what needs me".
   * Escape unwinds them one at a time (App.svelte's key handler).
   */
  goCockpit(scopeProject = "") {
    this.view = "cockpit";
    this.scoped = scopeProject !== "";
    this.project = scopeProject;
    this.dropFocus();
  }
  /**
   * What a sidebar project row does when clicked. Clicking the project the
   * cockpit is ALREADY scoped to drops the scope — the same destination as the
   * "All" breadcrumb. Without it the project list was the one nav group with no
   * way back to every-project: Triage carries an explicit "All sessions" row,
   * Projects does not, so the only exits were the breadcrumb or Escape.
   *
   * The toggle fires ONLY while the scope is actually in effect on screen. From
   * the project hub or the home list a row is still drawn active (`scoped`
   * outlives `goDetail`), and a click there has to mean "take me to this
   * project's sessions" — un-scoping would send you somewhere you never asked
   * for and look like the click was swallowed.
   */
  toggleProjectScope(name: string) {
    const clearing = this.view === "cockpit" && this.scoped && this.project === name;
    this.goCockpit(clearing ? "" : name);
  }
  goHome() {
    this.view = "home";
    this.dropFocus();
  }
  goDetail(name: string) {
    this.project = name;
    this.view = "detail";
    this.dropFocus();
  }
  goPRPicker(name: string) {
    this.project = name;
    this.view = "prpicker";
    this.dropFocus();
  }
  goTicketPicker(name: string) {
    this.project = name;
    this.view = "ticketpicker";
    this.dropFocus();
  }

  openOverlay(o: Overlay, project = "", tab = "") {
    this.overlay = o;
    this.overlayProject = project;
    this.overlayTab = tab;
  }
  closeOverlay() {
    this.overlay = null;
    this.overlayProject = "";
    this.overlayTab = "";
  }

  select(id: string) {
    this.selectedId = id;
  }
  /**
   * Switch lens. Always go through here rather than assigning `lens`: the grid
   * lens renders NO detail panel, so a `focusedTerm` surviving into it would
   * leave the app with no mounted terminal to own the keyboard — and the global
   * key handler bails out early while `focusedTerm` is set, wedging every
   * shortcut including the one that would clear it.
   */
  setLens(l: Lens) {
    this.lens = l;
    // A lens picked BY HAND is where the user wants to be, so it also cancels
    // any pending return — otherwise closing a terminal later would yank them
    // back to a lens they had since left.
    if (l === "grid") this.dropFocus();
    else this.returnLens = "";
  }
  cycleLens() {
    this.setLens(this.lens === "list" ? "kanban" : this.lens === "kanban" ? "grid" : "list");
  }
  toggleFocusTerm(id: string) {
    if (this.focusedTerm === id) {
      // Closing: hand the lens back to whoever opened the terminal.
      this.focusedTerm = "";
      if (this.returnLens) {
        this.lens = this.returnLens;
        this.returnLens = "";
      }
      return;
    }
    // Focusing from the grid has to leave it first — see setLens — so remember
    // where to land when it closes.
    if (this.lens === "grid") {
      this.returnLens = "grid";
      this.lens = "list";
    }
    this.focusedTerm = id;
  }

  /** Clear the focused terminal AND any lens it owed a return to. */
  private dropFocus() {
    this.focusedTerm = "";
    this.returnLens = "";
  }
}

export const nav = new Nav();
