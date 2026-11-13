// The SSH workspace's client state: the timeline a session has fetched, plus the
// view choices that must survive leaving the tab (filter, drafts). Keyed by
// session id, so coming back to a session is instant and two sessions never share
// a draft. Every daemon call goes through here; components read and call.
//
// Nothing here decides policy. The daemon re-checks every action, and a refusal
// comes back as an error that is shown in lola's own sentence.

import type { Attachment } from "./composerpaste";
import { SvelteMap, SvelteSet, createSubscriber } from "svelte/reactivity";
import { SSHService } from "@bindings/desktop";
import { store, type SessionInfo } from "./store.svelte";
import { nav } from "./nav.svelte";
import { confirm } from "./confirm.svelte";
import { terms, AGENT, CHECKPOINTS, DIFF, SSH, isChangesTab, isViewTab } from "./terms.svelte";
import { changesOffered, conversationOffered, stripCells, type StripCell } from "./sessionview";
import { landingFor, nextWaiting } from "./queue";
import { checkpoints } from "./checkpoints.svelte";
import { AGENT_DEST } from "./composer";
import { isTalkUnsupported, talk } from "./talk.svelte";
import {
  FILTERS,
  SSH_REMOTE_OUTLIVES,
  agentName,
  hostName,
  type WidgetFault,
  type WidgetFaultKind,
  isEnded,
  mergeItems,
  nextApprovalTarget,
  plural,
  itemConn,
  sshTabs,
  urgentConn,
  type Filter,
  type SSHBlock,
  type SSHConnInfo,
  type SSHElement,
  type SSHPromptInfo,
  type SSHHostInfo,
  type SSHTimelineItem,
} from "./ssh";

import { OVERVIEW_PANEL, CHANGES_PANEL, SESSION_PANELS } from "./sessionpanels";
import { addTab, allTabs, closeTab, deserialize, emptyLayout, groupOf, groups, retainTabs, selectTab, serialize, type DockNode } from "./docklayout";

export const PREDATES_RE = /unknown cmd/i;
const RESTART_READS = 20;
const RESTART_READ_MS = 500;
/** The daemon's words for a revision pin that no longer matches. */
const STALE_RE = /changed since/i;
/** The daemon's words for a block somebody or something already decided. */
const DECIDED_RE = /not waiting for approval/i;

/** A one-second clock any number of readers can share. Stops when nobody reads it. */
const subscribeClock = createSubscriber((update) => {
  const t = setInterval(update, 1000);
  return () => clearInterval(t);
});

export const sshClock = {
  get now(): number {
    subscribeClock();
    return Date.now();
  },
};

/** Webview-local, per session, best-effort: a dock layout is not worth a config key. The
 *  arrangement is kept for the most recent few sessions. */
const DOCK_KEY = "lola.ssh.dock";
const DOCK_KEEP = 50;
type SavedDock = { root: unknown; closed: string[] };

function readDocks(): Record<string, SavedDock> {
  try {
    const raw = JSON.parse(localStorage.getItem(DOCK_KEY) ?? "{}");
    return raw && typeof raw === "object" && !Array.isArray(raw) ? raw : {};
  } catch {
    return {};
  }
}

/**
 * A draft to the agent outlives the app, because writing it was work. Webview-local and capped,
 * like the dock. A command's draft is never kept: an old command waiting in a field is a thing
 * nobody should come back to.
 */
const DRAFTS_KEY = "lola.drafts";
const DRAFTS_KEEP = 50;
const DRAFT_MAX_BYTES = 8192;

function readDrafts(): Record<string, string> {
  try {
    const raw = JSON.parse(localStorage.getItem(DRAFTS_KEY) ?? "{}");
    if (!raw || typeof raw !== "object" || Array.isArray(raw)) return {};
    return Object.fromEntries(Object.entries(raw).filter(([, v]) => typeof v === "string")) as Record<string, string>;
  } catch {
    return {};
  }
}

function capDraft(text: string): string {
  const bytes = new TextEncoder().encode(text);
  if (bytes.length <= DRAFT_MAX_BYTES) return text;
  // A cut may land inside a character, and the decoder drops what is left of it.
  return new TextDecoder().decode(bytes.subarray(0, DRAFT_MAX_BYTES)).replace(/\uFFFD$/, "");
}

export type HandbackState = {
  conn: string;
  note: string;
  transcript: string;
  loading: boolean;
  truncated: boolean;
  failed: boolean;
  busy: boolean;
  /** The time the wheel was taken, for the panel's "since" line. */
  since: string;
};

class SessionView {
  items = $state<SSHTimelineItem[]>([]);
  /** Local receipt while Send awaits the daemon; never claims agent delivery. */
  sending = $state<{ text: string; conn: string; item?: string } | null>(null);
  /** Acknowledged sends stay visible until the timeline catches up, even after a failed poll. */
  outgoing = $state<{ text: string; conn: string; item: string; delivered: boolean }[]>([]);
  connections = $state<SSHConnInfo[]>([]);
  loaded = $state(false);
  /** How many timeline reads have come back, answered or not. */
  fetches = $state(0);
  error = $state("");
  predates = $state(false);
  lastSeq = 0;
  private inflight = false;
  private again = false;

