<script lang="ts">
  import { tick } from 'svelte';
  import { DaemonService } from '@bindings/desktop';
  import type { ReviewPackage } from '@bindings/internal/protocol';
  import { markLocation, type ReviewMark } from '$lib/reviewmaterial';
  import Button from '../Button.svelte';
  import Icon from '../Icon.svelte';
  import Select from '../Select.svelte';
  let { pkg, canComment = true, freshness = 'unknown', index = $bindable(0), onComment }: { pkg: ReviewPackage; freshness?: 'unknown' | 'stale' | 'current'; index?: number; canComment?: boolean; onComment: (screen: number, text: string, previous?: string) => boolean } = $props();
  let infoOpen = $state(true);
  const screens = $derived(pkg.screens ?? []);
  const screen = $derived(screens[index] ?? screens[0]);
  const modeKey = 'lola.review.comparisonMode';
  let preferredMode = 'slider';
  try {
    const saved = localStorage.getItem(modeKey);
    if (saved && ['slider', 'split', 'before', 'after'].includes(saved)) preferredMode = saved;
  } catch { /* storage unavailable — keep the default */ }
  let mode = $state('slider');
  function chooseMode(value: string) {
    preferredMode = value; mode = value; tool = 'view';
    // Temporary switches for annotations or missing baselines aren't preferences.
    try { localStorage.setItem(modeKey, value); } catch { /* remember for this review */ }
  }
  let tool = $state<'view' | 'pin' | 'area'>('view');
  let zoom = $state(100);
  let split = $state(50);
  let before = $state(''), after = $state(''), error = $state('');
  let retry = $state(0);
  let viewport = $state<HTMLDivElement>();
  let canvasArea = $state<HTMLDivElement>();
  let imageStage = $state<HTMLDivElement>();
  let editor = $state<HTMLDivElement>();
  let editorPosition = $state({left:12, top:12});
  let available = $state({width:1000,height:600});
  let dimensions = $state({before:{width:1440,height:900},after:{width:1440,height:900}});
  const fitWidth = $derived.by(() => {
    const d = mode === 'before' ? dimensions.before : dimensions.after;
    const ratio = mode === 'split' ? Math.min(dimensions.before.width / dimensions.before.height, dimensions.after.width / dimensions.after.height) * 2 : d.width / d.height;
    return Math.max(100, Math.min(available.width, (available.height - (mode === 'split' ? 30 : 0)) * ratio));
  });
  $effect(() => {
    const node = viewport;
    if (!node) return;
    const observer = new ResizeObserver(() => { available = {width:node.clientWidth-32,height:node.clientHeight-32}; });
    observer.observe(node);
    return () => observer.disconnect();
  });
  let sliding = false;
  function slideAt(e: PointerEvent) {
    const stage = (e.currentTarget as HTMLElement).parentElement!;
    const rect = stage.getBoundingClientRect();
    split = Math.max(0,Math.min(100,(e.clientX-rect.left)/rect.width*100));
  }
  function decoded(e: Event, which: 'before' | 'after') {
    const img = e.currentTarget as HTMLImageElement;
    dimensions = {...dimensions,[which]:{width:img.naturalWidth || 1440,height:img.naturalHeight || 900}};
  }

  let marks = $state<ReviewMark[]>([]);
  let active = $state(-1);
  let message = $state('');
  let start: {x:number;y:number} | undefined;
  let dragging = $state<ReviewMark>();
  const key = $derived(`lola.review.canvas.${pkg.session}.${pkg.id}.${index}`);
  const single = $derived(mode === 'before' || mode === 'after');
  const side = $derived(mode === 'before' ? 'Before' : 'After');
  const selected = $derived(marks[active]);
  const editing = $derived(canComment && single && selected?.side === side && tool === 'view');
  $effect(() => {
    const mark = selected, frame = canvasArea, stage = imageStage, popup = editor, scroller = viewport;
    if (!editing || !mark || !frame || !stage || !popup || !scroller) return;
    function position() {
      const bounds = frame!.getBoundingClientRect(), image = stage!.getBoundingClientRect();
      const x = image.left - bounds.left + mark!.x * image.width;
      const y = image.top - bounds.top + mark!.y * image.height;
      const width = popup!.offsetWidth, height = popup!.offsetHeight;
      const left = x + 20 + width <= bounds.width - 12 ? x + 20 : x - width - 20;
      editorPosition = {
        left: Math.max(12, Math.min(left, bounds.width - width - 12)),
        top: Math.max(12, Math.min(y + 16, bounds.height - height - 12)),
      };
    }
    const observer = new ResizeObserver(position);
    observer.observe(frame); observer.observe(stage); observer.observe(popup);
    scroller.addEventListener('scroll', position, {passive:true});
    position();
    return () => { observer.disconnect(); scroller.removeEventListener('scroll', position); };
  });
  function openComment(i: number) {
    active = i; mode = marks[i].side.toLowerCase(); tool = 'view'; message = '';
    void tick().then(() => document.getElementById('canvas-comment')?.focus({preventScroll:true}));
  }
  function closeComment(restoreFocus = true) {
    const i = active; active = -1;
    if (restoreFocus) void tick().then(() => imageStage?.querySelector<HTMLButtonElement>(`[data-mark="${i}"]`)?.focus({preventScroll:true}));
  }
  $effect(() => {
    if (!editing || !editor) return;
    const popup = editor;
    function clickAway(event: MouseEvent) {
      if (popup.contains(event.target as Node)) return;
      if (saveComment()) closeComment(false);
      else { event.preventDefault(); event.stopImmediatePropagation(); }
    }
    document.addEventListener('click', clickAway, true);
    return () => document.removeEventListener('click', clickAway, true);
  });

  $effect(() => {
    const sid = pkg.session, pid = pkg.id, s = screen;
    retry;
    let live = true;
    before = ''; after = ''; error = ''; active = -1; tool = 'view'; zoom = 100; start = undefined; dragging = undefined;
    mode = s?.before ? preferredMode : 'after';
    async function load(id: string) {
      const r = await DaemonService.ReviewPackageImage(sid, pid, id);
      if (!/^data:image\/(png|jpeg);base64,/.test(r.dataURL)) throw new Error('Unsupported screenshot');
      return r.dataURL;
    }
    if (s) {
      load(s.after).then(value => { if (live) after = value; }).catch(e => { if (live) error = String(e); });
      if (s.before) load(s.before).then(value => { if (live) before = value; }).catch(e => { if (live) error = String(e); });
    }
    return () => { live = false; };
  });
  $effect(() => {
    const storage = key; marks = []; active = -1; message = '';
    try {
      const saved = JSON.parse(localStorage.getItem(storage) ?? '[]');
      if (Array.isArray(saved)) marks = saved.filter(m => ['Before','After'].includes(m.side) && typeof m.text === 'string' && ['x','y','w','h'].every(k => Number.isFinite(m[k]) && m[k] >= 0 && m[k] <= 1)).slice(0,30);
    } catch { /* local drafts are optional */ }
  });
  function save() { try { localStorage.setItem(key, JSON.stringify(marks)); } catch { message = 'Draft storage unavailable. Keep this review open until you send your feedback.'; } }
  function selectTool(value: typeof tool) { message = ''; tool = value; if (value !== 'view' && !single) mode = 'after'; }
  function place(mark: ReviewMark) {
    if (!canComment || marks.length >= 30) { message = 'This screen has reached its 30-annotation limit.'; return; }
    marks = [...marks, mark]; save(); openComment(marks.length - 1);
  }
  function point(e: PointerEvent) {
    const r = (e.currentTarget as HTMLElement).getBoundingClientRect();
    return {x:Math.max(0,Math.min(1,(e.clientX-r.left)/r.width)), y:Math.max(0,Math.min(1,(e.clientY-r.top)/r.height))};
  }
  function down(e: PointerEvent) {
    if (tool === 'view' || !canComment || e.button !== 0) return;
    e.preventDefault(); start = point(e);
    (e.currentTarget as HTMLElement).setPointerCapture(e.pointerId);
    dragging = { ...start, w:0, h:0, side, text:'' };
  }
  function move(e: PointerEvent) {
    if (!start || tool !== 'area') return;
    const p = point(e);
    dragging = {x:Math.min(start.x,p.x),y:Math.min(start.y,p.y),w:Math.abs(start.x-p.x),h:Math.abs(start.y-p.y),side,text:''};
  }
  function up() { if (dragging) place(dragging); dragging = undefined; start = undefined; }
  function update(text: string) { marks = marks.map((m,i) => i === active ? {...m,text} : m); save(); }
  function saveComment(): boolean {
    if (!selected?.text.trim() || selected.text.trim() === selected.savedText) return true;
    const text = selected.text.trim();
    const id = selected.id || crypto.randomUUID();
    const content = `Annotation ${id} (latest comment)\n${markLocation(selected)}\n${text}`;
    if (onComment(index, content, selected.savedFeedback)) {
      marks = marks.map((m,i) => i === active ? {...m,id,savedText:text,savedFeedback:content} : m); save();
      message = 'Saved on this screenshot and added to feedback.';
      return true;
    }
    message = 'The feedback batch is full. Close this editor to send existing feedback, then retry.';
    return false;
  }
  function add() { if (saveComment()) closeComment(); }
