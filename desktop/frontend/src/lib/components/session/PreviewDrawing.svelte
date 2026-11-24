<script lang="ts">
  import { onMount } from "svelte";
  import { makeMark, type PreviewTool, type PreviewMark, type Point } from "$lib/previewmark";
  import { simplifyStroke } from "$lib/reviewmaterial";
  import Mark from "./PreviewMark.svelte";
  let { tool, style, scale, place, hover, cancel }: {
    tool: PreviewTool; style: string; scale: number;
    place: (point: Point, mark?: PreviewMark) => void; hover: (point: Point) => void; cancel: () => void;
  } = $props();
  let surface: HTMLButtonElement;
  onMount(() => surface.focus({ preventScroll: true }));
  let pointer: number | undefined;
  let path: number[] = [];
  let mark = $state.raw<PreviewMark>();
  function point(e: PointerEvent) {
    const r = surface.getBoundingClientRect();
    return {
      x: Math.round(Math.max(0, Math.min(r.width - scale, e.clientX - r.left)) / scale),
      y: Math.round(Math.max(0, Math.min(r.height - scale, e.clientY - r.top)) / scale),
    };
  }
  function start(e: PointerEvent) {
    if (e.button !== 0 || pointer !== undefined) return;
    const p = point(e);
    pointer = e.pointerId;
    path = [p.x, p.y];
    surface.setPointerCapture(pointer);
  }
  function update(p: Point) {
    if (tool === "text") return;
    if (tool !== "pen") path.length = 2;
    if (path.at(-2) !== p.x || path.at(-1) !== p.y) path.push(p.x, p.y);
    if (path.length > 768) path = simplifyStroke(path, 192);
    mark = makeMark(tool, path);
  }
  function move(e: PointerEvent) {
    const p = point(e);
    if (pointer === undefined) { if (tool === "comment") hover(p); }
    else if (e.pointerId === pointer) update(p);
  }
  function reset() { pointer = undefined; path = []; mark = undefined; }
  function finish(e: PointerEvent) {
    if (e.pointerId !== pointer) return;
    const p = point(e);
    update(p);
    const drawn = mark && Math.max(mark.w, mark.h) * scale >= 4 ? mark : undefined;
    reset();
    if (tool === "text") place(p, makeMark(tool, [p.x, p.y]));
    else if (drawn || tool === "comment") place(p, drawn);
  }
</script>

<button bind:this={surface} class="draw" {style} type="button" aria-label="Place preview annotation"
  onpointerdown={start} onpointermove={move} onpointerup={finish} onlostpointercapture={reset}
  onpointercancel={reset} onkeydown={e => { if (e.key === "Escape") { e.preventDefault(); e.stopPropagation(); cancel(); } }}
  onclick={e => {
    if (e.detail !== 0) return;
    const p = { x: Math.round(surface.clientWidth / 2), y: Math.round(surface.clientHeight / 2) };
    if (tool === "comment") place(p);
    else place(p, makeMark(tool, tool === "text" ? [p.x, p.y] : [p.x - 24, p.y + 12, p.x, p.y]));
  }}>
  {#if mark}<Mark {mark} rect={mark}/>{/if}
</button>

<style>
  .draw { grid-area:1 / 1; z-index:2; position:relative; overflow:hidden; padding:0; border:0; background:transparent; touch-action:none; cursor:crosshair; user-select:none; }
  .draw:focus-visible { outline:2px solid var(--color-accent); outline-offset:-2px; }
</style>