  filter = $state<Filter>("everything");
  /** Connection the header, terminal and dock apply to. */
  activeConn = $state("");
  /**
   * The host the story is filtered to and a Run goes to, "" for All. All is the default: an
   * agent's conversation is one story, and a host is a filter on it, never a place you go.
   */
  scope = $state("");
  /**
   * Which host's shell the strip's `$` cell put on show, "" when the last terminal used was
   * not a host's. Separate from scope: looking at a staging shell leaves the story on All.
   * New code reads these two and never active(), which answers neither question.
   */
  shellConn = $state("");
  /**
   * The human hid the dock. The tree stays as it is, because a tab taken out of it would drop
   * its frame and reload the page, so this feeds DockArea's compact path with the drawer shut.
   */
  dockHidden = $state(false);
  /** A jump already chose where the keyboard goes, so arriving must not send it to the composer. */
  holdArrival = $state(false);
  briefCollapsed = $state(false);
  /** The `✎` on a roster row. Reads every destination's draft, because a row has no scope. */
  get hasDraft(): boolean {
    if (this.agentDraftText.trim()) return true;
    for (const text of this.commandDrafts.values()) if (text.trim()) return true;
    return false;
  }
  /** What is written to the agent. One per session, whatever the scope, and it is kept. */
  private agentDraftText = $state("");
  /** Per host: a command half typed for one host must never be sitting in another's field. */
  private commandDrafts = new SvelteMap<string, string>();
  /**
   * The page that put this text in the field. Text a widget wrote is not persisted, or a restart
   * would bring it back with nothing saying where it came from.
   */
  widgetFill = $state<{ name: string } | null>(null);
  get agentDraft(): string {
    return this.agentDraftText;
  }
  set agentDraft(text: string) {
    this.agentDraftText = text;
    if (!text) this.widgetFill = null;
    this.saveDraft?.(this.widgetFill ? "" : text);
  }
  commandDraft(conn: string): string {
    return this.commandDrafts.get(conn) ?? "";
  }
  setCommandDraft(conn: string, text: string) {
    this.commandDrafts.set(conn, text);
  }
  /** A widget's text, marked as such in the field. It fills and nothing else: see SSHWidget. */
  fillFromWidget(name: string, text: string) {
    this.widgetFill = { name };
    this.agentDraft = text;
  }
  /** Per destination, so ↑ can never recall a staging command into production. Memory only. */
  private histories = new SvelteMap<string, string[]>();
  historyFor(dest: string): string[] {
    return this.histories.get(dest) ?? [];
  }
  pushHistory(dest: string, text: string) {
    this.histories.set(dest, [...this.historyFor(dest).filter((h) => h !== text), text].slice(-100));
  }
  /** Double ⌘ in the composer: ↩ runs on the host, and ⌘↩ asks. */
  runDefault = $state(false);
  /** The story line's Report overlay. Here because the session menu opens it from any view. */
  reportOpen = $state(false);
  /** Someone hid the sidebar. It stays hidden for this session until they ask for it back. */
  sideHidden = $state(false);
  sessionPanels = $state(false);
  /** What the agent draft's references stand for. See composerpaste.ts. Reactive: its tiles read it. */
  pastes = new SvelteMap<number, Attachment>();
  pasteSeq = 0;
  /** The card the tray's ‹ › last stood on. "" follows the oldest. */
  trayCard = $state("");
  /** The daemon's sentence for why the last message queued, shown until the queue moves. */
  queueWhy = $state("");
  /** The human let this session's widgets run. In memory on purpose: see SSHWidget. */
  widgetsAllowed = $state(false);
  /** Panels and tabs of this session's widgets. Raw, because every change replaces the tree. */
  dock = $state.raw<DockNode>(emptyLayout());
  /** Widgets the human closed. A later update to one must not bring it back. */
  dockClosed = new SvelteSet<string>();
  /** The window is too small for the layout, so the dock is a drawer over the timeline. */
  dockCompact = $state(false);
  dockDrawer = $state(false);
  /** The tab on show in the drawer, which flattens every panel into one strip. */
  dockDrawerTab = $state("");
  /** The kind of failure each widget last reported, for a tab that is not on show. */
  widgetFaults = new SvelteMap<string, WidgetFaultKind>();
  /** Tabs that joined behind another one, and nobody has looked at yet. */
  dockUnseen = new SvelteSet<string>();
  /** The tab a timeline row just pointed at. A counter, so a second click pulses again. */
  dockPulse = $state.raw<{ tab: string; n: number } | null>(null);
  /** How many times the human asked for each page again. See SSHWidget. */
  widgetReloads = new SvelteMap<string, number>();
  /** A command went into the shell, so its block is about to arrive: open it when it does. */
  expectShellBlock = $state(false);
  /** The prompt whose answer is on its way. Its text is never here: see SSHPromptCard. */
  promptBusy = $state("");
  /** Prompts already answered, so a push that was in flight cannot bring a card back. */
  answeredPrompts = new SvelteSet<string>();
  /** One short line where the card was: sent, or cancelled. */
  promptNote = $state<{ conn: string; text: string; signIn: boolean } | null>(null);
  private promptNoteTimer: ReturnType<typeof setTimeout> | undefined;
  flash = $state("");
  flashCls = $state("text-faint");
  private flashTimer: ReturnType<typeof setTimeout> | undefined;

  expanded = new SvelteSet<string>();
  /** One command open at a time, whoever opened it: a timeline of open outputs is a wall. */
  openBlock(id: string) {
    this.expanded.clear();
    this.expanded.add(id);
  }
  openedFolds = new SvelteSet<string>();
  /** The thumbnails of images sent from here, by path. Memory only: a restart draws a plain tile. */
  sentThumbs = new SvelteMap<string, string>();
  /**
   * Focus requests are counters, and `seen` is how far each has been honoured. Both
   * live here rather than in the component, so a request made before the workspace
   * mounted still lands (⌘⇧A from another session) and coming back to the tab later
   * never steals the keyboard.
   */
  seen = { dock: 0, composer: 0, terminal: 0, stream: 0, find: 0, jump: 0 };
  /** Which card a press of ⌘⇧A or a jump last asked to focus. */
  dockFocus = $state(0);
  composerFocus = $state(0);
  terminalFocus = $state(0);
  streamFocus = $state(0);
  findFocus = $state(0);
  /** A block a dig should scroll to. */
  jump = $state<{ block: string; n: number; expand?: boolean } | null>(null);
  /** The item the stream's keyboard stands on. */
  cursor = $state("");
  /** The block an evidence plate is pointing at: its row lights up. */
  hoverBlock = $state("");
  /** The block a dig just landed on: its border flashes. */
  flashBlock = $state("");
  /** The block whose output fills the stream column. */
  outputMax = $state("");
  /** The stream of each expanded block, so a refetch never flips a tab. */
  streams = new SvelteMap<string, "stdout" | "stderr">();

  /** Which of the two this session is showing: a plain view switch, at any width. */
  narrow = $state<"stream" | "terminal">("stream");
  handback = $state<HandbackState | null>(null);
  dismissedHints = new SvelteSet<string>();

  constructor(dock?: SavedDock, draft = "") {
    if (dock) {
      this.dock = deserialize(dock.root);
      if (Array.isArray(dock.closed)) for (const id of dock.closed) if (typeof id === "string") this.dockClosed.add(id);
    }
    this.agentDraftText = draft;
  }

  private saveDock?: (d: SavedDock) => void;
  private saveDraft?: (text: string) => void;
  /** The widgets this view has already seen, so only a new post earns a dot. */
  private widgetsSeen: Set<string> | null = null;
  /** A timeline answered at least once since it was last empty, so absence means something. */
  private fetchedOk = false;

