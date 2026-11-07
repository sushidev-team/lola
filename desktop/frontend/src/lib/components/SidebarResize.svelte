<script lang="ts">
  import { nav } from '$lib/nav.svelte';
  import { SIDEBAR_DEFAULT, SIDEBAR_MIN } from '$lib/sidebarwidth';
  let { width, maxWidth }: { width: number; maxWidth: number } = $props();
  let drag = $state<{ x: number; width: number; pointer: number } | null>(null);
  $effect(() => { if (nav.overlay || !nav.sidebarOpen) drag = null; });
  function resize(value: number) { if (nav.overlay || !nav.sidebarOpen) return; nav.setSidebarWidth(Math.min(maxWidth, Math.max(Math.min(SIDEBAR_MIN, maxWidth), value))); }
  function down(event: PointerEvent) {
    if (event.button !== 0 || nav.overlay || !nav.sidebarOpen) return;
    event.preventDefault();
    drag = { x: event.clientX, width, pointer: event.pointerId };
    (event.currentTarget as HTMLElement).setPointerCapture(event.pointerId);
  }
  function key(event: KeyboardEvent) {
    const delta = event.shiftKey ? 40 : 10;
    if (event.key === 'ArrowLeft') resize(width - delta);
    else if (event.key === 'ArrowRight') resize(width + delta);
    else if (event.key === 'Home') resize(SIDEBAR_MIN);
    else if (event.key === 'End') resize(maxWidth);
    else return;
    event.preventDefault(); event.stopPropagation();
  }
</script>

{#if nav.sidebarOpen && !nav.overlay}
<!-- A focusable ARIA window splitter is a separator with a value and keyboard controls. -->
<!-- svelte-ignore a11y_no_noninteractive_tabindex, a11y_no_noninteractive_element_interactions -->
<div class="sidebar-resize" class:resizing={!!drag} role="separator" tabindex={nav.sidebarOpen ? 0 : -1}
  aria-label="Resize sidebar" aria-orientation="vertical" aria-valuemin={Math.min(SIDEBAR_MIN, maxWidth)} aria-valuemax={maxWidth} aria-valuenow={Math.round(width)}
  title="Drag to resize · double-click to reset" onpointerdown={down}
  onpointermove={event => { if (drag && event.pointerId === drag.pointer) resize(drag.width + event.clientX - drag.x); }}
  onpointerup={() => drag = null} onpointercancel={() => drag = null} onlostpointercapture={() => drag = null}
  ondblclick={() => nav.setSidebarWidth(SIDEBAR_DEFAULT)} onkeydown={key}></div>

{/if}
