// Best-of-N (SUSHI-623): the compare overlay's state. One group at a time —
// the overlay is opened FROM a session, and the daemon resolves that session to
// its group — so this holds the session it was opened from plus the latest
// comparison, and a stale answer for a session the user has since left is
// dropped.
import { DaemonService } from "@bindings/desktop";
import type { BestOfCompareData } from "@bindings/internal/protocol";
import { store } from "$lib/store.svelte";
import { nav } from "$lib/nav.svelte";
import { confirm } from "$lib/confirm.svelte";

class BestOf {
  /** The session the overlay was opened from ("" = closed). */
  session = $state("");
  data = $state<BestOfCompareData | null>(null);
  loading = $state(false);
  error = $state("");
  /** A pick is in flight. */
  busy = $state(false);
  /** Steps the last pick could not complete (a PR left draft/open, a teardown). */
  problems = $state<string[]>([]);
  private gen = 0;

  open(id: string) {
    this.session = id;
    this.data = null;
    this.error = "";
    this.problems = [];
    nav.openOverlay("bestof");
    void this.load();
  }

  close() {
    this.session = "";
    this.data = null;
    nav.closeOverlay();
  }

  /**
   * Re-read every `everyMs` until stopped, starting the next read only once the
   * previous one has SETTLED: a comparison can outlast the interval, and a newer
   * read would discard the older answer — every time, if each one is slow.
   */
  poll(everyMs: number): () => void {
    let stopped = false;
    let timer: ReturnType<typeof setTimeout> | undefined;
    const tick = async () => {
      await this.load();
      if (!stopped) timer = setTimeout(tick, everyMs);
    };
    timer = setTimeout(tick, everyMs);
    return () => {
      stopped = true;
      clearTimeout(timer);
    };
  }

  async load() {
    const id = this.session;
    if (!id) return;
    const my = ++this.gen;
    this.loading = true;
    try {
      const d = await DaemonService.BestOfCompare(id);
      if (my !== this.gen) return;
      this.data = d;
      this.error = "";
    } catch (err) {
      if (my !== this.gen) return;
      this.error = String(err);
    } finally {
      if (my === this.gen) this.loading = false;
    }
  }

  /** Ask, then keep `id` and discard every other attempt of its group. */
  askPick(id: string) {
    const d = this.data;
    const others = (d?.attempts ?? []).filter((a) => a.session !== id);
    const withPR = others.filter((a) => (a.prNumber ?? 0) > 0 && a.prState?.toUpperCase() === "OPEN");
    confirm.ask({
      title: "Keep this attempt?",
      body: `Keep ${id} for ${d?.issue ?? "this issue"} and discard the other ${others.length}.`,
      detail:
        (withPR.length
          ? `Their PRs (${withPR.map((a) => `#${a.prNumber}`).join(", ")}) are closed — branches kept. `
          : "") +
        "Their agents are stopped and their worktrees removed; a worktree with uncommitted changes is kept. The kept PR is marked ready for review.",
      confirmLabel: "Keep it",
      onConfirm: async () => {
        this.busy = true;
        const r = await store.bestOfPick(id);
        this.busy = false;
        if (r) {
          this.problems = r.problems ?? [];
          await this.load();
        }
      },
    });
  }
}

export const bestOf = new BestOf();