  private id = "";
  bind(id: string, saveDock?: (d: SavedDock) => void, saveDraft?: (text: string) => void) {
    this.id = id;
    this.saveDock = saveDock;
    this.saveDraft = saveDraft;
  }

  /** Replaces the layout. A sash saves once at the end of its drag, not on every pixel. */
  setDock(next: DockNode, save = true) {
    this.dock = next;
    if (save) this.persistDock();
  }

  persistDock() {
    this.saveDock?.({ root: serialize(this.dock), closed: [...this.dockClosed] });
  }

  /**
   * Brings the dock in line with the timeline. In the session workspace, new widgets join
   * as background tabs. In the older compact view, a post with no open panel gets a dot
   * and a story card until the person opens it. Neither path grants widget execution.
   * With panels open, a new one joins behind the tab on show, because an agent posting a page
   * must not flip the one the human is reading. One that no longer exists goes, but only after
   * a real answer, or a daemon that is down would empty the dock.
   */
  syncDock() {
    const known = new Set([...SESSION_PANELS, ...this.items.flatMap((i) => (i.element?.kind === "widget" ? [i.element.id] : []))]);
    let next = this.dock;
    const seen = this.widgetsSeen;
    for (const id of known) {
      if (SESSION_PANELS.includes(id)) continue;
      if (this.dockClosed.has(id) || groupOf(next, id)) continue;
      if (this.sessionPanels || groups(next).length) {
        this.dockUnseen.add(id);
        next = addTab(next, id, undefined, false);
      } else if (seen && !seen.has(id)) {
        this.dockUnseen.add(id);
      }
    }
    // The first look at a session sets the baseline: a story being read for the first time has
    // nothing new in it, and its widgets already show as cards.
    if (seen || this.fetchedOk) this.widgetsSeen = known;
    if (this.fetchedOk) {
      next = retainTabs(next, known);
      for (const id of [...this.dockClosed]) if (!known.has(id)) this.dockClosed.delete(id);
      for (const id of [...this.dockUnseen]) if (!known.has(id)) this.dockUnseen.delete(id);
    }
    if (next !== this.dock) this.setDock(next);
  }

  /** Seed one right column. Existing splits and closed tabs remain the person's choice. */
  ensureSessionPanels(diffable: boolean) {
    // Retire the old widget-directory tab from saved layouts without touching widget tabs.
    let next = closeTab(this.dock, "@lola/widgets");
    this.dockClosed.delete("@lola/widgets");
    for (const id of [OVERVIEW_PANEL, ...(diffable ? [CHANGES_PANEL] : [])]) {
      if (!groupOf(next, id) && !this.dockClosed.has(id)) next = addTab(next, id, undefined, false);
    }
    if (next !== this.dock) this.setDock(next);
    this.syncDock();
  }

  /** Every widget the timeline knows of, in the order they were posted. */
  get knownWidgets(): string[] {
    return this.items.flatMap((i) => (i.element?.kind === "widget" ? [i.element.id] : []));
  }

  /**
   * `w` and the strip's Widgets. Hiding never takes a tab out of the tree, or the frames would
   * reload. With no tabs at all it brings back every widget the human has not closed. In a
   * window too small for the layout the dock is a drawer, so that is what opens and shuts.
   */
  toggleDock() {
    if (!allTabs(this.dock).length) {
      let next = this.dock;
      for (const id of this.knownWidgets) if (!this.dockClosed.has(id)) next = addTab(next, id, undefined, false);
      this.dockHidden = false;
      this.dockUnseen.clear();
      if (next !== this.dock) this.setDock(next);
      return;
    }
    if (this.dockHidden) {
      this.dockHidden = false;
      // If the layout fits again, DockArea clears this at once. If not, the drawer is the dock.
      this.dockDrawer = true;
      return;
    }
    if (this.dockCompact) {
      this.dockDrawer = !this.dockDrawer;
      return;
    }
    this.dockHidden = true;
  }

  /** The timeline row. It brings a closed widget back or points at an open one, and takes no focus. */
  openWidget(id: string) {
    this.sideHidden = false;
    this.dockHidden = false;
    this.dockClosed.delete(id);
    this.dockUnseen.delete(id);
    this.setDock(selectTab(addTab(this.dock, id), id));
    this.dockDrawerTab = id;
    if (this.dockCompact) this.dockDrawer = true;
    this.dockPulse = { tab: id, n: (this.dockPulse?.n ?? 0) + 1 };
  }

  reloadWidget(id: string) {
    this.widgetFaults.delete(id);
    this.widgetReloads.set(id, (this.widgetReloads.get(id) ?? 0) + 1);
  }

  closeWidget(id: string) {
    this.dockClosed.add(id);
    this.dockUnseen.delete(id);
    this.widgetFaults.delete(id);
    this.setDock(closeTab(this.dock, id));
    if (!allTabs(this.dock).length) this.dockDrawer = false;
  }

  setFlash(text: string, cls = "text-faint") {
    this.flash = text;
    this.flashCls = cls;
    clearTimeout(this.flashTimer);
    this.flashTimer = setTimeout(() => (this.flash = ""), 6000);
  }

  notePrompt(conn: string, text: string, signIn: boolean) {
    this.promptNote = { conn, text, signIn };
    clearTimeout(this.promptNoteTimer);
    this.promptNoteTimer = setTimeout(() => (this.promptNote = null), 8000);
  }

  active(): SSHConnInfo | undefined {
    const tabs = this.connections.filter((c) => c.state !== "denied");
    return tabs.find((c) => c.id === this.activeConn) ?? urgentConn(tabs);
  }

  /** The open tab's timeline. An item no connection owns shows in every tab. */
  get tabItems(): SSHTimelineItem[] {
    const id = this.active()?.id ?? "";
    return this.items.filter((i) => {
      const c = itemConn(i);
      return !c || c === id;
    });
  }

