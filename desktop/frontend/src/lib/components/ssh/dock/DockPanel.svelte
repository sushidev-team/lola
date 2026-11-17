<script lang="ts">
  import Icon from "$lib/components/Icon.svelte";
  // One panel's card: the tab strip and, while no widget may run yet, the gate. The strip IS
  // the chrome that labels the page below as the agent's: a magenta hairline, an underline on
  // the tab on show and `by <agent>`. Magenta is a label here, never the most saturated thing on
  // screen, so the strip has no tint of its own.
  // The page itself is not in here: it lives in DockArea's frame layer and is laid over this
  // card, because this component comes and goes as the layout changes and a frame must not.
  //
  // Tabs are hand-rolled for the same reason as the machine tab chips in ViewStrip: one chip
  // holds two buttons, the label and the ×, so the wrapper paints it and the × runs bare.
  import { tick } from "svelte";
  import { PANEL_PAD, TAB_STRIP_H, type Rect } from "$lib/docklayout";
  import Button from "../../Button.svelte";

  let {
    id,
    rect,
    tabs,
    active,
    agent,
    gate,
    titleOf,
    tabLabel,
    faulted,
    unseen,
    dragging = "",
    pulse = null,
    drawer = false,
    systemActive = false,
    onselect,
    onclose,
    onallow,
    onshift,
    onmenu,
    onlist,
    ondragstart,
    onhide,
  }: {
    id: string;
    rect: Rect;
    tabs: string[];
    active: string;
    agent: string;
    /** Whether the one-click gate is drawn here. One copy is enough, as one click allows all. */
    gate: boolean;
    titleOf: (tab: string) => string;
    tabLabel?: (tab: string) => string;
    faulted: (tab: string) => boolean;
    unseen: (tab: string) => boolean;
    dragging?: string;
    pulse?: { tab: string; n: number } | null;
    drawer?: boolean;
    systemActive?: boolean;
    onselect: (tab: string) => void;
    onclose: (tab: string) => void;
    onallow: () => void;
    onshift?: (tab: string, delta: number) => void;
    onmenu: (tab: string, x: number, y: number, byKey: boolean) => void;
    onlist: (tabs: string[], x: number, y: number, byKey: boolean) => void;
    ondragstart?: (e: PointerEvent, tab: string) => void;
    onhide?: () => void;
  } = $props();

  let strip = $state<HTMLDivElement | null>(null);
  // The one place that spells out the limit: a sandboxed page has no network of its own, but it
  // can open an address, and that request can carry whatever the page was given.
  const label = $derived(`Written by ${agent}. It runs sandboxed, with no access to your files. It could still send its own data to a website by opening an address.`);

  // By hand rather than scrollIntoView, which would also scroll the overflow-hidden ancestors.
  $effect(() => {
    const on = active;
    const s = strip;
    if (!s) return;
    const el = s.querySelector<HTMLElement>(`[data-dock-tab="${CSS.escape(on)}"]`);
    if (!el) return;
    const left = el.offsetLeft - s.offsetLeft;
    if (left < s.scrollLeft) s.scrollLeft = left;
    else if (left + el.offsetWidth > s.scrollLeft + s.clientWidth) s.scrollLeft = left + el.offsetWidth - s.clientWidth;
  });

  // Which way there are more tabs than fit. Measured, since neither the fonts nor the panel's
  // width are known here.
  let more = $state<"" | "left" | "right" | "both">("");
  function measure() {
    const s = strip;
    if (!s) return;
    const l = s.scrollLeft > 1;
    const r = s.scrollLeft + s.clientWidth < s.scrollWidth - 1;
    more = l && r ? "both" : l ? "left" : r ? "right" : "";
  }
  $effect(() => {
    tabs;
    const s = strip;
    if (!s) return;
    measure();
    if (typeof ResizeObserver === "undefined") return;
    // A frame later, as the overflow button it may add changes the size being observed.
    const ro = new ResizeObserver(() => requestAnimationFrame(measure));
    ro.observe(s);
    return () => ro.disconnect();
  });

  function focusTab(tab: string) {
    strip?.querySelector<HTMLElement>(`[data-dock-tab="${CSS.escape(tab)}"] [role="tab"]`)?.focus();
  }

  async function onkey(e: KeyboardEvent, tab: string, i: number) {
    if (e.key === "ContextMenu" || (e.key === "F10" && e.shiftKey)) {
      e.preventDefault();
      e.stopPropagation();
      const r = (e.currentTarget as HTMLElement).getBoundingClientRect();
      onmenu(tab, r.left, r.bottom + 4, true);
      return;
    }
    if (e.key !== "ArrowLeft" && e.key !== "ArrowRight") return;
    e.preventDefault();
    // These arrows belong to the strip, not the app's list navigation.
    e.stopPropagation();
    const delta = e.key === "ArrowRight" ? 1 : -1;
    if (e.altKey) {
      onshift?.(tab, delta);
      await tick();
      focusTab(tab);
    } else {
      const next = tabs[(i + delta + tabs.length) % tabs.length];
      onselect(next);
      await tick();
      focusTab(next);
    }
  }

  function wheel(e: WheelEvent) {
    if (!strip || strip.scrollWidth <= strip.clientWidth || Math.abs(e.deltaX) > Math.abs(e.deltaY)) return;
    strip.scrollLeft += e.deltaY;
  }
