<script lang="ts">
  import {
    displayFor,
    displayLabel,
    displayPill,
    inputReasonLabel,
    pillClasses,
    statusLabel,
  } from "$lib/theme";
  import Button from "./Button.svelte";
  let {
    agentState = "",
    inputReason = "",
    delivery = "",
    status = "",
    interpreted = "",
    onResolve = undefined,
    resolveBranch = "",
  }: {
    /** The AGENT axis — what this pill is about. See the block comment below. */
    agentState?: string;
    /** WHY the agent is blocked; rendered beside the pill when it is. */
    inputReason?: string;
    /** The DELIVERY axis. Read ONLY to decide whether the conflict action applies. */
    delivery?: string;
    /**
     * The legacy rolled-up status, used ONLY as the pill's word when `agentState`
     * is absent. The axes are optional on the wire (protocol.SessionInfo), so a
     * daemon predating the split sends neither — and displayFor("") answers
     * "working", which would draw a parked PR as a live agent. With the rollup
     * word in hand the pre-axis pill is exactly what it always was.
     */
    status?: string;
    /** The [statusagent] interpreter's disagreeing judgement, "≈"-marked. */
    interpreted?: string;
    /**
     * Makes a CONFLICTING session's pill ACTIONABLE: hovering (or focusing) it
     * turns the agent word into "resolve", and clicking asks the session's coding
     * agent to merge the project's default branch and fix the conflicts
     * (cmd=resolveConflict). Omitted on read-only surfaces, and ignored unless
     * the DELIVERY axis actually says merge_conflict.
     */
    onResolve?: (() => void) | undefined;
    /** The project's default_branch, named in the tooltip so the promise is exact. */
    resolveBranch?: string;
  } = $props();

  // THIS PILL IS THE AGENT AXIS. It used to be the rolled-up status
  // (state.Rollup), which collapses both axes into one word and — measured over
  // 20MB of daemon.log — spent 90% of its `needs_input` transitions on the
  // coding agent's own 60s idle nudge while hiding the agent entirely behind
  // every delivery word post-PR. So the pill now says what the RUNNER is doing,
  // in the six-word Display vocabulary, and the PR gets its own chip
  // (<PrBadge>) beside it. Neither masks the other any more.
  //
  // No `dim` opacity. Group opacity composites the pill — fill AND label as one
  // layer — over whatever is behind it, which pulls the two toward a common
  // colour and quietly undoes the AA the pill's measured tokens guarantee. The
  // de-emphasis already comes from the bare (unfilled) treatments the quiet
  // Display values carry; fading the whole pill only cost legibility on top.
  //
  // The agent-axis BADGE is gone from here — the two-letter glyphs ("·wk",
  // "·?!", "·en") ported from the TUI's agentBadge. They existed because the
  // pill was the delivery state and the agent axis had nowhere to go; the pill
  // IS the agent axis now, so the glyph would be the word restated in code.

  const axes = $derived(agentState !== "");
  const display = $derived(displayFor(agentState));
  const word = $derived(axes ? displayLabel(display) : statusLabel(status));
  const fill = $derived(axes ? displayPill(display) : pillClasses(status));

  // The reason rides BESIDE the pill rather than inside it: "needs you" stays
  // one word wide in a table column whose neighbours must not reflow, and the
  // qualifier is free to be as long as "permission prompt". It is only ever
  // shown for a blocked agent — the daemon leaves inputReason set on records it
  // has since moved off waiting_input, so gating on the reason alone would print
  // a stale explanation under a "working" pill.
  const reason = $derived(display === "needs_you" && axes ? inputReasonLabel(inputReason) : "");

  // Driven by the DELIVERY axis, not by the pill's own word: the pill says
  // "working" on a session whose branch conflicts, and that session is exactly
  // the one worth offering the merge to. Falls back to the rollup word for a
  // pre-axis session, where `merge_conflict` could only have come from delivery.
  const actionable = $derived(
    (axes ? delivery === "merge_conflict" : status === "merge_conflict") && !!onResolve,
  );
  const branch = $derived(resolveBranch || "the default branch");
  const tip = $derived(
    `Resolve the conflicts: merges ${branch} (the project's default branch) into this branch — ` +
      `the session's coding agent does the work and pushes the merge.`,
  );

  let busy = $state(false);

  // stopPropagation, because the pill sits inside a row whose own click selects
  // the session: without it a click here would ALSO move the selection, and a
  // dblclick-adjacent misfire would open the terminal.
  async function resolve(e: MouseEvent) {
    e.stopPropagation();
    if (!onResolve || busy) return;
    busy = true;
    try {
      await onResolve();
    } finally {
      busy = false;
    }
  }
</script>

<span class="inline-flex items-center gap-1 align-middle whitespace-nowrap">
  {#if actionable}
    <!-- A Button, not a hand-rolled control (see the Button invariant): the
         overrides are only the pill's own geometry, which sits below the ladder's
         smallest size — `h-auto!`/`rounded!` beat the base's h-6/rounded-md, and
         the pill's own fill classes supply the colour the `bare` variant leaves
         alone. -->
    <Button
      variant="bare"
      size="xs"
      loading={busy}
      title={tip}
      class="group h-auto! rounded! py-[1px]! {fill}"
      onclick={resolve}
    >
      <!-- Both labels share ONE grid cell, so the pill is as wide as the wider
           of the two and the row does not reflow when the word swaps under the
           cursor. No aria-label over them: `visibility:hidden` takes the resting
           word out of the accessibility tree too, so the button's name follows
           the same swap the eye sees, and `title` supplies the explanation. -->
      <span class="grid">
        <span class="col-start-1 row-start-1 group-hover:invisible group-focus-visible:invisible">
          {word}
        </span>
        <span class="invisible col-start-1 row-start-1 group-hover:visible group-focus-visible:visible">
          resolve
        </span>
      </span>
    </Button>
  {:else}
    <span class="inline-flex items-center whitespace-nowrap rounded px-1.5 py-[1px] text-sm {fill}">
      {word}
    </span>
  {/if}
  {#if reason}
    <!-- "needs you" is a status; "needs you · permission prompt" is an
         instruction. Without it the only way to learn what is being asked is to
         open the terminal, which is the whole cost this pill exists to save. -->
    <span class="text-sm text-orange">· {reason}</span>
  {/if}
  {#if interpreted}
    <!-- The [statusagent] interpreter DISAGREES with the deterministic axis:
         an untrusted approximation, "≈"-marked (statusPillFor in the TUI). -->
    <span class="text-sm text-orange">≈{statusLabel(interpreted)}</span>
  {/if}
</span>
