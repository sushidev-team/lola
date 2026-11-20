<script lang="ts" module>
  export interface Segment<T extends string = string> {
    id: T;
    label: string;
  }
</script>

<script lang="ts" generics="T extends string">
  import { tick } from "svelte";
  import { haptic } from "@mobile/lib/haptics";
  import { overflowFade } from "@mobile/lib/edgefade";
  import { largeType } from "@mobile/lib/inbox.svelte";

  // The Session screen's segment control (spec §5.2, §6.2): a 32 pt track with
  // segments and one thumb that slides under the selection. Under large Dynamic
  // Type, or when the labels do not fit, the track scrolls sideways with
  // content-sized segments and keeps the selection in view; a label is never clipped, and a hidden segment is announced
  // by an edge fade (never a hard cut).
  //
  // Invariants: the thumb is a `transform` and nothing else moves (220 ms, no
  // motion under reduced motion); it is re-placed whenever a segment's size
  // changes (Dynamic Type). Hidden segments are the caller's to omit, so the bar
  // never draws a segment that cannot be opened. A change to another segment
  // plays the `selection` haptic here, so every caller gets it.

  let {
    segments,
    value,
    onchange,
    label = "Sections",
  }: {
    segments: readonly Segment<T>[];
    value: T;
    onchange: (id: T) => void;
    label?: string;
  } = $props();

  let buttons = $state<Record<string, HTMLElement>>({});
  let thumb = $state({ x: 0, w: 0 });
  let track = $state<HTMLElement>();
  let overflows = $state(false);
  const scrolling = $derived(largeType.value || overflows);

  async function place(): Promise<void> {
    await tick();
    if (track) {
      overflows = track.scrollWidth > track.clientWidth + 1;
      // Re-run the edge fade now that layout has settled (it listens for scroll).
      track.dispatchEvent(new Event("scroll"));
    }
    const el = buttons[value];
    if (el && track) {
      thumb = { x: el.offsetLeft, w: el.offsetWidth };
      // Reveal the tab within this strip. Scrolling its ancestors can pull a sheet
      // or a parked session screen back to the tab during a live count update.
      if (el.offsetLeft < track.scrollLeft) track.scrollLeft = el.offsetLeft;
      else if (el.offsetLeft + el.offsetWidth > track.scrollLeft + track.clientWidth)
        track.scrollLeft = el.offsetLeft + el.offsetWidth - track.clientWidth;
    }
  }

  $effect(() => {
    void value;
    void segments;
    void place();
  });

  $effect(() => {
    if (typeof ResizeObserver === "undefined") return;
    const els = Object.values(buttons);
    const ro = new ResizeObserver(() => void place());
    for (const el of els) ro.observe(el);
    if (track) ro.observe(track);
    return () => ro.disconnect();
  });

  function choose(id: T): void {
    if (id !== value) haptic("select");
    onchange(id);
  }

  function key(e: KeyboardEvent, i: number): void {
    const step = e.key === "ArrowRight" ? 1 : e.key === "ArrowLeft" ? -1 : 0;
    if (step === 0) return;
    e.preventDefault();
    const next = segments[(i + step + segments.length) % segments.length];
    choose(next.id);
    buttons[next.id]?.focus({ preventScroll: true });
  }
</script>

<div
  bind:this={track}
  class="segment-track relative flex min-h-8 w-full items-stretch rounded-[9px] bg-sel px-0.5"
  class:scrolling
  use:overflowFade={24}
  data-scrolling={scrolling ? "" : undefined}
  role="tablist"
  aria-label={label}
>
  <span
    class="thumb pointer-events-none absolute top-0.5 bottom-0.5 left-0 rounded-[7px] bg-segment-thumb"
    style="box-shadow: var(--segment-thumb-shadow); width: {thumb.w}px; transform: translateX({thumb.x}px)"
    aria-hidden="true"
  ></span>
  {#each segments as seg, i (seg.id)}
    <button
      bind:this={buttons[seg.id]}
      type="button"
      role="tab"
      aria-selected={seg.id === value}
      tabindex={seg.id === value ? 0 : -1}
      class="relative touch-manipulation rounded-[7px] px-3 whitespace-nowrap {scrolling
        ? 'flex-none'
        : 'flex-auto'} {seg.id === value ? 'text-ink' : 'text-faint'}"
      style="font-size: min(var(--text-subhead), 17px); font-weight: var(--text-subhead--font-weight); line-height: min(1.125rem, 22px)"
      onclick={() => choose(seg.id)}
      onkeydown={(e) => key(e, i)}
    >
      {seg.label}
    </button>
  {/each}
</div>

<style>
  /* Scrolling mode: a sideways row, no scrollbar, segments sized by content. */
  .segment-track {
    min-width: 0;
    overflow: clip;
  }
  .segment-track.scrolling {
    overflow: auto hidden;
    overscroll-behavior: none;
    touch-action: pan-x pinch-zoom;
    scrollbar-width: none;
  }
  .segment-track.scrolling::-webkit-scrollbar {
    display: none;
  }
  .thumb {
    transition: transform 220ms cubic-bezier(0.2, 0.8, 0.2, 1);
  }
  @media (prefers-reduced-motion: reduce) {
    .thumb {
      transition: none;
    }
  }
</style>
