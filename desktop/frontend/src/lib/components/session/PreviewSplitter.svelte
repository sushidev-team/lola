<script lang="ts">
  import { onDestroy } from "svelte";

  let { label, orientation = "vertical", value, min, max, onresize, onreset }: {
    label: string; orientation?: "vertical" | "horizontal"; value: number; min: number; max: number;
    onresize: (size: number) => void; onreset: () => void;
  } = $props();
  const vertical = $derived(orientation === "vertical");
  let pointer = $state<number | null>(null);
  let origin = 0, initial = 0, next = 0, frame = 0;
  const coordinate = (e: PointerEvent) => vertical ? e.clientX : e.clientY;
  const resize = (size: number) => onresize(Math.round(Math.max(min, Math.min(size, max))));
  onDestroy(() => cancelAnimationFrame(frame));

  function start(e: PointerEvent) {
    if (e.button !== 0 || pointer !== null) return;
    e.preventDefault();
    const handle = e.currentTarget as HTMLElement;
    handle.focus({ preventScroll: true });
    handle.setPointerCapture(e.pointerId);
    pointer = e.pointerId;
    origin = coordinate(e);
    initial = next = value;
  }
  function move(e: PointerEvent) {
    if (pointer !== e.pointerId) return;
    next = initial + coordinate(e) - origin;
    if (!frame) frame = requestAnimationFrame(() => { frame = 0; resize(next); });
  }
  function end(e: PointerEvent) {
    if (pointer !== e.pointerId) return;
    cancelAnimationFrame(frame);
    frame = 0;
    pointer = null;
    if (e.type === "pointerup") {
      const final = initial + coordinate(e) - origin;
      if (final !== initial || value !== initial) resize(final);
    }
  }
  function key(e: KeyboardEvent) {
    const step = e.shiftKey ? 50 : 10;
    let size: number;
    if (e.key === (vertical ? "ArrowLeft" : "ArrowUp")) size = value - step;
    else if (e.key === (vertical ? "ArrowRight" : "ArrowDown")) size = value + step;
    else if (e.key === "Home") size = min;
    else if (e.key === "End") size = max;
    else return;
    e.preventDefault();
    e.stopPropagation();
    resize(size);
  }
</script>

<!-- svelte-ignore a11y_no_noninteractive_tabindex, a11y_no_noninteractive_element_interactions -->
<div role="separator" tabindex="0" aria-label={label} aria-orientation={orientation}
  aria-valuemin={min} aria-valuemax={max} aria-valuenow={value} aria-valuetext={value + " pixels"}
  class="sash" class:vertical class:dragging={pointer !== null} title={label + " · Double-click to reset"}
  onpointerdown={start} onpointermove={move} onpointerup={end} onpointercancel={end} onlostpointercapture={end}
  onkeydown={key} ondblclick={onreset}></div>

<style>
  .sash { display:grid; place-items:center; flex:none; touch-action:none; user-select:none; height:12px; cursor:row-resize; background:var(--color-panel); }
  .sash.vertical { width:12px; height:auto; cursor:col-resize; }
  .sash::after { content:""; width:28px; height:3px; border-radius:3px; background:var(--color-faint); }
  .sash.vertical::after { width:3px; height:28px; }
  .sash:is(:hover,:focus-visible,.dragging)::after { background:var(--color-accent); }
</style>
