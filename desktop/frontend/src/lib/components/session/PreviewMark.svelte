<script lang="ts">
  import { arrowHead } from "$lib/reviewmaterial";
  import { labelFontSize, transformMark, type Grip, type PreviewMark, type Rect } from "$lib/previewmark";

  let { mark, rect, visible, number, edit, select, change, selected = false, scale = 1 }: {
    mark: PreviewMark; rect: Rect; visible?: Rect; number?: number; edit?: () => void; select?: () => void;
    change?: (mark: PreviewMark) => void; selected?: boolean; scale?: number;
  } = $props();
  let element: HTMLDivElement;
  let moveButton = $state<HTMLButtonElement>();
  let lettering = $state<HTMLSpanElement>();
  let gesture: { id: number; x: number; y: number; base: PreviewMark; grip: Grip } | undefined;
  let live = $state.raw<PreviewMark>();
  const shown = $derived(live ?? { ...mark, x: rect.x, y: rect.y });
  const labelClip = $derived.by(() => {
    if (!visible || live || mark.kind !== "text") return;
    const inset = [visible.y - rect.y, rect.x + mark.w - visible.x - visible.w,
      rect.y + mark.h - visible.y - visible.h, visible.x - rect.x];
    if (inset.some(n => n > 0.5)) return `inset(${inset.map(n => Math.max(0, n) + "px").join(" ")})`;
  });
  const editAction = $derived(mark.kind === "text" ? "edit text" : "comment");
  const grips = $derived<Grip[]>(mark.kind === "arrow" ? ["start", "end"] : mark.kind === "text" ? ["se"] : ["nw", "ne", "sw", "se"]);
  function handlePosition(grip: Grip) {
    if (grip === "start" || grip === "end") {
      const i = grip === "start" ? 0 : 2;
      return `left:${shown.points[i]}px;top:${shown.points[i + 1]}px`;
    }
    return `left:${grip.endsWith("w") ? 0 : 100}%;top:${grip.startsWith("n") ? 0 : 100}%`;
  }
  function start(e: PointerEvent) {
    if (!change || gesture || e.button !== 0) return;
    // WebKit can leave keyboard focus in the iframe after clicking an SVG or label.
    e.preventDefault();
    const handle = (e.target as Element).closest<HTMLButtonElement>("[data-grip]");
    (handle ?? moveButton)?.focus({ preventScroll: true });
    select?.();
    const grip = handle?.dataset.grip as Grip | undefined;
    const base = { ...shown };
    if (lettering) { base.w = lettering.offsetWidth; base.h = lettering.offsetHeight; }
    gesture = { id: e.pointerId, x: e.clientX, y: e.clientY, base, grip: grip ?? "move" };
    // Capture on the hit target so a stationary double-click still reaches it.
    (e.target as Element).setPointerCapture(e.pointerId);
  }
  function move(e: PointerEvent) {
    if (gesture?.id !== e.pointerId) return;
    const dx = e.clientX - gesture.x, dy = e.clientY - gesture.y;
    if (!live && Math.hypot(dx, dy) < 3) return;
    const base = gesture.base;
    const changed = transformMark(base, gesture.grip, Math.round(dx / scale), Math.round(dy / scale));
    const parent = element.parentElement!;
    // Keep a visible part to grab even when the pointer leaves the page.
    changed.x = Math.round(Math.max(Math.min(12, changed.w) - changed.w, Math.min(parent.clientWidth - Math.min(12, changed.w), changed.x)));
    changed.y = Math.round(Math.max(Math.min(12, changed.h) - changed.h, Math.min(parent.clientHeight - Math.min(12, changed.h), changed.y)));
    live = changed;
  }
  function reset() { gesture = undefined; live = undefined; }
  function finish(e: PointerEvent) {
    if (gesture?.id !== e.pointerId) return;
    move(e);
    if (live) change?.(lettering ? { ...live, w: lettering.offsetWidth, h: lettering.offsetHeight } : live);
    reset();
  }
  function key(e: KeyboardEvent) {
    if (!change || e.isComposing || e.altKey || e.metaKey || e.ctrlKey) return;
    if (e.key === "Enter") { e.preventDefault(); edit?.(); return; }
    const steps: Record<string, [number, number]> = {ArrowLeft:[-1,0], ArrowRight:[1,0], ArrowUp:[0,-1], ArrowDown:[0,1]};
    const step = steps[e.key];
    if (!step) return;
    e.preventDefault(); e.stopPropagation();
    const grip = (e.target as HTMLElement).dataset.grip as Grip | undefined;
    const amount = e.shiftKey ? 10 : 1;
    const base = { ...shown, w: element.offsetWidth, h: element.offsetHeight };
    if (grip && base.kind === "text") {
      const ratio = (step[0] + step[1]) * amount / (base.fontSize ?? labelFontSize);
      change(transformMark(base, grip, base.w * ratio, base.h * ratio));
    } else {
      change(transformMark(base, grip ?? "move", step[0] * amount, step[1] * amount));
    }
  }