</script>

{#if screen}
<section class="canvas" aria-label="Screenshot review canvas">
  <div class="tools">
    <div class="tool-group">
      <Button icon size="sm" selected={infoOpen} aria-label="Screen details" title="Screen details" aria-expanded={infoOpen} onclick={() => infoOpen = !infoOpen}><Icon name="panels-top-left" size={15} /></Button>
      <span class="toolbar-separator"></span>
      <Button icon size="sm" aria-label="Previous" title="Previous screen" disabled={index === 0} onclick={() => index--}><Icon name="chevron-left" size={15} /></Button>
      <span class="screen-count">{index+1} / {screens.length}</span>
      <Button icon size="sm" aria-label="Next" title="Next screen" disabled={index >= screens.length-1} onclick={() => index++}><Icon name="chevron-right" size={15} /></Button>
    </div>
    <div class="tool-group comparison" aria-label="Comparison mode">
      {#each [{id:'slider',label:'Slider'},{id:'split',label:'Side by side'},{id:'before',label:'Before'},{id:'after',label:'After'}] as item}
        <Button size="sm" selected={mode === item.id} disabled={item.id !== 'after' && !screen.before} onclick={() => chooseMode(item.id)}>{item.label}</Button>
      {/each}
    </div>
    <div class="tool-group">
      {#if marks.length && canComment}<Button size="sm" aria-label="Next comment" title="Open next comment" onclick={() => openComment((active+1)%marks.length)}>{marks.length} {marks.length === 1 ? 'comment' : 'comments'}</Button>{/if}
      {#if canComment}<Button icon size="sm" selected={tool === 'pin'} disabled={!after} aria-label="Add pin" title="Add pin" onclick={() => selectTool(tool === 'pin' ? 'view' : 'pin')}><Icon name="message-square-text" size={15} /></Button><Button icon size="sm" selected={tool === 'area'} disabled={!after} aria-label="Mark area" title="Mark area" onclick={() => selectTool(tool === 'area' ? 'view' : 'area')}><Icon name="square" size={15} /></Button>{/if}
      <span class="toolbar-separator"></span>
      <Select aria-label="Screenshot zoom" value={String(zoom)} onchange={e => zoom = Number(e.currentTarget.value)}><option value="100">Fit</option><option value="150">150%</option><option value="200">200%</option></Select>
    </div>
  </div>
  <div class="canvas-body" class:info-open={infoOpen}>
  {#if infoOpen}<aside class="info-rail" aria-label="Screen details">
    <nav class="screen-list" aria-label="Review screens">
      {#each screens as s,i}<button type="button" class="screen-link" class:active={index === i} aria-current={index === i ? 'true' : undefined} onclick={() => index = i}><Icon name="image" size={14} /><span>{s.title}</span></button>{/each}
    </nav>
    <div class="screen-info">
      <p class="source-status" class:outdated={freshness === 'stale'} role="status">{freshness === 'stale' ? 'Outdated material' : freshness === 'unknown' ? 'Revision unconfirmed' : 'Agent-provided'} · {pkg.head?.slice(0,8)}</p>
      <h2 class="sr-only">{screen.title}</h2>
      {#if screen.caption}<p>{screen.caption}</p>{/if}
      {#if screen.viewport}<p class="capture-detail"><Icon name="monitor" size={13} />{screen.viewport}</p>{/if}
      {#if screen.url}<p class="capture-url selectable">{screen.url}</p>{/if}
      {#if !screen.before}<p class="hint">Before capture unavailable</p>{/if}
      {#if mode === 'slider'}<p class="hint">Drag anywhere on the image to compare. Images fit the same frame; different viewport sizes may not align.</p>{/if}
      {#if tool !== 'view'}<p class="hint" role="status">{tool === 'pin' ? 'Click to place a pin' : 'Drag to mark an area'} on {side}. Keyboard: tab to the image and press Enter.</p>{/if}
    </div>
  </aside>{/if}
  <div class="canvas-area" bind:this={canvasArea}>
  <div class="viewport" bind:this={viewport}>
    {#if error}<p role="alert" class="p-4 text-warn">Could not load screenshot. <Button onclick={() => retry++}>Retry image</Button></p>
    {:else if !after || (mode !== 'after' && !before)}<p role="status" class="p-4 text-faint">Loading screenshots…</p>
    {:else}
      <div class="zoom-frame" style:width={`${fitWidth * zoom / 100}px`}>
        {#if mode === 'split'}
          <div class="split-view"><figure><figcaption>Before</figcaption><img src={before} onload={e => decoded(e, 'before')} alt={`${screen.title} — Before`} onerror={() => error = 'Image could not be decoded'} /></figure><figure><figcaption>After</figcaption><img src={after} onload={e => decoded(e, 'after')} alt={`${screen.title} — After`} onerror={() => error = 'Image could not be decoded'} /></figure></div>
        {:else}
          <div class="image-stage" bind:this={imageStage}>
            <img src={mode === 'before' ? before : after} onload={e => decoded(e, mode === 'before' ? 'before' : 'after')} alt={`${screen.title} — ${side}`} draggable="false" onerror={() => error = 'Image could not be decoded'} />
            {#if mode === 'slider'}<img class="baseline" style:clip-path={`inset(0 ${100-split}% 0 0)`} src={before} onload={e => decoded(e, 'before')} alt={`${screen.title} — Before`} draggable="false" onerror={() => error = 'Image could not be decoded'} /><div class="divider" style:left={`${split}%`}></div><div class="comparison-drag" role="slider" tabindex="0" aria-label="Before and after comparison" aria-valuemin="0" aria-valuemax="100" aria-valuenow={Math.round(split)} aria-valuetext={`${Math.round(split)}% before`}
              onpointerdown={e => { if (e.button !== 0) return; sliding = true; e.currentTarget.setPointerCapture(e.pointerId); e.currentTarget.focus({preventScroll:true}); slideAt(e); e.preventDefault(); }}
              onpointermove={e => { if (sliding) slideAt(e); }} onpointerup={() => sliding = false} onpointercancel={() => sliding = false}
              onkeydown={e => { if (['ArrowLeft','ArrowRight','Home','End'].includes(e.key)) { e.preventDefault(); split = e.key === 'Home' ? 0 : e.key === 'End' ? 100 : Math.max(0,Math.min(100,split+(e.key === 'ArrowLeft' ? -5 : 5))); } }}><span class="divider-handle" style:left={`${split}%`}><Icon name="arrow-right-left" size={14} /></span></div><span class="image-label before-label">Before</span><span class="image-label after-label">After</span>{/if}
            {#if single && canComment && tool !== 'view'}
              <button type="button" class="annotation-surface" aria-label={`Place comment on ${side} screenshot`}
                onpointerdown={down} onpointermove={move} onpointerup={up} onpointercancel={() => { start = undefined; dragging = undefined; }}
                onclick={e => { if (e.detail === 0) place({x:.5,y:.5,w:tool === 'area' ? .2 : 0,h:tool === 'area' ? .2 : 0,side,text:''}); }}
                onkeydown={e => { if (e.key === 'Escape') { e.stopPropagation(); tool = 'view'; dragging = undefined; start = undefined; } }}></button>
            {/if}
            {#if single}
              {#each marks as mark,i}{#if mark.side === side}<button class="pin" class:chosen={active === i} style:left={`${mark.x*100}%`} style:top={`${mark.y*100}%`} data-mark={i} aria-label={`Comment ${i+1}`} aria-expanded={active === i && editing} onclick={e => { e.stopPropagation(); openComment(i); }}>{i+1}</button>{#if mark.w || mark.h}<button type="button" class="region region-hit" aria-label={`Open area comment ${i+1}`} style:left={`${mark.x*100}%`} style:top={`${mark.y*100}%`} style:width={`${mark.w*100}%`} style:height={`${mark.h*100}%`} onclick={() => openComment(i)}></button>{/if}{/if}{/each}
              {#if dragging}<div class="region" style:left={`${dragging.x*100}%`} style:top={`${dragging.y*100}%`} style:width={`${dragging.w*100}%`} style:height={`${dragging.h*100}%`}></div>{/if}
            {/if}
          </div>
        {/if}
      </div>
    {/if}
  </div>
  {#if editing && selected}
    <div class="annotation-editor" bind:this={editor} role="dialog" aria-label={`Comment ${active+1} · ${selected.side}`} tabindex="-1" style:left={`${editorPosition.left}px`} style:top={`${editorPosition.top}px`}
      onkeydown={e => { if (e.key === 'Escape') { e.preventDefault(); e.stopPropagation(); closeComment(); } }}>
      <div class="editor-heading"><label for="canvas-comment">Comment {active+1} · {selected.side}</label><Button icon size="xs" aria-label="Close comment" title="Close without discarding" onclick={() => closeComment()}><Icon name="x" size={14} /></Button></div>
      <textarea id="canvas-comment" aria-label="Screenshot comment" rows="3" maxlength="2000" placeholder="What should change here?" value={selected.text} oninput={e => update(e.currentTarget.value)}></textarea>
      <div class="editor-actions"><Button variant="primary" disabled={!selected.text.trim()} onclick={add}>{selected.text.trim() === selected.savedText ? 'Done' : selected.savedText ? 'Update feedback' : 'Add comment to feedback'}</Button>{#if !selected.savedText}<Button size="sm" aria-label="Discard comment" title="Discard comment" onclick={() => { marks = marks.filter((_,i) => i !== active); active = -1; save(); }}>Discard</Button>{/if}</div>
      {#if message}<p class="hint" role="status">{message}</p>{/if}
    </div>
  {:else if message}<p class="canvas-notice" role="status">{message}</p>{/if}
  </div>
  </div>

</section>
{/if}

<style>
  .canvas { min-width:0; height:100%; display:grid; grid-template-rows:auto minmax(0,1fr); }
  .tools { display:flex; align-items:center; justify-content:space-between; gap:12px; padding:8px 12px; border-bottom:1px solid var(--color-edge); }
  .tool-group { display:flex; align-items:center; gap:3px; }
  .comparison { padding:2px; border-radius:6px; background:var(--color-canvas); }
  .toolbar-separator { height:18px; width:1px; background:var(--color-edge); margin:0 5px; }
  .screen-count { color:var(--color-faint); font-size:12px; min-width:32px; text-align:center; }
  .tools :global(.lola-select) { width:78px; }
  .tools :global(select) { height:28px; padding-top:2px; padding-bottom:2px; }
  .canvas-body { display:grid; grid-template-columns:minmax(0,1fr); min-height:0; }
  .canvas-body.info-open { grid-template-columns:220px minmax(0,1fr); }
  .info-rail { overflow:auto; min-width:0; padding:12px; border-right:1px solid var(--color-edge); }
  .screen-list { display:grid; gap:4px; padding-bottom:16px; }
  .screen-link { display:grid; grid-template-columns:14px minmax(0,1fr); align-items:start; gap:8px; padding:9px 10px; border-radius:5px; text-align:left; font-size:12px; line-height:18px; color:var(--color-ink-2); cursor:pointer; }
  .screen-link :global(svg) { margin-top:2px; }
  .screen-link:hover { background:var(--color-sel); color:var(--color-ink); }
  .screen-link.active { background:var(--color-accent-fill); color:var(--color-accent-ink); }
  .screen-info { border-top:1px solid var(--color-edge); padding-top:16px; font-size:12px; line-height:1.6; color:var(--color-ink-2); }
  .source-status { margin-bottom:12px; color:var(--color-faint); }
  .source-status.outdated { color:var(--color-warn); }
  .capture-detail { display:flex; align-items:center; gap:6px; margin-top:16px; color:var(--color-faint); }
  .capture-url { overflow-wrap:anywhere; margin-top:6px; color:var(--color-faint); }
  .canvas-area { position:relative; min-width:0; min-height:0; }
  .viewport { height:100%; min-width:0; min-height:0; overflow:auto; background:var(--color-canvas); padding:16px; display:flex; }
  .zoom-frame { margin:auto; flex-shrink:0; }
  .image-stage { position:relative; width:100%; }
  .annotation-surface { position:absolute; z-index:3; inset:0; width:100%; height:100%; cursor:crosshair; touch-action:none; border:0; background:transparent; }
  img { display:block; width:100%; height:auto; user-select:none; }
  .baseline { position:absolute; inset:0; height:100%; object-fit:contain; background:var(--color-canvas); pointer-events:none; }
  .comparison-drag { position:absolute; inset:0; cursor:ew-resize; touch-action:none; }
  .divider-handle { position:absolute; top:50%; transform:translate(-50%,-50%); display:grid; place-items:center; width:32px; height:36px; border:1px solid var(--color-accent); border-radius:6px; color:var(--color-accent-ink); background:var(--color-panel); cursor:ew-resize; touch-action:none; }
  .divider { position:absolute; top:0; bottom:0; width:2px; background:var(--color-accent); pointer-events:none; }
  .split-view { display:grid; grid-template-columns:1fr 1fr; gap:16px; align-items:start; }
  figcaption { color:var(--color-ink-2); margin-bottom:8px; font-size:12px; }
  .hint { color:var(--color-ink-2); font-size:12px; line-height:1.6; margin-top:12px; }
  .image-label { position:absolute; top:8px; padding:3px 7px; font-size:11px; color:var(--color-ink); background:var(--color-panel); border:1px solid var(--color-edge); border-radius:4px; pointer-events:none; }
  .before-label { left:8px; } .after-label { right:8px; }
  .pin { position:absolute; z-index:2; width:26px; height:26px; transform:translate(-50%,-50%); border-radius:50%; background:var(--color-accent-fill); color:var(--color-accent-ink); border:2px solid var(--color-accent); cursor:pointer; font-size:12px; }
  .pin.chosen { outline:2px solid var(--color-ink); outline-offset:2px; }
  .region { position:absolute; border:2px solid var(--color-accent); background:color-mix(in srgb,var(--color-accent) 12%,transparent); pointer-events:none; }
  .region-hit { pointer-events:auto; cursor:pointer; }
  .annotation-editor { position:absolute; z-index:5; width:min(300px,calc(100% - 24px)); max-height:calc(100% - 24px); overflow:auto; padding:12px; border:1px solid var(--color-edge); border-radius:8px; background:var(--color-panel); box-shadow:0 8px 28px #0005; }
  .editor-heading { display:flex; align-items:center; justify-content:space-between; gap:8px; margin-bottom:8px; }
  .editor-heading label { margin:0; }
  .editor-actions { display:flex; flex-wrap:wrap; align-items:center; gap:6px; }
  .canvas-notice { position:absolute; bottom:12px; left:12px; max-width:calc(100% - 24px); padding:8px 12px; font-size:12px; color:var(--color-ink-2); border:1px solid var(--color-edge); border-radius:6px; background:var(--color-panel); pointer-events:none; }
  label { display:block; font-size:12px; color:var(--color-ink-2); margin-bottom:8px; }
  textarea { display:block; width:100%; padding:10px; margin-bottom:10px; background:var(--color-canvas); border:1px solid var(--color-edge); border-radius:6px; color:var(--color-ink); resize:vertical; font-size:13px; }
  .screen-link:focus-visible,.annotation-surface:focus-visible,.comparison-drag:focus-visible { outline:2px solid var(--color-accent); outline-offset:2px; }
  @media(max-width:900px) { .canvas-body.info-open { grid-template-columns:190px minmax(0,1fr); } .tools { gap:6px; padding:6px; flex-wrap:wrap; } .comparison { order:3; flex:1; justify-content:center; } .viewport { padding:16px; } }
  @media(max-width:560px) { .canvas-body { position:relative; } .canvas-body.info-open { grid-template-columns:minmax(0,1fr); } .info-rail { position:absolute; top:0; bottom:0; left:0; width:210px; background:var(--color-panel); z-index:4; box-shadow:4px 6px 16px #0003; } }
</style>
