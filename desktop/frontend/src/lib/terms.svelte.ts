// Per-session terminal tabs. Every session shows its live AGENT pane; on demand
// it can also open any number of SHELLS — each a real "<id>-shell-<n>" tmux
// session on the lola server, rooted in the session's worktree. The shells are
// DISCOVERED from the server (TermService.Shells), so a shell opened in the TUI
// shows up here as a tab and vice versa — the two stay in sync. This module holds
// only the client-side view (the discovered names + which tab each session is
// on), keyed by session id so it survives the SessionEmbed's sessionId prop
// changing as the selection moves.
import { SvelteMap } from "svelte/reactivity";
import { TermService } from "@bindings/desktop";
import { store } from "./store.svelte";
import { conversationOffered, defaultView, feedKnownFrom, type SessionViewKind } from "./sessionview";

/** The agent pane's tab key (a sentinel; every other tab is a shell tmux name). */
export const AGENT = "agent";

/** The diff tab's key: the session's changes against its base branch, where a
 * human leaves line comments for the agent (DiffView). A sentinel like AGENT —
 * it is not a tmux session, so it is never discovered and never closed. */
export const DIFF = "diff";

/** One turn of Changes: what the turn behind the selected checkpoint changed. A
 * view sentinel like DIFF, and the strip's Changes cell lights for both. The turn
 * rail switches between the two, so neither one is ever "the previous view". */
export const CHECKPOINTS = "checkpoints";

/** The conversation's key: the story you read and the composer you talk in. A view
 * sentinel like DIFF, and still spelled SSH because old stored `lola.tabs` values
 * hold that word. A "<id>-ssh-N" tmux session never becomes a tab of its own: it is
 * mounted only inside the conversation's workspace, because one tmux session with
 * two xterm clients would fight over its size. */
export const SSH = "ssh";

/** The same sentinel by the name the UI uses. */
export const CONVERSATION = SSH;

/** A view tab (DIFF, CHECKPOINTS, SSH) is not a terminal: never discovered from
 * tmux, never closed, never in the tab-cycling ring. */
export function isViewTab(tab: string): boolean {
  return tab === DIFF || tab === CHECKPOINTS || tab === SSH;
}

/** Either half of Changes: everything against the base, or one turn. */
export function isChangesTab(tab: string): boolean {
  return tab === DIFF || tab === CHECKPOINTS;
}

/** Suffix of a session's REVIEW pane ("<id>-review"). The daemon opens it for a
 * visible review pass and holds it open afterwards so the findings stay
 * readable; the app only discovers it (TermService.Shells returns it last) and
 * shows it as a tab. It is never created here — there is no "+ Review". */
const REVIEW_SUFFIX = "-review";

/** Whether a discovered tab name is a session's review pane rather than a shell. */
export function isReviewTab(name: string): boolean {
  return name.endsWith(REVIEW_SUFFIX);
}

/** Matches a session's DEV tab ("<id>-dev-<n>"): one of the project's
 * dev_commands, started by the DAEMON (internal/daemon/dev.go) for whichever
 * session currently holds them. Like the review pane it is only discovered here,
 * never created — the "+ Shell" button has no dev counterpart, because which
 * session runs them is a per-project decision the daemon arbitrates. */
const DEV_RE = /-dev-(\d+)$/;

/** Whether a discovered tab name is a dev tab. */
export function isDevTab(name: string): boolean {
  return DEV_RE.test(name);
}

/** The 1-based command index of a session's dev tab, or 0 when name is not one.
 * The prefix is matched in full because "lola-fe-4" is a prefix of
 * "lola-fe-42-dev-1" — the same trap internal/devtab guards in Go. */
export function devTabIndex(id: string, name: string): number {
  const rest = name.startsWith(`${id}-dev-`) ? name.slice(id.length + 5) : "";
  const n = Number(rest);
  return rest !== "" && Number.isInteger(n) && n > 0 ? n : 0;
}

/** Webview-local tab names (the right-click → Rename). Deliberately NOT a
 * config.toml key and not daemon state: the tmux session keeps its real name,
 * this is only what the app calls it. Mirrored to localStorage — best-effort,
 * like nav's sidebar flag — because a tmux shell outlives the app window, so a
 * name that vanished on relaunch would be a name nobody bothers to set. */
const NAMES_KEY = "lola.terms.names";

type NameMap = Record<string, Record<string, string>>; // session id -> tab name -> label