</script>

<!-- The annotation itself moves with the arrow keys. Its handles resize with the same keys. -->
<div bind:this={element} role="presentation"
  class="mark" class:editable={!!change} class:selected data-preview-mark={number}
  style:clip-path={labelClip} style:left={shown.x + 'px'} style:top={shown.y + 'px'} style:width={shown.kind === "text" ? 'max-content' : shown.w + 'px'}
  style:height={shown.kind === "text" ? 'auto' : shown.h + 'px'} style:font-size={(shown.fontSize ?? labelFontSize) + 'px'}
  onpointerdown={start} onpointermove={move} onpointerup={finish} onlostpointercapture={reset} onpointercancel={reset}
  onfocusin={select} onkeydown={key} ondblclick={e => { if (!(e.target as Element).closest("[data-grip]")) edit?.(); }}
  title={edit ? `Drag to move · Double-click to ${editAction} · Backspace to remove` : undefined}>
  {#if shown.kind === "text"}
    <span bind:this={lettering} class="text-mark preview-lettering">{shown.label}</span>
  {:else}
    <svg width="100%" height="100%" overflow="visible" aria-hidden="true">
      {#if shown.kind === "area"}
        <rect width="100%" height="100%" rx="2" fill="currentColor" fill-opacity=".09" stroke="currentColor" stroke-width="2" />
        {#if change}<rect class="area-hit" width="100%" height="100%" fill="transparent" stroke="transparent" stroke-width="10"/>{/if}
      {:else}
        <polyline points={shown.points.join(" ")} fill="none" stroke="currentColor" stroke-width="2.5" stroke-linejoin="round" stroke-linecap="round" />
        {#if shown.kind === "arrow"}
          <polygon fill="currentColor" class="tip" points={arrowHead(shown.points[0], shown.points[1], shown.points[2], shown.points[3], 12)} />
        {/if}
        {#if change}<polyline class="stroke-hit" points={shown.points.join(" ")} fill="none" stroke="transparent" stroke-width="14" stroke-linecap="round"/>{/if}
      {/if}
    </svg>
  {/if}
  {#if number}
    <button bind:this={moveButton} type="button" class="move-key" aria-keyshortcuts="Backspace Delete" aria-label={`Move annotation ${number}`} disabled={!edit}
      title={`Use arrow keys to move · Enter to ${editAction}`} onclick={edit}></button>
  {/if}
  {#if selected && edit}
    {#each grips as grip}
      <button type="button" class="grip" data-grip={grip} aria-label={`Resize annotation ${number} (${grip})`} style={handlePosition(grip)}></button>
    {/each}
  {/if}
</div>

<style>
  .mark { position:absolute; pointer-events:none; color:var(--preview-mark-color); user-select:none; touch-action:none; }
  .text-mark { font-size:inherit; }
  .move-key { position:absolute; width:1px; height:1px; padding:0; opacity:0; pointer-events:none; }
  .editable { cursor:grab; }
  .editable:active { cursor:grabbing; }
  .selected { z-index:2; }
  .selected:not(:has([data-grip="start"])), .editable:focus-visible { outline:1.5px solid #387cf3; outline-offset:2px; }
  .editable .text-mark, .editable .tip, .area-hit { pointer-events:auto; touch-action:none; }
  .stroke-hit { pointer-events:stroke; touch-action:none; }
  :global(.drawing-active) .mark:not(.selected) .area-hit { pointer-events:stroke; }
  .grip { position:absolute; transform:translate(-50%, -50%); border:0; width:20px; height:20px; background:transparent;
    display:grid; place-items:center; pointer-events:auto; touch-action:none; cursor:nwse-resize; padding:0; }
  .grip::after { content:""; width:9px; height:9px; background:#387cf3; border:2px solid white; border-radius:3px; box-shadow:0 1px 3px #0006; }
  .grip[data-grip="ne"], .grip[data-grip="sw"] { cursor:nesw-resize; }
  .grip[data-grip="start"], .grip[data-grip="end"] { cursor:crosshair; }
  .grip[data-grip="start"]::after, .grip[data-grip="end"]::after { border-radius:50%; }
</style>
