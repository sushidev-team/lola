// Session → presentation, the desktop mirror of Go's internal/state.
//
// THE FILE HAS TWO HALVES, and the split is the whole point.
//
// (1) THE TWO-AXIS HALF — what the desktop renders. A session is two
//     orthogonal facts: the AGENT axis (what the runner is doing) and the
//     DELIVERY axis (where its PR stands). The primary pill is the agent axis
//     reduced to the `Display` vocabulary; the secondary chip is the delivery
//     axis; and "does a human need to look at this" is a PREDICATE over both
//     (`attention`) rather than a value either axis can hold. Mirrors
//     internal/state/display.go — desktop/state_parity_test.go pins the
//     vocabulary and the kanban columns against it.
//
// (2) THE LEGACY HALF — the 16-word rolled-up status string (state.Rollup),
//     still shipped on protocol.SessionInfo.status and still the ONLY
//     vocabulary the mobile companion reads. Every function down there takes a
//     status STRING, and its signature is load-bearing: mobile/ resolves
//     `$lib/*` straight at this directory through a vite alias, so a signature
//     change here is a compile break in a project this one may not edit.
//     Nothing in the legacy half is deprecated-by-neglect — it is the wire
//     compatibility layer, and it stays until mobile migrates itself.
//
// Both halves are ports of the same Go package, so neither may invent a word
// the daemon cannot produce.

// ---------------------------------------------------------------------------
// (1) THE AGENT AXIS — the primary pill
//
// Every function in this half takes `string | undefined`, because that is what
// a caller holding a SessionInfo actually has: agentState, delivery and
// inputReason are all optional on the wire (a daemon predating the axis split
// sends none of them). Making them required would put a `?? ""` at forty call
// sites to say what the default branch of each switch already says.
// ---------------------------------------------------------------------------

/**
 * The primary pill vocabulary: what the agent runner is doing, derived from the
 * agent axis ALONE and never masked by PR facts. Mirrors Go's state.Display.
 *
 * Deliberately smaller than the agent axis itself — `starting` collapses into
 * `working` (a spawn that has not heartbeat yet is still a running agent, and a
 * pill that flickers for one observer cycle is noise) and `exited`/`dead`
 * collapse into `gone` (the difference matters to teardown, not to a reader).
 */
export type Display = "working" | "idle" | "needs_you" | "gone" | "shell" | "orphaned";

/**
 * The complete Display vocabulary, busiest first and terminal last, mirroring
 * Go's state.AllDisplays(). desktop/state_parity_test.go parses this array and
 * fails the build when the two drift — keep the order identical.
 */
export const ALL_DISPLAYS: Display[] = [
  "working", "idle", "needs_you", "gone", "shell", "orphaned",
];

/**
 * Reduce the agent axis to the primary pill vocabulary (state.DisplayFor).
 *
 * An unrecognized or ABSENT agentState reports "working", matching Go's default
 * and kanbanColumn's fallback for the same reason: a state the vocabulary has
 * not caught up to is most likely a LIVE agent, and drawing a live agent as
 * "gone" would hide it from the very views built to surface it. That also
 * covers the empty string an older daemon sends, which is a real case — the
 * axes are optional on the wire.
 */
export function displayFor(agentState: string | undefined): Display {
  switch (agentState) {
    case "idle":
      return "idle";
    case "waiting_input":
      return "needs_you";
    case "exited":
    case "dead":
      return "gone";
    case "shell":
      return "shell";
    case "orphaned":
      return "orphaned";
    default:
      // "working", "starting", "", and anything unknown.
      return "working";
  }
}

/** Human label for a Display value. */
export function displayLabel(d: Display): string {
  return d === "needs_you" ? "needs you" : d;
}

/**
 * Tailwind classes for the primary pill's fill. All six values are VISUALLY
 * DISTINCT, which is the property the pill was missing while it carried the
 * rolled-up status: sixteen words collapsed onto six colours meant a reader had
 * to know the vocabulary to tell "the agent is stuck" from "the PR is parked".
 * Six words on six treatments needs no vocabulary.
 *
 * The two loud ones are filled and the four quiet ones are bare text, so a
 * screenful of parked sessions has exactly as many fills as it has sessions
 * doing something.
 */