function readNames(): NameMap {
  try {
    const raw = localStorage.getItem(NAMES_KEY);
    const parsed: unknown = raw ? JSON.parse(raw) : null;
    return parsed && typeof parsed === "object" ? (parsed as NameMap) : {};
  } catch {
    return {}; // unreadable / disabled storage → everything falls back to "Shell N"
  }
}

/** The view a human last picked, per session. localStorage: the daemon never reads it. */
const TABS_KEY = "lola.tabs";
const TABS_KEEP = 200;
/** The global preference. Conversation decides nothing, so only Terminal is ever stored. */
const GLOBAL_VIEW_KEY = "lola.session.view";
/** The agents whose log this client last found unreadable, so the next open knows before it paints. */
const FEED_BAD_KEY = "lola.feed.bad";
/** Set once the one line about the terminal has been shown. Global: it is said once, ever. */
const INTRO_KEY = "lola.session.intro";

function readTabs(): Record<string, string> {
  try {
    const raw: unknown = JSON.parse(localStorage.getItem(TABS_KEY) ?? "{}");
    return raw && typeof raw === "object" && !Array.isArray(raw) ? (raw as Record<string, string>) : {};
  } catch {
    return {};
  }
}

function readFeedBad(): Record<string, boolean> {
  try {
    const raw: unknown = JSON.parse(localStorage.getItem(FEED_BAD_KEY) ?? "{}");
    return raw && typeof raw === "object" && !Array.isArray(raw) ? (raw as Record<string, boolean>) : {};
  } catch {
    return {};
  }
}

function readGlobalView(): SessionViewKind {
  try {
    return localStorage.getItem(GLOBAL_VIEW_KEY) === "terminal" ? "terminal" : "conversation";
  } catch {
    return "conversation";
  }
}

class Terms {
  private shells = new SvelteMap<string, string[]>(); // session id -> discovered shell tmux names
  private active = new SvelteMap<string, string>(); // session id -> AGENT | shell tmux name
  private order = new SvelteMap<string, string[]>(); // session id -> hand-sorted tab order (see shellsFor)
  private names = new SvelteMap<string, Record<string, string>>(Object.entries(readNames())); // -> hand-given labels
  private sticky = readTabs(); // session id -> the tab a human last picked, across relaunches
  private lastTerm = new SvelteMap<string, string>(); // session id -> the terminal last on show
  private previous = new SvelteMap<string, string>(); // session id -> what Changes returns to
  private focusAsks = new SvelteMap<string, number>(); // session id -> requests for the keyboard
  /** Open sessions in: the preference behind `defaultView`. */
  globalView = $state<SessionViewKind>(readGlobalView());

  setGlobalView(v: SessionViewKind) {
    this.globalView = v;
    try {
      localStorage.setItem(GLOBAL_VIEW_KEY, v);
    } catch {
      /* the preference just does not persist */
    }
  }

  private introSession = "";
  private introDismissed = false;
  /**
   * The first-open line. The first conversation to ask claims it and keeps it for as long as it
   * stays mounted; nothing else ever gets it. A storage that throws means it shows once per run.
   */
  claimIntro(id: string): boolean {
    if (this.introDismissed) return false;
    if (this.introSession) return this.introSession === id;
    try {
      if (localStorage.getItem(INTRO_KEY)) return false;
      localStorage.setItem(INTRO_KEY, "seen");
    } catch {
      /* said once for this run */
    }
    this.introSession = id;
    return true;
  }
  /** Answered, so a remount of the same conversation does not bring the line back. */
  dismissIntro() {
    this.introDismissed = true;
  }

  /** Where this session would open, given what is known about it right now. */
  private resolve(id: string, feedKnown?: boolean): string {
    const saved = this.sticky[id];
    const kind: SessionViewKind | null = saved === SSH ? "conversation" : saved && !isViewTab(saved) ? "terminal" : null;
    const s = store.sessionById(id);
    const known = feedKnown ?? (s && readFeedBad()[s.agent || "claude"] ? false : undefined);
    const view = s ? defaultView(s, kind, this.globalView, known) : (kind ?? "terminal");
    if (view === "conversation") return SSH;
    return saved && !isViewTab(saved) ? saved : AGENT;
  }

