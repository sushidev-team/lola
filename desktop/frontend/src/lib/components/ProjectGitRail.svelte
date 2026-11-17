<script lang="ts">
  import { onDestroy, type Snippet } from 'svelte';
  import Button from './Button.svelte';

  let { sections }: { sections: { name: string; heading: Snippet; content: Snippet; controls?: Snippet }[] } = $props();
  const uid = $props.id();
  const storageKey = 'lola.git.section-heights';
  function readWeights(): number[] {
    try {
      const saved = JSON.parse(localStorage.getItem(storageKey) || 'null');
      if (Array.isArray(saved) && saved.length === sections.length && saved.every(n => Number.isFinite(n) && n > 0)) return saved;
    } catch { /* Use the default layout when storage is unavailable. */ }
    return sections.map(() => 1);
  }
  let weights = $state(readWeights());
  function readClosed(): number[] {
    try {
      const saved = JSON.parse(localStorage.getItem('lola.git.collapsed-sections') || '[]');
      if (Array.isArray(saved)) return saved.filter(i => Number.isInteger(i) && i >= 0 && i < sections.length);
    } catch { /* Keep all sections open by default. */ }
    return [];
  }
  let closed = $state(readClosed());
  function toggle(index: number) {
    cancelDrag();
    closed = closed.includes(index) ? closed.filter(i => i !== index) : [...closed, index];
    try { localStorage.setItem('lola.git.collapsed-sections', JSON.stringify(closed)); } catch { /* Keep this session's layout. */ }
  }
  let root: HTMLDivElement;
  let dragging = $state(false);
  const rows = $derived(sections.map((_, i) => `${closed.includes(i) ? '34px' : `minmax(34px, ${weights[i]}fr)`}${i < sections.length - 1 ? ' 0px' : ''}`).join(' '));
  let cancelDrag = () => {};
  onDestroy(() => cancelDrag());

  function nextOpen(index: number) { return sections.findIndex((_, i) => i > index && !closed.includes(i)); }
  function save(next: number[]) {
    weights = next;
    try { localStorage.setItem(storageKey, JSON.stringify(next)); } catch { /* Keep the current layout. */ }
  }
  function heights() {
    const measured = Array.from(root.querySelectorAll<HTMLElement>(':scope > section')).map(node => node.getBoundingClientRect().height);
    const openWeight = weights.reduce((sum, weight, i) => sum + (closed.includes(i) ? 0 : weight), 0);
    const scale = measured.reduce((sum, height, i) => sum + (closed.includes(i) ? 0 : height), 0) / openWeight;
    return measured.map((height, i) => closed.includes(i) ? weights[i] * scale : height);
  }
  function resize(index: number, next: number, sizes: number[], delta: number) {
    const total = sizes[index] + sizes[next];
    if (!total) return;
    const minimum = Math.min(62, total / 2);
    const height = Math.max(minimum, Math.min(total - minimum, sizes[index] + delta));
    save(sizes.map((size, i) => i === index ? height : i === next ? total - height : size));
  }
  function start(event: PointerEvent, index: number) {
    const next = nextOpen(index);
    if (event.button !== 0 || closed.includes(index) || next < 0) return;
    event.preventDefault();
    cancelDrag();
    const sizes = heights(), y = event.clientY;
    dragging = true;
    const move = (e: PointerEvent) => { if (e.pointerId === event.pointerId) resize(index, next, sizes, e.clientY - y); };
    const finish = () => {
      dragging = false;
      window.removeEventListener('pointermove', move);
      window.removeEventListener('pointerup', finish);
      window.removeEventListener('pointercancel', finish);
      window.removeEventListener('blur', finish);
      cancelDrag = () => {};
    };
    cancelDrag = finish;
    window.addEventListener('pointermove', move);
    window.addEventListener('pointerup', finish);
    window.addEventListener('pointercancel', finish);
    window.addEventListener('blur', finish);
  }
  function key(event: KeyboardEvent, index: number) {
    const next = nextOpen(index);
    if (closed.includes(index) || next < 0 || !['ArrowUp', 'ArrowDown', 'Home', 'End'].includes(event.key)) return;
    event.preventDefault(); event.stopPropagation();
    const delta = event.key === 'Home' ? -Infinity : event.key === 'End' ? Infinity : (event.key === 'ArrowUp' ? -1 : 1) * (event.shiftKey ? 40 : 10);
    resize(index, next, heights(), delta);
  }