export function displayPill(d: Display): string {
  switch (d) {
    case "needs_you":
      // The only reason a human is on the critical path for the AGENT axis, so
      // it takes the loudest treatment the palette has.
      return "bg-pill-urgent text-pill-urgent-fg font-semibold";
    case "working":
      return "bg-pill-work text-pill-work-fg";
    case "idle":
      return "bg-pill-grey text-pill-grey-fg";
    case "orphaned":
      // An adoption anomaly: a lola pane with no matching worktree. Noticeable
      // without being an alarm — nothing is broken for the user, and lola never
      // kills one.
      return "text-bad";
    case "shell":
      // `lola open`: an agentless checkout, which never had an agent to have a
      // state. Informational, not a status.
      return "text-magenta";
    default:
      // gone — the quietest thing on the screen. Nothing is running and nothing
      // is waiting.
      return "text-faint";
  }
}

/**
 * Bare text colour for a Display value, for surfaces that print the word
 * without a fill behind it. Distinct across all six for the same reason
 * displayPill is; never returns a colour that assumes a fill.
 */
export function displayText(d: Display): string {
  switch (d) {
    case "working":
      return "text-info";
    case "idle":
      // Resting, and nothing is wrong: the app's ordinary foreground, a step
      // above `gone`'s faint and below the coloured states.
      return "text-ink";
    case "needs_you":
      return "text-orange";
    case "shell":
      return "text-magenta";
    case "orphaned":
      return "text-bad";
    default:
      return "text-faint"; // gone
  }
}

/**
 * WHY the agent is blocked (protocol.SessionInfo.inputReason → state's
 * InputReason). "needs you" on its own is a status; "needs you · permission
 * prompt" is an instruction, and the difference is whether the reader has to
 * open the terminal to find out what is being asked.
 *
 * Returns "" for anything outside the four answerable reasons — including
 * `idle_notification`, which the daemon no longer files under waiting_input at
 * all (it was 90% of the old needs_input traffic and 0% of its questions) but
 * which a snapshot written before that change can still carry. A record from
 * that era says "needs you" with no explanation rather than an explanation that
 * is not true any more.
 */
export function inputReasonLabel(reason: string | undefined): string {
  switch (reason) {
    case "question":
      return "question";
    case "permission_prompt":
      return "permission prompt";
    case "dialog":
      return "dialog";
    case "quota_limited":
      return "usage limit";
    default:
      return "";
  }
}

// ---------------------------------------------------------------------------
// (1b) THE DELIVERY AXIS — the secondary chip
// ---------------------------------------------------------------------------

/**
 * Human label for a delivery state. Delegates to statusLabel rather than
 * repeating its table: every delivery word IS a rolled-up status word (Rollup
 * returns the delivery word verbatim post-PR), so two spellings of "conflict"
 * would be two spellings of the same fact.
 */
export function deliveryLabel(delivery: string | undefined): string {
  return !delivery || delivery === "none" ? "" : statusLabel(delivery);
}

/**
 * One-character mark for a delivery state, so the chip reads at a glance in a
 * table cell that is mostly a PR number. All BMP glyphs a system UI font
 * carries — nothing that falls back to Apple Color Emoji, which paints its own
 * multi-colour art at 12px (see the skull in SessionEmbed).
 */
export function deliveryGlyph(delivery: string | undefined): string {
  const m: Record<string, string> = {
    draft: "◌",
    ci_pending: "⧗",
    ci_failed: "✕",
    merge_conflict: "⚠",
    changes_requested: "✎",
    review_pending: "◇",
    approved: "✓",
    merged: "◆",
    closed: "⊘",
  };
  return (delivery && m[delivery]) || "";
}

/** Tailwind text colour for a delivery state. Printed bare, never on a fill. */
export function deliveryText(delivery: string | undefined): string {
  switch (delivery) {
    case "ci_failed":
    case "merge_conflict":
      return "text-bad";
    case "changes_requested":
      return "text-orange";
    case "ci_pending":
      return "text-warn";
    case "review_pending":
      return "text-info";
    case "approved":
      return "text-good";
    default:
      // draft / merged / closed / none: the PR is not asking for anything.
      return "text-faint";
  }
}

