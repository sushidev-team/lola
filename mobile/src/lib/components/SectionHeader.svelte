<script lang="ts">
  // A section heading in the sessions list: a caps label, a hairline that eats
  // the remaining width, and a count.
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
  // to nothing. The RULE is aria-hidden: it is a ruled line, not a separator
  // between siblings, and announcing it would break the name in half.
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

<!-- `text-xs` is the design's 10/12 bold tracked label step and carries no
     text-transform of its own (see app.css), so `uppercase` is spelled here.
     The label stays lowercase in the DOM for the same reason StatusChip's does:
     capitals in the accessibility tree are read as an initialism.

     `h-px` rather than a border, because the rule has to be a flex ITEM — it
     takes whatever width the label and the count leave, and a border cannot
     flex. `bg-edge-soft` is the design's list hairline; plain `edge` is the
     heavier panel border and is visibly wrong at this weight. -->
<h2 class="flex items-center gap-3 px-5 pt-3.5 pb-[5px]">
  <span class="text-xs uppercase text-faint">{title}</span>
  <span class="h-px flex-1 bg-edge-soft" aria-hidden="true"></span>
  {#if count !== undefined}
    <span class="num text-sm font-medium text-faint">{count}</span>
  {/if}
</h2>
