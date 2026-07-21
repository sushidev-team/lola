// Navigation state: which top-level view is showing, the project scope, the
// active modal overlay, and the selected session / terminal lens. Mirrors the
// TUI's rootModel.view + overlay precedence, extended with a "grid" lens for the
// new live-terminal overview.

export type View = "cockpit" | "home" | "detail" | "prpicker" | "ticketpicker";
// There is no separate "poll" overlay any more: a project IS the poll unit, so
// the project overlay covers repo setup, filter, labels and write-back in tabs.
export type Overlay = null | "doctor" | "settings" | "project" | "setup" | "update";
export type Lens = "list" | "kanban" | "grid";

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

  goCockpit(scopeProject = "") {
    this.view = "cockpit";
    this.scoped = scopeProject !== "";
    this.project = scopeProject;
    this.focusedTerm = "";
  }
  goHome() {
    this.view = "home";
    this.focusedTerm = "";
  }
  goDetail(name: string) {
    this.project = name;
    this.view = "detail";
    this.focusedTerm = "";
  }
  goPRPicker(name: string) {
    this.project = name;
    this.view = "prpicker";
    this.focusedTerm = "";
  }
  goTicketPicker(name: string) {
    this.project = name;
    this.view = "ticketpicker";
    this.focusedTerm = "";
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
  cycleLens() {
    this.lens = this.lens === "list" ? "kanban" : this.lens === "kanban" ? "grid" : "list";
  }
  toggleFocusTerm(id: string) {
    this.focusedTerm = this.focusedTerm === id ? "" : id;
  }
}

export const nav = new Nav();
