<script lang="ts">
  import type { Snippet } from "svelte";
  let {
    title,
    count,
    focused = false,
    note,
    pad = true,
    children,
    actions,
  }: {
    title?: string;
    count?: number;
    focused?: boolean;
    note?: string;
    pad?: boolean;
    children: Snippet;
    actions?: Snippet;
  } = $props();
</script>

<!--
  h-full + w-full fill the parent GRID cell. The rail and the cockpit's main
  column are CSS grids, not nested flex columns: WebKit (the production WKWebView)
  fails to stretch a display:flex child inside a flex column, but stretches grid
  cells reliably — so panels always span the full available width/height.
-->
<section
  class="flex h-full w-full min-h-0 min-w-0 flex-col overflow-hidden rounded-[10px] border bg-[color-mix(in_srgb,var(--color-panel)_82%,var(--color-canvas))] transition-colors"
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
