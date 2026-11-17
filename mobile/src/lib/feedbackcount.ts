import { feedback } from "$lib/feedback.svelte";
import { parseReviewNote } from "$lib/reviewnote";

/**
 * How many items the phone's feedback sheet lists: each line draft, each review-package
 * comment riding in the note, and the person's own text as one more. Display only; the
 * send gate stays on `feedback.count`.
 */
export function listedFeedbackCount(id: string): number {
  const p = parseReviewNote(feedback.noteFor(id));
  return feedback.draftsFor(id).length + p.comments.length + (p.free.trim() ? 1 : 0);
}