// ---------------------------------------------------------------------------
// (1c) PREDICATES OVER BOTH AXES
// ---------------------------------------------------------------------------

/**
 * Does this session put a human on the critical path? A PREDICATE over both
 * axes (state.Attention), not a status value, because the two reasons a human
 * is needed live on different axes and can be true at once: the agent is
 * blocked on a person, or its delivered work regressed.
 *
 * Collapsing those into one word is what made the old `needs_input` both
 * over-broad and lossy — a red CI on a happily working agent had to pick one.
 */
export function attention(agentState: string | undefined, delivery: string | undefined): boolean {
  if (agentState === "waiting_input") return true;
  return delivery === "ci_failed" || delivery === "changes_requested" || delivery === "merge_conflict";
}

/**
 * Attention-first sort tier over both axes (state.SortRank). Lower sorts first:
 *
 *   0  blocked on a human
 *   1  action needed — the delivered work regressed
 *   2  actively working, or a PR still moving under its own steam
 *   3  parked for review
 *   4  quiet (idle / shell / orphaned / unknown)
 *   5  done
 *
 * The cases are evaluated in exactly the order written and that order is the
 * contract: a working agent whose CI just went red sorts as tier 1 (fix it),
 * not tier 2.
 */
export function sortRank(agentState: string | undefined, delivery: string | undefined): number {
  if (agentState === "waiting_input") return 0;
  if (delivery === "ci_failed" || delivery === "changes_requested" || delivery === "merge_conflict") return 1;
  if (agentState === "working" || agentState === "starting") return 2;
  if (delivery === "ci_pending" || delivery === "draft") return 2;
  if (delivery === "review_pending" || delivery === "approved") return 3;
  if (agentState === "dead" || agentState === "exited") return 5;
  if (delivery === "merged" || delivery === "closed") return 5;
  return 4;
}

/**
 * Kanban columns: a stable key and a human title, mirroring Go's
 * state.KanbanColumns() left-to-right by triage priority (the leftmost column
 * is the human's queue). desktop/state_parity_test.go pins key + title + order.
 *
 * It carries NO status set any more. Membership is a function of the PAIR and
 * is answered by kanbanKey; a list of collapsed status words could not express
 * "working agent, red CI" landing in Fixing while its agent axis stays visible
 * on the card.
 */
export const KANBAN_COLUMNS: { key: string; title: string }[] = [
  { key: "needs", title: "Needs You" },
  { key: "working", title: "Working" },
  { key: "fixing", title: "Fixing" },
  { key: "review", title: "In Review" },
  { key: "done", title: "Done" },
];

/**
 * Which kanban column a session falls in, by key (state.KanbanKeyFor).
 *
 * A session can satisfy several rules at once (a dead pane over a merged PR, a
 * waiting agent over a red build), so the order below IS the semantics and the
 * first match wins:
 *
 *  1. done — the session is over, whichever axis ended it.
 *  2. needs — a human is blocked. Ahead of fixing because they cannot act on
 *     the CI failure until they have answered the agent anyway.
 *  3. fixing — the delivered work regressed.
 *  4. review — parked on someone else.
 *  5. working — everything else, and the catch-all for an unknown state.
 */
export function kanbanKey(agentState: string | undefined, delivery: string | undefined): string {
  if (agentState === "dead" || agentState === "exited") return "done";
  if (delivery === "merged" || delivery === "closed") return "done";
  if (agentState === "waiting_input") return "needs";
  if (delivery === "ci_failed" || delivery === "changes_requested" || delivery === "merge_conflict") return "fixing";
  if (delivery === "review_pending" || delivery === "approved") return "review";
  return "working";
}

/** The column TITLE for a pair — kanbanKey's answer, spelled for a human. */
export function kanbanTitle(agentState: string | undefined, delivery: string | undefined): string {
  const k = kanbanKey(agentState, delivery);
  return KANBAN_COLUMNS.find((c) => c.key === k)?.title ?? "Working";
}

/**
 * The bucket dot's colour, by column key. Borrowed by the sidebar's triage rows
 * and nothing else; it exists here rather than at that call site so the board
 * and the filter can never disagree about what "Fixing" is coloured.
 *
 * It used to be derived from "the column's first status", which stops being
 * expressible the moment a column is a predicate rather than a status list.
 */