  /** Shell tmux names open for session `id`, in the order its tabs are shown.
   *
   * Discovery order is the server's; a hand drag (moveTab) records an order that
   * is layered on top of it here rather than written back over the discovered
   * list, so the next `refresh` cannot undo a sort. Names the order doesn't know
   * (a shell opened since the drag) keep their discovered position at the end. */
  shellsFor(id: string): string[] {
    const names = this.shells.get(id) ?? [];
    const sorted = this.order.get(id);
    if (!sorted || names.length < 2) return names;
    const open = new Set(names);
    const front = sorted.filter((n) => open.has(n));
    if (!front.length) return names;
    const placed = new Set(front);
    return [...front, ...names.filter((n) => !placed.has(n))];
  }

  /** Move the tab at `from` to index `to` within this session's shell tabs (the
   * drag-to-sort). Indices are positions in `shellsFor`, which the new order
   * then becomes. Out-of-range or no-op moves are ignored. */
  moveTab(id: string, from: number, to: number) {
    const cur = this.shellsFor(id);
    if (from === to || from < 0 || to < 0 || from >= cur.length || to >= cur.length) return;
    const next = [...cur];
    next.splice(to, 0, ...next.splice(from, 1));
    this.order.set(id, next);
  }

  /** Whether a tab is this session's review pane (see isReviewTab). Exposed as a
   * method so the template can ask without a second import. */
  isReviewTab(name: string): boolean {
    return isReviewTab(name);
  }

  /** Whether a tab runs one of the project's dev_commands (see isDevTab). */
  isDevTab(name: string): boolean {
    return isDevTab(name);
  }

  /** Display label for a tab: a hand-given name if it has one, "Review" for the
   * review pane, else "Shell N" — N being the shell's 1-based position among the
   * SHELLS (the review pane must not shift it, and a drag renumbers them). */
  labelFor(id: string, name: string): string {
    const given = this.names.get(id)?.[name];
    if (given) return given;
    if (isReviewTab(name)) return "Review";
    // A dev tab is labelled with the COMMAND it runs, which is the only thing
    // that tells two of them apart at a glance. The command list travels on the
    // session (SessionInfo.devCommands, index N-1 for "<id>-dev-N"); a tab whose
    // command is gone from config falls back to its number.
    const dev = devTabIndex(id, name);
    if (dev > 0) {
      const cmd = store.sessionById(id)?.devCommands?.[dev - 1];
      return cmd || `Dev ${dev}`;
    }
    const i = this.shellsFor(id)
      .filter((n) => !isReviewTab(n) && !isDevTab(n))
      .indexOf(name);
    return i === -1 ? "Shell" : `Shell ${i + 1}`;
  }

  /** Rename a tab. A blank label CLEARS the name, so the tab falls back to
   * "Shell N" — that is the only way back, and it is why the rename field starts
   * on the current label rather than on an empty box. */
  rename(id: string, name: string, label: string) {
    const next = { ...(this.names.get(id) ?? {}) };
    const trimmed = label.trim().slice(0, 40); // a tab is a chip, not a paragraph
    if (trimmed) next[name] = trimmed;
    else delete next[name];
    this.setNames(id, next);
  }

  // setNames writes one session's labels through to storage. The whole map is
  // rewritten rather than patched: it is a handful of short strings, and a
  // partial write is the kind of bug that only shows up after a relaunch.
  private setNames(id: string, labels: Record<string, string>) {
    if (Object.keys(labels).length) this.names.set(id, labels);
    else this.names.delete(id);
    try {
      const all: NameMap = {};
      for (const [k, v] of this.names) all[k] = v;
      localStorage.setItem(NAMES_KEY, JSON.stringify(all));
    } catch {
      /* storage unavailable — the name still applies for this run */
    }
  }

  // dropNames forgets labels for tabs that are no longer open. A closed shell's
  // tmux name is reused by the next one ("<id>-shell-1" after shell 1 exits), so
  // a kept label would land on a shell nobody named.
  private dropNames(id: string, open: string[]) {
    const cur = this.names.get(id);
    if (!cur) return;
    const live = new Set(open);
    const next = Object.fromEntries(Object.entries(cur).filter(([n]) => live.has(n)));
    if (Object.keys(next).length !== Object.keys(cur).length) this.setNames(id, next);
  }

  /** The tab session `id` shows: AGENT, a view tab, or a shell name — never a stale/closed one. */
  activeTab(id: string): string {
    const a = this.active.get(id) ?? this.resolve(id);
    const s = store.sessionById(id);
    // Its last connection can go while it is on show, and nothing could then say why it is open.
    if (a === SSH && s && !conversationOffered(s)) return AGENT;
    return a !== AGENT && !isViewTab(a) && !this.shellsFor(id).includes(a) ? AGENT : a;
  }

