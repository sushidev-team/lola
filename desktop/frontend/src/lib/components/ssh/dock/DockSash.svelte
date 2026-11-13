<script lang="ts">
  // The line between two panels. The hit area is wider than the line and sits above the frames.
  // The drag reports its total distance from where it began, so the parent can recompute from
  // the layout it started with and a clamped pixel is never lost on the way back.
  import { SASH_STEP, SASH_STEP_LARGE, type Sash } from "$lib/docklayout";

  let {
    sash,
    active = false,
    onstart,
    onmove,
    onend,
    onstep,
    oneven,
  }: {
    sash: Sash;
    active?: boolean;
    onstart: () => void;
    onmove: (delta: number) => void;
    onend: (commit: boolean) => void;
    onstep: (delta: number) => void;
    oneven: () => void;
  } = $props();

  const row = $derived(sash.dir === "row");
  const HIT = 7;
  const box = $derived(
    row
      ? { x: sash.rect.x - (HIT - sash.rect.w) / 2, y: sash.rect.y, w: HIT, h: sash.rect.h }
      : { x: sash.rect.x, y: sash.rect.y - (HIT - sash.rect.h) / 2, w: sash.rect.w, h: HIT },
  );

  function down(e: PointerEvent) {
    if (e.button !== 0) return;
    e.preventDefault();
    const el = e.currentTarget as HTMLElement;
    const id = e.pointerId;
    const from = row ? e.clientX : e.clientY;
    el.setPointerCapture(id);
    onstart();
    const move = (ev: PointerEvent) => onmove((row ? ev.clientX : ev.clientY) - from);
    const finish = (commit: boolean) => {
      el.removeEventListener("pointermove", move);
      el.removeEventListener("pointerup", up);
      el.removeEventListener("pointercancel", cancel);
      window.removeEventListener("keydown", esc, true);
      if (el.hasPointerCapture(id)) el.releasePointerCapture(id);
      onend(commit);
    };
    const up = () => finish(true);
    const cancel = () => finish(false);
    const esc = (ev: KeyboardEvent) => {
      if (ev.key !== "Escape") return;
      ev.preventDefault();
      ev.stopImmediatePropagation();
      finish(false);
    };
    el.addEventListener("pointermove", move);
    el.addEventListener("pointerup", up);
    el.addEventListener("pointercancel", cancel);
    window.addEventListener("keydown", esc, true);
  }

  function key(e: KeyboardEvent) {
    const back = row ? "ArrowLeft" : "ArrowUp";
    const on = row ? "ArrowRight" : "ArrowDown";
    if (e.key !== back && e.key !== on) return;
    e.preventDefault();
    // These arrows are this control's, not the app's list navigation.
    e.stopPropagation();
    const step = e.shiftKey ? SASH_STEP_LARGE : SASH_STEP;
    onstep(e.key === on ? step : -step);
  }
</script>

<!-- A focusable separator is an interactive widget in ARIA, so it takes keys and pointers. -->
<!-- svelte-ignore a11y_no_noninteractive_tabindex, a11y_no_noninteractive_element_interactions -->
<div
  role="separator"
  aria-orientation={row ? "vertical" : "horizontal"}
  aria-label="Resize panels"
  tabindex="0"
  class="group absolute z-20 grid touch-none outline-none {row ? 'justify-center' : 'content-center'} {!sash.movable ? 'cursor-default' : row ? 'cursor-col-resize' : 'cursor-row-resize'}"
  style="left:{box.x}px;top:{box.y}px;width:{box.w}px;height:{box.h}px"
  data-dock-sash={sash.split + ":" + sash.index}
  onpointerdown={down}
  onkeydown={key}
  ondblclick={oneven}
>
  <div class="transition-colors {row ? 'h-full w-px' : 'h-px w-full'} {active ? 'bg-accent' : 'bg-edge/50 group-hover:bg-accent group-focus-visible:bg-accent'}"></div>
</div>
