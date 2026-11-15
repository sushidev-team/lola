// The memory bound for per-session views fed by a pushed source (the phone). A view is held
// while something follows it; of the views nobody follows, only the IDLE_VIEWS_KEPT most recently
// released keep their story, and the rest are trimmed in place. Trimming never replaces the
// object, so a component still holding it reads an empty view that the next follow refills, and
// what a person typed (drafts, attachments) is not the view's to drop. Without a source nothing
// follows, so the desktop never trims.

export const IDLE_VIEWS_KEPT = 8;

export class ViewRetention {
  private holders = new Map<string, number>();
  /** Idle views, least recently released first. */
  private idle: string[] = [];

  constructor(
    private readonly trim: (id: string) => void,
    private readonly keep = IDLE_VIEWS_KEPT,
  ) {}

  /** Marks `id` followed until the returned release is called (once). */
  hold(id: string): () => void {
    this.holders.set(id, (this.holders.get(id) ?? 0) + 1);
    this.idle = this.idle.filter((x) => x !== id);
    let held = true;
    return () => {
      if (!held) return;
      held = false;
      const n = (this.holders.get(id) ?? 1) - 1;
      if (n > 0) {
        this.holders.set(id, n);
        return;
      }
      this.holders.delete(id);
      this.idle.push(id);
      while (this.idle.length > this.keep) this.trim(this.idle.shift()!);
    };
  }
}
