// Cross-session chips: the [merge_queue] posture and the overlap warning
// (SessionInfo.mergeQueue / SessionInfo.overlaps). Pure helpers so the wording
// is tested once and both the list and any future card say the same thing.
//
// Both are DISPLAY-ONLY on the daemon side too: an overlap is a hint for a
// human (another session of the project edits the same files), never an input
// to dispatch, reactions or the queue.
import type { MergeQueueInfo, SessionOverlap } from "@bindings/internal/protocol";
import type { StageTone } from "./stage";

export interface Chip {
  label: string;
  tone: StageTone;
  hint: string;
}

const QUEUE_STEPS: Record<string, { label: string; tone: StageTone; hint: string }> = {
  queued: { label: "queued", tone: "neutral", hint: "waiting behind earlier PRs in lola's merge queue" },
  waiting: { label: "waiting", tone: "warn", hint: "next to land — waiting for CI or GitHub's mergeability check" },
  syncing: {
    label: "syncing",
    tone: "warn",
    hint: "next to land — the agent was asked to merge the default branch in so CI runs on what will land",
  },
  merging: { label: "merging", tone: "good", hint: "next to land — lola issued the merge" },
  conflict: { label: "conflict", tone: "bad", hint: "next to land — conflicts with the default branch; the queue holds behind it" },
  blocked: {
    label: "held",
    tone: "bad",
    hint: "next to land — held because a fact the queue needs is unknown (stale PR facts, no checks, another base branch)",
  },
  failed: { label: "refused", tone: "bad", hint: "next to land — GitHub refused the merge; lola retries after a cool-down" },
};

/** The merge-queue chip, or null when the session is not queued. */
export function mergeQueueChip(q: MergeQueueInfo | null | undefined): Chip | null {
  if (!q || q.position <= 0) return null;
  const step = QUEUE_STEPS[q.step] ?? { label: q.step, tone: "neutral" as StageTone, hint: "in lola's merge queue" };
  return { label: `Queue #${q.position} · ${step.label}`, tone: step.tone, hint: step.hint };
}

/** The overlap chip, or null when no other session edits the same files. */
export function overlapChip(overlaps: SessionOverlap[] | null | undefined): Chip | null {
  if (!overlaps?.length) return null;
  const lines = overlaps.map((o) => {
    const files = o.files ?? [];
    const more = (o.more ?? 0) + Math.max(0, files.length - 5);
    const shown = files.slice(0, 5).join(", ") + (more > 0 ? ` +${more} more` : "");
    return `${o.issue || o.session}: ${shown}`;
  });
  const who = overlaps.length === 1 ? overlaps[0].issue || overlaps[0].session : `${overlaps.length} sessions`;
  return {
    label: `Overlaps ${who}`,
    tone: "warn",
    hint: `Another session of this project edits the same files — merging both will likely conflict.\n${lines.join("\n")}`,
  };
}
