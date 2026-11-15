<script lang="ts" module>
  /** Below this many rows the list renders every row, keyed. */
  export const VIRTUALIZE_AT = 50;
  /** Rows rendered beyond the viewport on each side. */
  export const OVERSCAN = 6;
  /** Estimated heights per row kind, in pt, until a row is measured. */
  export const ESTIMATE = { row: 56, card: 112, section: 32 } as const;
  export type RowKind = keyof typeof ESTIMATE;

  /** First index whose bottom edge lies below `y`; `offsets` has one more entry than there are rows. */
  export function indexAt(offsets: readonly number[], y: number): number {
    let lo = 0;
    let hi = offsets.length - 1;
    while (lo < hi) {
      const mid = (lo + hi) >> 1;
      if (offsets[mid + 1] > y) hi = mid;
      else lo = mid + 1;
    }
    return lo;
  }
</script>

<script lang="ts" generics="T">
  import type { Snippet } from "svelte";

  // A scroller that mounts only the rows near the viewport (spec §8.2).
  //
  // Invariants:
  //  - Rows are keyed; a row whose key is unchanged is never re-created, so a
  //    keyed delta touches only the rows that changed.
  //  - Heights start as the per-kind estimates and become the measured height,
  //    one shared ResizeObserver for every row.
  //  - Rows inserted above the viewport do not move what the person is reading:
  //    the scroll offset follows the anchor row (the browser's own scroll
  //    anchoring is off, so the offset is corrected once).
  //  - Below `VIRTUALIZE_AT` rows nothing is measured or windowed.

  let {
    items,
    key,
    kind = () => "row",
    row,
    label,
    class: cls = "",
  }: {
    items: readonly T[];
    key: (item: T) => string;
    kind?: (item: T) => RowKind;
    row: Snippet<[T, number]>;
    label?: string;
    class?: string;
  } = $props();

  let scroller = $state<HTMLElement>();
  let scrollTop = $state(0);
  let viewport = $state(0);
  /** Measured heights by key; `rev` is bumped once per observer batch. */
  const measured = new Map<string, number>();
  let rev = $state(0);

  const windowed = $derived(items.length >= VIRTUALIZE_AT);

  const offsets = $derived.by(() => {
    void rev;
    const out = new Array<number>(items.length + 1);
    out[0] = 0;
    for (let i = 0; i < items.length; i++) {
      const k = key(items[i]);
      out[i + 1] = out[i] + (measured.get(k) ?? ESTIMATE[kind(items[i])]);
    }
    return out;
  });

  const range = $derived.by(() => {
    if (!windowed) return { start: 0, end: items.length };
    const first = indexAt(offsets, scrollTop);
    const last = indexAt(offsets, scrollTop + (viewport || window.innerHeight));
    return {
      start: Math.max(0, first - OVERSCAN),
      end: Math.min(items.length, last + 1 + OVERSCAN),
    };
  });

  const visible = $derived(items.slice(range.start, range.end));
  const padTop = $derived(windowed ? offsets[range.start] : 0);
  const padBottom = $derived(windowed ? offsets[items.length] - offsets[range.end] : 0);

  const ro =
    typeof ResizeObserver === "undefined"
      ? null
      : new ResizeObserver((entries) => {
          let changed = false;
          for (const e of entries) {
            const k = (e.target as HTMLElement).dataset.vkey;
            const h = Math.round(e.borderBoxSize?.[0]?.blockSize ?? e.contentRect.height);
            if (k !== undefined && h > 0 && measured.get(k) !== h) {
              measured.set(k, h);
              changed = true;
            }
          }
          if (changed) rev++;
        });

  /** Attachment: observes a row's height, and only while the list is windowed. */
  function measure(node: HTMLElement) {
    if (!windowed || !ro) return;
    ro.observe(node);
    return () => ro.unobserve(node);
  }

  $effect(() => {
    const el = scroller;
    if (!el) return;
    viewport = el.clientHeight;
    if (typeof ResizeObserver === "undefined") return;
    const size = new ResizeObserver(() => (viewport = el.clientHeight));
    size.observe(el);
    return () => size.disconnect();
  });

  $effect(() => () => ro?.disconnect());

  // Heights of rows that left the list are dropped once they outnumber the live
  // rows, so a long-lived list's memory follows its length.
  $effect(() => {
    if (measured.size <= 2 * items.length) return;
    const live = new Set(items.map(key));
    for (const k of measured.keys()) if (!live.has(k)) measured.delete(k);
  });

  // Scroll anchoring: remember which row sits at the top and how far into it the
  // viewport is; after the layout changes, put that row back where it was.
  let anchorKey: string | undefined;
  let anchorOffset = 0;

  // Runs after the DOM took the new padding, so the corrected offset is never
  // clamped by a scroll height that has not grown yet.
  $effect(() => {
    // Dependencies: the layout. The write below is the only mutation.
    const o = offsets;
    const el = scroller;
    if (!windowed || !el) {
      anchorKey = undefined;
      return;
    }
    if (anchorKey !== undefined && el.scrollTop > 0) {
      const at = items.findIndex((it) => key(it) === anchorKey);
      if (at >= 0 && o[at] !== anchorOffset) {
        el.scrollTop += o[at] - anchorOffset;
        scrollTop = el.scrollTop;
      }
    }
    const i = indexAt(o, el.scrollTop);
    anchorKey = items[i] === undefined ? undefined : key(items[i]);
    anchorOffset = o[i] ?? 0;
  });

  function onscroll(): void {
    if (!scroller) return;
    scrollTop = scroller.scrollTop;
    const i = indexAt(offsets, scrollTop);
    anchorKey = items[i] === undefined ? undefined : key(items[i]);
    anchorOffset = offsets[i] ?? 0;
  }
</script>

<div
  bind:this={scroller}
  class="h-full overflow-y-auto overscroll-contain [overflow-anchor:none] {cls}"
  role="list"
  aria-label={label}
  data-virtual={windowed ? "on" : "off"}
  {onscroll}
>
  <div style="padding-top: {padTop}px; padding-bottom: {padBottom}px">
    {#each visible as item, i (key(item))}
      <div role="listitem" data-vkey={key(item)} {@attach measure}>
        {@render row(item, range.start + i)}
      </div>
    {/each}
  </div>
</div>