  /** Item ids that came in while the reader was elsewhere are the stream's business. */
  async fetch(): Promise<void> {
    if (!this.id) return;
    if (this.inflight) {
      this.again = true;
      return;
    }
    this.inflight = true;
    try {
      let more = true;
      while (more) {
        const r = await SSHService.Timeline({ session: this.id, after: this.lastSeq });
        this.items = mergeItems(this.items, r.items);
        this.outgoing = this.outgoing.filter(sent => !this.items.some(item => item.id === sent.item));
        if (r.connections) {
          this.connections = r.connections;
          // A delta only says what changed, so what a trashed tab owned is dropped here.
          const known = new Set(r.connections.map((c) => c.id));
          if (this.items.some((i) => itemConn(i) && !known.has(itemConn(i)))) this.items = this.items.filter((i) => !itemConn(i) || known.has(itemConn(i)));
        }
        this.lastSeq = Math.max(this.lastSeq, r.lastSeq ?? 0);
        more = !!r.more && (r.items?.length ?? 0) > 0;
      }
      this.error = "";
      this.predates = false;
      this.fetchedOk = true;
    } catch (err) {
      const msg = String(err);
      this.predates = PREDATES_RE.test(msg);
      this.error = msg;
    } finally {
      this.loaded = true;
      this.inflight = false;
      this.fetches++;
      if (this.again) {
        this.again = false;
        void this.fetch();
      }
    }
  }

  /** A daemon store that restarted its counter must not leave a stale view behind. */
  resetIfBehind(seq: number) {
    if (seq < this.lastSeq) {
      this.lastSeq = 0;
      this.items = [];
      this.fetchedOk = false;
    }
  }

  block(id: string): SSHBlock | undefined {
    return this.items.find((i) => i.block?.id === id)?.block ?? undefined;
  }

  /** Puts an item's new state on screen at once. The next fetch is the truth. */
  patchConn(id: string, patch: Partial<SSHConnInfo>) {
    this.connections = this.connections.map((c) => (c.id === id ? { ...c, ...patch } : c));
  }
}

class SSHStore {
  // A plain Map on purpose. view() creates on first read, and every component calls
  // it inside $derived, where writing to reactive state throws state_unsafe_mutation.
  // Nothing subscribes to the map itself: each SessionView holds its own $state.
  private views = new Map<string, SessionView>();
  private docks = readDocks();
  private drafts = readDrafts();

  hosts = $state<SSHHostInfo[]>([]);
  hostsLoaded = $state(false);
  hostsError = $state("");
  /** The host the new-session modal should start on. */
  newHost = $state("");

  view(id: string): SessionView {
    let v = this.views.get(id);
    if (!v) {
      v = new SessionView(this.docks[id], this.drafts[id] ?? "");
      v.bind(
        id,
        (d) => this.saveDock(id, d),
        (text) => this.saveDraft(id, text),
      );
      this.views.set(id, v);
    }
    return v;
  }

  private saveDock(id: string, d: SavedDock) {
    // Most recent last, so the ones dropped are the sessions nobody has opened in a while.
    delete this.docks[id];
    this.docks[id] = d;
    for (const old of Object.keys(this.docks).slice(0, -DOCK_KEEP)) delete this.docks[old];
    try {
      localStorage.setItem(DOCK_KEY, JSON.stringify(this.docks));
    } catch {
      /* storage unavailable: the layout still holds for this run */
    }
  }

  private saveDraft(id: string, text: string) {
    delete this.drafts[id];
    if (text) this.drafts[id] = capDraft(text);
    for (const old of Object.keys(this.drafts).slice(0, -DRAFTS_KEEP)) delete this.drafts[old];
    try {
      localStorage.setItem(DRAFTS_KEY, JSON.stringify(this.drafts));
    } catch {
      /* storage unavailable: the draft still holds for this run */
    }
  }

  async loadHosts(): Promise<void> {
    try {
      const r = await SSHService.Hosts();
      this.hosts = r.hosts ?? [];
      this.hostsError = "";
    } catch (err) {
      this.hostsError = String(err);
    } finally {
      this.hostsLoaded = true;
    }
  }

  /**
   * Restart a daemon that predates ssh, then read again. The answer on screen came
   * from the old process, so nothing changes until someone asks the new one, and it
   * takes a moment to start listening.
   */
  async restartDaemon(): Promise<void> {
    await store.restartDaemon();
    for (let i = 0; i < RESTART_READS; i++) {
      await this.loadHosts();
      if (!this.hostsError) break;
      await new Promise((r) => setTimeout(r, RESTART_READ_MS));
    }
    for (const v of this.views.values()) void v.fetch();
  }

  // --- navigation -----------------------------------------------------------

  /** The host bar's own question has been answered. The record goes for good. */
  confirmTrash(session: string, c: SSHConnInfo) {
    void this.trash(session, c.id);
  }

  private async trash(session: string, conn: string) {
    const v = this.view(session);
    const r = await this.run(session, () => SSHService.Trash(conn));
    if (!r) return;
    if (r.warnings?.length) store.setFlash(r.warnings.join(" "), "warn");
    // The tab beside it takes over, or the Agent tab when it was the last.
    const tabs = sshTabs(v.connections);
    const at = tabs.findIndex((t) => t.conn.id === conn);
    const rest = tabs.filter((t) => t.conn.id !== conn);
    if (v.activeConn === conn) v.activeConn = rest[Math.min(Math.max(at, 0), rest.length - 1)]?.conn.id ?? "";
    if (v.scope === conn) v.scope = "";
    if (v.shellConn === conn) {
      v.shellConn = "";
      v.narrow = "stream";
    }
    v.connections = v.connections.filter((c) => c.id !== conn);
    v.items = v.items.filter((i) => itemConn(i) !== conn);
    const s = store.sessionById(session);
    if (!rest.length && terms.activeTab(session) === SSH && s && !conversationOffered(s)) terms.show(session, AGENT);
    await store.refresh();
  }

  /** Opens a session on its conversation. It never toggles, so a chip on the open one is safe. */
  open(id: string, opts: { conn?: string; dock?: boolean } = {}) {
    nav.openSession(id);
    terms.select(id, SSH);
    const v = this.view(id);
    v.narrow = "stream";
    if (opts.conn) v.activeConn = opts.conn;
    if (opts.dock) {
      v.holdArrival = true;
      v.dockFocus++;
    }
  }

  // --- the session's views ---------------------------------------------------

  /** Which cell of the strip this session is on, for `<` `>` and the strip's own underline. */
  currentCell(s: SessionInfo): string {
    const tab = terms.activeTab(s.id);
    if (tab === DIFF || tab === CHECKPOINTS) return "changes";
    if (tab === AGENT) return "terminal";
    if (tab !== SSH) return tab;
    const v = this.view(s.id);
    if (v.narrow !== "terminal") return "conversation";
    const live = v.connections.length ? v.connections : (s.ssh?.connections ?? []);
    const conn = live.find((c) => c.id === v.shellConn && !isEnded(c)) ?? v.active();
    return conn ? `host:${conn.id}` : "conversation";
  }

