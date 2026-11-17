<script lang="ts">
  import { onDestroy, onMount } from "svelte";
  import { Terminal } from "@xterm/xterm";
  import { WebLinksAddon } from "@xterm/addon-web-links";
  import { TERM_FONT, appearance, termFontLoaded, termFontReady } from "$lib/theme-runtime.svelte";
  import type { PaneEvent, PaneSubscription } from "@mobile/wire";
  import { connection } from "@mobile/lib/connection.svelte";
  import { DEFAULT_MODES, type TerminalModes } from "@mobile/lib/keybytes";
  import { openExternal } from "@mobile/lib/openurl";
  import { SHIFT_ENTER, ScrollQueue, TouchGestures, isShiftEnter } from "@mobile/lib/terminalinput";
  import { geometryChanged, resyncToBytes } from "@mobile/lib/resync";
  import { loadFontSize, saveFontSize } from "@mobile/lib/prefs";
  import { INSET_PX, terminalInset } from "@mobile/lib/terminalgeometry.svelte";
  import {
    capacityCells,
    clampPan,
    cursorVisible,
    followCursor,
    firstVisibleColumn,
    fitWidth,
    isPanning,
    panBy,
    partialRowHeight,
    visibleColumns,
    visibleRows,
    wheelPixels,
    type PanBox,
  } from "@mobile/lib/viewport";

  // A live pane on a phone. Not the desktop's LiveTerminal: this one never fits
  // or resizes the grid (the developer's tmux window is rendered whole and
  // panned), its font size is a pinch control, and it is fed by resync and PTY
  // frames off the remote transport. LiveTerminal's invariants that still apply
  // are marked CARRIED.

  let {
    /** The tmux pane name, already resolved (see paneNameFor). */
    pane,
    /** Applies the accessory bar's latched modifier to soft-keyboard input. */
    transform = (d: string) => d,
    /** Called once per write that reaches the PTY: a latched modifier is consumed only by a sent byte. */
    onsent,
    onexit,
    onerror,
    onstate,
    /** No typing: this phone does not hold the shell tier. The pane still shows. */
    readonly = false,
  }: {
    pane: string;
    transform?: (data: string) => string;
    onsent?: () => void;
    onexit?: () => void;
    /** The current subscription error; "" (sent on every attach) clears it. */
    onerror?: (message: string) => void;
    /** Pushed on every change the screen draws (modes, grid, font): the accessory bar needs DECCKM at once. */
    onstate?: (s: TerminalState) => void;
    readonly?: boolean;
  } = $props();

  export interface TerminalState {
    modes: TerminalModes;
    cols: number;
    rows: number;
    /** Columns actually on screen. Equal to `cols` when nothing is clipped. */
    shown: number;
    /** Rows actually on screen, the mirror of `shown`. */
    shownRows: number;
    /** What this phone could show if the grid were sized to it, unclamped: the size to pin to (panepin.ts). */
    capCols: number;
    capRows: number;
    /** The leftmost visible column, 1-based. */
    first: number;
    panning: boolean;
    font: number;
    /** Whether a smaller size would show more of the grid. */
    canFit: boolean;
    /** Whether a fit-width zoom is currently in effect. */
    fitActive: boolean;
    /** The size a fit would land on, for the control's label. */
    fitSize: number;
  }

  /** Live DECCKM / bracketed-paste, so the accessory bar encodes correctly. */
  let modes = $state<TerminalModes>(DEFAULT_MODES);
  /** The grid the daemon is sending. */
  let cols = $state(0);
  let rows = $state(0);
  // Seeded before boot() builds the Terminal, so the first frame has the stored size.
  let fontSize = $state(loadFontSize());
  /** The reading size while a fit-width zoom is in effect, else null. It is what is persisted. */
  let fitFrom = $state<number | null>(null);
  let exited = $state(false);
  let attached = $state(false);
  let box = $state<PanBox>({ contentWidth: 0, contentHeight: 0, viewWidth: 0, viewHeight: 0 });
  let pan = $state({ x: 0, y: 0 });
  /** The text's inline padding in px: 12, or 8 when 12 would drop a column of the grid (terminalInset). */
  let inset = $state(INSET_PX);
  /** Padding above the first row. The window is the content box, so `measure` reads rows from it. */
  const TOP_PAD_PX = 12;
  // Whether the cursor was visible before the write being applied (the follow
  // rule's input). Plain `let`: written in the write path, never rendered.
  let wasFollowing = true;

  const panning = $derived(isPanning(box));
  const shown = $derived(visibleColumns(box, cols));
  const shownRows = $derived(visibleRows(box, rows));
  const capacity = $derived(capacityCells(box, { cols, rows }));
  const first = $derived(firstVisibleColumn(box, cols, pan.x));
  /** Height of the sliced bottom row, masked rather than drawn. */
  const cutRow = $derived(
    partialRowHeight(pan.y, box.viewHeight, rows > 0 ? box.contentHeight / rows : 0),
  );
  /** The size a fit-width zoom would land on, recomputed as the box moves. */
  const fit = $derived(fitWidth(box, fontSize));
  /** Whether a smaller size would show more of the grid (false at the 8-point floor). */
  const canFit = $derived(fitFrom === null && fit.size < fontSize);

  // The only channel out: no getters, so the screen never reads a stale value.
  $effect(() => {
    onstate?.({
      modes,
      cols,
      rows,
      shown,
      shownRows,
      capCols: capacity.cols,
      capRows: capacity.rows,
      first,
      panning,
      font: fontSize,
      canFit,
      fitActive: fitFrom !== null,
      fitSize: fit.size,
    });
  });

  export function setFont(size: number): void {
    // A chosen size ends a fit.
    fitFrom = null;
    fontSize = size;
  }
  export function isAttached(): boolean {
    return attached && !exited;
  }
  /** Give the terminal the keyboard. Must be called inside a user gesture on
   *  iOS, or the soft keyboard does not appear. */
  export function focus(): void {
    term?.focus();
  }

  /**
   * Send raw bytes to the pane: the accessory bar's exit and xterm's typing.
   * CARRIED: a human's PTY write bypasses the idle gate, which guards lola's own
   * automation, not a person (Ctrl-C mid-turn must work).
   */
  export function send(bytes: string): void {
    if (bytes === "" || !sub || exited || readonly) return;
    onsent?.();
    void sub.write(bytes).catch(() => {});
  }

  let frame: HTMLDivElement;
  /** The clipping window: the frame less `inset` each side, so text never meets the screen edge. */
  let view: HTMLDivElement;
  let inner: HTMLDivElement;
  let host: HTMLDivElement;
  let term: Terminal | undefined;
  let sub: PaneSubscription | undefined;
  let offEvent: (() => void) | undefined;
  let offError: (() => void) | undefined;
  let ro: ResizeObserver | undefined;
  let disposed = false;

  // CARRIED: scroll is the daemon's RPC, never synthesized keys.
  const scroll = new ScrollQueue((lines) => {
    if (sub && !disposed) void sub.scroll(lines).catch(() => {});
  });

  /**
   * The xterm theme on the page's own ground. The terminal is continuous with the
   * screen around it in every flavor, so its background is the live
   * `--color-canvas` (mobiletokens writes it), not the flavor's terminal colour.
   */
  function paintedTheme() {
    let canvas = "";
    try {
      canvas = getComputedStyle(document.documentElement).getPropertyValue("--color-canvas").trim();
    } catch {
      /* no document: the flavor's own background stands */
    }
    return canvas ? { ...appearance.term, background: canvas } : appearance.term;
  }

  /** Swallows a pinch's per-frame sizes; onDestroy flushes the last one. */
  const FONT_SAVE_DEBOUNCE_MS = 400;
  let saveTimer: ReturnType<typeof setTimeout> | undefined;

  onMount(() => {
    void boot();
  });

  async function boot() {
    // Bounded: a late font is re-measured below.
    const ready = await termFontReady(600);
    if (disposed) return;

    term = new Terminal({
      // History is the daemon's (scroll RPC); a local one would fill with resync repaints.
      scrollback: 0,
      fontFamily: TERM_FONT.fontFamily,
      fontWeight: TERM_FONT.fontWeight,
      fontWeightBold: TERM_FONT.fontWeightBold,
      allowTransparency: false,
      fontSize,
      // TERM_FONT's spacing is matched to 13pt; at a variable 8–16pt no tracking keeps columns.
      lineHeight: 1.2,
      letterSpacing: 0,
      cursorBlink: true,
      // DOM renderer: WKWebView evicts GL contexts across backgrounding.
      theme: paintedTheme(),
    });
    term.open(host);

    // Text URLs and OSC 8 links open on this phone, http(s) only (openurl.ts).
    term.loadAddon(new WebLinksAddon((_e, uri) => void openExternal(uri)));
    term.options.linkHandler = { activate: (_e, uri) => void openExternal(uri) };

    term.attachCustomKeyEventHandler((e) => {
      // CARRIED: a hardware Shift+Enter is ESC CR (a line break), and the bare CR is swallowed.
      if (isShiftEnter(e)) {
        e.preventDefault();
        send(SHIFT_ENTER);
        return false;
      }
      return true;
    });

    // CARRIED: false stops xterm turning a wheel into cursor keys on an alternate screen.
    term.attachCustomWheelEventHandler((ev: WheelEvent) => {
      const cell = cellHeight();
      scroll.add(wheelPixels(ev.deltaY, ev.deltaMode, cell, rows), cell);
      return false;
    });

    term.onData((d) => send(transform(d)));

    ro = new ResizeObserver(() => measure());
    ro.observe(frame);
    measure();

    await attach();

    if (!ready) {
      // xterm re-measures only on a family change: swap and restore it.
      void termFontLoaded().then((ok) => {
        if (!ok || disposed || !term) return;
        term.options.fontFamily = "monospace";
        term.options.fontFamily = TERM_FONT.fontFamily;
        measure();
      });
    }
  }

  async function attach() {
    try {
      // cols/rows are advisory; the grid is the tmux window's (panepin.ts sizes it).
      sub = await connection.subscribe(pane, { cols: 80, rows: 24 });
    } catch (e) {
      onerror?.(e instanceof Error ? e.message : String(e));
      return;
    }
    if (disposed) {
      void sub.close().catch(() => {});
      return;
    }
    attached = true;
    // An attach clears the last error.
    onerror?.("");
    // Paint first, then listen: frames after the acknowledging resync are held
    // until a listener exists, and must land on top of it.
    if (sub.screen) paint(sub.screen);
    offEvent = sub.onEvent(handle);
    offError = sub.onError((e) => onerror?.(e.message));
  }

  function handle(ev: PaneEvent) {
    if (!term) return;
    if (ev.kind === "resync") {
      paint(ev.screen);
      return;
    }
    if (ev.kind === "exit") {
      paint(ev.screen);
      exited = true;
      onexit?.();
      return;
    }
    if (ev.gap) {
      // Dropped PTY output cannot be replayed; a new subscription sends a fresh screen.
      void resubscribe();
      return;
    }
    noteFollowing();
    term.write(ev.data, () => followCursorNow());
    syncModes();
  }

  async function resubscribe() {
    offEvent?.();
    offError?.();
    offEvent = offError = undefined;
    const old = sub;
    sub = undefined;
    attached = false;
    void old?.close().catch(() => {});
    if (!disposed) await attach();
  }

  function paint(screen: Parameters<typeof resyncToBytes>[0]) {
    if (!term) return;
    if (geometryChanged({ cols: term.cols, rows: term.rows }, screen)) {
      // The only grid change: the tmux window's size, announced by a resync.
      term.resize(screen.cols, screen.rows);
    }
    cols = term.cols;
    rows = term.rows;
    // reset() first, so a repaint never pushes the old screen into scrollback.
    term.reset();
    term.write(resyncToBytes(screen), () => {
      syncModes();
      measure();
      // A fresh screen aligns to the cursor wherever the pan was.
      followCursorNow(true);
    });
  }

  function syncModes() {
    if (!term) return;
    const m = term.modes;
    if (
      m.applicationCursorKeysMode !== modes.applicationCursorKeysMode ||
      m.bracketedPasteMode !== modes.bracketedPasteMode
    ) {
      // xterm's parser learns the modes from output; this copies them for the bar.
      modes = {
        applicationCursorKeysMode: m.applicationCursorKeysMode,
        bracketedPasteMode: m.bracketedPasteMode,
      };
    }
  }

  function cellHeight(): number {
    return rows > 0 && box.contentHeight > 0 ? box.contentHeight / rows : 17;
  }

  /**
   * Zoom out until the whole grid fits across the screen, and back. A view zoom
   * only: it sends nothing and never resizes the tmux window (panepin.ts does
   * that). At a grid wider than the 8-point floor allows, the popover still
   * reports the visible range.
   */
  export function toggleFit() {
    // Refocus only if focused: a tap with the keyboard down must not raise it.
    const refocus = !!host && host.contains(document.activeElement);
    if (fitFrom !== null) {
      fontSize = fitFrom;
      fitFrom = null;
    } else {
      const target = fitWidth(box, fontSize);
      if (target.size >= fontSize) return;
      fitFrom = fontSize;
      fontSize = target.size;
    }
    if (refocus) term?.focus();
  }

  function measure() {
    if (!frame || !inner || !view) return;
    // Measured on the padded window, not the frame: cols, the pan limits and the
    // pinned size all follow what is actually drawn in. The width is the frame
    // less the inset (never wider), computed here rather than read back from
    // `view`, whose own width only follows the inset a render later.
    const cell = cols > 0 ? inner.offsetWidth / cols : 0;
    const next = terminalInset(frame.clientWidth, cell, cols);
    if (next !== inset) inset = next;
    box = {
      contentWidth: inner.offsetWidth,
      contentHeight: inner.offsetHeight,
      viewWidth: Math.max(0, frame.clientWidth - 2 * next),
      viewHeight: view.clientHeight,
    };
    pan = clampPan(pan, box);
  }

  /**
   * Focusing xterm's hidden textarea scrolls any overflow container around it,
   * `hidden` ones included, which slid the whole pane left of the page. Nothing
   * here is ever scrolled but by the `pan` transform, so put it back.
   */
  function unscroll(e: Event) {
    const el = e.currentTarget as HTMLElement;
    if (el.scrollLeft !== 0) el.scrollLeft = 0;
    if (el.scrollTop !== 0) el.scrollTop = 0;
  }

  /** The cursor in grid cells, or null with no rendered buffer (no follow then). */
  function cursorAt(): { x: number; y: number } | null {
    const b = term?.buffer?.active;
    if (!b) return null;
    return { x: b.cursorX, y: b.cursorY };
  }

  /**
   * Keep the line being typed into on screen. `force` aligns to the cursor;
   * otherwise the follow holds only while the cursor was visible before the
   * write, so reading further up is left alone.
   */
  function followCursorNow(force = false) {
    const at = cursorAt();
    if (!at) return;
    if (!force && !wasFollowing) return;
    pan = followCursor(pan, box, { cols, rows }, at);
  }

  /** Whether the cursor was on screen before the write now being applied. */
  function noteFollowing() {
    const at = cursorAt();
    wasFollowing = at ? cursorVisible(pan, box, { cols, rows }, at) : true;
  }

  // Remember the reading size on a trailing debounce; onDestroy flushes it.
  $effect(() => {
    const size = fitFrom ?? fontSize;
    clearTimeout(saveTimer);
    saveTimer = setTimeout(() => saveFontSize(size), FONT_SAVE_DEBOUNCE_MS);
  });

  // A font change keeps the grid and moves the pan limits: re-measure.
  $effect(() => {
    const size = fontSize;
    if (term && term.options.fontSize !== size) {
      term.options.fontSize = size;
      requestAnimationFrame(measure);
    }
  });

  // A pane subscription does not survive its connection: re-subscribe on the
  // false -> true edge of `ready` while attached. Plain `let` edge detector, so
  // the effect does not invalidate itself.
  let wasReady = connection.ready;
  $effect(() => {
    const ready = connection.ready;
    const back = ready && !wasReady;
    wasReady = ready;
    if (back && attached && !exited && !disposed) void resubscribe();
  });

  // Re-theme a live terminal; assigning options.theme is complete.
  // The canvas token is written by the same theme change, in an order this
  // effect cannot see, so it paints now and once more a frame later.
  $effect(() => {
    void appearance.term;
    if (!term) return;
    term.options.theme = paintedTheme();
    const raf = requestAnimationFrame(() => {
      if (term && !disposed) term.options.theme = paintedTheme();
    });
    return () => cancelAnimationFrame(raf);
  });

  // --- touch ----------------------------------------------------------------
  //
  //   one finger, sideways   pan horizontally (no network)
  //   one finger, vertical   scroll, an RPC into the daemon's history
  //   two fingers            pan freely, and pinch the font (8 to 16 points)

  const gestures = new TouchGestures();

  function onTouchStart(e: TouchEvent) {
    gestures.begin(e.touches, fontSize);
  }

  function onTouchMove(e: TouchEvent) {
    const fx = gestures.move(e.touches);
    if (!fx.claim) return;
    e.preventDefault();
    if (fx.font !== undefined && fx.font !== fontSize) {
      // A pinch is a chosen size: it ends a fit.
      fitFrom = null;
      fontSize = fx.font;
    }
    if (fx.pan) pan = panBy(pan, fx.pan.dx, fx.pan.dy, box);
    if (fx.scrollPx !== undefined) scroll.add(fx.scrollPx, cellHeight());
  }

  function onTouchEnd(e: TouchEvent) {
    // A still tap focuses inside the gesture: iOS raises the keyboard only then.
    if (gestures.end(e.touches.length) && !readonly) term?.focus();
  }

  onDestroy(() => {
    disposed = true;
    offEvent?.();
    offError?.();
    ro?.disconnect();
    scroll.dispose();
    clearTimeout(saveTimer);
    saveFontSize(fitFrom ?? fontSize);
    void sub?.close().catch(() => {});
    term?.dispose();
  });