export function kanbanDotText(key: string | undefined): string {
  switch (key) {
    case "needs":
      return "text-orange";
    case "working":
      return "text-info";
    case "fixing":
      return "text-bad";
    case "review":
      return "text-magenta";
    default:
      return "text-faint"; // done
  }
}

// ---------------------------------------------------------------------------
// (2) THE LEGACY HALF — the rolled-up status string (state.Rollup)
//
// MOBILE READS THIS. Every signature below is consumed through mobile's
// `$lib/*` vite alias; none of them may change shape. See the header.
// ---------------------------------------------------------------------------

export type PillKind = "urgent" | "broken" | "work" | "done" | "grey" | "plain";

/** Tailwind text-color utility for a status label (statusStyle in the TUI). */
export function statusText(status: string): string {
  switch (status) {
    case "working":
      return "text-info";
    case "ci_failed":
    case "changes_requested":
    case "merge_conflict":
    // `dead` used to return Tailwind's built-in white here, on the premise that
    // the caller always paints it on a bad-colored fill. Only pillClasses does —
    // Rail and SessionsKanban print statusText bare on a panel, where white was
    // 1.14:1 on latte. It is a bad-family status, so it takes the bad-family
    // color; the pill supplies its own on-fill foreground.
    case "dead":
      return "text-bad";
    case "approved":
      return "text-good";
    case "needs_input":
      return "text-orange";
    case "merged":
    case "session_ended":
    case "idle":
    case "closed":
    case "shell":
    case "orphaned":
      return "text-faint";
    default:
      return "text-ink";
  }
}

/** The pill kind for a status (statusPill in the TUI). */
export function pillKind(status: string): PillKind {
  switch (status) {
    case "needs_input":
      return "urgent";
    case "ci_failed":
    case "changes_requested":
    case "merge_conflict":
      return "broken";
    case "working":
    case "ci_pending":
    case "draft":
      return "work";
    case "approved":
      return "done";
    case "review_pending":
      return "grey";
    default:
      return "plain"; // merged/dead/session_ended/idle/unknown → plain text
  }
}

/** Tailwind classes for a status pill fill. `plain` returns the text color. */
export function pillClasses(status: string): string {
  switch (pillKind(status)) {
    case "urgent":
      return "bg-pill-urgent text-pill-urgent-fg font-semibold";
    case "broken":
      return "bg-pill-broken text-pill-broken-fg font-semibold";
    case "work":
      return "bg-pill-work text-pill-work-fg";
    case "done":
      return "bg-pill-done text-pill-done-fg";
    case "grey":
      return "bg-pill-grey text-pill-grey-fg";
    default:
      // `dead` is the one plain status that still gets a solid fill, so it is
      // the one plain status that needs an on-fill foreground. --color-on-bad
      // is onFill(f, red), the same measured rule the urgent/broken pills use.
      // What it replaces was Tailwind's built-in white — the one foreground no
      // flavor could override, and 2.32:1 on the default Mocha.
      return status === "dead" ? "bg-bad text-on-bad" : statusText(status);
  }
}

/**
 * Human label for a status (or agent-axis / interpreted) word — statusLabel
 * in the TUI, kept identical. Every raw identifier gets a readable spelling
 * (a rendered "ci_failed" reads like a translation placeholder, not a badge);
 * the fallback de-underscores so an unmapped future word can never leak an
 * identifier into the UI. Display-only: control flow keys on the raw string.
 */
export function statusLabel(status: string): string {
  switch (status) {
    case "changes_requested":
      return "changes";
    case "review_pending":
      return "review";
    case "merge_conflict":
      return "conflict";
    case "session_ended":
      return "ended";
    case "ci_pending":
      return "ci running";
    case "ci_failed":
      return "ci failed";
    case "needs_input":
      return "needs you";
    case "waiting_input": // agent axis / interpreted overlay
      return "waiting";
    case "quota_limited":
      return "usage limit";
    default:
      return status.replaceAll("_", " ");
  }
}

