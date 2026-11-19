// Pickup settings for several projects at once (the sidebar's bulk menu). The
// dialog edits a draft held here; the daemon does the work (cmd=bulkProjects)
// and saves all projects or none.

import { DaemonService } from "@bindings/desktop";
import type { BulkProjectsArgs, BulkProjectChange } from "@bindings/internal/protocol/models";
import { textToLines } from "./lines";

export type PickupDraft = {
  matchLabels: string;
  matchMode: string;
  onSent: string;
  blocked: string;
  keepNames: boolean;
};

const blank = (): PickupDraft => ({ matchLabels: "", matchMode: "", onSent: "", blocked: "", keepNames: false });

/** The request a draft makes. A blank field is left alone; nothing set returns null. */
export function pickupArgs(projects: string[], d: PickupDraft, dryRun: boolean): BulkProjectsArgs | null {
  const labels = textToLines(d.matchLabels);
  const args: BulkProjectsArgs = { projects, keepNames: d.keepNames, dryRun };
  if (labels.length) args.matchLabels = labels;
  if (d.matchMode) args.matchMode = d.matchMode;
  if (d.onSent.trim()) args.onSentSetLabel = d.onSent.trim();
  if (d.blocked.trim()) args.blockedLabel = d.blocked.trim();
  return args.matchLabels || args.matchMode || args.onSentSetLabel || args.blockedLabel ? args : null;
}

class BulkPickup {
  projects = $state<string[] | null>(null);
  draft = $state<PickupDraft>(blank());
  busy = $state(false);
  error = $state("");
  /** Outcome of the last preview (applied=false) or save (applied=true). */
  changes = $state<BulkProjectChange[] | null>(null);
  applied = $state(false);

  open(projects: string[]) {
    this.projects = projects;
    this.draft = blank();
    this.error = "";
    this.changes = null;
    this.applied = false;
  }

  close() {
    this.projects = null;
  }

  async run(dryRun: boolean) {
    if (!this.projects || this.busy) return;
    const args = pickupArgs(this.projects, this.draft, dryRun);
    if (!args) {
      this.error = "Set at least one field.";
      return;
    }
    this.busy = true;
    this.error = "";
    try {
      const r = await DaemonService.BulkProjects(args);
      this.changes = r.changes ?? [];
      this.applied = r.applied;
    } catch (err) {
      this.error = String(err).replace(/^Error:\s*/, "");
      this.changes = null;
    } finally {
      this.busy = false;
    }
  }
}

export const bulkPickup = new BulkPickup();
