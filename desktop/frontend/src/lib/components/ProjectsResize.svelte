<script lang="ts">
  import { nav } from '$lib/nav.svelte';
  import { PROJECTS_DEFAULT, PROJECTS_MIN } from '$lib/sidebarwidth';
  // The divider between the projects column and the sessions beside it. In Git mode the projects
  // column is the whole sidebar, and this is its right edge.
  let { width, maxWidth, minWidth = PROJECTS_MIN }: { width: number; maxWidth: number; minWidth?: number } = $props();
  let drag = $state<{ x: number; width: number; pointer: number } | null>(null);
  $effect(() => { if (nav.overlay || !nav.sidebarOpen) drag = null; });
  const max = $derived(Math.max(minWidth, maxWidth));
  function resize(value: number) { if (nav.overlay || !nav.sidebarOpen) return; nav.setProjectsWidth(Math.min(max, Math.max(minWidth, value))); }
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
    else if (event.key === 'Home') resize(minWidth);
    else if (event.key === 'End') resize(max);
    else return;
    event.preventDefault(); event.stopPropagation();
  }
</script>

{#if nav.sidebarOpen && !nav.overlay}
<!-- svelte-ignore a11y_no_noninteractive_tabindex, a11y_no_noninteractive_element_interactions -->
<div class="projects-resize" class:resizing={!!drag} style="left:{width - 3}px" role="separator" tabindex="0"
  aria-label="Resize projects column" aria-orientation="vertical" aria-valuemin={minWidth} aria-valuemax={max} aria-valuenow={Math.round(width)}
  title="Drag to resize · double-click to reset" onpointerdown={down}
  onpointermove={event => { if (drag && event.pointerId === drag.pointer) resize(drag.width + event.clientX - drag.x); }}
  onpointerup={() => drag = null} onpointercancel={() => drag = null} onlostpointercapture={() => drag = null}
  ondblclick={() => nav.setProjectsWidth(PROJECTS_DEFAULT)} onkeydown={key}></div>
{/if}