  /** A click on a strip cell, or `<` `>` arriving on one. A host's cell leaves the scope alone. */
  selectCell(id: string, cell: StripCell) {
    const v = this.view(id);
    switch (cell.kind) {
      case "conversation":
        v.narrow = "stream";
        terms.select(id, SSH);
        v.composerFocus++;
        break;
      case "host":
        v.shellConn = cell.conn.id;
        v.activeConn = cell.conn.id;
        v.narrow = "terminal";
        terms.select(id, SSH);
        v.terminalFocus++;
        break;
      case "terminal":
        v.shellConn = "";
        terms.select(id, AGENT);
        break;
      case "changes":
        terms.select(id, DIFF);
        break;
      default:
        v.shellConn = "";
        terms.select(id, cell.name);
    }
  }

  /** `<` and `>`: every cell of the strip, in visual order, wrapping. */
  ringStrip(s: SessionInfo, dir: 1 | -1) {
    const { views, machines } = stripCells(s, terms.shellsFor(s.id));
    const cells = [...views, ...machines];
    if (cells.length < 2) return;
    const at = cells.findIndex((c) => c.key === this.currentCell(s));
    this.selectCell(s.id, cells[(Math.max(at, 0) + dir + cells.length) % cells.length]);
  }

  /**
   * The menu's `Report`: back to the conversation with the overlay open, because a terminal and
   * Changes have no story line and a terminal-first person would otherwise lose the report.
   */
  showReport(s: SessionInfo) {
    nav.openSession(s.id);
    const v = this.view(s.id);
    v.narrow = "stream";
    terms.select(s.id, SSH);
    v.holdArrival = true;
    v.reportOpen = true;
  }

  /** On a terminal of any kind, including a host's. */
  onTerminal(s: SessionInfo): boolean {
    const tab = terms.activeTab(s.id);
    return tab === SSH ? this.view(s.id).narrow === "terminal" : !isViewTab(tab);
  }

  /**
   * ⌘\ and `h`. From anywhere that is not a terminal: the shell of the host in scope, else the
   * last terminal used here, by default the agent's. From a terminal, back to the conversation,
   * or to Overview when this session has none. From Overview it opens the session on one.
   */
  toggleTerminalView(s: SessionInfo) {
    const open = nav.focusedTerm === s.id;
    nav.openSession(s.id);
    const v = this.view(s.id);
    const tab = terms.activeTab(s.id);
    // Changes names the conversation on its footer's ⌘\, so that is where the chord goes.
    const onChanges = (tab === DIFF || tab === CHECKPOINTS) && conversationOffered(s);
    if (open && (this.onTerminal(s) || onChanges)) {
      this.leaveTerminal(s);
      return;
    }
    const conns = v.connections.length ? v.connections : (s.ssh?.connections ?? []);
    const live = (id: string) => conns.find((c) => c.id === id && !isEnded(c));
    const host = live(v.scope) ?? live(v.shellConn);
    if (host) {
      this.selectCell(s.id, { key: `host:${host.id}`, kind: "host", conn: host, label: hostName(host) });
      return;
    }
    v.shellConn = "";
    terms.select(s.id, terms.lastTerminal(s.id));
    terms.focusTerminal(s.id);
  }

  /** Ctrl-Q and ⌘\ from a terminal. Where the terminal was reached from is not the question. */
  leaveTerminal(s: SessionInfo) {
    if (!conversationOffered(s)) {
      nav.focusSessions();
      return;
    }
    const v = this.view(s.id);
    v.narrow = "stream";
    terms.select(s.id, SSH);
    v.composerFocus++;
  }

  /** ⌘L and `i`. From a terminal this goes to the conversation. With none, its terminal is the prompt. */
  focusComposer(s: SessionInfo) {
    nav.openSession(s.id);
    if (!conversationOffered(s)) {
      terms.focusTerminal(s.id);
      return;
    }
    const v = this.view(s.id);
    v.narrow = "stream";
    terms.select(s.id, SSH);
    v.holdArrival = true;
    v.composerFocus++;
  }

  /** ⌘⇧D and `f`. Opens the session if it has to, and refuses only what has no Changes. */
  toggleChanges(s: SessionInfo) {
    if (!this.mayShowChanges(s)) return;
    const open = nav.focusedTerm === s.id;
    nav.openSession(s.id);
    // From Overview the promise is to open it. Left on Changes, a toggle would close what the key
    // came here to open.
    if (open) terms.toggleDiff(s.id);
    else if (!isChangesTab(terms.activeTab(s.id))) terms.select(s.id, DIFF);
  }

  /**
   * Lands on Changes from a link rather than toggling it: on one turn when `seq` names it, on the
   * newest when it is -1, else on everything against the base. A second click on a receipt must
   * not close what it opened.
   */
  openChanges(s: SessionInfo, seq?: number) {
    if (!this.mayShowChanges(s)) return;
    nav.openSession(s.id);
    if (seq && seq > 0) checkpoints.select(s.id, seq);
    terms.select(s.id, seq ? CHECKPOINTS : DIFF);
  }

  private mayShowChanges(s: SessionInfo): boolean {
    if (changesOffered(s)) return true;
    store.setFlash(s.goalId ? "A goal runs this session, so it has no Changes." : "This session has no checkout to compare.", "warn");
    return false;
  }

  /**
   * ⌘J, and `n` `N` inside a session: the next one waiting on a person, and the keyboard goes
   * where it can answer. See landingFor.
   */
  nextWaiting(dir: 1 | -1 = 1) {
    const list = store.rosterSessions;
    const item = nextWaiting(list, nav.focusedTerm || nav.selectedId, dir);
    if (!item) {
      store.setFlash("Nothing is waiting on you.", "good");
      return;
    }
    const s = list.find((x) => x.id === item.id);
    if (!s) return;
    nav.openSession(s.id);
    terms.arrive(s.id);
    // Wherever this session actually is, which already weighs a person's choice, the global
    // preference and what the agent can do. An agent with no cards goes to its terminal whatever
    // was chosen, because nothing else can answer it, and landingFor keeps an ssh approval here.
    const cardless = talk.state(s.id)?.caps?.cards === false || (!!s.agent && s.agent !== "claude");
    const opensOnTerminal = cardless || !isViewTab(terms.activeTab(s.id));
    const v = this.view(s.id);
    switch (landingFor(s, item, opensOnTerminal)) {
      case "terminal":
        if (terms.activeTab(s.id) === SSH || isViewTab(terms.activeTab(s.id))) terms.show(s.id, terms.lastTerminal(s.id));
        terms.focusTerminal(s.id);
        break;
      case "card":
        v.narrow = "stream";
        terms.show(s.id, SSH);
        v.holdArrival = true;
        v.dockFocus++;
        break;
      case "moment":
        v.narrow = "stream";
        terms.show(s.id, SSH);
        v.holdArrival = true;
        v.streamFocus++;
        break;
    }
  }

