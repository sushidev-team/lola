// The inventory behind the pane tab strip and the pane menu sheet: which panes a
// session has, what this device calls them, and the three things done to them
// (create a shell, close a pane, rename a tab). The strip component keeps the
// gestures and the markup; the sheet is hosted outside it and reads the same
// instance.
//
// Invariants:
//  - The inventory is derived from tmux on every `cmd=panes`; nothing here
//    re-sorts or caches it beyond the latest answer.
//  - A superseded answer is dropped whole (`seq`), so a stale list can neither
//    resurrect a closed tab nor retire the wrong size-pin record.
//  - Only a SUCCESSFUL load prunes nicknames and reports the inventory: a
//    refused or unsupported `cmd=panes` means "unknown", never "empty".
//  - An empty inventory navigates nowhere; a pane that is still listed is never
//    left; otherwise the tab that inherited the position is taken.
//  - A refusal is shown in the daemon's own sentence.
//  - One instance. `release()` returns it to its initial state when the strip
//    goes away, so nothing from one session's panes is drawn for the next.

import { DaemonService } from "@mobile/wailsshim";
import { PANE_KIND_AGENT, type PaneInfo } from "@mobile/wire";
import { forgetOwnShell, rememberOwnShell } from "@mobile/lib/panepinstore";
import { copy, fill } from "@mobile/lib/copy";
import { loadPaneLabels, prunePaneLabels, savePaneLabel } from "@mobile/lib/prefs";

/** What the strip knows about its surroundings, read at call time. */
export interface PaneTabsContext {
  session: string;
  /** The pane the terminal is attached to. */
  active: string;
  /** What the phone can show, in cells; a new shell is born at this size. */
  capacity: { cols: number; rows: number };
  /** Whether this phone may start shells (the `shell` tier). */
  canShell: boolean;
  onselect(pane: string): void;
  onnotice?(message: string): void;
  /** The live inventory after every successful load. */
  onpanes?(session: string, names: string[]): void;
}

const CLOSED = "That pane closed.";

/** `unknown cmd "panes"`: a Mac whose daemon predates the command. */
function isUnknownCommand(err: unknown): boolean {
  return /^unknown cmd\b/i.test(err instanceof Error ? err.message : "");
}

const sentence = (err: unknown, fallback: string): string =>
  err instanceof Error && err.message ? err.message : fallback;

export class PaneTabsModel {
  panes = $state<PaneInfo[]>([]);
  canCreateShell = $state(false);
  creating = $state(false);
  /** The pane a close is in flight for, or "". */
  closing = $state("");
  /** The Mac's daemon does not know `cmd=panes`. */
  unsupported = $state(false);
  /** True once an inventory has been read for the current session. */
  loaded = $state(false);
  /** Nicknames on this device, by tmux pane name. */
  labels = $state<Record<string, string>>(loadPaneLabels());
  /** The pane whose menu is open, or "". */
  menuPane = $state("");

  private ctx: (() => PaneTabsContext) | null = null;
  private seq = 0;

  /** The pane the open menu is about. */
  get menuFor(): PaneInfo | undefined {
    return this.panes.find((p) => p.name === this.menuPane);
  }

  /** Called by the strip with a getter over its props. */
  bind(ctx: () => PaneTabsContext): void {
    this.ctx = ctx;
  }

  release(): void {
    this.ctx = null;
    this.seq++;
    this.panes = [];
    this.canCreateShell = false;
    this.creating = false;
    this.closing = "";
    this.unsupported = false;
    this.loaded = false;
    this.menuPane = "";
    this.labels = loadPaneLabels();
  }

  /**
   * The daemon's label in Sentence case: "agent" -> "Agent", "shell 2" ->
   * "Shell 2". The label is the daemon's English word for the pane's kind, so
   * the two known shapes come from copy and anything else (a dev tab, the
   * review pane) only has its first letter raised.
   */
  defaultName(p: PaneInfo): string {
    if (p.kind === PANE_KIND_AGENT) return copy.terminal.panes.agent;
    const raw = p.label || p.name;
    const shell = /^shell\s*(\d+)$/i.exec(raw);
    if (shell) return fill(copy.terminal.panes.shell, { n: shell[1] });
    return raw.charAt(0).toUpperCase() + raw.slice(1);
  }

  /** What a tab shows: this device's nickname, else the Sentence-case label. */
  displayName(p: PaneInfo): string {
    return this.labels[p.name] || this.defaultName(p);
  }

