<script lang="ts">
  import type { Snippet } from "svelte";
  let {
    title,
    count,
    focused = false,
    note,
    pad = true,
    fill = false,
    children,
    actions,
  }: {
    title?: string;
    count?: number;
    focused?: boolean;
    note?: string;
    pad?: boolean;
    /** Grow to fill a flex column (the rail's Activity). In the cockpit's main
     * column the panel is a GRID cell instead — grid stretches it on its own, so
     * fill isn't needed there. */
    fill?: boolean;
    children: Snippet;
    actions?: Snippet;
  } = $props();
</script>

<!--
  w-full spans the parent's width. The cockpit's main column is a CSS grid (grid
  cells stretch a display:flex child reliably; a flex column does not — WebKit in
  the production WKWebView collapses it to content width). The rail is a flex
  column where `fill` grows the middle panel and the rest size to content.
-->
<section
  class="flex w-full min-h-0 min-w-0 flex-col overflow-hidden rounded-[10px] border bg-[color-mix(in_srgb,var(--color-panel)_82%,var(--color-canvas))] transition-colors"
  class:flex-1={fill}
  class:border-accent={focused}
  class:border-edge={!focused}
  style={focused ? "box-shadow:0 0 0 1px color-mix(in srgb,var(--color-accent) 30%,transparent)" : ""}
>
  {#if title}
    <header
      class="flex shrink-0 items-center gap-2 border-b border-edge/60 px-3 py-1.5 text-xs font-semibold tracking-wide"
      class:text-accent={focused}
      class:text-ink={!focused}
    >
      <span>{title}</span>
      {#if count !== undefined}<span class="text-faint">· {count}</span>{/if}
      {#if note}<span class="truncate text-[11px] font-normal text-faint">— {note}</span>{/if}
      {#if actions}<span class="ml-auto">{@render actions()}</span>{/if}
    </header>
  {/if}
  <div class="min-h-0 flex-1 overflow-auto" class:p-3={pad}>
    {@render children()}
  </div>
</section>