  /**
   * Pins where an open session landed. Health arrives after paint, so without this a session
   * that learned its log is unreadable would flip under the person reading it. It never does:
   * the notice says so, and the NEXT open lands on the terminal (noteFeedHealth, leave).
   */
  arrive(id: string, feedKnown?: boolean) {
    if (!this.active.has(id)) this.active.set(id, this.resolve(id, feedKnown));
  }

  /** The pin only guards an open session. What nobody chose is decided afresh next time. */
  leave(id: string) {
    const a = this.active.get(id);
    if (!(id in this.sticky) && (a === SSH || a === AGENT)) this.active.delete(id);
  }

  /**
   * What a client last learned about an agent's log. Per agent, because a new version of one
   * breaks every session of it, and kept across a relaunch, or a log that nothing can read would
   * open on an empty conversation every time. A good answer clears it, which is how this ends.
   */
  noteFeedHealth(agent: string | undefined, health: string | undefined) {
    const known = feedKnownFrom(health);
    if (known === undefined) return;
    const key = agent || "claude";
    const bad = readFeedBad();
    if (!known === !!bad[key]) return;
    if (known) delete bad[key];
    else bad[key] = true;
    try {
      localStorage.setItem(FEED_BAD_KEY, JSON.stringify(bad));
    } catch {
      /* said again on the next open */
    }
  }

  /** The tab a human last picked for this session, if it opens on the terminal by choice. */
  stickyTerminal(id: string): boolean {
    const saved = this.sticky[id];
    return !!saved && saved !== SSH && !isViewTab(saved);
  }

  /** Switch tabs. Ignores a shell name that isn't open. */
  select(id: string, tab: string) {
    // Whatever opened Changes, leaving it goes back to where this came from.
    if (isChangesTab(tab) && !isChangesTab(this.activeTab(id))) this.previous.set(id, this.activeTab(id));
    if (!this.show(id, tab)) return;
    // Most recent last, so what falls off is what nobody has opened in a while.
    if (isChangesTab(tab)) return;
    delete this.sticky[id];
    this.sticky[id] = tab;
    for (const old of Object.keys(this.sticky).slice(0, -TABS_KEEP)) delete this.sticky[old];
    try {
      localStorage.setItem(TABS_KEY, JSON.stringify(this.sticky));
    } catch {
      /* the choice still holds for this run */
    }
  }

  /** Puts a tab on show without calling it the human's preference: a jump landing there. */
  show(id: string, tab: string): boolean {
    if (tab !== AGENT && !isViewTab(tab) && !this.shellsFor(id).includes(tab)) return false;
    this.active.set(id, tab);
    if (!isViewTab(tab)) this.lastTerm.set(id, tab);
    return true;
  }

  /** The terminal last used in this session, by default the agent's. */
  lastTerminal(id: string): string {
    const t = this.lastTerm.get(id) ?? AGENT;
    return t === AGENT || this.shellsFor(id).includes(t) ? t : AGENT;
  }

  /** Asks for the keyboard to go to this session's terminal. Consumed by its cell. */
  focusTerminal(id: string) {
    this.focusAsks.set(id, (this.focusAsks.get(id) ?? 0) + 1);
  }
  terminalFocusAsks(id: string): number {
    return this.focusAsks.get(id) ?? 0;
  }

  /** Where a session lives when a terminal it was on goes. An attached terminal defers queued sends. */
  private home(id: string): string {
    const s = store.sessionById(id);
    return s && conversationOffered(s) ? SSH : AGENT;
  }

  /** Toggle Changes: open it, or go back to the view it was opened from. */
  toggleDiff(id: string) {
    const cur = this.activeTab(id);
    if (isChangesTab(cur)) {
      // What it came from may have closed since, and there has to be a way out by keyboard.
      if (!this.show(id, this.previous.get(id) ?? this.resolve(id)) && !this.show(id, this.resolve(id))) this.show(id, this.home(id));
      return;
    }
    this.previous.set(id, cur);
    this.active.set(id, DIFF);
  }

