<script lang="ts">
  import { tick, type Snippet } from 'svelte';
  import { SvelteMap } from 'svelte/reactivity';
  import { DaemonService } from '@bindings/desktop';
  import type { ReviewPackage } from '@bindings/internal/protocol';
  import { markLocation, markHit, markKind, validMark, arrowHead, simplifyStroke, type MarkKind, type ReviewMark } from '$lib/reviewmaterial';
  import './annotation-editor.css';
  import SessionPreview from './SessionPreview.svelte';
  import { prepOf } from '$lib/preview.svelte';
  import { reviewLivePages, reviewSessionId } from '$lib/reviewworkspace.svelte';
  import { touchSurface } from '$lib/touchsurface';
  import Button from '../Button.svelte';
  import Icon from '../Icon.svelte';
  import Select from '../Select.svelte';
  let { pkg, canComment = true, freshness = 'unknown', index = $bindable(0), onComment, onDeleteComment, onToggleFeedback, reviewAction, compact = false }: { pkg: ReviewPackage; freshness?: 'unknown' | 'stale' | 'current'; index?: number; canComment?: boolean; onComment: (screen: number, text: string, previous?: string) => boolean; onDeleteComment?: (screen: number, text: string) => void; onToggleFeedback?: () => void; compact?: boolean; reviewAction?: Snippet } = $props();
  // `compact` (the screenshots half of the Review screen) starts with the screen list folded away.
  let shortcutsOpen = $state(false);
  let root = $state<HTMLElement>();
  let lastComment = $state(-1);
  const saveKeys = typeof navigator !== 'undefined' && /Mac|iPhone|iPad/.test(navigator.platform) ? '⌘↵' : 'Ctrl+Enter';
  const shortcuts = [
    ['C', 'Comment (click or drag)'], ['R', 'Rectangle'], ['E', 'Ellipse'], ['A', 'Arrow'], ['L', 'Line'], ['P', 'Pen'], ['V / Esc', 'Cancel drawing'],
    ['↑ / ↓', 'Previous / next screenshot'], ['← / →', 'Move comparison divider'], ['N / Shift+N', 'Next / previous comment'],
    ['1 / 2', 'Slider / side by side'], ['3 / 4', 'Before / after'],
    ['+ / − / 0', 'Zoom in / out / fit'], ['I', 'Show or hide review instruction'], ['B', 'Checks & feedback'],
    [saveKeys, 'Save comment'], ['Esc', 'Close comment'], ['⌘⌫ / ⌘Delete', 'Delete selected comment'], ['Delete', 'Delete focused comment'], ['?', 'Keyboard shortcuts'],
  ];
  const screens = $derived(pkg.screens ?? []);
  const screen = $derived(screens[index]);
  const live = $derived(reviewLivePages(pkg));
  const page = $derived(live.pages[index - screens.length]);
  const total = $derived(screens.length + live.pages.length);
  const caption = $derived(screen?.caption || page?.caption || '');
  const noteId = $props.id();
  const noteStates = new SvelteMap<number, 'hidden' | 'pinned'>();
  const noteVisible = $derived(!!caption && noteStates.get(index) !== 'hidden');
  function toggleNote() {
    noteStates.set(index, noteVisible ? 'hidden' : 'pinned');
  }
  function inspectUnderNote(event: PointerEvent) {
    if (event.pointerType === 'mouse' && !noteStates.has(index)) noteStates.set(index, 'hidden');
  }
  let returnScreen = $state(-1);
  const liveIndex = $derived(screen ? live.screenPages[index] : index - screens.length);
  const screenshotIndex = $derived(screen ? index : returnScreen >= 0 && live.screenPages[returnScreen] === liveIndex
    ? returnScreen : live.screenPages.indexOf(liveIndex));
  const pairedScreen = $derived(screens[screenshotIndex]);
  function openLive() {
    if (!screen || liveIndex < 0) return;
    returnScreen = index;
    index = screens.length + liveIndex;
  }
  // Prewarm the screenshot's related page and retain visited documents while
  // this package is open. Screenshots keep their own indexes for annotations.
  let warmed = $state<number[]>([]);
  $effect(() => {
    if (touchSurface()) return;
    const related = live.screenPages[index];
    const next = page ? index - screens.length : Math.max(0, related ?? 0);
    if (live.pages[next] && !warmed.includes(next)) warmed = [...warmed, next];
  });
  const modeKey = 'lola.review.comparisonMode';
  let preferredMode = 'slider';
  try {
    const saved = localStorage.getItem(modeKey);
    if (saved && ['slider', 'split', 'before', 'after'].includes(saved)) preferredMode = saved;
  } catch { /* storage unavailable — keep the default */ }
  let mode = $state('slider');
  function chooseMode(value: string) {
    index = screenshotIndex;
    preferredMode = value; mode = value; tool = 'view';
    // Temporary switches for annotations or missing baselines aren't preferences.
    try { localStorage.setItem(modeKey, value); } catch { /* remember for this review */ }
  }
  const modes = [{id:'slider',label:'Slider',icon:'arrow-right-left'},{id:'split',label:'Side by side',icon:'columns-2'},{id:'before',label:'Before',icon:'panel-left'},{id:'after',label:'After',icon:'panel-right'}] as const;
  let tool = $state<'view' | MarkKind>('view');
  let stageW = $state(0), stageH = $state(0);
  const drawTools: { id: MarkKind; label: string; key: string; icon: 'message-square-text' | 'square' | 'circle' | 'arrow-up-right' | 'minus' | 'pencil'; title: string }[] = [
    { id: 'pin', label: 'Add pin', key: 'c', icon: 'message-square-text', title: 'Comment: click or drag' },
    { id: 'area', label: 'Mark area', key: 'r', icon: 'square', title: 'Rectangle' },
    { id: 'ellipse', label: 'Draw ellipse', key: 'e', icon: 'circle', title: 'Ellipse' },
    { id: 'arrow', label: 'Draw arrow', key: 'a', icon: 'arrow-up-right', title: 'Arrow' },
    { id: 'line', label: 'Draw line', key: 'l', icon: 'minus', title: 'Line' },
    { id: 'pen', label: 'Draw freehand', key: 'p', icon: 'pencil', title: 'Pen' },
  ];
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
  // The drawn shapes are in pixels of the stage, so arrow heads keep their shape at any zoom.
  $effect(() => {
    const node = imageStage;
    if (!node) return;
    const measure = () => { stageW = node.clientWidth; stageH = node.clientHeight; };
    const observer = new ResizeObserver(measure);
    observer.observe(node); measure();
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
      const at = anchor(mark!);
      const x = image.left - bounds.left + at.x * image.width;
      const y = image.top - bounds.top + at.y * image.height;
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
  function goToPage(next: number) {
    if (editing && !requestClose(false)) return;
    index = next;
    focusCanvas();
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
    if (selected?.savedFeedback) marks = marks.map((m,i) => i === active ? {...m,text:m.savedText ?? ''} : m);
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
      if (Array.isArray(saved)) marks = saved.filter(validMark).slice(0,30);
    } catch { /* local drafts are optional */ }
  });
  function save() { try { localStorage.setItem(key, JSON.stringify(marks)); } catch { message = 'Draft storage unavailable. Keep this review open until you send your feedback.'; } }
  function selectTool(value: typeof tool) { message = ''; tool = value; if (value !== 'view' && !single) mode = 'after'; }
  // Comment marks open the editor. A drawn shape is feedback on its own: it is sent as it is, and a double click adds words to it.
  function place(mark: ReviewMark, comment = true) {
    if (!canComment || marks.length >= 30) { message = 'This screen has reached its 30-annotation limit.'; return; }
    if (comment) { marks = [...marks, mark]; save(); openComment(marks.length - 1); return; }
    const id = crypto.randomUUID();
    const content = `Annotation ${id} (latest comment)\n${markLocation(mark)}\nMarked without a comment.`;
    if (!onComment(index, content, undefined)) { message = 'Couldn’t add the drawing to feedback.'; return; }
    marks = [...marks, {...mark, id, text:'', savedText:'', savedFeedback:content}]; save(); message = 'Drawing added to feedback.';
  }
  function point(e: PointerEvent) {
    const r = (e.currentTarget as HTMLElement).getBoundingClientRect();
    return {x:Math.max(0,Math.min(1,(e.clientX-r.left)/r.width)), y:Math.max(0,Math.min(1,(e.clientY-r.top)/r.height))};
  }
  // Shift locks a line or arrow to the nearer axis (90° steps), measured in stage pixels, not normalized units.
  function locked(e: PointerEvent, from: {x:number;y:number}, to: {x:number;y:number}) {
    if (!e.shiftKey || (tool !== 'line' && tool !== 'arrow')) return to;
    const r = (e.currentTarget as HTMLElement).getBoundingClientRect();
    return Math.abs(to.x-from.x)*r.width >= Math.abs(to.y-from.y)*r.height ? {x:to.x, y:from.y} : {x:from.x, y:to.y};
  }
  // A stroke or a two-point shape between its first and current point.
  function shape(kind: MarkKind, from: {x:number;y:number}, to: {x:number;y:number}, path: number[]): ReviewMark {
    if (kind === 'pen') {
      const xs = path.filter((_,i) => i % 2 === 0), ys = path.filter((_,i) => i % 2 === 1);
      const x = Math.min(...xs), y = Math.min(...ys);
      return {kind, x, y, w:Math.max(...xs)-x, h:Math.max(...ys)-y, pts:path, side, text:''};
    }
    if (kind === 'arrow' || kind === 'line') return {kind, x:from.x, y:from.y, x2:to.x, y2:to.y, w:Math.abs(from.x-to.x), h:Math.abs(from.y-to.y), side, text:''};
    return {kind, x:Math.min(from.x,to.x), y:Math.min(from.y,to.y), w:Math.abs(from.x-to.x), h:Math.abs(from.y-to.y), side, text:''};
  }
  let path: number[] = [];
  // Keyboard path: Enter on the armed surface drops a default mark in the middle.
  function placeDefault() {
    if (tool === 'view') return;
    if (tool === 'pen') { message = 'Freehand drawing needs a pointer. Use an arrow or a pin from the keyboard.'; return; }
    if (tool === 'arrow' || tool === 'line') place(shape(tool, {x:.4,y:.5}, {x:.6,y:.5}, []), false);
    else if (tool === 'pin') place({kind:'pin', x:.5,y:.5,w:0,h:0,side,text:''});
    else place({kind:tool, x:.5,y:.5,w:.2,h:.2,side,text:''}, false);
  }
  function down(e: PointerEvent) {
    if (tool === 'view' || !canComment || e.button !== 0) return;
    e.preventDefault(); start = point(e); path = [start.x, start.y];
    (e.currentTarget as HTMLElement).setPointerCapture(e.pointerId);
    dragging = shape(drawKind(), start, start, path);
  }
  // The comment tool draws a rectangle while dragging; a plain click stays a point.
  const drawKind = (): MarkKind => tool === 'pin' ? 'area' : tool as MarkKind;
  function move(e: PointerEvent) {
    if (!start || tool === 'view') return;
    const p = locked(e, start, point(e));
    if (tool === 'pen') path = [...path, p.x, p.y];
    dragging = shape(drawKind(), start, p, path);
  }
  function up(e: PointerEvent) {
    // The release can carry a position no move reported, so the shape ends where the pointer was lifted.
    if (start && tool !== 'view') {
      const p = locked(e, start, point(e));
      if (tool === 'pen') path = [...path, p.x, p.y];
      dragging = shape(drawKind(), start, p, path);
    }
    const mark = dragging, kind = tool;
    dragging = undefined; start = undefined;
    if (!mark || kind === 'view') return;
    const extent = Math.max(mark.w, mark.h);
    if (kind === 'pin') { place(extent >= .008 ? mark : {...mark, kind:'pin', w:0, h:0}); return; }
    // A click without a drag is no rectangle, arrow, ellipse or stroke.
    if (extent < .008) return;
    place(kind === 'pen' ? {...mark, pts:simplifyStroke(path)} : mark, false);
  }
  // Where the number badge sits: a stroke is numbered at its first point, every other mark at x/y.
  // A drawing sent without words carries no number; the number appears once there is a comment to point at.
  const numbered = (m: ReviewMark, i: number) => i === active || markKind(m) === 'pin' || !!(m.text.trim() || m.savedText);
  const number = (i: number) => marks.slice(0, i + 1).filter((m, n) => numbered(m, n)).length;
  const anchor = (m: ReviewMark) => m.kind === 'pen' && m.pts ? {x:m.pts[0], y:m.pts[1]} : {x:m.x, y:m.y};
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
  function armTool(value: MarkKind) {
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
    if (e.key.toLowerCase() === 'i' && caption && !editing) {
      consume(); toggleNote(); return;
    }
    if (e.key === 'ArrowUp' || e.key === 'ArrowDown') {
      consume();
      goToPage(Math.max(0, Math.min(total - 1, index + (e.key === 'ArrowUp' ? -1 : 1))));
      return;
    }
    if (/^[1-4]$/.test(e.key) && pairedScreen) {
      consume();
      if (editing && !requestClose(false)) return;
      const choice = modes[Number(e.key) - 1];
      if (pairedScreen.before || choice.id === 'after') chooseMode(choice.id);
      return;
    }
    if (!screen) return;
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
    if (!['c','r','e','a','l','p','v','b','n'].includes(key)) return;
    // Never bypass the unsaved-changes prompt by switching tools or images.
    consume();
    if (editing && !requestClose(false)) return;
    switch (key) {
      case 'c': case 'r': case 'e': case 'a': case 'l': case 'p': {
        const kind = drawTools.find(t => t.key === key)!.id;
        if (canComment && after && !error) armTool(kind);
        break;
      }
      case 'v': cancelTool(); break;
      case 'b': onToggleFeedback?.(); break;
      case 'n': if (canComment) nextComment(e.shiftKey ? -1 : 1); break;

    }
  }
