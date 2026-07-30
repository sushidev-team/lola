// The reaction posture (PLAN P3), filtered down to what is actually NEW.
//
// The daemon derives SessionInfo.reacting from status + ciRetries + escalated
// (reactingLabel in internal/daemon/server.go). For four of its six outcomes
// that derivation is a pure relabelling of the status it was handed:
//
//   review_pending    -> "awaiting review"
//   changes_requested -> "addressing review"
//   merge_conflict    -> "rebasing"
//   approved          -> "ready to merge"
//
// Those carry no information the status pill on the same row does not already
// carry — which is why the cockpit's REACTING column read as a second, vaguer
// status column and left people asking what "reacting" even meant. The two
// remaining outcomes DO say something new, because neither is recoverable from
// the status:
//
//   "ci retry 1/2" — lola is auto-retrying, and how much budget is left
//   "escalated"    — the retries are spent and it is a human's problem now
//
// Keeping the budget in the string (rather than shipping ciRetries and the cap
// as separate fields and reformatting here) means the daemon stays the single
// author of that sentence.
export function reactionNote(reacting: string): string {
  if (!reacting) return "";
  if (reacting === "escalated" || reacting.startsWith("ci retry")) return reacting;
  return "";
}

/** Whether the note is an alarm (a human must act) rather than progress. */
export function reactionIsAlarm(note: string): boolean {
  return note === "escalated";
}
