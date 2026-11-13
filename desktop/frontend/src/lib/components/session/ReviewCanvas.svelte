<script lang="ts">
  import { tick } from 'svelte';
  import { DaemonService } from '@bindings/desktop';
  import type { ReviewPackage } from '@bindings/internal/protocol';
  import { markLocation, type ReviewMark } from '$lib/reviewmaterial';
  import Button from '../Button.svelte';
  import Icon from '../Icon.svelte';
  import Select from '../Select.svelte';
  let { pkg, canComment = true, freshness = 'unknown', index = $bindable(0), onComment, onDeleteComment, onToggleFeedback }: { pkg: ReviewPackage; freshness?: 'unknown' | 'stale' | 'current'; index?: number; canComment?: boolean; onComment: (screen: number, text: string, previous?: string) => boolean; onDeleteComment?: (screen: number, text: string) => void; onToggleFeedback?: () => void } = $props();
  let infoOpen = $state(true);
  let shortcutsOpen = $state(false);
  let root = $state<HTMLElement>();
  let lastComment = $state(-1);
  const saveKeys = typeof navigator !== 'undefined' && /Mac|iPhone|iPad/.test(navigator.platform) ? '⌘↵' : 'Ctrl+Enter';
  const shortcuts = [
    ['C', 'Comment pin'], ['R', 'Rectangle'], ['V / Esc', 'Cancel drawing'],
    ['↑ / ↓', 'Previous / next screenshot'], ['← / →', 'Move comparison divider'], ['N / Shift+N', 'Next / previous comment'],
    ['1 / 2', 'Slider / side by side'], ['3 / 4', 'Before / after'],
    ['+ / − / 0', 'Zoom in / out / fit'], ['I', 'Screen details'], ['B', 'Checks & feedback'],
    [saveKeys, 'Save comment'], ['Esc', 'Close comment'], ['⌘⌫ / ⌘Delete', 'Delete selected comment'], ['Delete', 'Delete focused comment'], ['?', 'Keyboard shortcuts'],
  ];
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
  const zoomLevels = [100, 150, 200, 300, 400];
  let split = $state(50);
  let before = $state(''), after = $state(''), error = $state('');
  let retry = $state(0);
  let viewport = $state<HTMLDivElement>();
  let zoomFrame = $state<HTMLDivElement>();
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
  let zoomRevision = 0;
  async function setZoom(value: number, anchor?: {x:number;y:number}) {
    const next = Math.max(100, Math.min(400, Math.round(value)));
    if (!Number.isFinite(next) || next === zoom) return;
    const scroller = viewport, frame = zoomFrame;
    const bounds = scroller?.getBoundingClientRect(), image = frame?.getBoundingClientRect();
    const point = anchor ?? (bounds && scroller ? {x:bounds.left + scroller.clientWidth / 2, y:bounds.top + scroller.clientHeight / 2} : undefined);
    const position = point && image && image.width && image.height
      ? {x:(point.x-image.left)/image.width, y:(point.y-image.top)/image.height} : undefined;
    const revision = ++zoomRevision;
    zoom = next;
    await tick();
    if (revision !== zoomRevision || !scroller || !frame || frame !== zoomFrame) return;
    if (next === 100) { scroller.scrollLeft = 0; scroller.scrollTop = 0; }
    else if (position && point) {
      // Keep the same image coordinate under the pointer (or viewport center).
      const resized = frame.getBoundingClientRect();
      scroller.scrollLeft += resized.left + position.x * resized.width - point.x;
      scroller.scrollTop += resized.top + position.y * resized.height - point.y;
    }
  }
  function stepZoom(direction: number) {
    void setZoom(direction > 0 ? zoomLevels.find(level => level > zoom) ?? 400 : [...zoomLevels].reverse().find(level => level < zoom) ?? 100);
  }
  $effect(() => {
    const node = viewport;
    if (!node) return;
    function wheel(event: WheelEvent) {
      if (!event.ctrlKey && !event.metaKey) return; // Ordinary scrolling pans the enlarged image.
      event.preventDefault();
      const delta = event.deltaY * (event.deltaMode === 1 ? 16 : event.deltaMode === 2 ? node!.clientHeight : 1);
      void setZoom(zoom * Math.exp(-delta * .01), {x:event.clientX,y:event.clientY});
    }
    // WKWebView reports trackpad pinches as gesture events rather than Ctrl+wheel.
    let gestureZoom = 100;
    function gestureStart(event: Event) { event.preventDefault(); gestureZoom = zoom; }
    function gestureChange(event: Event) {
      event.preventDefault();
      const gesture = event as Event & {scale:number;clientX:number;clientY:number};
      const anchor = Number.isFinite(gesture.clientX) && Number.isFinite(gesture.clientY) ? {x:gesture.clientX,y:gesture.clientY} : undefined;
      void setZoom(gestureZoom * gesture.scale, anchor);
    }
    function gestureEnd(event: Event) { event.preventDefault(); }
    node.addEventListener('wheel', wheel, {passive:false});
    node.addEventListener('gesturestart', gestureStart, {passive:false});
    node.addEventListener('gesturechange', gestureChange, {passive:false});
    node.addEventListener('gestureend', gestureEnd, {passive:false});
    return () => {
      node.removeEventListener('wheel', wheel);
      node.removeEventListener('gesturestart', gestureStart);
      node.removeEventListener('gesturechange', gestureChange);
      node.removeEventListener('gestureend', gestureEnd);
    };
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
  let saveFailed = $state(false);
  let confirmClose = $state(false);
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
    active = i; lastComment = i; mode = marks[i].side.toLowerCase(); tool = 'view'; message = ''; saveFailed = false; confirmClose = false;
    void tick().then(() => document.getElementById('canvas-comment')?.focus({preventScroll:true}));
  }
  function closeComment(restoreFocus = true) {
    const i = active; active = -1; confirmClose = false;
    if (restoreFocus) void tick().then(() => imageStage?.querySelector<HTMLButtonElement>(`[data-mark="${i}"]`)?.focus({preventScroll:true}));
  }
  function requestClose(restoreFocus = true): boolean {
    if (selected && selected.text.trim() !== (selected.savedText ?? '')) {
      confirmClose = true;
      return false;
    }
    closeComment(restoreFocus);
    return true;
  }
  function focusCanvas() { void tick().then(() => root?.focus({preventScroll:true})); }
  function deleteComment(comment = active) {
    const mark = marks[comment];
    if (!mark) return;
    if (mark.savedFeedback) onDeleteComment?.(index, mark.savedFeedback);
    marks = marks.filter((_,i) => i !== comment);
    lastComment = comment - 1;
    save(); closeComment(false); focusCanvas();
  }
  function discardChanges() {
    if (selected?.savedText) marks = marks.map((m,i) => i === active ? {...m,text:m.savedText!} : m);
    else marks = marks.filter((_,i) => i !== active);
    save(); closeComment(false); focusCanvas();
  }
  $effect(() => {
    if (!editing || !editor) return;
    const popup = editor;
    function clickAway(event: MouseEvent) {
      if (popup.contains(event.target as Node)) return;
      if (event.target instanceof Element && event.target.closest('.zoom-controls')) return;
      if (!requestClose(false)) { event.preventDefault(); event.stopImmediatePropagation(); }
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
    const storage = key; marks = []; active = -1; lastComment = -1; message = '';
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
  function update(text: string) { saveFailed = false; marks = marks.map((m,i) => i === active ? {...m,text} : m); save(); }
  function saveComment(): boolean {
    if (!selected?.text.trim() || selected.text.trim() === selected.savedText) return true;
    const text = selected.text.trim();
    const id = selected.id || crypto.randomUUID();
    const content = `Annotation ${id} (latest comment)\n${markLocation(selected)}\n${text}`;
    if (onComment(index, content, selected.savedFeedback)) {
      marks = marks.map((m,i) => i === active ? {...m,id,savedText:text,savedFeedback:content} : m); save();
      saveFailed = false; message = 'Added to feedback.';
      return true;
    }
    saveFailed = true;
    return false;
  }
  function add() { if (saveComment()) closeComment(); }

  function nextComment(direction = 1) {
    if (marks.length) openComment(lastComment < 0 ? (direction > 0 ? 0 : marks.length-1) : (lastComment + direction + marks.length) % marks.length);
  }
  function armTool(value: 'pin' | 'area') {
    selectTool(tool === value ? 'view' : value);
    if (tool !== 'view') void tick().then(() => root?.querySelector<HTMLButtonElement>('.annotation-surface')?.focus({preventScroll:true}));
  }
  function cancelTool() { tool = 'view'; dragging = undefined; start = undefined; focusCanvas(); }
  // Called by the canvas and the review modal, so shortcuts also work when a
  // tab/header control has focus. Consumed events never reach app-level keys.
  export function handleKey(e: KeyboardEvent) {
    if (e.defaultPrevented || e.isComposing || e.keyCode === 229 || e.repeat) return;
    const target = e.target instanceof Element ? e.target : null;
    const inEditor = !!target && !!editor?.contains(target);
    const typing = !!target?.closest('input,textarea,select,[contenteditable]:not([contenteditable="false"]),[role="textbox"]');
    const focusedMark = target?.closest<HTMLElement>('[data-mark]');
    const consume = () => { e.preventDefault(); e.stopPropagation(); };
    if ((e.metaKey || e.ctrlKey) && !e.altKey && !e.shiftKey && e.key === 'Enter' && editing && inEditor) {
      consume(); if (selected?.text.trim()) add(); return;
    }
    if (e.metaKey && !e.ctrlKey && !e.altKey && !e.shiftKey && ['Backspace','Delete'].includes(e.key) && canComment
      && (focusedMark || (editing && (inEditor || !typing)))) {
      consume(); deleteComment(focusedMark ? Number(focusedMark.dataset.mark) : active); return;
    }
    if (e.metaKey || e.ctrlKey || e.altKey) return;
    if (e.key === 'Escape') {
      if (shortcutsOpen) { consume(); shortcutsOpen = false; focusCanvas(); }
      else if (editing) { consume(); requestClose(); }
      else if (tool !== 'view') { consume(); cancelTool(); }
      return;
    }
    if (typing) return;
    if (['Delete','Backspace'].includes(e.key) && canComment && (focusedMark || (editing && inEditor))) {
      consume(); deleteComment(focusedMark ? Number(focusedMark.dataset.mark) : active); return;
    }
    if (e.key === '?') { consume(); shortcutsOpen = !shortcutsOpen; return; }
    const key = e.key.toLowerCase();
    if (['+','=','-','0'].includes(key)) {
      consume();
      if (key === '0') void setZoom(100); else stepZoom(key === '-' ? -1 : 1);
      return;
    }
    if (['arrowleft','arrowright'].includes(key)) {
      if (mode === 'slider') { consume(); split = Math.max(0,Math.min(100,split+(key === 'arrowleft' ? -5 : 5))); }
      return;
    }
    if (!['c','r','v','i','b','n','arrowup','arrowdown','1','2','3','4'].includes(key)) return;
    // Never bypass the unsaved-changes prompt by switching tools or images.
    consume();
    if (editing && !requestClose(false)) return;
    switch (key) {
      case 'c': if (canComment && after && !error) armTool('pin'); break;
      case 'r': if (canComment && after && !error) armTool('area'); break;
      case 'v': cancelTool(); break;
      case 'i': infoOpen = !infoOpen; break;
      case 'b': onToggleFeedback?.(); break;
      case 'n': if (canComment) nextComment(e.shiftKey ? -1 : 1); break;
      case 'arrowup': index = Math.max(0,index-1); focusCanvas(); break;
      case 'arrowdown': index = Math.min(screens.length-1,index+1); focusCanvas(); break;
      case '1': case '2': case '3': case '4': {
        const mode = ['slider','split','before','after'][Number(key)-1];
        if (screen.before || mode === 'after') chooseMode(mode);
        break;
      }
    }
  }
</script>

{#if screen}
<!-- Route keys bubbling from canvas controls; this region isn't an extra tab stop. -->
<!-- svelte-ignore a11y_no_noninteractive_element_interactions -->
<section class="canvas" aria-label="Screenshot review canvas" bind:this={root} tabindex="-1" onkeydown={handleKey}
  onpointerdown={e => {
    const target = e.target as Element;
    if (!editing && target.closest('.viewport') && !target.closest('button,input,textarea,select,a,[tabindex]:not([tabindex="-1"])')) {
      e.preventDefault(); root?.focus({preventScroll:true});
    }
  }}>
  <div class="tools">
    <div class="tool-group">
      <Button icon size="sm" selected={infoOpen} aria-label="Screen details" title="Screen details · I" aria-keyshortcuts="i" aria-expanded={infoOpen} onclick={() => infoOpen = !infoOpen}><Icon name="panels-top-left" size={15} /></Button>
      <span class="toolbar-separator"></span>
      <Button icon size="sm" aria-label="Previous" title="Previous screen · ↑" aria-keyshortcuts="ArrowUp" disabled={index === 0} onclick={() => index--}><Icon name="chevron-up" size={15} /></Button>
      <span class="screen-count">{index+1} / {screens.length}</span>
      <Button icon size="sm" aria-label="Next" title="Next screen · ↓" aria-keyshortcuts="ArrowDown" disabled={index >= screens.length-1} onclick={() => index++}><Icon name="chevron-down" size={15} /></Button>
    </div>
    <div class="tool-group comparison" aria-label="Comparison mode">
      {#each [{id:'slider',label:'Slider'},{id:'split',label:'Side by side'},{id:'before',label:'Before'},{id:'after',label:'After'}] as item,i}
        <Button size="sm" title={`${item.label} · ${i+1}`} aria-keyshortcuts={String(i+1)} selected={mode === item.id} disabled={item.id !== 'after' && !screen.before} onclick={() => chooseMode(item.id)}>{item.label}</Button>
      {/each}
    </div>
    <div class="tool-group">
      {#if marks.length && canComment}<Button size="sm" aria-label="Next comment" title="Next comment · N / Previous · Shift+N" aria-keyshortcuts="n Shift+n" onclick={() => nextComment()}>{marks.length} {marks.length === 1 ? 'comment' : 'comments'}</Button>{/if}
      {#if canComment}<Button icon size="sm" selected={tool === 'pin'} disabled={!after} aria-label="Add pin" title="Comment pin · C" aria-keyshortcuts="c" onclick={() => armTool('pin')}><Icon name="message-square-text" size={15} /></Button><Button icon size="sm" selected={tool === 'area'} disabled={!after} aria-label="Mark area" title="Mark area · R" aria-keyshortcuts="r" onclick={() => armTool('area')}><Icon name="square" size={15} /></Button>{/if}
      <span class="toolbar-separator"></span>
      <div class="tool-group zoom-controls" role="group" aria-label="Preview zoom">
        <Button icon size="sm" aria-label="Zoom out" title="Zoom out · −" disabled={zoom <= 100} onclick={() => stepZoom(-1)}><Icon name="minus" size={15} /></Button>
        <Select title="Zoom relative to Fit · + / − / 0 to fit · Pinch or Ctrl+scroll" aria-keyshortcuts="+ - 0" aria-label="Screenshot zoom" value={String(zoom)} onchange={e => void setZoom(Number(e.currentTarget.value))}>
          {#each zoomLevels as level}<option value={String(level)}>{level === 100 ? 'Fit' : `${level}%`}</option>{/each}
          {#if !zoomLevels.includes(zoom)}<option value={String(zoom)}>{zoom}%</option>{/if}
        </Select>
        <Button icon size="sm" aria-label="Zoom in" title="Zoom in · +" disabled={zoom >= 400} onclick={() => stepZoom(1)}><Icon name="plus" size={15} /></Button>
      </div>
      <Button icon size="sm" aria-label="Screenshot shortcuts" title="Keyboard shortcuts · ?" aria-keyshortcuts="?" aria-expanded={shortcutsOpen} selected={shortcutsOpen} onclick={() => shortcutsOpen = !shortcutsOpen}><Icon name="circle-help" size={15} /></Button>
    </div>
    {#if shortcutsOpen}<aside class="shortcut-help" aria-label="Screenshot shortcuts">
      <div class="shortcut-heading"><strong>Screenshot shortcuts</strong><Button icon size="xs" aria-label="Close shortcuts" onclick={() => { shortcutsOpen = false; focusCanvas(); }}><Icon name="x" size={14} /></Button></div>
      <dl>{#each shortcuts as [keys,action]}<div><dt>{action}</dt><dd><kbd>{keys}</kbd></dd></div>{/each}</dl>
      <p>Pinch or Ctrl+scroll to zoom around the pointer. Scroll to pan. Zoom percentages are relative to Fit.</p>
      <p>In a comment, {saveKeys} saves and ⌘Delete removes it. Plain Delete only removes comments outside text fields.</p>
    </aside>{/if}
  </div>
  <div class="canvas-body" class:info-open={infoOpen}>
  {#if infoOpen}<aside class="info-rail" aria-label="Screen details">
    <nav class="screen-list" aria-label="Review screens">
      {#each screens as s,i}<button type="button" class="screen-link" class:active={index === i} aria-current={index === i ? 'true' : undefined} onclick={() => index = i}><Icon name="image" size={14} /><span>{s.title}</span></button>{/each}
    </nav>
    <h2 class="sr-only">{screen.title}</h2>
    {#if screen.caption || screen.url || screen.viewport || screen.selector || freshness === 'stale'}<div class="screen-info">
      {#if freshness === 'stale'}<p class="outdated" role="status"><Icon name="circle-alert" size={13} />Outdated screenshots</p>{/if}
      {#if screen.caption}<p>{screen.caption}</p>{/if}
      {#if screen.url}<p class="break-words">{screen.url}</p>{/if}
      {#if screen.viewport}<p>{screen.viewport}</p>{/if}
      {#if screen.selector}<p class="break-words">Element: <code>{screen.selector}</code></p>{/if}
    </div>{/if}
  </aside>{/if}
  <div class="canvas-area" bind:this={canvasArea}>
  <div class="viewport" bind:this={viewport}>
    {#if error}<p role="alert" class="p-4 text-warn">Could not load screenshot. <Button onclick={() => retry++}>Retry image</Button></p>
    {:else if !after || (mode !== 'after' && !before)}<p role="status" class="p-4 text-faint">Loading screenshots…</p>
    {:else}
      <div class="zoom-frame" bind:this={zoomFrame} style:width={`${fitWidth * zoom / 100}px`}>
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
                ></button>
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
      onkeydown={handleKey}>
      <div class="editor-heading">
        <label for="canvas-comment">Comment {active+1}<span class="editor-side">{selected.side}</span></label>
        <div class="editor-controls">
          <Button icon size="xs" variant="danger" aria-label="Delete comment" title="Delete comment · ⌘Backspace / ⌘Delete" aria-keyshortcuts="Meta+Backspace Meta+Delete Delete Backspace" onclick={() => deleteComment()}><Icon name="trash-2" size={14} /></Button>
          <Button icon size="xs" aria-label="Close comment" title="Close comment · Esc" aria-keyshortcuts="Escape" onclick={() => requestClose()}><Icon name="x" size={14} /></Button>
        </div>
      </div>
      <textarea id="canvas-comment" aria-label="Screenshot comment" rows="3" maxlength="2000" placeholder="What should change?" value={selected.text} oninput={e => update(e.currentTarget.value)}></textarea>
      {#if confirmClose}<p class="save-question">Save changes?</p>{/if}
      <div class="editor-actions">
        {#if confirmClose}
          <Button size="xs" onclick={() => confirmClose = false}>Keep editing</Button>
          <Button size="xs" variant="danger" onclick={discardChanges}>Discard</Button>
        {/if}
        <Button variant="primary" size="sm" disabled={!selected.text.trim()} title={`Save comment · ${saveKeys}`} aria-keyshortcuts="Meta+Enter Control+Enter" onclick={add}><Icon name="check" size={14} />{selected.text.trim() === selected.savedText ? 'Done' : 'Save'}</Button>
      </div>
      {#if saveFailed}<p class="hint" role="status">Couldn’t save. Your draft is kept—try again.</p>{/if}
      {#if message}<p class="hint" role="status">{message}</p>{/if}
    </div>
  {:else if message}<p class="canvas-notice" role="status">{message}</p>{/if}
  </div>
  </div>

</section>
{/if}

<style>
  .canvas { min-width:0; height:100%; display:grid; grid-template-rows:auto minmax(0,1fr); }
  .canvas:focus { outline:none; }
  .tools { position:relative; display:flex; align-items:center; justify-content:space-between; gap:12px; padding:8px 12px; border-bottom:1px solid var(--color-edge); }
  .shortcut-help { position:absolute; z-index:6; top:calc(100% + 6px); right:8px; width:min(340px,calc(100% - 16px)); max-height:70vh; overflow:auto; padding:12px; border:1px solid var(--color-edge); border-radius:8px; background:var(--color-panel); box-shadow:0 8px 28px #0005; font-size:12px; }
  .shortcut-heading { display:flex; align-items:center; justify-content:space-between; margin-bottom:8px; }
  .shortcut-help dl div { display:flex; justify-content:space-between; gap:16px; padding:4px 0; }
  .shortcut-help dt { color:var(--color-ink-2); }
  .shortcut-help kbd { font:inherit; color:var(--color-ink); white-space:nowrap; }
  .shortcut-help p { margin-top:10px; color:var(--color-ink-2); font-size:11px; }
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
  .outdated { display:flex; align-items:center; gap:6px; margin-bottom:12px; color:var(--color-warn); }
  .canvas-area { position:relative; min-width:0; min-height:0; }
  .viewport { height:100%; min-width:0; min-height:0; overflow:auto; overflow-anchor:none; background:var(--color-canvas); padding:16px; display:flex; }
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
  .annotation-editor { position:absolute; z-index:5; width:min(300px,calc(100% - 24px)); max-height:calc(100% - 24px); overflow:auto; padding:10px; border:1px solid var(--color-edge); border-radius:8px; background:var(--color-panel); box-shadow:0 8px 28px #0005; animation:comment-open 150ms cubic-bezier(.16,1,.3,1); }
  .editor-heading { display:flex; align-items:center; justify-content:space-between; gap:8px; margin-bottom:8px; }
  .editor-heading label { display:flex; align-items:center; gap:8px; margin:0; }
  .editor-side { color:var(--color-faint); font-size:11px; }
  .editor-controls { display:flex; align-items:center; gap:2px; }
  .save-question { margin:0 0 8px; font-size:12px; color:var(--color-ink); }
  .editor-actions { display:flex; flex-wrap:wrap; justify-content:flex-end; align-items:center; gap:8px; }
  @keyframes comment-open { from { opacity:.6; transform:translateY(4px); } to { opacity:1; transform:translateY(0); } }
  @media(prefers-reduced-motion:reduce) { .annotation-editor { animation:none; } }
  .canvas-notice { position:absolute; bottom:12px; left:12px; max-width:calc(100% - 24px); padding:8px 12px; font-size:12px; color:var(--color-ink-2); border:1px solid var(--color-edge); border-radius:6px; background:var(--color-panel); pointer-events:none; }
  label { display:block; font-size:12px; color:var(--color-ink-2); margin-bottom:8px; }
  textarea { display:block; width:100%; padding:10px; margin-bottom:10px; background:var(--color-canvas); border:1px solid var(--color-edge); border-radius:6px; color:var(--color-ink); resize:vertical; font-size:13px; }
  .screen-link:focus-visible,.annotation-surface:focus-visible,.comparison-drag:focus-visible { outline:2px solid var(--color-accent); outline-offset:2px; }
  @media(max-width:900px) { .canvas-body.info-open { grid-template-columns:190px minmax(0,1fr); } .tools { gap:6px; padding:6px; flex-wrap:wrap; } .comparison { order:3; flex:1; justify-content:center; } .viewport { padding:16px; } }
  @media(max-width:560px) { .canvas-body { position:relative; } .canvas-body.info-open { grid-template-columns:minmax(0,1fr); } .info-rail { position:absolute; top:0; bottom:0; left:0; width:210px; background:var(--color-panel); z-index:4; box-shadow:4px 6px 16px #0003; } }
</style>