</script>

{#snippet stroke(m: ReviewMark, i: number)}
  {@const kind = markKind(m)}
  {#if kind === 'line' || kind === 'arrow'}
    {@const x1 = m.x*stageW}{@const y1 = m.y*stageH}{@const x2 = (m.x2 ?? m.x)*stageW}{@const y2 = (m.y2 ?? m.y)*stageH}
    <g class="stroke" class:chosen={active === i} data-kind={kind} role="presentation" ondblclick={() => i >= 0 && openComment(i)}><line class="hit" {x1} {y1} {x2} {y2} /><line {x1} {y1} {x2} {y2} />{#if kind === 'arrow'}<polygon points={arrowHead(x1, y1, x2, y2, 14)} />{/if}</g>
  {:else if kind === 'pen' && m.pts}
    {@const pts = m.pts.map((v, n) => v * (n % 2 ? stageH : stageW)).join(' ')}
    <g class="stroke" class:chosen={active === i} data-kind="pen" role="presentation" ondblclick={() => i >= 0 && openComment(i)}><polyline class="hit" points={pts} /><polyline points={pts} /></g>
  {/if}
{/snippet}

{#if screen || page}
<!-- Route keys bubbling from canvas controls; this region isn't an extra tab stop. -->
<!-- svelte-ignore a11y_no_noninteractive_element_interactions -->
<section class="canvas" aria-label="Screenshot review canvas" bind:this={root} tabindex="-1" onkeydown={handleKey}
  onpointerdown={e => {
    const target = e.target as Element;
    if (!editing && target.closest('.viewport') && !target.closest('button,input,textarea,select,a,[tabindex]:not([tabindex="-1"])')) {
      e.preventDefault(); root?.focus({preventScroll:true});
    }
  }}>
  <header class="tools" aria-label="Review view controls">
    <div class="screen-name">
      <Icon name={screen ? 'image' : 'globe'} size={15} class="text-info" />
      <strong title={screen?.title ?? page?.title}>{screen?.title ?? page?.title}</strong>
      {#if caption}
        <Button icon size="xs" aria-label="Review instruction" aria-expanded={noteVisible} aria-controls={noteId} aria-keyshortcuts="i" title={`${noteVisible ? 'Hide' : 'Show'} review instruction · I`} onclick={toggleNote}><Icon name="info" size={14} /></Button>
      {/if}
    </div>
    <div class="comparison" role="group" aria-label="Comparison mode">
      {#each pairedScreen ? modes : [] as item,i}
        <Button size="xs" aria-label={item.label} title={`${item.label} · ${i+1}`} aria-keyshortcuts={String(i+1)} aria-pressed={!!screen && mode === item.id} selected={!!screen && mode === item.id} disabled={item.id !== 'after' && !pairedScreen?.before} onclick={() => chooseMode(item.id)}>
          <Icon name={item.icon} size={14} /><span class="mode-name">{item.label}</span>
        </Button>
      {/each}
      {#if liveIndex >= 0 && !touchSurface()}
        <Button size="xs" aria-label="Open live" title="Open the prepared live view" aria-pressed={!!page} selected={!!page} onclick={openLive}><Icon name="globe" size={14} /><span class="mode-name">Live</span></Button>
      {/if}
    </div>
    <div class="review-pager" role="group" aria-label="Review pages">
      <Button icon size="sm" variant="bare" class="page-arrow" aria-label="Previous" title="Previous page · ↑" aria-keyshortcuts="ArrowUp" disabled={index === 0} onclick={() => goToPage(index - 1)}><Icon name="chevron-up" size={18} /></Button>
      <span class="page-count" role="status" aria-label={`Page ${index + 1} of ${total}`}>
        <strong>{index + 1}</strong><span class="page-divider">/</span>{total}
      </span>
      <Button icon size="sm" variant="bare" class="page-arrow" aria-label="Next" title="Next page · ↓" aria-keyshortcuts="ArrowDown" disabled={index === total - 1} onclick={() => goToPage(index + 1)}><Icon name="chevron-down" size={18} /></Button>
    </div>
  </header>
  {#if caption}
    <aside class="review-note" id={noteId} aria-label="Review instruction" hidden={!noteVisible} onpointermove={inspectUnderNote}>
      {caption}
    </aside>
  {/if}
  {#each warmed as n (n)}
    {@const entry = live.pages[n]}
    {@const visible = index === screens.length + n}
    {#if entry}
      <div class="live-item" class:parked={!visible} inert={!visible} aria-hidden={!visible}>
        <SessionPreview {canComment} inline={{sessionId: reviewSessionId(pkg.session), url: entry.url, caption: entry.caption, prep: prepOf(entry)}} resumeKey={JSON.stringify([pkg.session, pkg.id, n])} active={visible} reviewAction={visible ? reviewAction : undefined} />
      </div>
    {/if}
  {/each}
  {#if screen}
  <div class="canvas-body">
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
                onclick={e => { if (e.detail === 0) placeDefault(); }}
                ondblclick={e => { if (tool === 'pin') return; const r = e.currentTarget.getBoundingClientRect(); const hit = marks.findLastIndex(m => m.side === side && markHit(m, e.clientX - r.left, e.clientY - r.top, r.width, r.height)); if (hit >= 0) openComment(hit); }}
                ></button>
            {/if}
            {#if single}
              <svg class="shapes" viewBox={`0 0 ${stageW || 1} ${stageH || 1}`} aria-hidden="true">
                {#each [...marks.map((m,i) => ({m,i})).filter(({m}) => m.side === side), ...(dragging ? [{m:dragging,i:-1}] : [])] as {m,i}}{@render stroke(m, i)}{/each}
              </svg>
              {#each marks as mark,i}{#if mark.side === side}{@const at = anchor(mark)}{@const kind = markKind(mark)}{#if numbered(mark, i)}<button class="pin" class:chosen={active === i} style:left={`${at.x*100}%`} style:top={`${at.y*100}%`} data-mark={i} aria-label={`Comment ${number(i)}`} aria-expanded={active === i && editing} onclick={e => { e.stopPropagation(); openComment(i); }}>{number(i)}</button>{/if}{#if kind === 'area' || kind === 'ellipse'}<button type="button" class="region region-hit" class:ellipse={kind === 'ellipse'} aria-label={`Open ${kind === 'area' ? 'area' : 'ellipse'} comment${numbered(mark, i) ? ` ${number(i)}` : ''}`} style:left={`${mark.x*100}%`} style:top={`${mark.y*100}%`} style:width={`${mark.w*100}%`} style:height={`${mark.h*100}%`} onclick={e => { if (e.detail === 0) openComment(i); }} ondblclick={() => openComment(i)}></button>{/if}{/if}{/each}
              {#if dragging && (dragging.kind === 'area' || dragging.kind === 'ellipse')}<div class="region" class:ellipse={dragging.kind === 'ellipse'} style:left={`${dragging.x*100}%`} style:top={`${dragging.y*100}%`} style:width={`${dragging.w*100}%`} style:height={`${dragging.h*100}%`}></div>{/if}
            {/if}
          </div>
        {/if}
      </div>
    {/if}
  </div>
  {#if editing && selected}
    <div class="annotation-editor" bind:this={editor} role="dialog" aria-label={`Comment ${number(active)} · ${selected.side}`} tabindex="-1" style:left={`${editorPosition.left}px`} style:top={`${editorPosition.top}px`}
      onkeydown={handleKey}>
      <div class="editor-heading">
        <label for="canvas-comment">Comment {number(active)}<span class="editor-side">{selected.side}</span></label>
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
    {#if canComment || reviewAction}
      <div class="review-dock draw-tools" role="group" aria-label="Annotation tools">
        {#if canComment}
          {#if marks.length}
            <Button size="xs" aria-label="Next comment" title="Next comment · N / Previous · Shift+N" aria-keyshortcuts="n Shift+n" onclick={() => nextComment()}>{marks.length} {marks.length === 1 ? 'comment' : 'comments'}</Button>
          {/if}
          {#each drawTools as t}
            <Button icon={t.id !== 'pin'} size="sm" selected={tool === t.id} disabled={!after} aria-label={t.label} title={`${t.title} · ${t.key.toUpperCase()}`} aria-keyshortcuts={t.key} onclick={() => armTool(t.id)}>
              <Icon name={t.icon} size={15} />{#if t.id === 'pin'}<span class="pin-label">Add pin</span>{/if}
            </Button>
          {/each}
        {/if}
        {@render reviewAction?.()}
      </div>
    {/if}
  <div class="view-overlay">
    {#if shortcutsOpen}<aside class="shortcut-help" aria-label="Screenshot shortcuts">
      <div class="shortcut-heading"><strong>Screenshot shortcuts</strong><Button icon size="xs" aria-label="Close shortcuts" onclick={() => { shortcutsOpen = false; focusCanvas(); }}><Icon name="x" size={14} /></Button></div>
      <dl>{#each shortcuts as [keys,action]}<div><dt>{action}</dt><dd><kbd>{keys}</kbd></dd></div>{/each}</dl>
      <p>Pinch or Ctrl+scroll to zoom around the pointer. Scroll to pan. Zoom percentages are relative to Fit.</p>
      <p>In a comment, {saveKeys} saves and ⌘Delete removes it. Plain Delete only removes comments outside text fields.</p>
    </aside>{/if}
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
  </div>
  </div>

  {/if}
</section>
{/if}

<style>
  .canvas { min-width:0; height:100%; display:grid; grid-template: auto minmax(0,1fr) / minmax(0,1fr); container-type:inline-size; isolation:isolate; }
  .canvas-body, .live-item { grid-area:2 / 1; min-height:0; min-width:0; }
  .live-item.parked { opacity:0; pointer-events:none; z-index:-1; }
  .canvas:focus { outline:none; }
  .tools { display:grid; grid-template-columns:minmax(0,1fr) auto minmax(0,1fr); align-items:center; gap:4px 12px; padding:6px 12px; background:var(--color-panel); border-bottom:1px solid var(--color-edge); }
  .screen-name { display:flex; align-items:center; gap:7px; min-width:0; }
  .screen-name strong { overflow:hidden; text-overflow:ellipsis; white-space:nowrap; font-size:14px; font-weight:600; color:var(--color-ink); }
  .review-note { grid-area:2 / 1; place-self:start; z-index:3; margin:12px; max-width:min(64ch,calc(100% - 24px)); max-height:45%; overflow:auto; overflow-wrap:anywhere; white-space:pre-line; padding:8px 12px; border:1px solid var(--color-edge); border-radius:8px; background:var(--color-panel); box-shadow:0 3px 12px #0003; color:var(--color-ink); font-size:13px; line-height:1.45; }
  .review-pager { justify-self:end; display:flex; align-items:center; gap:3px; padding:1px 2px; border-radius:8px; background:var(--color-accent-fill); color:var(--color-accent-ink); }
  .page-count { display:flex; align-items:baseline; justify-content:center; gap:4px; min-width:32px; font-size:13px; line-height:1; font-variant-numeric:tabular-nums; }
  .page-count strong { font-weight:700; }
  .page-divider { opacity:.65; }
  .review-pager :global(.page-arrow:disabled) { opacity:.52; }
  .review-pager :global(.page-arrow:enabled:hover) { background:var(--color-accent-fill-hover); }
  .review-pager :global(.page-arrow:focus-visible) { outline:2px solid var(--color-accent); outline-offset:-2px; }
  .comparison { justify-self:center; display:flex; align-items:center; padding:2px; border:1px solid var(--color-edge); border-radius:7px; background:var(--color-canvas); }
  .canvas :global(.review-dock) { position:absolute; z-index:4; bottom:12px; left:50%; transform:translateX(-50%); display:flex; align-items:center; gap:2px; width:max-content; max-width:calc(100% - 16px); flex-wrap:wrap; justify-content:center; padding:3px; border:1px solid var(--color-edge); border-radius:9px; background:var(--color-panel); box-shadow:0 4px 16px #0005; }
  @container (max-width: 900px) {
    .mode-name, .pin-label { display:none; }
  }
  @container (max-width: 700px) {
    .canvas .draw-tools { bottom:54px; }
  }
  @container (max-width: 620px) {
    .tools { grid-template-columns:minmax(0,1fr) auto; gap:4px; padding:5px 8px; }
    .comparison { grid-column:1 / -1; grid-row:2; }
    .review-pager { grid-column:2; grid-row:1; }
  }
  .view-overlay { position:absolute; z-index:6; right:12px; bottom:12px; display:flex; align-items:center; gap:4px; padding:3px; border:1px solid var(--color-edge); border-radius:10px; background:color-mix(in srgb,var(--color-panel) 88%,transparent); backdrop-filter:blur(8px); box-shadow:0 4px 16px #0005; }
  .view-overlay :global(.lola-select) { width:72px; }
  .view-overlay :global(.lola-field) { border-color:transparent; background:transparent; box-shadow:none; }
  .view-overlay :global(.lola-field:hover) { background:color-mix(in srgb,var(--color-ink) 8%,transparent); }
  .shortcut-help { position:absolute; z-index:6; bottom:calc(100% + 8px); right:0; width:min(400px,calc(100% - 16px)); max-height:70vh; overflow:auto; padding:12px; border:1px solid var(--color-edge); border-radius:8px; background:var(--color-panel); box-shadow:0 8px 28px #0005; font-size:12px; }
  .shortcut-heading { display:flex; align-items:center; justify-content:space-between; margin-bottom:8px; }
  .shortcut-help dl { display:grid; gap:6px; }
  .shortcut-help dl div { display:flex; flex-direction:row-reverse; justify-content:flex-end; align-items:baseline; gap:12px; }
  .shortcut-help dt { color:var(--color-ink); font-size:13px; }
  /* Same key chip as the app-wide Keyboard shortcuts sheet. */
  .shortcut-help kbd { display:inline-block; min-width:5.5rem; flex-shrink:0; padding:1px 8px; border:1px solid var(--color-edge); border-radius:4px; background:var(--color-canvas); color:var(--color-accent-ink); font-family:var(--font-mono); font-size:12px; text-align:center; white-space:nowrap; }
  .shortcut-help p { margin-top:10px; color:var(--color-ink-2); font-size:11px; }
  .tool-group { display:flex; align-items:center; gap:3px; }
  .canvas-body { display:grid; grid-template-columns:minmax(0,1fr); min-height:0; }
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
  .shapes { position:absolute; inset:0; width:100%; height:100%; z-index:1; pointer-events:none; overflow:visible; }
  .stroke { stroke:var(--color-accent); stroke-width:3; stroke-linecap:round; stroke-linejoin:round; fill:none; }
  .stroke polygon { fill:var(--color-accent); stroke-width:1; }
  .stroke .hit { stroke:transparent; stroke-width:16; pointer-events:stroke; cursor:pointer; }
  .stroke.chosen { stroke:var(--color-ink); filter:drop-shadow(0 0 3px var(--color-accent)); }
  .stroke.chosen polygon { fill:var(--color-ink); }
  .region.ellipse { border-radius:50%; }
  .region { position:absolute; border:2px solid var(--color-accent); background:color-mix(in srgb,var(--color-accent) 12%,transparent); pointer-events:none; }
  .region-hit { pointer-events:auto; cursor:pointer; }
  .canvas-notice { position:absolute; bottom:12px; left:12px; max-width:calc(100% - 24px); padding:8px 12px; font-size:12px; color:var(--color-ink-2); border:1px solid var(--color-edge); border-radius:6px; background:var(--color-panel); pointer-events:none; }
  .annotation-surface:focus-visible,.comparison-drag:focus-visible { outline:2px solid var(--color-accent); outline-offset:2px; }
</style>
