<script lang="ts">
  import { displayFor, displayLabel, inputReasonLabel, statusLabel } from "$lib/theme";
  import StateGlyph from "./StateGlyph.svelte";

  // The AGENT axis as one 16px cell, the list's replacement for the status pill's
  // word. It draws the same vocabulary as the roster and the Glance (see
  // StateGlyph), because one session must not wear two sets of glyphs on one
  // screen. Static, like the roster: only the open session's footer glyph moves.
  //
  // The word, the input reason and the interpreter's disagreement go to the
  // tooltip and to a visually-hidden label, so nothing the pill used to say is
  // lost — it just stops taking a column.
  let {
    glyph,
    agentState = "",
    inputReason = "",
    status = "",
    interpreted = "",
    size = 16,
  }: {
    /** The session's cell, from stateGlyph. */
    glyph: { glyph: string; cls: string; label: string; mono?: boolean };
    agentState?: string;
    inputReason?: string;
    /** Legacy rolled-up status: the label for a pre-axis daemon's session. */
    status?: string;
    interpreted?: string;
    size?: number;
  } = $props();

  // A daemon predating the axis split sends no agentState, and displayFor("")
  // answers "working" — which would set a parked PR spinning. Such a session
  // gets a static dot and its rollup word instead.
  const axes = $derived(agentState !== "");
  const d = $derived(displayFor(agentState));
  const reason = $derived(d === "needs_you" ? inputReasonLabel(inputReason) : "");
  const word = $derived(axes ? (d === "working" ? "agent running" : displayLabel(d)) : statusLabel(status));
  const tip = $derived(
    [word, reason, interpreted ? `≈ ${statusLabel(interpreted)} (interpreted)` : ""].filter(Boolean).join(" · "),
  );
</script>

<span
  class="relative inline-flex shrink-0 items-center justify-center align-middle"
  style="width:{size}px;height:{size}px"
  title={tip}
>
  <span class="sr-only">{tip}</span>
  {#if !axes}
    <span class="h-[40%] w-[40%] rounded-full bg-faint/70" aria-hidden="true"></span>
  {:else}
    <StateGlyph label={glyph.label} glyph={glyph.glyph} cls={glyph.cls} mono={glyph.mono} />
  {/if}
</span>
