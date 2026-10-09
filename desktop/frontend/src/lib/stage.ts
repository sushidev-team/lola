// The session list's lifecycle chip: ONE plain-language step per session,
// derived from the delivery axis plus lola's reaction posture. It replaces the
// PR badge's glyph-and-word ("◇ review", "× ci failed") and the separate
// reaction note ("escalated") on the list: a glyph vocabulary has to be learned,
// and "ci failed · escalated" spent red three times on one fact.
//
// Tone is SEVERITY, never decoration, and each tone means one thing across the
// list:
//   bad     — a human must act (CI failing with retries spent, a conflict)
//   orange  — feedback the agent must address (changes requested)
//   warn    — lola or CI is handling it (CI running, lola retrying)
//   info    — waiting on a reviewer
//   good    — approved
//   done    — merged (GitHub's purple)
//   neutral — nothing is asked of anyone (draft, closed)
//
// Pure: it reads the same wire fields theme.ts does and invents no state.
import { reactionNote, reactionIsAlarm } from "./reaction";

export type StageTone = "bad" | "orange" | "warn" | "info" | "good" | "done" | "neutral";

export interface Stage {
  label: string;
  tone: StageTone;
  /** A longer explanation for the chip's tooltip. */
  hint: string;
}

export function stageFor(delivery: string | undefined, reacting: string | undefined): Stage | null {
  const note = reactionNote(reacting ?? "");
  switch (delivery) {
    case "draft":
      return { label: "Draft PR", tone: "neutral", hint: "the pull request is still a draft" };
    case "ci_pending":
      return { label: "CI running", tone: "warn", hint: "checks are running on the pull request" };
    case "ci_failed":
      if (reactionIsAlarm(note))
        return {
          label: "CI failing · needs you",
          tone: "bad",
          hint: "checks fail and lola has spent its retry budget — this needs a human",
        };
      if (note)
        return {
          label: `CI failing · ${note}`,
          tone: "warn",
          hint: "checks fail; lola is re-prompting the agent to fix them",
        };
      return { label: "CI failing", tone: "bad", hint: "checks fail on the pull request" };
    case "merge_conflict":
      return { label: "Merge conflict", tone: "bad", hint: "the branch conflicts with its base" };
    case "changes_requested":
      return { label: "Changes requested", tone: "orange", hint: "a reviewer asked for changes" };
    case "review_pending":
      return { label: "Awaiting review", tone: "info", hint: "checks pass; waiting for a review" };
    case "approved":
      return { label: "Approved", tone: "good", hint: "approved and ready to merge" };
    case "merged":
      return { label: "Merged", tone: "done", hint: "the pull request is merged" };
    case "closed":
      return { label: "Closed", tone: "neutral", hint: "the pull request was closed unmerged" };
    default:
      return null;
  }
}

// Literal class strings: Tailwind scans source text, so a composed
// `bg-${tone}/12` would compile to nothing.
export const STAGE_CHIP: Record<StageTone, string> = {
  bad: "bg-bad/12 text-bad",
  orange: "bg-orange/12 text-orange",
  warn: "bg-warn/12 text-warn",
  info: "bg-info/12 text-info",
  good: "bg-good/12 text-good",
  done: "bg-magenta/12 text-magenta",
  neutral: "bg-edge/40 text-faint",
};

export const STAGE_DOT: Record<StageTone, string> = {
  bad: "bg-bad",
  orange: "bg-orange",
  warn: "bg-warn",
  info: "bg-info",
  good: "bg-good",
  done: "bg-magenta",
  neutral: "bg-faint",
};
