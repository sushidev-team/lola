<script lang="ts">
  // A section heading in the sessions list: a caps label and a right-aligned
  // count, with no rule (the grouped list below carries the edge).
  //
  // IT IS A REAL HEADING, not a styled row. The list it introduces is a run of
  // buttons with nothing between them, so without an <h2> a screen reader's
  // rotor offers one flat list of forty controls and no way to skip the settled
  // ones — which is the whole reason the design partitions the list in the
  // first place. The element is cheap; Tailwind's preflight already resets its
  // size and weight, so the type comes entirely from the classes below.
  //
  // The COUNT is inside the heading rather than beside it, so the accessible
  // name reads "Needs you 2" — the count is part of what the section says, and
  // a number floating outside the heading is announced as loose text belonging
  // to nothing.
  //
  // THE COUNT IS OPTIONAL, and that is what lets the settings screen use this
  // rather than keep a second copy of the geometry. A number is right for a
  // bucket of sessions and meaningless for a group of controls — "Connection 0"
  // reads as a fault — so an omitted count draws no span at all rather than a
  // zero. It is `undefined` and not `0` for exactly that reason: zero is a real
  // answer a session bucket gives.

  let {
    /** The bucket's name. Comes from `$lib/filters`' TRIAGE_FILTERS at the
     *  call site — this component names no buckets of its own. */
    title,
    /** How many sessions the section holds. A zero is worth drawing: it says
     *  the bucket is empty rather than that the list is broken. Omitted
     *  entirely for a heading that counts nothing. */
    count = undefined,
  }: { title: string; count?: number } = $props();
</script>

<!-- No rule: the label sits on the ground and the grouped list below carries the edge.
     `text-micro` is the phone's 11/13 bold tracked label step and carries no
     text-transform of its own, so `uppercase` is spelled here; the label stays
     lowercase in the DOM for the same reason StatusChip's does (capitals in the
     accessibility tree are read as an initialism). -->
<h2 class="flex items-baseline px-4 pt-6 pb-1.5">
  <span class="text-micro uppercase text-faint">{title}</span>
  {#if count !== undefined}
    <span class="ml-auto text-footnote text-faint tabular-nums">{count}</span>
  {/if}
</h2>
