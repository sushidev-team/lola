// The SSH workspace's client state: the timeline a session has fetched, plus the
// view choices that must survive leaving the tab (filter, split, drafts). Keyed by
// session id, so coming back to a session is instant and two sessions never share
// a draft. Every daemon call goes through here; components read and call.
//
// Nothing here decides policy. The daemon re-checks every action, and a refusal
// comes back as an error that is shown in lola's own sentence.

import { SvelteMap, SvelteSet, createSubscriber } from "svelte/reactivity";
import { SSHService } from "@bindings/desktop";
import { store, type SessionInfo } from "./store.svelte";
import { nav } from "./nav.svelte";
import { confirm } from "./confirm.svelte";
import { terms, AGENT, SSH } from "./terms.svelte";
import {
  FILTERS,
  SPLIT_DEFAULT,
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

/** Webview-local, per session, best-effort: a split is not worth a config key. */
const SPLIT_KEY = "lola.ssh.split";

function readSplits(): Record<string, number> {
  try {
    const raw = JSON.parse(localStorage.getItem(SPLIT_KEY) ?? "{}");
    return raw && typeof raw === "object" ? raw : {};
  } catch {
    return {};
  }
}

/** Same reasoning. The dock's arrangement, per session, kept for the most recent few. */
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

export type TerminalMode = "split" | "hidden" | "max";

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
  connections = $state<SSHConnInfo[]>([]);
  loaded = $state(false);
  error = $state("");
  predates = $state(false);
  lastSeq = 0;
  private inflight = false;
  private again = false;

  filter = $state<Filter>("everything");
  /** Connection the header, terminal and dock apply to. */
  activeConn = $state("");
  briefCollapsed = $state(false);
  /** Per tab: a command half typed for one host must never be sitting in another's field. */
  private drafts = new SvelteMap<string, string>();
  get draft(): string {
    return this.drafts.get(this.active()?.id ?? "") ?? "";
  }
  set draft(text: string) {
    this.drafts.set(this.active()?.id ?? "", text);
  }
  history = $state<string[]>([]);
  /** Double ⌘ in the composer: ↩ runs on the host, and ⌘↩ asks. */
  runDefault = $state(false);
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
  jump = $state<{ block: string; n: number } | null>(null);
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

  split = $state(SPLIT_DEFAULT);
  terminal = $state<TerminalMode>("split");
  /** "Stream" or "Terminal" under 980px. */
  narrow = $state<"stream" | "terminal">("stream");
  handback = $state<HandbackState | null>(null);
  dismissedHints = new SvelteSet<string>();

  constructor(split: number | undefined, dock?: SavedDock) {
    if (split) this.split = split;
    if (dock) {
      this.dock = deserialize(dock.root);
      if (Array.isArray(dock.closed)) for (const id of dock.closed) if (typeof id === "string") this.dockClosed.add(id);
    }
  }

  private saveDock?: (d: SavedDock) => void;
  /** A timeline answered at least once since it was last empty, so absence means something. */
  private fetchedOk = false;

  private id = "";
  bind(id: string, saveDock?: (d: SavedDock) => void) {
    this.id = id;
    this.saveDock = saveDock;
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
   * Brings the dock in line with the timeline: a widget not seen before opens, and one that no
   * longer exists goes. Only after a real answer, or a daemon that is down would empty the dock.
   */
  syncDock() {
    const known = new Set(this.items.flatMap((i) => (i.element?.kind === "widget" ? [i.element.id] : [])));
    let next = this.dock;
    for (const id of known) {
      if (this.dockClosed.has(id) || groupOf(next, id)) continue;
      // An agent posting a page must not flip the one the human is reading.
      if (groups(next).length) this.dockUnseen.add(id);
      next = addTab(next, id, undefined, false);
    }
    if (this.fetchedOk) {
      next = retainTabs(next, known);
      for (const id of [...this.dockClosed]) if (!known.has(id)) this.dockClosed.delete(id);
    }
    if (next !== this.dock) this.setDock(next);
  }

  /** The timeline row. It brings a closed widget back or points at an open one, and takes no focus. */
  openWidget(id: string) {
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
  private splits = readSplits();
  private docks = readDocks();

  hosts = $state<SSHHostInfo[]>([]);
  hostsLoaded = $state(false);
  hostsError = $state("");
  /** The host the new-session modal should start on. */
  newHost = $state("");

  view(id: string): SessionView {
    let v = this.views.get(id);
    if (!v) {
      v = new SessionView(this.splits[id], this.docks[id]);
      v.bind(id, (d) => this.saveDock(id, d));
      this.views.set(id, v);
    }
    return v;
  }

  saveSplit(id: string, pct: number) {
    this.splits[id] = pct;
    try {
      localStorage.setItem(SPLIT_KEY, JSON.stringify(this.splits));
    } catch {
      /* storage unavailable: the ratio still holds for this run */
    }
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

  /** The ×: closes the connection and deletes the tab's record for good, after one question. */
  trashTab(session: string, c: SSHConnInfo) {
    const host = hostName(c);
    const running = c.runningBlocks ?? 0;
    confirm.ask({
      title: `Delete the ${host} tab?`,
      body: isEnded(c) ? "Its timeline and output are deleted for good." : "The connection closes, and its timeline and output are deleted for good.",
      detail: isEnded(c) ? undefined : running > 0 ? `${plural(running, "command")} ${running === 1 ? "is" : "are"} still running. ${SSH_REMOTE_OUTLIVES}` : (c.commandsRun ?? 0) > 0 ? SSH_REMOTE_OUTLIVES : undefined,
      confirmLabel: "Delete tab",
      onConfirm: () => {
        void this.trash(session, c.id);
      },
    });
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
    v.connections = v.connections.filter((c) => c.id !== conn);
    v.items = v.items.filter((i) => itemConn(i) !== conn);
    if (!rest.length && terms.activeTab(session) === SSH) terms.select(session, AGENT);
    await store.refresh();
  }

  /** Selects a session and opens its SSH tab. The grid lens hosts no embed, so it is left first. */
  open(id: string, opts: { conn?: string; dock?: boolean } = {}) {
    if (nav.lens === "grid") nav.setLens("list");
    nav.select(id);
    terms.select(id, SSH);
    const v = this.view(id);
    if (opts.conn) v.activeConn = opts.conn;
    if (opts.dock) v.dockFocus++;
  }

  /** `h`: open the tab, or leave it for the agent's. */
  toggleTab(s: SessionInfo) {
    if (terms.activeTab(s.id) === SSH) terms.select(s.id, AGENT);
    else {
      // Back to the tab you left, unless it is gone.
      const v = this.view(s.id);
      const here = s.ssh?.connections?.some((c) => c.id === v.activeConn && c.state !== "denied");
      this.open(s.id, { conn: here ? v.activeConn : urgentConn(s.ssh?.connections)?.id });
    }
  }

  /** ⌘⇧A or `A`: the card that has waited longest, wherever it lives, with focus on it. */
  reviewNext(): boolean {
    const target = nextApprovalTarget(store.sessions);
    if (!target) {
      store.setFlash("Nothing is waiting for an approval.", "good");
      return false;
    }
    if (nav.focusedTerm && nav.focusedTerm !== target.session) nav.toggleFocusTerm(nav.focusedTerm);
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

  async recall(session: string, item: string) {
    const r = await this.run(session, () => SSHService.Recall(session, item));
    if (r && !r.recalled) {
      const v = this.view(session);
      v.setFlash(`Too late: ${agentName(store.sessionById(session)?.agent)} already has this.`, "text-warn");
    }
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
    v.draft = `About ${blockId}: ${v.draft}`;
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

  /** For what truly needs a TTY. Nothing opens it by itself except running such a program. */
  showTerminal(session: string) {
    const v = this.view(session);
    v.narrow = "terminal";
    v.terminalFocus++;
  }

  showTimeline(session: string) {
    const v = this.view(session);
    v.narrow = "stream";
    v.composerFocus++;
  }

  toggleTerminal(session: string) {
    const v = this.view(session);
    if (v.narrow === "terminal") this.showTimeline(session);
    else {
      v.narrow = "terminal";
      v.terminalFocus++;
    }
  }

  /** Run a command the human typed. Its output opens, since seeing it is why they ran it. */
  async runCommand(session: string, conn: string, text: string): Promise<boolean> {
    const v = this.view(session);
    const r = await this.run(session, () => SSHService.RunCommand({ session, conn, command: text }));
    if (!r) return false;
    if (r.id) v.openBlock(r.id);
    else v.expectShellBlock = true;
    v.history = [...v.history.filter((h) => h !== text), text].slice(-100);
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

  /** Bare keys at the selection level. Returns whether the key was the workspace's. */
  bareKey(key: string, s: SessionInfo | undefined): boolean {
    if (key === "A") {
      this.reviewNext();
      return true;
    }
    if (!s) return false;
    const hasConn = (s.ssh?.connections?.length ?? 0) > 0;
    if (key === "h" && hasConn) {
      this.toggleTab(s);
      return true;
    }
    if (key === "W" && hasConn) {
      this.wheelKey(s);
      return true;
    }
    if (terms.activeTab(s.id) !== SSH || !hasConn) return false;
    const v = this.view(s.id);
    switch (key) {
      case "i":
        v.composerFocus++;
        return true;
      case "t":
        v.streamFocus++;
        return true;
      case "F": {
        const i = FILTERS.findIndex((f) => f.id === v.filter);
        v.filter = FILTERS[(i + 1) % FILTERS.length].id;
        return true;
      }
      case "/":
        v.findFocus++;
        return true;
    }
    return false;
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
    if (!s || !(s.ssh?.connections?.length ?? 0)) {
      store.setFlash("This session has no SSH connection.", "warn");
      return;
    }
    if (action === "ssh-wheel") {
      this.wheelKey(s);
      return;
    }
    this.open(s.id);
    const v = this.view(s.id);
    if (action === "ssh-talk") v.composerFocus++;
    else if (action === "ssh-terminal") this.toggleTerminal(s.id);
  }

}

export const ssh = new SSHStore();
export type { SessionView };
