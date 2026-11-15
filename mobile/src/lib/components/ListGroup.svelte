<script lang="ts">
  import type { Snippet } from "svelte";

  // An inset grouped section: a rounded panel of ListRows with an optional header
  // above it and a footnote below.
  //
  // The separators are not drawn here. app.css draws a 0.5px hairline between any
  // two adjacent `[data-list-row]` elements, inset from the left by the row's own
  // `--row-inset` (16px, or 52px when the row has an icon), so a group never
  // needs to know how many rows it has. The header is the SectionHeader's look
  // (caps label, optional right-aligned count) and a real heading, for the same
  // rotor reason.
  let {
    header,
    count,
    footer,
    children,
  }: {
    header?: string;
    count?: number | string;
    footer?: string;
    children: Snippet;
  } = $props();
</script>

<section>
  {#if header}
    <h2 class="flex items-baseline px-8 pt-6 pb-1.5">
      <span class="text-micro uppercase text-faint">{header}</span>
      {#if count !== undefined}
        <span class="ml-auto text-footnote text-faint tabular-nums">{count}</span>
      {/if}
    </h2>
  {/if}
  <div class="mx-gutter overflow-hidden rounded-button bg-edge-soft" data-list-group>
    {@render children()}
  </div>
  {#if footer}
    <p class="px-8 pt-1.5 text-footnote text-faint">{footer}</p>
  {/if}
</section>