  // refresh re-reads the tmux server for this session's shells so tabs reflect
  // shells opened anywhere (the TUI, another window). Best-effort — a tmux error
  // leaves the last-known list. Falls the active tab back if its shell vanished.
  async refresh(id: string): Promise<void> {
    if (!id) return;
    try {
      const names = (await TermService.Shells(id)) ?? [];
      // Polls often return a fresh array with exactly the same tabs. Preserve
      // identity and established order, so refreshes cannot replay tab layout.
      const previous = this.shells.get(id) ?? [];
      const present = new Set(names);
      const known = new Set(previous);
      const stable = [...previous.filter(n => present.has(n)), ...names.filter(n => !known.has(n))];
      if (stable.length !== previous.length || stable.some((n, i) => n !== previous[i])) this.shells.set(id, stable);
      this.dropNames(id, names);
      const a = this.active.get(id);
      if (a && a !== AGENT && !isViewTab(a) && !names.includes(a)) this.active.set(id, names.at(-1) ?? this.home(id));
    } catch {
      /* keep last-known */
    }
  }

  // nextName picks the next free "<id>-shell-N" (max known index + 1) from the
  // discovered list, so it doesn't collide with a shell opened in the TUI.
  private nextName(id: string): string {
    const prefix = `${id}-shell-`;
    let max = 0;
    for (const n of this.shellsFor(id).filter((n) => !isReviewTab(n) && !isDevTab(n))) {
      const k = Number(n.slice(prefix.length));
      if (Number.isFinite(k) && k > max) max = k;
    }
    return `${prefix}${max + 1}`;
  }

  // openShell creates a fresh shell tmux session and shows its tab. Optimistically
  // adds + activates it, then reconciles with discovery. The session must exist
  // BEFORE the LiveTerminal mounts, or its Attach races a missing session — hence
  // the await before recording it.
  async openShell(id: string, worktree: string): Promise<void> {
    if (!worktree) {
      store.setFlash("session has no worktree", "bad");
      return;
    }
    const name = this.nextName(id);
    try {
      await TermService.Shell(name, worktree);
      this.shells.set(id, [...this.shellsFor(id), name]);
      this.show(id, name);
      void this.refresh(id);
    } catch (err) {
      store.setFlash(String(err), "bad");
    }
  }

  /** File > Close Tab uses the same teardown as the tab's ×. Permanent views
   * have no close button, so they leave the session and app running. */
  async closeActiveTab(id: string): Promise<void> {
    const tab = this.activeTab(id);
    if (tab === AGENT || isViewTab(tab)) return;
    await this.closeShell(id, tab);
  }

  // closeShell drops a shell tab and kills its tmux session (the "×"). Flip UI
  // state FIRST so the LiveTerminal unmounts (and detaches) before the kill, then
  // reconcile.
  //
  // Closing a DEV tab is not a tab operation but a state change: the session
  // stops being the project's active one, so it goes through the daemon
  // (cmd=dev off), which stops every dev tab of the session and re-derives the
  // toggle. Killing the tmux session behind the daemon's back would leave the
  // app claiming "active" until the next observe cycle corrected it.
  async closeShell(id: string, name: string): Promise<void> {
    if (isDevTab(name)) {
      this.forget(id, name);
      await store.dev(id, false);
      void this.refresh(id);
      return;
    }
    this.forget(id, name);
    try {
      await TermService.CloseShell(name);
    } catch {
      /* best-effort teardown */
    }
    void this.refresh(id);
  }

  // shellExited retires a tab whose shell died on its own — the user typed `exit`,
  // or an Attach hit an already-gone session. The tmux session is already gone, so
  // this only prunes client state; nothing is killed.
  shellExited(id: string, name: string): void {
    this.forget(id, name);
  }

  // forget removes a shell from the list and, if it was the active tab, falls back
  // to the last remaining shell or the session's home, so a close never lands on a dead tab.
  private forget(id: string, name: string) {
    const rest = this.shellsFor(id).filter((s) => s !== name);
    if (rest.length === this.shellsFor(id).length) return; // not ours / already gone
    if (this.activeTab(id) === name) this.active.set(id, rest.at(-1) ?? this.home(id));
    if (this.sticky[id] === name) {
      delete this.sticky[id];
      try {
        localStorage.setItem(TABS_KEY, JSON.stringify(this.sticky));
      } catch {
        /* the choice just outlives the shell until the next write */
      }
    }
    this.shells.set(id, rest);
    this.dropNames(id, rest);
  }

  // openShell backs the "s" shortcut too — the limit is gone, so "s" always opens
  // a fresh shell and lands on it.
  newShell(id: string, worktree: string): void {
    void this.openShell(id, worktree);
  }
}

export const terms = new Terms();
