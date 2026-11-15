import type { SessionInfo } from "$lib/store.svelte";
import { changesOffered } from "$lib/sessionview";
import type { Seg } from "@mobile/lib/router.svelte";

/**
 * The segments a session offers (spec §5.2). Conversation always; Changes
 * needs a checkout; Review needs a PR or a review package; Terminal needs a
 * pane or a shell.
 *
 * A plain module, not Session.svelte's module script: a segment that reads it
 * must not import the screen, or the bundler folds Session into a shared chunk
 * and the bundle budget stops measuring it.
 */
export function offeredSegments(s: SessionInfo | undefined, packages: number): Seg[] {
  const out: Seg[] = ["conversation"];
  if (!s) return out;
  if (changesOffered(s)) out.push("changes");
  if (s.prNumber > 0 || packages > 0) out.push("review");
  if (s.tmuxName !== "" || s.status === "shell" || s.agentState === "shell") out.push("terminal");
  return out;
}