  /**
   * The accessible name of a renamed tab carries both names, so it stays
   * identifiable across this phone and the Mac. Undefined when never renamed.
   */
  tabName(p: PaneInfo): string | undefined {
    const nick = this.labels[p.name];
    if (!nick) return undefined;
    const from = this.defaultName(p);
    return nick === from ? nick : `${nick} (${from})`;
  }

  /**
   * Reads the inventory for `id` and drops the answer when the strip has since
   * moved on. `announce` says whether a pane vanishing is news: false when the
   * user did the vanishing, true when it died on its own.
   */
  async load(id: string, announce = false): Promise<void> {
    const ctx = this.ctx?.();
    if (!ctx) return;
    if (id === "") {
      this.panes = [];
      this.canCreateShell = false;
      this.unsupported = false;
      this.loaded = false;
      return;
    }
    // Captured before the await: where the attached pane sat in the old list is
    // the only thing that says where to go if it is not in the new one.
    const wasAt = this.panes.findIndex((p) => p.name === ctx.active);
    const seq = ++this.seq;
    try {
      const d = await DaemonService.Panes(id);
      if (!this.current(id, seq)) return;
      this.panes = d.panes;
      this.canCreateShell = d.canCreateShell;
      this.unsupported = false;
      this.loaded = true;
      this.labels = prunePaneLabels(d.panes.map((p) => p.name));
      ctx.onpanes?.(id, d.panes.map((p) => p.name));
      if (this.menuPane !== "" && !d.panes.some((p) => p.name === this.menuPane)) {
        this.menuPane = "";
      }
      this.reconcileActive(wasAt, announce);
    } catch (err) {
      if (!this.current(id, seq)) return;
      this.panes = [];
      this.canCreateShell = false;
      this.unsupported = isUnknownCommand(err);
      this.loaded = false;
      this.menuPane = "";
    }
  }

  private current(id: string, seq: number): boolean {
    return this.ctx?.().session === id && seq === this.seq;
  }

  /**
   * `onselect` comes before `onnotice`: the screen's attach clears its notice on
   * the way through, so a sentence sent first would be wiped by the move it
   * describes.
   */
  private reconcileActive(wasAt: number, announce: boolean): void {
    const ctx = this.ctx?.();
    if (!ctx || this.panes.length === 0 || ctx.active === "") return;
    if (this.panes.some((p) => p.name === ctx.active)) return;
    const next = this.panes[Math.min(Math.max(wasAt, 0), this.panes.length - 1)] ?? this.panes[0];
    if (!next) return;
    ctx.onselect(next.name);
    if (announce) ctx.onnotice?.(`${CLOSED} Showing ${this.displayName(next)}.`);
  }

  /**
   * Starts a shell and attaches to it. The daemon allocates the index; a shell
   * this phone started is remembered so it is sized to the phone.
   */
  async createShell(): Promise<void> {
    const ctx = this.ctx?.();
    if (!ctx || this.creating || !ctx.canShell || !this.canCreateShell) return;
    this.creating = true;
    try {
      const d = await DaemonService.ShellCreate(ctx.session, ctx.capacity.cols, ctx.capacity.rows);
      rememberOwnShell(d.pane);
      ctx.onnotice?.("");
      await this.load(ctx.session);
      ctx.onselect(d.pane);
    } catch (err) {
      ctx.onnotice?.(sentence(err, "Could not start a shell."));
    } finally {
      this.creating = false;
    }
  }

  /** Closes a pane on the Mac and re-reads what is left. Never the agent pane. */
  async closePane(p: PaneInfo): Promise<void> {
    const ctx = this.ctx?.();
    if (!ctx || this.closing !== "" || p.kind === PANE_KIND_AGENT) return;
    this.closing = p.name;
    try {
      await DaemonService.PaneClose(ctx.session, p.name);
      forgetOwnShell(p.name);
      ctx.onnotice?.("");
      this.menuPane = "";
      await this.load(ctx.session);
    } catch (err) {
      ctx.onnotice?.(sentence(err, "Could not close that pane."));
      this.menuPane = "";
    } finally {
      this.closing = "";
    }
  }

  /** Stores or forgets a nickname and redraws from what was stored. */
  rename(p: PaneInfo, label: string): void {
    savePaneLabel(p.name, label);
    this.labels = loadPaneLabels();
    this.menuPane = "";
  }
}

export const paneTabs = new PaneTabsModel();
