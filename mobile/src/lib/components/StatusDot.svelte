<script lang="ts" module>
  // The bare coloured dot a filter chip carries before its label.
  //
  // WHAT IT IS NOT FOR, because an earlier version of this file claimed it and
  // the claim was false: the compact row does NOT lead its meta line with this.
  // <SessionRow> renders `$lib/components/LivePulse.svelte` there and argues at
  // length for why — the dot on a row is about the AGENT axis (is this runner
  // alive), and the row has the agent state to hand while this component is
  // given a bucket. The two marks look alike and mean different things.
  //
  // So the surface here is deliberately small: a colour and a size. It briefly
  // also took a `status` (to derive its own colour) and a `pulse` (to animate),
  // and neither ever had a caller — the one place that would have passed
  // `pulse` is the row, which uses LivePulse instead. A prop with no call site
  // is a promise nothing keeps, so both are gone.
  //
  // WHY IT IS NOT LivePulse. That component renders NOTHING unless `agentState`
  // is working or starting, is always `bg-info` and is always 6 points. A filter
  // chip's dot is its bucket's colour, is 5 points, and must be there whether
  // anything is running or not. Wrapping LivePulse could not express any of the
  // three.

  /**
   * The two sizes the design draws, as LITERAL classes (rule 4). 6px is the
   * list's; 5px is the filter chip's and the status chip's, small enough that
   * rounding it to the 4px scale would be visible next to a 10px label.
   */
  const SIZES = {
    5: "h-[5px] w-[5px]",
    6: "h-1.5 w-1.5",
  } as const;
</script>

<script lang="ts">
  let {
    size = 6,
    /**
     * The dot's colour, as a whole literal Tailwind class.
     *
     * A class rather than a token name on purpose. Every value comes from a
     * table Tailwind has already seen in some source file — `kanbanDotText`,
     * `statusText`, `statusTone` — so the utility is compiled; a caller that
     * composes one here (`` `text-${x}` ``) gets a colourless dot, exactly as
     * rule 4 warns. There is no default: a dot with no colour is invisible, and
     * failing at the call site is better than shipping one.
     */
    tone,
    class: klass = "",
  }: {
    size?: keyof typeof SIZES;
    tone: string;
    class?: string;
  } = $props();
</script>

<!-- `bg-current`, so the shape can never drift from the colour: `tone` sets the
     foreground on this one element and the circle inherits it.

     aria-hidden, always. The dot never carries a fact of its own — it stands
     inside a chip that is already labelled with the bucket it colours — so
     announcing it would only double what is said. -->
<span
  class="inline-flex shrink-0 rounded-full bg-current {SIZES[size]} {tone} {klass}"
  aria-hidden="true"
></span>