</script>

<div
  class="absolute grid grid-cols-[minmax(0,1fr)] grid-rows-[minmax(0,1fr)] {drawer ? 'z-[5] bg-canvas' : ''}"
  style="left:{rect.x}px;top:{rect.y}px;width:{rect.w}px;height:{rect.h}px;padding:{PANEL_PAD}px"
  data-dock-group={id}
>
  <div class="grid min-h-0 min-w-0 overflow-hidden bg-panel {drawer ? 'shadow-xl' : ''}" style="grid-template-rows:{TAB_STRIP_H}px minmax(0,1fr)">
    <div class="@container grid min-w-0 grid-cols-[minmax(0,1fr)_auto] items-stretch border-b border-edge/60 bg-canvas text-sm" title={systemActive ? "Drag tabs to split or join panels. Drag dividers to resize." : label}>
      <div bind:this={strip} role="tablist" aria-label="Session panels" class="dock-strip relative flex min-w-0 overflow-x-auto" data-more={more || undefined} onwheel={wheel} onscroll={measure}>
        {#each tabs as tab, i (tab)}
          {@const on = tab === active}
          <div
            role="presentation"
            class="group relative flex max-w-44 min-w-16 shrink-0 touch-none items-center border-b-2 pl-2.5 select-none {on ? 'border-accent text-ink' : 'border-transparent text-faint hover:text-ink'} {dragging === tab ? 'opacity-50' : ''}"
            data-dock-tab={tab}
            onpointerdown={(e) => ondragstart?.(e, tab)}
            oncontextmenu={(e) => {
              e.preventDefault();
              onmenu(tab, e.clientX, e.clientY, false);
            }}
          >
            {#if pulse?.tab === tab}{#key pulse.n}<span class="dock-pulse pointer-events-none absolute inset-0" aria-hidden="true" data-dock-pulse></span>{/key}{/if}
            {#if faulted(tab)}<span class="mr-1.5 h-1.5 w-1.5 shrink-0 rounded-full bg-warn" aria-label="Failed"></span>
            {:else if unseen(tab)}<span class="mr-1.5 h-1.5 w-1.5 shrink-0 rounded-full bg-magenta" aria-label="New" data-dock-unseen></span>{/if}
            <button type="button" role="tab" aria-selected={on} tabindex={on ? 0 : -1} class="min-w-0 flex-1 cursor-default truncate py-1 text-left outline-none focus-visible:underline" title={titleOf(tab)} aria-label={titleOf(tab)} onclick={() => onselect(tab)} onkeydown={(e) => onkey(e, tab, i)}>{tabLabel?.(tab) ?? titleOf(tab)}</button>
            <span class="flex w-6 shrink-0 justify-center" onpointerdown={(e) => e.stopPropagation()} role="presentation">
              <Button variant="bare" size="xs" icon class="h-5! w-5! {on ? '' : 'opacity-0'} transition-[opacity,color] group-hover:opacity-100 hover:text-bad focus-visible:opacity-100" aria-label="Close {titleOf(tab)}" title="Close" onclick={() => onclose(tab)}><Icon name="x" size={14} /></Button>
            </span>
          </div>
        {/each}
      </div>
      <div class="flex shrink-0 items-center gap-1 pr-2 pl-1">
        {#if more}<Button size="xs" icon class="h-5! w-5!" aria-label="List {tabs.length} tabs" aria-haspopup="menu" title="All tabs" onclick={(e) => { const r = e.currentTarget.getBoundingClientRect(); onlist(tabs, r.right, r.bottom + 4, e.detail === 0); }}>⌄</Button>{/if}
        <!-- Only where there is room for it. The magenta chrome and the tooltip still say who
             wrote this, and the page cannot draw over either. -->
        {#if !systemActive}<span class="hidden whitespace-nowrap text-magenta @min-[360px]:inline">by {agent}</span>{/if}
        {#if onhide}<Button size="xs" icon class="h-5! w-5!" aria-label="Hide widgets" title="Hide" onclick={onhide}><Icon name="chevron-right" size={14} /></Button>{/if}
      </div>
    </div>
    <div class="grid min-h-0 min-w-0 place-items-center p-3">
      {#if gate}
        <div class="grid max-w-64 justify-items-center gap-2 text-center text-sm text-faint" data-widget-gate>
          <p title={systemActive ? "Drag tabs to split or join panels. Drag dividers to resize." : label}>Written by {agent}. Sandboxed, no file access.</p>
          <Button size="xs" variant="secondary" onclick={onallow}>Show widgets</Button>
        </div>
      {/if}
    </div>
  </div>
</div>
