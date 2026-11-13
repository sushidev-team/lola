import { SIDEBAR_DEFAULT, SIDEBAR_MIN, SIDEBAR_MAX } from "./sidebarwidth";
// Navigation state: which top-level view is showing, the project scope, the
// active modal overlay, and the active session in the full-height workspace.

export type View = "cockpit" | "home" | "detail" | "prpicker" | "ticketpicker";
// There is no separate "poll" overlay any more: a project IS the poll unit, so
// the project overlay covers repo setup, filter, labels and write-back in tabs.
export type Overlay = null | "doctor" | "settings" | "project" | "setup" | "update" | "help" | "bestof" | "newsession" | "commands" | "rename-session" | "activity" | "sshsession" | "sshconnect" | "pr-review";

/** Webview-local sidebar preference. Deliberately NOT in config.toml. */
const SIDEBAR_KEY = "lola.sidebar";
/** Which of the sidebar's two tabs is on show. */
const SIDEBAR_TAB_KEY = "lola.sidebar.tab";
const PROJECT_TAB_KEY = "lola.project.tab";
/** When each session was last on screen, so a finished turn can read as not yet seen. */
const SEEN_KEY = "lola.seen";
const SEEN_KEEP = 200;

export type ProjectTab = "overview" | "git";

function readProjectTab(): ProjectTab {
  try { return localStorage.getItem(PROJECT_TAB_KEY) === "git" ? "git" : "overview"; }
  catch { return "overview"; }
}

export type SidebarTab = "sessions" | "projects";

function readSidebarWidth(): number {
  try {
    const value = Number(localStorage.getItem("lola.sidebar.width") ?? SIDEBAR_DEFAULT);
    return Number.isFinite(value) ? Math.max(SIDEBAR_MIN, Math.min(SIDEBAR_MAX, value)) : SIDEBAR_DEFAULT;
  } catch { return SIDEBAR_DEFAULT; }
}

function readSidebarTab(): SidebarTab {
  try {
    return localStorage.getItem(SIDEBAR_TAB_KEY) === "projects" ? "projects" : "sessions";
  } catch {
    return "sessions";
  }
}

function readSeen(): Record<string, number> {
  try {
    const raw: unknown = JSON.parse(localStorage.getItem(SEEN_KEY) ?? "{}");
    return raw && typeof raw === "object" && !Array.isArray(raw) ? (raw as Record<string, number>) : {};
  } catch {
    return {};
  }
}

function readSidebar(): boolean {
  try {
    return localStorage.getItem(SIDEBAR_KEY) !== "0";
  } catch {
    return true; // storage unavailable → the sidebar is the default state
  }
}

class Nav {
  sessionLocation = $state<"worktree" | "main" | "temporary">("worktree");
  sessionAgent = $state(true);
  newSession(project = "", location: "worktree" | "main" | "temporary" = "worktree", agent = true) {
    this.sessionLocation = project ? location : "temporary";
    this.sessionAgent = project ? agent : false;
    this.openOverlay("newsession", project);
  }

  gitSearchRequest = $state(0);
  view = $state<View>("cockpit");
  private preferredProjectTab = $state<ProjectTab>(readProjectTab());
  get projectTab(): ProjectTab { return this.preferredProjectTab; }
  set projectTab(tab: ProjectTab) {
    this.preferredProjectTab = tab;
    try { localStorage.setItem(PROJECT_TAB_KEY, tab); } catch { /* keep the in-memory choice */ }
  }
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
  /** The active session; independent of keyboard focus and never a layout mode. */
  focusedTerm = $state<string>("");
  sidebarBrowsing = $state(false);
  sidebarQuery = $state("");
  sidebarFocus = $state(0);
  sidebarWidth = $state(readSidebarWidth());
  setSidebarWidth(width: number) {
    this.sidebarWidth = Number.isFinite(width) ? Math.max(SIDEBAR_MIN, Math.min(SIDEBAR_MAX, width)) : SIDEBAR_DEFAULT;
    try { localStorage.setItem("lola.sidebar.width", String(this.sidebarWidth)); } catch { /* best effort */ }
  }
  focusSessions() {
    this.sidebarBrowsing = true;
    this.sidebarOpen = true;
    try { localStorage.setItem(SIDEBAR_KEY, "1"); } catch { /* best effort */ }
    this.setSidebarTab("sessions");
    this.sidebarFocus++;
  }
  /**
   * Sidebar visible. Webview-local UI state — NOT a config.toml key; the daemon
   * and the TUI never learn about it. Mirrored to localStorage so the choice
   * survives a relaunch, best-effort: a webview preference is never worth
   * throwing over.
   */
  sidebarOpen = $state(readSidebar());
  /** The sidebar's tab. Projects is today's body, unchanged. */
  sidebarTab = $state<SidebarTab>(readSidebarTab());
  /** Sidebar attention filter. Empty means all sessions. */
  triage = $state<string>("");

  toggleSidebar() {
    this.sidebarOpen = !this.sidebarOpen;
    try {
      localStorage.setItem(SIDEBAR_KEY, this.sidebarOpen ? "1" : "0");
    } catch {
      /* private mode / disabled storage — the preference just doesn't persist */
    }
  }
  setSidebarTab(t: SidebarTab) {
    this.sidebarTab = t;
    try {
      localStorage.setItem(SIDEBAR_TAB_KEY, t);
    } catch {
      /* the choice just doesn't persist */
    }
  }
  private seen = $state<Record<string, number>>(readSeen());
  /** When the human last had this session open, 0 for never. */
  seenAt(id: string): number {
    return this.seen[id] ?? 0;
  }
  /** Most recent last, so what gets dropped is what nobody has opened in a while. */
  markSeen(id: string, at = Date.now()) {
    const next = { ...this.seen };
    delete next[id];
    next[id] = at;
    for (const old of Object.keys(next).slice(0, -SEEN_KEEP)) delete next[old];
    this.seen = next;
    try {
      localStorage.setItem(SEEN_KEY, JSON.stringify(next));
    } catch {
      /* a mark that does not survive a relaunch costs one dot */
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
    this.setSidebarTab("sessions");
  }
  goHome() {
    this.view = "home";
    this.dropFocus();
  }
  goDetail(name: string, tab: ProjectTab = this.projectTab) {
    this.project = name;
    this.projectTab = tab;
    // Project navigation is a workspace destination, not a session filter.
    this.scoped = false;
    this.view = "detail";
    this.dropFocus();
    this.setSidebarTab("projects");
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

  select(id: string) { this.selectedId = id; }

  /** Open without changing filters or recreating the session workspace. */
  openSession(id: string, keepSidebarFocus = false) {
    this.sidebarBrowsing = keepSidebarFocus;
    this.selectedId = id;
    this.focusedTerm = id;
    this.view = "cockpit";
  }

  /** Release the active session while another top-level view covers it. */
  private dropFocus() {
    this.focusedTerm = "";
  }
}

export const nav = new Nav();
