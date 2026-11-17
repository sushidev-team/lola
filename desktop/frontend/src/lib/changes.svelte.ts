// What a session has changed against its base, as two numbers, for the strip's `Changes +84 −12`.
//
// The wire has no such field and gains none, because anything on SessionInfo reaches phones. So
// this is one `cmd=diff` when a session opens and one each time its agent comes to rest, and only
// for the session on screen. DiffView reports what it loads, so a person on Changes costs nothing
// extra. An unknown figure renders as nothing, never as zero.
import { SvelteMap } from "svelte/reactivity";
import { DaemonService } from "@bindings/desktop";
import type { DiffData } from "@bindings/internal/protocol";

export type ChangeStat = { add: number; del: number; files: number };

class Changes {
  private stats = new SvelteMap<string, ChangeStat>();
  private gen = new Map<string, number>();
  private wanted = new SvelteMap<string, string>();

  /** A file someone asked to land on, from an edit row. Taken once by the diff that can show it. */
  want(id: string, path: string) {
    this.wanted.set(id, path);
  }
  /** What is being asked for, without taking it: the diff may not have loaded yet. */
  wantedFile(id: string): string {
    return this.wanted.get(id) ?? "";
  }
  taken(id: string) {
    this.wanted.delete(id);
  }

  of(id: string): ChangeStat | undefined {
    return this.stats.get(id);
  }

  /** The totals of a base diff someone already fetched. */
  note(id: string, d: DiffData | null | undefined) {
    if (!d) return;
    let add = 0;
    let del = 0;
    for (const f of d.files ?? []) {
      add += f.additions;
      del += f.deletions;
    }
    this.stats.set(id, { add, del, files: d.files?.length ?? 0 });
  }

  /** A failed read keeps the last figure: a stale count is better than one that blinks. */
  async load(id: string) {
    const my = (this.gen.get(id) ?? 0) + 1;
    this.gen.set(id, my);
    try {
      const d = await DaemonService.Diff(id);
      if (this.gen.get(id) === my) this.note(id, d);
    } catch {
      /* no figure, or the last one */
    }
  }
}

export const changes = new Changes();