  /** ⌘⇧A or `A`: the card that has waited longest, wherever it lives, with focus on it. */
  reviewNext(): boolean {
    const target = nextApprovalTarget(store.sessions);
    if (!target) {
      store.setFlash("Nothing is waiting for an approval.", "good");
      return false;
    }
    this.open(target.session, { conn: target.conn, dock: true });
    return true;
  }

  /** Reach a session's new-session form, on a host when one was named. */
  newSession(host = "") {
    this.newHost = host;
    nav.openOverlay("sshsession");
  }

  /** Connect an existing session to a host: choose one, or set one up first. */
  connectHost(sessionId: string) {
    nav.openOverlay("sshconnect", sessionId);
  }

  // --- going and connecting -------------------------------------------------

  private async run<T>(session: string, fn: () => Promise<T>, onError?: (msg: string) => void): Promise<T | undefined> {
    const v = this.view(session);
    if (!store.alive) {
      v.setFlash("The daemon is down. Nothing was sent.", "text-bad");
      return undefined;
    }
    try {
      const r = await fn();
      void v.fetch();
      return r;
    } catch (err) {
      const msg = String(err);
      if (onError) onError(msg);
      else v.setFlash(msg, "text-bad");
      void v.fetch();
      return undefined;
    }
  }

  /**
   * The go, a retry or a reconnect. The sign-in's questions come to the timeline as cards,
   * so this moves nothing. Someone already in the terminal stays there.
   */
  async go(session: string, conn: string) {
    const v = this.view(session);
    v.activeConn = conn;
    if (store.alive) v.patchConn(conn, { state: "connecting" });
    const r = await this.run(session, () => SSHService.Go(conn));
    if (r && v.narrow === "terminal") v.terminalFocus++;
    return r;
  }

  /**
   * Answers a shell's question. `secret` passes through here and is not kept: it is not put
   * on the view, and neither an error nor the reply can carry it.
   */
  async answerPrompt(session: string, conn: SSHConnInfo, prompt: SSHPromptInfo, action: "send" | "yes" | "no" | "cancel", secret = "") {
    const v = this.view(session);
    if (v.promptBusy) return;
    v.promptBusy = prompt.id;
    let gone = false;
    const r = await this.run(
      session,
      () => SSHService.PromptAnswer({ conn: conn.id, prompt: prompt.id, action, secret }),
      (msg) => {
        gone = msg.includes("has gone");
        v.setFlash(msg, "text-bad");
      },
    );
    v.promptBusy = "";
    if (r || gone) {
      v.answeredPrompts.add(prompt.id);
      v.patchConn(conn.id, { prompt: undefined });
    }
    if (r) v.notePrompt(conn.id, r.status === "cancelled" ? "Cancelled." : `Sent. Waiting for ${hostName(conn)}.`, prompt.stage === "signin");
    v.composerFocus++;
  }

  deny(session: string, conn: string, reason: string) {
    return this.run(session, () => SSHService.Deny(conn, reason));
  }

  /** Confirming a card. A refusal in the same moment reads as what it is. */
  async approve(session: string, b: SSHBlock, grantMinutes = 0) {
    const v = this.view(session);
    const r = await this.run(
      session,
      () => SSHService.Approve({ conn: b.conn, block: b.id, decision: "approve", grantMinutes: grantMinutes || undefined }),
      (msg) => v.setFlash(DECIDED_RE.test(msg) ? `That command was already refused: ${msg}` : msg, DECIDED_RE.test(msg) ? "text-warn" : "text-bad"),
    );
    if (r?.status === "refused") v.setFlash(`That command was already refused: ${r.statusDetail ?? ""}`, "text-warn");
    return r;
  }

  denyBlock(session: string, b: SSHBlock, reason: string) {
    return this.run(session, () => SSHService.Approve({ conn: b.conn, block: b.id, decision: "deny", reason: reason || undefined }));
  }

  revokeGrant(session: string, conn: string) {
    return this.run(session, () => SSHService.RevokeGrant(conn));
  }

  // --- the wheel ------------------------------------------------------------

  /**
   * Taking it is one action with no confirmation: instant and visible, then the
   * daemon. The terminal grows and takes the keyboard before any answer comes back,
   * because this is the moment someone wants to stop something.
   */
  async takeWheel(session: string, conn: string) {
    const v = this.view(session);
    const c = v.connections.find((x) => x.id === conn);
    if (!c) return;
    if (!store.alive) {
      v.setFlash("The daemon is down. The wheel did not move.", "text-bad");
      return;
    }
    const was = { wheel: c.wheel, wheelSince: c.wheelSince, grant: c.grant };
    v.activeConn = conn;
    v.patchConn(conn, { wheel: "human", wheelSince: new Date().toISOString(), grant: undefined });
    v.composerFocus++;
    const r = await this.run(
      session,
      () => SSHService.TakeWheel({ conn }),
      (msg) => {
        v.patchConn(conn, was);
        v.setFlash(msg, "text-bad");
      },
    );
    if (r?.warning) v.setFlash(r.warning, "text-warn");
    return r;
  }

  /** Opens the panel. Nothing is captured until now, and nothing is sent by this. */
  async openHandback(session: string, conn: string) {
    const v = this.view(session);
    const c = v.connections.find((x) => x.id === conn);
    v.narrow = "stream";
    const state: HandbackState = {
      conn,
      note: "",
      transcript: "",
      loading: true,
      truncated: false,
      failed: false,
      busy: false,
      since: c?.wheelSince ?? "",
    };
    v.handback = state;
    try {
      const r = await SSHService.HandbackPreview(conn);
      if (v.handback?.conn === conn) {
        v.handback.transcript = r.transcript ?? "";
        v.handback.truncated = !!r.truncated;
        v.handback.loading = false;
      }
    } catch {
      if (v.handback?.conn === conn) {
        v.handback.failed = true;
        v.handback.loading = false;
      }
    }
  }

