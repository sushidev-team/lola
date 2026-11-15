<script lang="ts" module>
  /** Scale after a pinch from `startDist` to `dist`, starting at `startScale`; clamped to 1–4. */
  export function pinchScale(startScale: number, startDist: number, dist: number): number {
    if (!(startDist > 0) || !(dist > 0)) return startScale;
    return Math.min(4, Math.max(1, startScale * (dist / startDist)));
  }
</script>

<script lang="ts">
  import { untrack } from 'svelte';
  import { DaemonService } from '@bindings/desktop';
  import type { ReviewPackage } from '@bindings/internal/protocol';
  import { parseReviewNote } from '$lib/reviewnote';
  import Button from '../Button.svelte';
  import Icon from '../Icon.svelte';

  // The Screenshots tab on a phone (ReviewWorkspace mounts it instead of ReviewCanvas on a
  // touch surface). Screens are stacked full-width cards; a screen with a baseline has a
  // Before / After switch. A tap opens a full-screen viewer with pinch zoom and large
  // Close / Previous / Next targets. A comment is written in a field docked to the bottom
  // and queued through the workspace's own comment path (`onComment`); pending comments
  // for a screen are read back from the feedback note. No pins, no drawing, no keyboard
  // shortcuts: those are the desktop canvas.

  let { pkg, note = '', canComment = false, onComment }: {
    pkg: ReviewPackage; note?: string; canComment?: boolean; onComment: (screen: number, text: string) => boolean;
  } = $props();

  const screens = $derived(pkg.screens ?? []);
  const sectionOf = (i: number) => `Screen ${i + 1}: ${screens[i]?.title}`;
  const pending = $derived.by(() => {
    const out: string[][] = screens.map(() => []);
    for (const c of parseReviewNote(note).comments) {
      if (c.packageId !== pkg.id) continue;
      const i = screens.findIndex((_, k) => sectionOf(k) === c.section);
      if (i >= 0) out[i].push(c.text);
    }
    return out;
  });

  // Which side each card shows; "after" unless the person switched.
  let side = $state<Record<number, 'before' | 'after'>>({});
  const sideOf = (i: number) => (screens[i]?.before ? side[i] ?? 'after' : 'after');
  const artifactOf = (i: number) => (sideOf(i) === 'before' ? screens[i]?.before ?? '' : screens[i]?.after ?? '');

  // Loaded images by artifact. The shim keeps its own small cache, so a reopened card does not re-ask.
  let images = $state<Record<string, string>>({});
  let failed = $state<Record<string, boolean>>({});
  function load(artifact: string) {
    if (!artifact || artifact in images || failed[artifact]) return;
    images[artifact] = '';
    DaemonService.ReviewPackageImage(pkg.session, pkg.id, artifact).then(r => {
      if (/^data:image\/(png|jpeg);base64,/.test(r.dataURL)) images[artifact] = r.dataURL;
      else failed[artifact] = true;
    }).catch(() => { failed[artifact] = true; });
  }
  function retry(artifact: string) {
    delete failed[artifact]; delete images[artifact];
    load(artifact);
  }
  $effect(() => {
    const wanted = screens.map((_, i) => artifactOf(i));
    untrack(() => wanted.forEach(load));
  });

  // The viewer.
  let viewing = $state<number | null>(null);
  let scale = $state(1);
  let pan = $state({ x: 0, y: 0 });
  const pointers = new Map<number, { x: number; y: number }>();
  let gesture: { dist: number; scale: number; x: number; y: number; pan: { x: number; y: number } } | null = null;
  function openViewer(i: number) { viewing = i; resetZoom(); }
  function resetZoom() { scale = 1; pan = { x: 0, y: 0 }; pointers.clear(); gesture = null; }
  function step(by: number) {
    if (viewing === null || !screens.length) return;
    viewing = (viewing + by + screens.length) % screens.length;
    resetZoom();
  }
  const distance = () => { const [a, b] = [...pointers.values()]; return a && b ? Math.hypot(a.x - b.x, a.y - b.y) : 0; };
  const centre = () => { const ps = [...pointers.values()]; return { x: ps.reduce((s, p) => s + p.x, 0) / ps.length, y: ps.reduce((s, p) => s + p.y, 0) / ps.length }; };
  function down(e: PointerEvent) {
    pointers.set(e.pointerId, { x: e.clientX, y: e.clientY });
    (e.currentTarget as Element).setPointerCapture?.(e.pointerId);
    const c = centre();
    gesture = { dist: distance(), scale, x: c.x, y: c.y, pan: { ...pan } };
  }
  function move(e: PointerEvent) {
    if (!pointers.has(e.pointerId) || !gesture) return;
    pointers.set(e.pointerId, { x: e.clientX, y: e.clientY });
    const c = centre();
    if (pointers.size >= 2) scale = pinchScale(gesture.scale, gesture.dist, distance());
    // One finger pans a zoomed image; at 1x the image fits and stays put.
    pan = scale > 1 ? { x: gesture.pan.x + c.x - gesture.x, y: gesture.pan.y + c.y - gesture.y } : { x: 0, y: 0 };
  }
  function up(e: PointerEvent) {
    pointers.delete(e.pointerId);
    const c = pointers.size ? centre() : { x: 0, y: 0 };
    gesture = pointers.size ? { dist: distance(), scale, x: c.x, y: c.y, pan: { ...pan } } : null;
  }
  function toggleZoom() { if (scale > 1) resetZoom(); else scale = 2; }

  // The docked comment field.
  let composing = $state<number | null>(null);
  let text = $state('');
  let notice = $state('');
  let field = $state<HTMLTextAreaElement>();
  function compose(i: number) {
    viewing = null; composing = i; notice = '';
    queueMicrotask(() => field?.focus());
  }
  function add() {
    if (composing === null || !text.trim()) return;
    if (onComment(composing, text)) { text = ''; composing = null; notice = 'Comment added to review feedback.'; }
  }
  $effect(() => {
    if (viewing === null) return;
    const key = (e: KeyboardEvent) => { if (e.key === 'Escape') viewing = null; };
    window.addEventListener('keydown', key);
    return () => window.removeEventListener('keydown', key);
  });
