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

/** The agent pane's tab key (a sentinel; every other tab is a shell tmux name). */
export const AGENT = "agent";

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

class Terms {
  private shells = new SvelteMap<string, string[]>(); // session id -> discovered shell tmux names
  private active = new SvelteMap<string, string>(); // session id -> AGENT | shell tmux name
  private order = new SvelteMap<string, string[]>(); // session id -> hand-sorted tab order (see shellsFor)
  private names = new SvelteMap<string, Record<string, string>>(Object.entries(readNames())); // -> hand-given labels

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

  /** The tab session `id` shows: AGENT, or a shell name — never a stale/closed one. */
  activeTab(id: string): string {
    const a = this.active.get(id) ?? AGENT;
    return a !== AGENT && !this.shellsFor(id).includes(a) ? AGENT : a;
  }

  /** Switch tabs. Ignores a shell name that isn't open. */
  select(id: string, tab: string) {
    if (tab !== AGENT && !this.shellsFor(id).includes(tab)) return;
    this.active.set(id, tab);
  }

  /** Cycle the active tab across [agent, …shells], wrapping. dir +1 next, -1 prev. */
  cycleTab(id: string, dir: number) {
    const tabs = [AGENT, ...this.shellsFor(id)];
    if (tabs.length <= 1) return; // only the agent — nothing to switch to
    const cur = Math.max(0, tabs.indexOf(this.activeTab(id)));
    const span = tabs.length;
    this.active.set(id, tabs[((cur + dir) % span + span) % span]);
  }

  // refresh re-reads the tmux server for this session's shells so tabs reflect
  // shells opened anywhere (the TUI, another window). Best-effort — a tmux error
  // leaves the last-known list. Falls the active tab back if its shell vanished.
  async refresh(id: string): Promise<void> {
    if (!id) return;
    try {
      const names = (await TermService.Shells(id)) ?? [];
      this.shells.set(id, names);
      this.dropNames(id, names);
      const a = this.active.get(id);
      if (a && a !== AGENT && !names.includes(a)) this.active.set(id, names.at(-1) ?? AGENT);
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
      this.active.set(id, name);
      void this.refresh(id);
    } catch (err) {
      store.setFlash(String(err), "bad");
    }
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
  // to the last remaining shell (or the agent), so a close never lands on a dead tab.
  private forget(id: string, name: string) {
    const rest = this.shellsFor(id).filter((s) => s !== name);
    if (rest.length === this.shellsFor(id).length) return; // not ours / already gone
    if (this.activeTab(id) === name) this.active.set(id, rest.at(-1) ?? AGENT);
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