  closeHandback(session: string) {
    this.view(session).handback = null;
  }

  /** Stop: end the agent's turn and its running commands, and leave the host to you. */
  async stopAgent(session: string, conn: string) {
    const v = this.view(session);
    v.composerFocus++;
    v.patchConn(conn, { wheel: "human", wheelSince: new Date().toISOString(), grant: undefined });
    const r = await this.run(session, () => SSHService.TakeWheel({ conn, stopInFlight: true, interrupt: true }));
    if (r?.warning) v.setFlash(r.warning, "text-warn");
    return r;
  }

  /** Sends only what the panel holds: never a fresh capture, and only from the "with" button. */
  async handback(session: string, withTranscript: boolean) {
    const v = this.view(session);
    const h = v.handback;
    if (!h) return;
    h.busy = true;
    const r = await this.run(session, () =>
      SSHService.Handback({
        conn: h.conn,
        note: h.note.trim() || undefined,
        transcript: withTranscript ? h.transcript : undefined,
      }),
    );
    if (r) {
      v.handback = null;
      v.patchConn(h.conn, { wheel: "agent" });
    } else if (v.handback) {
      v.handback.busy = false;
    }
  }

  /** ⌘⇧L, `W`, or the wheel's own buttons. */
  wheelKey(s: SessionInfo) {
    const conns = this.view(s.id).connections.length ? this.view(s.id).connections : (s.ssh?.connections ?? []);
    const live = conns.filter((c) => !isEnded(c));
    if (!live.length) {
      store.setFlash("This session has no connection.", "warn");
      return;
    }
    const v = this.view(s.id);
    if (!v.connections.length) v.connections = conns;
    const conn = live.find((c) => c.id === v.activeConn) ?? urgentConn(live) ?? live[0];
    this.open(s.id, { conn: conn.id });
    if (conn.wheel === "human") void this.openHandback(s.id, conn.id);
    else void this.takeWheel(s.id, conn.id);
  }

  // --- commands -------------------------------------------------------------

  async stop(session: string, b: SSHBlock) {
    const r = await this.run(session, () => SSHService.Stop(b.conn, b.id));
    if (r?.warning) this.view(session).setFlash(r.warning, "text-warn");
    return r;
  }

  /** A new shell once the human left theirs: the master stayed up, so no sign-in. */
  async terminal(session: string, conn: string) {
    const v = this.view(session);
    const r = await this.run(session, () => SSHService.Terminal(conn));
    if (r) {
      v.patchConn(conn, r);
      v.terminalFocus++;
    }
    return r;
  }

  askClose(session: string, c: SSHConnInfo) {
    const host = hostName(c);
    const s = store.sessionById(session);
    const running = c.runningBlocks ?? 0;
    confirm.ask({
      title: `Close the connection to ${host}?`,
      body: `This ends your shell and closes the connection. ${agentName(s?.agent)} will not be able to run commands here.`,
      detail:
        running > 0
          ? `${plural(running, "command")} ${running === 1 ? "is" : "are"} still running. ${SSH_REMOTE_OUTLIVES}`
          : (c.commandsRun ?? 0) > 0
            ? SSH_REMOTE_OUTLIVES
            : undefined,
      confirmLabel: "Close connection",
      onConfirm: () => {
        void this.close(session, c.id);
      },
    });
  }

  private async close(session: string, conn: string) {
    const r = await this.run(session, () => SSHService.Close(conn));
    if (r?.warnings?.length) store.setFlash(r.warnings.join(" "), "warn");
    void store.refresh();
  }

  // --- talking --------------------------------------------------------------

  /** The composer. It appears as a "You" item as soon as the daemon has it. */
  async say(session: string, text: string): Promise<boolean> {
    const v = this.view(session);
    const tab = v.active()?.id ?? "";
    const r = await this.run(session, () => SSHService.Say(session, tab, text));
    // The daemon handed this tab's wheel back along with the message.
    if (r && tab && v.connections.find((c) => c.id === tab)?.wheel === "human") v.patchConn(tab, { wheel: "agent" });
    return !!r;
  }

  /**
   * The composer's way to the agent. The host in scope goes along, and under All none does, so
   * one chat message cannot hand back every wheel. A daemon older than the conversation commands
   * still talks through the old path, which is all it knows. Answers whether the daemon took it.
   */
  async speak(session: string, text: string, stopFirst = false): Promise<boolean> {
    const v = this.view(session);
    if (!store.alive) {
      v.setFlash("The daemon is down. Nothing was sent.", "text-bad");
      return false;
    }
    if (v.sending) return false;
    const conn = v.scope;
    v.sending = { text, conn };
    try {
      const r = await talk.say({ session, text, conn: conn || undefined, stopFirst: stopFirst || undefined });
      if (conn && v.connections.find((c) => c.id === conn)?.wheel === "human") v.patchConn(conn, { wheel: "agent" });
      v.queueWhy = r.delivered ? "" : (r.why ?? "");
      if (r.item) v.outgoing.push({ text, conn, item: r.item, delivered: !!r.delivered });
      v.sending = null;
      void v.fetch();
      void talk.refresh(session);
      return true;
    } catch (err) {
      if (isTalkUnsupported(err)) return await this.say(session, text);
      v.setFlash(String(err), "text-bad");
      void v.fetch();
      return false;
    } finally {
      v.sending = null;
    }
  }

  /** Answers whether the message came back. Too late means the agent has it, and it says so. */
  async recall(session: string, item: string): Promise<boolean> {
    const r = await this.run(session, () => SSHService.Recall(session, item));
    if (r && !r.recalled) {
      const v = this.view(session);
      v.setFlash(`Too late: ${agentName(store.sessionById(session)?.agent)} already has this.`, "text-warn");
    }
    return !!r?.recalled;
  }

  /** "Send them": the human is speaking again, so whatever a Stop held goes now if it can. */
  async sendHeld(session: string) {
    const v = this.view(session);
    const r = await this.run(session, () => talk.queueRelease(session));
    if (r) v.queueWhy = r.delivered ? "" : (r.why ?? "");
    void talk.refresh(session);
  }