</script>

<div class="rail-sections" class:dragging bind:this={root} style:grid-template-rows={rows}>
  {#each sections as section, index (section.name)}
    <section class="repo-section" class:collapsed={closed.includes(index)} aria-label={section.name}>
      <div class="section-header">
      <Button variant="bare" block class="section-heading" aria-expanded={!closed.includes(index)} aria-controls={`${uid}-${index}`} onclick={() => toggle(index)}>
        {@render section.heading()}
      </Button>
      {#if section.controls}<div class="section-controls">{@render section.controls()}</div>{/if}
      </div>
      <div id={`${uid}-${index}`} class="section-content" hidden={closed.includes(index)}>{@render section.content()}</div>
    </section>
    {#if index < sections.length - 1}
      {@const next = nextOpen(index)}
      <Button variant="bare" class="section-resize" role="separator" aria-label={`Resize ${section.name} section`} aria-orientation="horizontal" aria-controls={`${uid}-${index}`} aria-valuemin={0} aria-valuemax={100} aria-valuenow={next < 0 ? 100 : Math.round(100 * weights[index] / (weights[index] + weights[next]))} disabled={closed.includes(index) || next < 0} title="Drag to resize · Double-click to reset sections" onpointerdown={event => start(event, index)} onkeydown={event => key(event, index)} ondblclick={() => save(sections.map(() => 1))}><span class="sr-only">Resize {section.name} section</span></Button>
    {/if}
  {/each}
</div>

<style>
  .rail-sections { display:grid; min-height:0; height:100%; overflow:hidden; }
  .repo-section { display:grid; grid-template-rows:34px minmax(0,1fr); min-height:0; overflow:hidden; }
  .repo-section.collapsed { grid-template-rows:34px; }
  .section-header { position:relative; min-width:0; }
  .section-controls { position:absolute; right:32px; top:5px; }
  .repo-section :global(.section-heading) { height:34px; min-width:0; gap:8px; padding:6px 12px; border-block:1px solid var(--color-edge); border-radius:0; background:var(--color-panel); color:var(--color-ink); font-size:13px; font-weight:600; }
  .repo-section :global(.section-heading:hover) { background:var(--color-sel); }
  .repo-section :global(.section-heading:focus-visible) { outline:2px solid var(--color-accent); outline-offset:-2px; }
  .repo-section :global(.section-heading > svg:first-child) { color:var(--color-ink-2); }
  .repo-section :global(.git-disclosure-chevron) { margin-left:auto; color:var(--color-faint); }
  .collapsed :global(.git-disclosure-chevron) { transform:rotate(-90deg); }
  .section-content { min-height:0; overflow:auto; overscroll-behavior:contain; padding-top:4px; }
  .section-content[hidden] { display:none; }
  .rail-sections :global(.section-resize) { position:relative; z-index:2; align-self:center; height:8px; min-height:0; width:100%; padding:0; border-radius:0; background:transparent; cursor:row-resize; touch-action:none; }
  .rail-sections :global(.section-resize:disabled) { pointer-events:none; }
  .rail-sections :global(.section-resize:enabled:hover), .rail-sections :global(.section-resize:focus-visible) { background:linear-gradient(transparent 3px,var(--color-accent) 3px,var(--color-accent) 5px,transparent 5px); outline:none; }
  .dragging, .dragging :global(*) { cursor:row-resize; user-select:none; }
</style>