</script>

<div class="screens grid min-w-0 gap-6" data-review-screens>
  {#each screens as s, i (i)}
    {@const artifact = artifactOf(i)}
    <article class="grid min-w-0 gap-2" aria-label={sectionOf(i)} data-screen-card={i}>
      <div class="flex min-w-0 items-baseline justify-between gap-3">
        <h3 class="min-w-0 text-[17px] leading-[22px] font-semibold text-ink">{s.title}</h3>
        {#if s.viewport}<span class="shrink-0 text-sm text-faint">{s.viewport}</span>{/if}
      </div>
      {#if s.caption}<p class="text-[15px] leading-[20px] text-ink-2">{s.caption}</p>{/if}
      {#if s.before}
        <div class="side-switch grid grid-cols-2 gap-1 rounded-lg p-0.5" role="group" aria-label={`Show ${s.title}`}>
          <Button size="md" class="h-10! justify-center" selected={sideOf(i) === 'before'} onclick={() => (side[i] = 'before')}>Before</Button>
          <Button size="md" class="h-10! justify-center" selected={sideOf(i) === 'after'} onclick={() => (side[i] = 'after')}>After</Button>
        </div>
      {/if}
      <button type="button" class="frame block w-full min-w-0 overflow-hidden rounded-lg" aria-label={`Open ${s.title}, ${sideOf(i)}`} onclick={() => openViewer(i)} disabled={!images[artifact]}>
        {#if failed[artifact]}
          <span class="grid place-items-center p-6 text-sm text-warn" role="status">This screenshot could not be loaded.</span>
        {:else if images[artifact]}
          <img src={images[artifact]} alt={`${s.title}, ${sideOf(i)}`} class="block h-auto w-full" />
        {:else}
          <span class="grid aspect-[9/16] max-h-[60vh] w-full place-items-center text-sm text-faint" role="status">Loading screenshot…</span>
        {/if}
      </button>
      {#if failed[artifact]}<div><Button size="md" variant="secondary" class="h-11!" onclick={() => retry(artifact)}>Retry</Button></div>{/if}
      {#each pending[i] as c}
        <p class="pending rounded-lg px-3 py-2 text-[15px] leading-[20px] text-ink" data-pending-comment>{c}</p>
      {/each}
      {#if canComment}
        <div><Button size="md" variant="secondary" class="h-11!" onclick={() => compose(i)}><Icon name="message-square" size={16} />Comment on this screen</Button></div>
      {/if}
    </article>
  {/each}
  {#if notice}<p role="status" class="text-sm text-ink-2">{notice}</p>{/if}
  {#if composing !== null}
    <div class="composer grid gap-2 border-t pt-3 pb-3" data-screen-composer>
      <label class="grid gap-1">
        <span class="text-sm text-faint">{sectionOf(composing)}</span>
        <textarea bind:this={field} bind:value={text} class="composer-input" rows="3" maxlength="3000" aria-label="Comment on this screen" placeholder="What needs changing?"></textarea>
      </label>
      <div class="grid grid-cols-2 gap-3">
        <Button size="md" variant="secondary" class="h-11! justify-center" onclick={() => { composing = null; text = ''; }}>Cancel</Button>
        <Button size="md" variant="primary" class="h-11! justify-center" disabled={!text.trim()} onclick={add}>Add to feedback</Button>
      </div>
    </div>
  {/if}
</div>

{#if viewing !== null && screens[viewing]}
  {@const s = screens[viewing]}
  {@const artifact = artifactOf(viewing)}
  <div class="viewer" role="dialog" aria-modal="true" aria-label={`${s.title}, ${sideOf(viewing)}`} data-screen-viewer>
    <div class="viewer-bar">
      <Button size="md" icon variant="bare" class="h-11! w-11! text-white" aria-label="Close screenshot" onclick={() => (viewing = null)}><Icon name="x" size={22} /></Button>
      <span class="min-w-0 truncate text-[15px] text-white">{viewing + 1} of {screens.length} · {s.title}</span>
      {#if canComment}<Button size="md" variant="bare" class="h-11! text-white" onclick={() => compose(viewing!)}>Comment</Button>{:else}<span class="w-11"></span>{/if}
    </div>
    <!-- svelte-ignore a11y_no_static_element_interactions -->
    <div class="stage" onpointerdown={down} onpointermove={move} onpointerup={up} onpointercancel={up} ondblclick={toggleZoom} data-viewer-stage>
      {#if images[artifact]}
        <img src={images[artifact]} alt={`${s.title}, ${sideOf(viewing)}`} draggable="false" style={`transform: translate(${pan.x}px, ${pan.y}px) scale(${scale})`} data-viewer-image />
      {/if}
    </div>
    {#if screens.length > 1}
      <div class="viewer-nav">
        <Button size="md" icon variant="bare" class="h-14! w-14! text-white" aria-label="Previous screen" onclick={() => step(-1)}><Icon name="chevron-left" size={26} /></Button>
        <Button size="md" icon variant="bare" class="h-14! w-14! text-white" aria-label="Next screen" onclick={() => step(1)}><Icon name="chevron-right" size={26} /></Button>
      </div>
    {/if}
  </div>
{/if}

<style>
  .side-switch { background:color-mix(in srgb,var(--color-edge) 40%,transparent); }
  .frame { border:1px solid var(--color-edge-soft); background:var(--color-canvas); }
  .frame:disabled { cursor:default; }
  .pending { background:color-mix(in srgb,var(--color-info) 8%,transparent); }
  .composer { position:sticky; bottom:0; background:var(--color-canvas); border-color:var(--color-edge-soft); }
  .composer-input { width:100%; resize:none; padding:12px; border:1px solid var(--color-edge); border-radius:10px; background:var(--color-canvas); color:var(--color-ink); font-size:16px; line-height:22px; }
  .composer-input:focus { border-color:var(--color-accent); outline:none; }
  .viewer { position:fixed; inset:0; z-index:80; display:grid; grid-template-rows:auto minmax(0,1fr) auto; background:#000; padding-top:env(safe-area-inset-top,0px); padding-bottom:env(safe-area-inset-bottom,0px); }
  .viewer-bar { display:grid; grid-template-columns:auto minmax(0,1fr) auto; align-items:center; gap:8px; padding:4px 8px; text-align:center; }
  .stage { display:grid; place-items:center; overflow:hidden; touch-action:none; }
  .stage { position:relative; }
  .stage img { position:absolute; inset:0; width:100%; height:100%; object-fit:contain; transform-origin:center; user-select:none; -webkit-user-select:none; }
  .viewer-nav { display:flex; justify-content:space-between; padding:8px 16px; }
</style>
