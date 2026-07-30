<script lang="ts">
  import type { Snippet } from "svelte";
  // Header props (title / count / note / actions) are GONE: <MainTopBar> names the
  // view, counts its rows and owns the lens switcher, so the panel is a pure
  // surface now. They were left behind as unreachable code — including a restyled
  // header no call site could render — after the sidebar rewrite.
  let {
    focused = false,
    pad = true,
    fill = false,
    children,
  }: {
    focused?: boolean;
    pad?: boolean;
    /** Grow to fill a flex parent — the focused terminal's fixed overlay, which
     * is the only flex container a Panel lives in. In the cockpit's main column
     * the panel is a GRID cell instead: grid stretches it on its own. */
    fill?: boolean;
    children: Snippet;
  } = $props();
</script>

<!--
  w-full spans the parent's width. The cockpit's main column is a CSS grid (grid
  cells stretch a display:flex child reliably; a flex column does not — WebKit in
  the production WKWebView collapses it to content width). The one flex parent
  left is the focused terminal's fixed overlay, where `fill` does the growing.
-->
<section
  class="flex w-full min-h-0 min-w-0 flex-col overflow-hidden rounded-[10px] border bg-[color-mix(in_srgb,var(--color-panel)_82%,var(--color-canvas))] transition-colors"
  class:flex-1={fill}
  class:border-accent={focused}
  class:border-edge={!focused}
  style={focused ? "box-shadow:0 0 0 1px color-mix(in srgb,var(--color-accent) 30%,transparent)" : ""}
>
  <div class="min-h-0 flex-1 overflow-auto" class:p-3={pad}>
    {@render children()}
  </div>
</section>