  /**
   * Only the daemon's revision refusal reads as "it changed". Anything else, a
   * dropped connection on a proposal say, is shown in the daemon's own words: telling
   * someone to re-read a card that did not move sends them looking for nothing.
   */
  private staleOr(session: string, onStale: () => void) {
    return (msg: string) => {
      if (STALE_RE.test(msg)) onStale();
      else this.view(session).setFlash(msg, "text-bad");
    };
  }

  /** Pinned to the revision on screen. A reworded question refuses rather than landing wrong. */
  answer(session: string, e: SSHElement, text: string, onStale: () => void) {
    return this.run(
      session,
      () => SSHService.Answer({ session, element: e.id, revision: e.revision, text }),
      this.staleOr(session, onStale),
    );
  }

  /** Pinned the same way. A proposal that moved runs nothing. */
  proposal(session: string, e: SSHElement, action: "run" | "decline", reason: string, onStale: () => void) {
    return this.run(
      session,
      () => SSHService.Proposal({ session, element: e.id, revision: e.revision, action, reason: reason || undefined }),
      this.staleOr(session, onStale),
    );
  }

  /**
   * Tells the agent one of its widgets failed. Best effort and quiet: this is reporting on a
   * failure, so a failure of its own has nowhere useful to go. Says whether the agent was told.
   */
  async reportWidget(session: string, element: string, revision: number, f: WidgetFault): Promise<boolean> {
    try {
      const d = await SSHService.WidgetStatus({ session, element, revision, kind: f.kind, message: f.message || undefined, line: f.line || undefined });
      return !!d?.told;
    } catch {
      return false;
    }
  }

  /** "Ask about this": from digging to talking without retyping an id. */
  askAbout(session: string, blockId: string) {
    const v = this.view(session);
    v.agentDraft = `About ${blockId}: ${v.agentDraft}`;
    v.runDefault = false;
    v.composerFocus++;
  }

  /** Scrolls to a block, expands it, and leaves a way back. */
  digTo(session: string, blockId: string) {
    const v = this.view(session);
    v.openBlock(blockId);
    v.jump = { block: blockId, n: (v.jump?.n ?? 0) + 1 };
  }

  // --- host-level helpers ---------------------------------------------------

  /** The agent's own pane is the session's Agent tab. */
  showAgentPane(session: string) {
    terms.select(session, AGENT);
  }

  /** The agent's own terminal, from a card or a footer that is about the agent, whatever the scope. */
  showAgentTerminal(session: string) {
    const v = this.view(session);
    v.shellConn = "";
    terms.select(session, AGENT);
    terms.focusTerminal(session);
  }

  /** For what truly needs a TTY. Nothing opens it by itself except running such a program. */
  showTerminal(session: string) {
    const v = this.view(session);
    // The keyboard goes to the host in front of a person. A shell looked at earlier may be another
    // one, and typing into it would land on the wrong machine.
    if (v.shellConn && v.activeConn && v.shellConn !== v.activeConn) v.shellConn = v.activeConn;
    v.narrow = "terminal";
    v.terminalFocus++;
  }

  /** A particular host's shell, from a card about it. Under All the card's host may not be the active one. */
  showHostTerminal(session: string, conn: string) {
    const v = this.view(session);
    v.shellConn = conn;
    v.activeConn = conn;
    this.showTerminal(session);
  }

  showTimeline(session: string) {
    const v = this.view(session);
    v.narrow = "stream";
    v.composerFocus++;
  }

  /** Run a command the human typed. Its output opens, since seeing it is why they ran it. */
  async runCommand(session: string, conn: string, text: string): Promise<boolean> {
    const v = this.view(session);
    const r = await this.run(session, () => SSHService.RunCommand({ session, conn, command: text }));
    if (!r) return false;
    if (r.id) v.openBlock(r.id);
    else v.expectShellBlock = true;
    v.pushHistory(conn, text);
    return true;
  }

  /** Bring the human's shell to the folder everyone else is in. */
  async terminalToCwd(session: string, conn: string) {
    await this.run(session, () => SSHService.TerminalCd(conn));
    this.view(session).terminalFocus++;
  }

  /** Lets the agent read a command the human ran, and tells it so. */
  async shareBlock(session: string, block: string) {
    await this.run(session, () => SSHService.ShareBlock({ session, block }));
  }

  /**
   * The bare keys that act on a session from either depth. `i t F /` are the session's own, in
   * App's sessionKey, because in Overview they would bump a focus counter for a composer that
   * is not mounted, and the request would fire on the next open.
   */
  bareKey(key: string, s: SessionInfo | undefined): boolean {
    if (key === "A") {
      this.reviewNext();
      return true;
    }
    if (!s) return false;
    if (key === "h") {
      this.toggleTerminalView(s);
      return true;
    }
    if (key === "W" && (s.ssh?.connections?.length ?? 0) > 0) {
      this.wheelKey(s);
      return true;
    }
    return false;
  }

  /** `w` and the strip's Widgets. From another view it goes to the conversation, dock showing. */
  dockKey(s: SessionInfo) {
    const v = this.view(s.id);
    if (v.sessionPanels) {
      this.open(s.id);
      const widget = v.knownWidgets.find(id => !v.dockClosed.has(id)) ?? v.knownWidgets[0];
      if (widget) v.openWidget(widget);
      return;
    }
    if (terms.activeTab(s.id) !== SSH) {
      this.open(s.id);
      if (v.dockHidden || !allTabs(v.dock).length) v.toggleDock();
      return;
    }
    v.toggleDock();
  }

  /** `F`: the next filter, with a host only. */
  cycleFilter(s: SessionInfo) {
    if (!(s.ssh?.connections?.length ?? 0)) return;
    const v = this.view(s.id);
    const i = FILTERS.findIndex((f) => f.id === v.filter);
    v.filter = FILTERS[(i + 1) % FILTERS.length].id;
  }

  /** A Session-menu chord. They work from inside a terminal, so no bare-key gate applies. */
  menuAction(action: string, s: SessionInfo | undefined) {
    if (action === "ssh-new") {
      this.newSession();
      return;
    }
    if (action === "ssh-approval") {
      this.reviewNext();
      return;
    }
    if (!s) {
      store.setFlash("no session selected", "warn");
      return;
    }
    // These two are every session's, connection or not.
    if (action === "ssh-talk") {
      this.focusComposer(s);
      return;
    }
    if (action === "ssh-terminal") {
      this.toggleTerminalView(s);
      return;
    }
    if (action === "ssh-wheel") this.wheelKey(s);
  }

}

export const ssh = new SSHStore();
export type { SessionView };