/** ≤2-char glyph for a status (statusBadge in the TUI). */
export function statusBadge(status: string): string {
  const m: Record<string, string> = {
    working: "wk",
    ci_pending: "ci",
    needs_input: "!!",
    ci_failed: "!x",
    changes_requested: "cr",
    merge_conflict: "mc",
    review_pending: "rv",
    approved: "ok",
    merged: "mg",
    dead: "xx",
    session_ended: "en",
    idle: "..",
    draft: "df",
    closed: "cl",
    shell: "sh",
    orphaned: "or",
  };
  return m[status] ?? "??";
}

/**
 * Attention-first sort tier over the collapsed status word. The FALLBACK for
 * `sortRank` when a session arrives with no agent axis at all — a daemon that
 * predates the split, or a snapshot written by one. Without it such a session
 * would answer `sortRank("", "")` = 4 and every row on that push would sort
 * into one indistinguishable tier.
 */
export function legacySortRank(status: string): number {
  switch (status) {
    case "needs_input":
      return 0;
    case "ci_failed":
    case "changes_requested":
    case "merge_conflict":
      return 1;
    case "working":
    case "ci_pending":
    case "draft":
      return 2;
    case "review_pending":
    case "approved":
      return 3;
    case "merged":
    case "dead":
    case "session_ended":
    case "closed":
      return 5;
    default:
      return 4;
  }
}

/** Statuses that need a human (attentionStatuses in the TUI). */
export const ATTENTION_STATUSES = new Set([
  "needs_input",
  "ci_failed",
  "changes_requested",
  "merge_conflict",
]);

export function isAttention(status: string): boolean {
  return ATTENTION_STATUSES.has(status);
}

/**
 * The statuses each kanban column used to hold, keyed by column key.
 *
 * The BOARD does not read this — it buckets by kanbanKey over the two axes.
 * This is the backing table for `kanbanColumn(status)`, the string-keyed entry
 * point mobile calls, and it reproduces the pre-split partition exactly.
 */
export const LEGACY_KANBAN_STATUSES: Record<string, string[]> = {
  needs: ["needs_input"],
  working: ["working", "ci_pending", "idle", "draft"],
  fixing: ["ci_failed", "changes_requested", "merge_conflict"],
  review: ["review_pending", "approved"],
  done: ["merged", "closed", "dead", "session_ended"],
};

/** Which kanban column a status falls in; unmapped → Working (the TUI fallback). */
export function kanbanColumn(status: string): string {
  for (const c of KANBAN_COLUMNS) if (LEGACY_KANBAN_STATUSES[c.key]?.includes(status)) return c.title;
  return "Working";
}

/** Activity-feed phrase for a status transition (eventPhrase in the TUI). */
export function eventPhrase(from: string, to: string): string {
  if (from === "") return "spawned";
  const m: Record<string, string> = {
    working: "resumed",
    needs_input: "needs you",
    draft: "PR opened",
    review_pending: "in review",
    ci_pending: "CI running",
    ci_failed: "CI failed",
    changes_requested: "changes req",
    merge_conflict: "conflict",
    approved: "approved",
    merged: "merged",
    closed: "PR closed",
    session_ended: "ended",
    dead: "died",
  };
  return m[to] ?? to;
}

/**
 * The complete rolled-up status vocabulary, mirroring Go's
 * internal/state.AllStatuses(). desktop/state_parity_test.go parses this
 * array and fails the build when the two lists drift — keep order identical.
 *
 * Still shipped on the wire (SessionInfo.status) and still read by mobile, so
 * this list stays even though the desktop's own surfaces have moved to the two
 * axes above.
 */
export const ALL_STATUSES: string[] = [
  "working", "idle", "needs_input", "session_ended", "dead",
  "shell", "orphaned",
  "draft", "ci_pending", "ci_failed", "merge_conflict",
  "changes_requested", "review_pending", "approved",
  "merged", "closed",
];

export interface Attentionish {
  status: string;
}

/**
 * Count of sessions needing a human, over the LEGACY status word
 * (AttentionCount in the TUI).
 *
 * Looks unreachable from the desktop and is not: mobile/src/views/Sessions.svelte
 * is its live caller. The desktop's own equivalent is `attention(agentState,
 * delivery)` counted at the call site.
 */
export function attentionCount(sessions: Attentionish[]): number {
  return sessions.reduce((n, s) => (isAttention(s.status) ? n + 1 : n), 0);
}
