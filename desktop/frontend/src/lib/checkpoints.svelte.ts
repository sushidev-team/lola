// A session's turn checkpoints (SUSHI-624), shared by the two places that show
// them: the sidebar's Checkpoints tab (the list and its actions) and the main
// pane's checkpoint diff (what the selected turn changed). One copy per session,
// so selecting a row in the sidebar is what the diff pane reads, and a restore
// started from either side reloads both.
import { SvelteMap } from "svelte/reactivity";
import { DaemonService } from "@bindings/desktop";
import type { CheckpointInfo } from "@bindings/internal/protocol";
import { store } from "$lib/store.svelte";
import { confirm } from "$lib/confirm.svelte";

export type CheckpointState = {
  list: CheckpointInfo[];
  /** The daemon's verdict on a restore right now (false mid-turn). */
  restorable: boolean;
  error: string;
  loading: boolean;
  /** The selected checkpoint's seq; 0 = none. */
  selected: number;
  /** A restore or fork is in flight. */
  busy: boolean;
};

const EMPTY: CheckpointState = { list: [], restorable: false, error: "", loading: false, selected: 0, busy: false };

class Checkpoints {
  private by = new SvelteMap<string, CheckpointState>();
  // Latest load per session: an older answer that lands late is dropped.
  private gen = new Map<string, number>();

  of(id: string): CheckpointState {
    return this.by.get(id) ?? EMPTY;
  }

  private patch(id: string, p: Partial<CheckpointState>) {
    this.by.set(id, { ...this.of(id), ...p });
  }

  async load(id: string) {
    const my = (this.gen.get(id) ?? 0) + 1;
    this.gen.set(id, my);
    this.patch(id, { loading: true });
    try {
      const d = await DaemonService.Checkpoints(id);
      if (this.gen.get(id) !== my) return;
      const list = d.checkpoints ?? [];
      // Keep the selection when it still exists; otherwise the newest.
      const sel = this.of(id).selected;
      const selected = list.some((c) => c.seq === sel) ? sel : (list.at(-1)?.seq ?? 0);
      this.patch(id, { list, restorable: d.restorable, error: "", loading: false, selected });
    } catch (err) {
      if (this.gen.get(id) !== my) return;
      this.patch(id, { error: String(err), loading: false });
    }
  }

  select(id: string, seq: number) {
    this.patch(id, { selected: seq });
  }

  askRestore(id: string, c: CheckpointInfo) {
    confirm.ask({
      title: `Restore checkpoint #${c.seq}?`,
      body: `Put ${id}'s files back to checkpoint #${c.seq} (${c.label}).`,
      detail:
        "Every change since is undone in the worktree — including later commits, which stay in history and show up as uncommitted edits. The current state is saved as a new checkpoint first, so you can restore it again. Tell the agent afterwards: it still remembers the discarded changes.",
      confirmLabel: "Restore",
      onConfirm: async () => {
        this.patch(id, { busy: true });
        await store.restoreCheckpoint(id, c.seq);
        this.patch(id, { busy: false });
        await this.load(id);
      },
    });
  }

  /** Asks, then forks; `onForked` gets the new session's id. */
  askFork(id: string, c: CheckpointInfo, onForked: (sessionId: string) => void) {
    confirm.ask({
      title: `Fork from checkpoint #${c.seq}?`,
      body: `Start a new agent session from ${id} as it was at checkpoint #${c.seq} (${c.label}).`,
      detail:
        "The fork gets its own worktree and branch, holding the checkpoint's files, and shares this session's context folder. This session keeps running untouched.",
      confirmLabel: "Fork",
      onConfirm: async () => {
        this.patch(id, { busy: true });
        const r = await store.forkCheckpoint(id, c.seq);
        this.patch(id, { busy: false });
        if (r?.sessionId) onForked(r.sessionId);
      },
    });
  }
}

export const checkpoints = new Checkpoints();

/** A checkpoint's wall-clock time, or "" for an unparsable stamp. */
export function checkpointTime(iso: string): string {
  const d = new Date(iso);
  return isNaN(d.getTime()) ? "" : d.toLocaleTimeString([], { hour: "2-digit", minute: "2-digit", second: "2-digit" });
}
