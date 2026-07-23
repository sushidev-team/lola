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

class Terms {
  private shells = new SvelteMap<string, string[]>(); // session id -> discovered shell tmux names
  private active = new SvelteMap<string, string>(); // session id -> AGENT | shell tmux name

  /** Discovered shell tmux names currently open for session `id`. */
  shellsFor(id: string): string[] {
    return this.shells.get(id) ?? [];
  }

  /** Display label for a shell tab: its 1-based position, stable within the row. */
  labelFor(id: string, name: string): string {
    const i = this.shellsFor(id).indexOf(name);
    return i === -1 ? "shell" : `sh ${i + 1}`;
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
    for (const n of this.shellsFor(id)) {
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
  async closeShell(id: string, name: string): Promise<void> {
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
  }

  // openShell backs the "s" shortcut too — the limit is gone, so "s" always opens
  // a fresh shell and lands on it.
  newShell(id: string, worktree: string): void {
    void this.openShell(id, worktree);
  }
}

export const terms = new Terms();