</script>

<!-- touch-none: every gesture here is ours. role="application": keys go to the
     terminal, not to screen-reader navigation. -->
<div
  bind:this={frame}
  role="application"
  aria-label="Terminal for {pane}"
  class="term-pane relative h-full w-full max-w-full min-w-0 touch-none overflow-hidden overflow-x-clip border-t-[0.5px] border-edge-soft bg-canvas"
  onscroll={unscroll}
  ontouchstart={onTouchStart}
  ontouchmove={onTouchMove}
  ontouchend={onTouchEnd}
  ontouchcancel={onTouchEnd}
>
  <div
    bind:this={view}
    class="absolute bottom-0 overflow-hidden overflow-x-clip"
    style="top: {TOP_PAD_PX}px; left: {inset}px; right: {inset}px"
    onscroll={unscroll}
    data-term-view
  >
    <div
      bind:this={inner}
      class="absolute top-0 left-0 will-change-transform"
      style="transform: translate3d({-pan.x}px, {-pan.y}px, 0)"
    >
      <div bind:this={host}></div>
    </div>
  </div>

  <!-- The sliced bottom row is masked in the pane's ground: an absent line, not a cut glyph. -->
  {#if cutRow > 0}
    <div
      class="pointer-events-none absolute right-0 bottom-0 left-0 bg-canvas"
      style="height: {cutRow}px"
      aria-hidden="true"
    ></div>
  {/if}
</div>

<style>
  /* scrollback is 0 and scrolling is the daemon's RPC, so xterm's own scrollbar
     track is only ever a stray bar under the text. */
  .term-pane :global(.xterm-viewport) {
    scrollbar-width: none;
    overflow: hidden !important;
  }
  .term-pane :global(.xterm-viewport::-webkit-scrollbar) {
    display: none;
  }
</style>
