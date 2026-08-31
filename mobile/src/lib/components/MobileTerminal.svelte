<script lang="ts">
  import { onDestroy, onMount } from "svelte";
  import { Terminal } from "@xterm/xterm";
  import { WebLinksAddon } from "@xterm/addon-web-links";
  import { TERM_FONT, appearance, termFontLoaded, termFontReady } from "$lib/theme-runtime.svelte";
  import type { PaneEvent, PaneSubscription } from "@mobile/wire";
  import { connection } from "@mobile/lib/connection.svelte";
  import { DEFAULT_MODES, type TerminalModes } from "@mobile/lib/keybytes";
  import { openExternal } from "@mobile/lib/openurl";
  import { geometryChanged, resyncToBytes } from "@mobile/lib/resync";
  import { loadFontSize, saveFontSize } from "@mobile/lib/prefs";
  import {
    NO_SCROLL,
    accumulateScroll,
    clampPan,
    fitWidth,
    isPanning,
    lockAxis,
    panBy,
    pinchFont,
    takeScroll,
    touchDistance,
    touchMidpoint,
    visibleColumns,
    wheelPixels,
    type Axis,
    type PanBox,
    type ScrollAccum,
  } from "@mobile/lib/viewport";

  // A live agent pane on a phone.
  //
  // WHY THIS IS NOT `$lib/components/LiveTerminal.svelte`. That component is
  // reused everywhere else in this app's lineage and it is the right component
  // on a desktop, but three of its load-bearing decisions are exactly inverted
  // here, and none of them can be turned off with a prop:
  //
  //   * It FITS. FitAddon sizes the terminal to its container, which reflows the
  //     grid to the viewport. The phone must NOT reflow: `attach-session -f
  //     ignore-size` means it cannot shrink the developer's tmux window, so the
  //     grid is whatever the Mac's is — commonly 200 columns — and the phone
  //     renders all of it and pans. Fitting here would render a 55-column
  //     terminal and quietly discard three quarters of the agent's output.
  //   * It has NO fontSize prop, deliberately, because TERM_FONT's size,
  //     lineHeight and letterSpacing are a matched set that reproduces one exact
  //     cell. Pinch-to-zoom is the whole answer to 200 columns on 390 points, so
  //     a variable size is mandatory here.
  //   * It is fed by `TermService` over the Wails bridge. This one is fed by a
  //     resync frame and then PTY frames off the remote transport.
  //
  // So it is a separate component, and every invariant CLAUDE.md records for
  // LiveTerminal is carried across DELIBERATELY rather than inherited. They are
  // marked "CARRIED" below. Nothing under desktop/ was modified.

  let {
    /** The tmux pane name, already resolved (see paneNameFor). */
    pane,
    /**
     * Applied to everything the SOFT keyboard produces, so a latched modifier on
     * the accessory bar reaches the letters — which only exist on the system
     * keyboard. Identity when nothing is latched.
     */
    transform = (d: string) => d,
    /**
     * Called once for every write that actually reaches the PTY. It exists so
     * the accessory bar's latched modifier is consumed on the same path the byte
     * took: `transform` only PEEKS at the latch, because a latch consumed by the
     * attempt rather than by the byte would clear ctrl for a keystroke that never
     * left, with nothing on screen explaining why.
     */
    onsent,
    onexit,
    onerror,
    onstate,
  }: {
    pane: string;
    transform?: (data: string) => string;
    onsent?: () => void;
    onexit?: () => void;
    /**
     * The CURRENT subscription error, latched by the screen into its banner.
     * An empty string means there is none — sent on every successful attach,
     * so a reconnect takes its own banner down. See the call site.
     */
    onerror?: (message: string) => void;
    /**
     * Pushed whenever anything the surrounding screen draws changes: the live
     * terminal modes (the accessory bar encodes against them), the grid and how
     * much of it is visible, and the font size.
     *
     * PUSHED rather than polled, and that is not a preference. The screen's font
     * buttons need a real size before the first keystroke, and its accessory bar
     * needs DECCKM the moment an application sets it — both of which happen
     * inside this component with no event the parent could hook. A poll would
     * make the arrows briefly wrong after a mode change, which is precisely the
     * bug class keybytes.ts exists to prevent.
     */
    onstate?: (s: TerminalState) => void;
  } = $props();

  export interface TerminalState {
    modes: TerminalModes;
    cols: number;
    rows: number;
    /** Columns actually on screen. Equal to `cols` when nothing is clipped. */
    shown: number;
    panning: boolean;
    font: number;
  }

  // --- reactive surface the screen around this component reads ---------------

  /** Live DECCKM / bracketed-paste, so the accessory bar encodes correctly. */
  let modes = $state<TerminalModes>(DEFAULT_MODES);
  /** The grid the daemon is sending, for the truncation chip. */
  let cols = $state(0);
  let rows = $state(0);
  // SEEDED from storage, not assigned in onMount. boot() reads fontSize
  // synchronously when it constructs the Terminal, so a restore one tick later
  // would paint a frame at the default size and then jump — and xterm would
  // re-measure its cell for nothing. loadFontSize clamps, so a value written by
  // a build with a different range cannot render a terminal this build has no
  // control able to reach.
  let fontSize = $state(loadFontSize());
  /**
   * The reading size to come back to, while a fit-width zoom is in effect, or
   * null when the current size IS the reading size.
   *
   * It is also what gets PERSISTED: a fit is a transient look at the whole grid,
   * often at the 8-point floor, and remembering that as the reading size would
   * mean every return to a terminal started out unreadable.
   */
  let fitFrom = $state<number | null>(null);
  let exited = $state(false);
  let attached = $state(false);
  let box = $state<PanBox>({ contentWidth: 0, contentHeight: 0, viewWidth: 0, viewHeight: 0 });
  let pan = $state({ x: 0, y: 0 });

  const panning = $derived(isPanning(box));
  const shown = $derived(visibleColumns(box, cols));
  /** The size a fit-width zoom would land on, recomputed as the box moves. */
  const fit = $derived(fitWidth(box, fontSize));
  /** Whether there is a smaller size that would show more of the grid. False at
   *  the 8-point floor, where the chip has nothing to offer and says so. */
  const canFit = $derived(fitFrom === null && fit.size < fontSize);

  // One effect, reading everything the parent draws. It is the only channel
  // out: no getter is exported for these, so the screen cannot read a stale
  // value by asking at the wrong moment.
  $effect(() => {
    onstate?.({ modes, cols, rows, shown, panning, font: fontSize });
  });

  export function setFont(size: number): void {
    // Same reasoning as the pinch: the A-/A+ buttons are the user choosing a
    // size, so they end a fit instead of being reverted by it.
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
   * Send raw bytes to the pane. The accessory bar's one exit, and the only way
   * anything other than xterm's own typing reaches the PTY.
   *
   * CARRIED: the write goes straight to the PTY master and deliberately bypasses
   * lola's AtPrompt idle gate. That gate exists to stop lola's OWN automation —
   * reactions, review hand-offs, resolveConflict — typing into a mid-turn agent
   * and corrupting it. A human at a keyboard is the case it was never about, and
   * a phone that could not send Ctrl-C mid-turn would not be worth carrying.
   * This is a stated exception, not an oversight. The screen above this one
   * carried a client-side "send anyway" confirmation once and no longer does;
   * that was UI friction in front of the same human, never a reinstatement of
   * the gate, and nothing about the gate changed when it went.
   */
  export function send(bytes: string): void {
    if (bytes === "" || !sub || exited) return;
    onsent?.();
    void sub.write(bytes).catch(() => {});
  }

  // --- internals -------------------------------------------------------------

  let frame: HTMLDivElement;
  let inner: HTMLDivElement;
  let host: HTMLDivElement;
  let term: Terminal | undefined;
  let sub: PaneSubscription | undefined;
  let offEvent: (() => void) | undefined;
  let offError: (() => void) | undefined;
  let ro: ResizeObserver | undefined;
  let disposed = false;

  let scroll: ScrollAccum = NO_SCROLL;
  let scrollTimer: ReturnType<typeof setTimeout> | undefined;

  /** Long enough to swallow a pinch, short enough that a tap on ‹ back beats
   *  nothing to the flush in onDestroy. */
  const FONT_SAVE_DEBOUNCE_MS = 400;
  let saveTimer: ReturnType<typeof setTimeout> | undefined;

  onMount(() => {
    void boot();
  });

  async function boot() {
    // Bounded, and shorter than the desktop's 2s: the desktop is waiting to get
    // one exact cell right, while here a person has just tapped a row and is
    // watching an empty screen. A late font is recovered below.
    const ready = await termFontReady(600);
    if (disposed) return;

    term = new Terminal({
      // xterm's OWN scrollback stays empty and that is correct. On an agent pane
      // there is nothing to put in it — tmux keeps no scrollback for an
      // alternate-screen program — and on a shell pane the history that matters
      // is the daemon's, reached by the scroll RPC. A local scrollback would
      // additionally fill with duplicate copies of the screen every time a
      // resync repainted it.
      scrollback: 0,
      fontFamily: TERM_FONT.fontFamily,
      fontWeight: TERM_FONT.fontWeight,
      fontWeightBold: TERM_FONT.fontWeightBold,
      allowTransparency: false,
      fontSize,
      // TERM_FONT's own lineHeight/letterSpacing are NOT spread here, and that
      // is deliberate. They are a matched set with fontSize 13 that reproduces
      // Ghostty's 8x17 cell on a desktop; this terminal's size is a user
      // control between 8 and 16, so the set cannot hold. letterSpacing 0 is
      // the mobile choice specifically — at 8pt a 1px tracking is a 12%
      // widening, which costs columns exactly where columns are scarcest.
      lineHeight: 1.2,
      letterSpacing: 0,
      cursorBlink: true,
      // WebGL is OFF by default on iOS. WKWebView evicts GL contexts far more
      // eagerly than a desktop WebView, especially across backgrounding, and the
      // DOM renderer is comfortably fast enough for a 55-column window. Turning
      // it on is a measurement, not a default.
      theme: appearance.term,
    });
    term.open(host);

    // CARRIED: xterm ships NO link handling. Two kinds exist — plain text URLs
    // and OSC 8 hyperlinks — and both go to one opener.
    //
    // NOT CARRIED: the exec host. The desktop routes a click to the daemon's
    // `cmd=openURL`, which runs `open` on the DAEMON's machine; from a phone
    // that would launch Safari on an unattended Mac in another room. The
    // http(s)-only GUARD survives verbatim (terminal text is untrusted — a log
    // line can print `file://` or `javascript:`); only the machine changes. See
    // openurl.ts.
    term.loadAddon(new WebLinksAddon((_e, uri) => void openExternal(uri)));
    term.options.linkHandler = { activate: (_e, uri) => void openExternal(uri) };

    term.attachCustomKeyEventHandler((e) => {
      // CARRIED VERBATIM: Shift+Enter must insert a LINE BREAK, not submit.
      // Nothing in the stack consults shift for Enter — xterm sends a bare CR
      // unless alt is held — so a coding agent sees the same byte as a plain
      // Enter and sends the message half-written. The pair an agent inserts a
      // newline for is meta+Enter, ESC CR. It cannot be fixed up in onData: the
      // modifier is gone by then. The event is SWALLOWED so the bare CR can
      // never follow.
      //
      // This branch is only reachable from a hardware keyboard (a Magic
      // Keyboard on an iPad); a soft keyboard cannot produce it at all, which is
      // why the accessory bar carries its own ⇧⏎ key over the same bytes.
      if (
        e.type === "keydown" &&
        e.key === "Enter" &&
        e.shiftKey &&
        !e.ctrlKey &&
        !e.metaKey &&
        !e.altKey
      ) {
        e.preventDefault();
        send("\x1b\r");
        return false;
      }
      return true;
    });

    // CARRIED VERBATIM, including the return value. Returning false is the
    // load-bearing half: it stops xterm's alternate-screen fallback, which
    // converts a wheel into CURSOR KEYS and therefore walks the AGENT's input
    // history instead of scrolling anything. That is the "the terminal is not
    // scrollable" bug as users actually meet it.
    //
    // Only a trackpad reaches this on a phone-shaped device; a finger goes
    // through the touch handlers below. Both end at the same RPC.
    term.attachCustomWheelEventHandler((ev: WheelEvent) => {
      const cell = cellHeight();
      const px = wheelPixels(ev.deltaY, ev.deltaMode, cell, rows);
      scroll = accumulateScroll(scroll, px, cell);
      queueScroll();
      return false;
    });

    term.onData((d) => send(transform(d)));

    ro = new ResizeObserver(() => measure());
    ro.observe(frame);
    measure();

    await attach();

    if (!ready) {
      // The cell was measured on the fallback face. xterm re-measures only on a
      // fontFamily/fontSize change, so a late font needs a nudge: assigning a
      // different family and the real one straight back re-runs the measurement.
      // Nothing paints in between (rendering is rAF-batched).
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
      // cols/rows on the `sub` are ADVISORY and the daemon records and ignores
      // them: the bus attaches once per pane at the developer's tmux window size
      // and fans the untruncated stream out. They are sent so the daemon's
      // record is honest, and the phone's real answer to a 200-column grid is
      // the pan below.
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
    // A SUCCESSFUL ATTACH CLEARS THE LAST ERROR, and "" is how that is said.
    //
    // The screen latches whatever `onerror` last reported and shows it until
    // something replaces it, which was correct while a dropped subscription
    // stayed dropped. It stopped being correct the moment the reconnect above
    // started re-attaching: backgrounding reports "connection_closed:
    // backgrounded", the pane then comes back and repaints with live output,
    // and the red banner above it still said the connection was gone. A banner
    // contradicting the pane underneath it is worse than no banner, because it
    // is the half a user checks first.
    onerror?.("");
    offEvent = sub.onEvent(handle);
    offError = sub.onError((e) => onerror?.(e.message));
    // The first resync usually arrives before onEvent is wired, since it IS the
    // subscribe's acknowledgement — so paint whatever the subscription already
    // holds rather than waiting for a frame that has been and gone.
    if (sub.screen) paint(sub.screen);
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
      // A gap on a PTY frame means the daemon's bus dropped output for a
      // subscriber that fell behind, and those bytes cannot be replayed. The
      // defined recovery is to re-subscribe, which replaces the subscription and
      // sends a fresh screen. Rendering the corruption instead would leave the
      // pane subtly wrong with nothing to indicate it.
      //
      // A gap on a RESYNC needs nothing: the daemon's own repair path already
      // sent a full screen, which is what "resync" means.
      void resubscribe();
      return;
    }
    term.write(ev.data);
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
      // The ONLY thing that ever changes the grid. It changes to the developer's
      // tmux window, which moves whenever a desktop client attaches, detaches or
      // resizes — and the daemon answers each of those with a fresh resync.
      term.resize(screen.cols, screen.rows);
    }
    cols = term.cols;
    rows = term.rows;
    // reset() before the repaint, so a plain shell pane does not push the
    // previous screen into a scrollback nothing should be reading.
    term.reset();
    term.write(resyncToBytes(screen), () => {
      syncModes();
      measure();
    });
  }

  function syncModes() {
    if (!term) return;
    const m = term.modes;
    if (
      m.applicationCursorKeysMode !== modes.applicationCursorKeysMode ||
      m.bracketedPasteMode !== modes.bracketedPasteMode
    ) {
      // Read off xterm rather than guessed. The mode is learned from the OUTPUT
      // stream by xterm's own parser; this only copies its answer somewhere the
      // accessory bar can encode against. See keybytes.ts's RULE 1.
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
   * Zoom out until the whole grid fits across the screen, and back again.
   *
   * THIS IS A ZOOM, NOT A RESIZE, and the distinction is the whole reason the
   * label says "fit" rather than "fit to phone". It scales this phone's own view
   * of a grid whose width belongs to the developer's tmux window; it sends
   * nothing, and it cannot narrow anybody else's screen. PLAN.md's "Fit to
   * phone" is a different, daemon-side feature — dropping `-f ignore-size` so
   * the phone's dimensions drive the tmux window, offered only when no other
   * client is attached — and the daemon does not implement it: a `pty` resize
   * frame is recorded and ignored today (internal/remote/pane.go), and
   * internal/panebus exposes no Resize at all. Wiring a toggle to that frame
   * would be a button that silently does nothing, so this is the honest half:
   * useful for orientation on a wide grid, which is what PLAN.md already says a
   * zoom-out is good for, and never for reading.
   *
   * At 200 columns even the floor does not fit, and that is not hidden — the
   * chip keeps showing shown/cols, so it reads "120/200 cols" after the tap.
   */
  function toggleFit() {
    // iOS raises the soft keyboard only for a focus a user action caused, so a
    // refocus is legal here (a click IS one) but must be conditional: tapping
    // this chip with the keyboard down must not summon it.
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

  /** Keep a tap or a drag on the chip out of the pane's own gesture handling:
   *  without this a tap focuses the terminal and raises the keyboard, and a drag
   *  that starts on the chip pans the grid underneath it. */
  function swallow(e: TouchEvent) {
    e.stopPropagation();
  }

  function measure() {
    if (!frame || !inner) return;
    box = {
      contentWidth: inner.offsetWidth,
      contentHeight: inner.offsetHeight,
      viewWidth: frame.clientWidth,
      viewHeight: frame.clientHeight,
    };
    pan = clampPan(pan, box);
  }

  // Remember the size, on a trailing debounce.
  //
  // Debounced because a pinch mutates fontSize on every touchmove: a
  // localStorage write per frame is a synchronous main-thread stall during the
  // one gesture that has to stay smooth. onDestroy flushes it, which is the case
  // that matters — the bug being fixed here is a size forgotten on navigation,
  // and navigating away is exactly what happens inside the debounce window.
  $effect(() => {
    const size = fitFrom ?? fontSize;
    clearTimeout(saveTimer);
    saveTimer = setTimeout(() => saveFontSize(size), FONT_SAVE_DEBOUNCE_MS);
  });

  // Re-measure whenever the font changes: the grid keeps its cols and rows and
  // changes its pixel size, so the pan limits move under the finger.
  $effect(() => {
    const size = fontSize;
    if (term && term.options.fontSize !== size) {
      term.options.fontSize = size;
      requestAnimationFrame(measure);
    }
  });

  // A RECONNECT LEAVES THIS SCREEN HOLDING A DEAD SUBSCRIPTION, and only this
  // component can notice.
  //
  // The socket is closed on the way into the background and reopened on the way
  // back (see appstate.ts and Connection#reconnect). That reopen restores the
  // session list, because the store polls — but a PaneSubscription is a
  // server-side registration on a socket that no longer exists, so the terminal
  // keeps its last painted screen forever and every keystroke goes nowhere. The
  // pane is not gone and the daemon is not down; the app has simply not asked
  // again.
  //
  // `resubscribe()` is the same recovery a dropped-frame gap already uses, so
  // this adds a trigger rather than a mechanism. It fires only on the false ->
  // true EDGE while this screen is attached, which is what keeps it from
  // re-running on the ordinary `ready` that follows the first attach, and the
  // `exited` check keeps it off a pane whose process is gone.
  // A PLAIN `let`, not `$state`: this is an edge detector, and an effect that
  // both reads and writes the same rune re-invalidates itself forever. An
  // untracked local is read for free and written for free.
  let wasReady = connection.ready;
  $effect(() => {
    const ready = connection.ready;
    const back = ready && !wasReady;
    wasReady = ready;
    if (back && attached && !exited && !disposed) void resubscribe();
  });

  // Re-theme a live terminal. Assigning options.theme is sufficient AND
  // complete — see LiveTerminal's note on why no clearTextureAtlas() belongs
  // here.
  $effect(() => {
    const theme = appearance.term;
    if (term) term.options.theme = theme;
  });

  // --- scroll ---------------------------------------------------------------

  function queueScroll() {
    if (scrollTimer !== undefined) return;
    scrollTimer = setTimeout(flushScroll, 16);
  }

  function flushScroll() {
    scrollTimer = undefined;
    const { lines, rest } = takeScroll(scroll);
    scroll = rest;
    if (lines === 0 || !sub || disposed) return;
    // CARRIED: scroll is an RPC and is NEVER client-synthesized keys. The daemon
    // asks the pane once whether it is a program that keeps its own transcript
    // and wants wheel events, or a plain shell whose history is tmux's copy
    // mode, and writes the right thing. Getting that wrong is not a degraded
    // scroll but NO scroll: copy mode on an agent pane reads [0/0] and moves
    // nothing. The client must never re-derive that decision.
    void sub.scroll(lines).catch(() => {});
  }

  // --- touch ----------------------------------------------------------------
  //
  //   one finger, sideways   pan horizontally (no network)
  //   one finger, vertical   SCROLL, an RPC into the daemon's history
  //   two fingers dragging   pan freely over the grid (no network)
  //   two fingers pinching   font size, 8 to 16 points
  //
  // The axis of a one-finger drag is locked ONCE, from the first movement that
  // clears the slop, so a curved swipe cannot alternate between a free pan and a
  // network round trip.

  let touchStart = { x: 0, y: 0 };
  let last = { x: 0, y: 0 };
  let axis: Axis = null;
  let moved = false;
  let pinchStart = 0;
  let pinchBase = 0;
  let fingers = 0;

  function onTouchStart(e: TouchEvent) {
    fingers = e.touches.length;
    moved = false;
    axis = null;
    if (fingers === 1) {
      const t = e.touches[0];
      touchStart = last = { x: t.clientX, y: t.clientY };
    } else if (fingers >= 2) {
      pinchStart = touchDistance(e.touches[0], e.touches[1]);
      pinchBase = fontSize;
      last = touchMidpoint(e.touches[0], e.touches[1]);
    }
  }

  function onTouchMove(e: TouchEvent) {
    if (e.touches.length >= 2) {
      e.preventDefault();
      moved = true;
      const d = touchDistance(e.touches[0], e.touches[1]);
      if (pinchStart > 0) {
        const next = pinchFont(pinchBase, d / pinchStart);
        if (next !== fontSize) {
          // A pinch is the user choosing a size, so it ENDS a fit rather than
          // being undone by it: leaving fitFrom set would make the chip offer to
          // "reset" to a size the user has just deliberately left.
          fitFrom = null;
          fontSize = next;
        }
      }
      const mid = touchMidpoint(e.touches[0], e.touches[1]);
      pan = panBy(pan, mid.x - last.x, mid.y - last.y, box);
      last = mid;
      return;
    }

    const t = e.touches[0];
    if (!t) return;
    const dx = t.clientX - touchStart.x;
    const dy = t.clientY - touchStart.y;
    axis ??= lockAxis(dx, dy);
    if (axis === null) return;

    e.preventDefault();
    moved = true;
    if (axis === "x") {
      pan = panBy(pan, t.clientX - last.x, 0, box);
    } else {
      // Positive movement means the finger went DOWN, which reveals what is
      // ABOVE — going back in history. accumulateScroll takes the screen
      // convention (positive = content moved up = forward) and flips it once, so
      // the sign is inverted here rather than there.
      scroll = accumulateScroll(scroll, -(t.clientY - last.y), cellHeight());
      queueScroll();
    }
    last = { x: t.clientX, y: t.clientY };
  }

  function onTouchEnd(e: TouchEvent) {
    // A tap that did not move gives the terminal the keyboard. It must happen
    // inside the gesture: iOS raises the soft keyboard only for a focus that a
    // user action caused, so an autofocus or a focus() from a later callback
    // does nothing at all and reads as "typing goes nowhere".
    if (!moved && fingers === 1) term?.focus();
    if (e.touches.length === 0) {
      fingers = 0;
      axis = null;
      pinchStart = 0;
    }
  }

  onDestroy(() => {
    disposed = true;
    offEvent?.();
    offError?.();
    ro?.disconnect();
    clearTimeout(scrollTimer);
    clearTimeout(saveTimer);
    saveFontSize(fitFrom ?? fontSize);
    void sub?.close().catch(() => {});
    term?.dispose();
  });
</script>

<!-- touch-action:none, because every gesture in here is ours. Without it Safari
     claims the vertical drag for its own overscroll and the pane rubber-bands
     instead of scrolling. The page-level pinch is already pinned by the viewport
     meta tag, so the only pinch that reaches anything is this one. -->
<!-- role="application" is the correct role for a terminal emulator, and it is
     also what silences the (correct) warning that a div carrying touch handlers
     announces nothing: it tells a screen reader to hand keystrokes to the widget
     rather than interpreting them as navigation, which is exactly right here. -->
<div
  bind:this={frame}
  role="application"
  aria-label="Terminal for {pane}"
  class="term-pane relative h-full w-full touch-none overflow-hidden bg-panel"
  ontouchstart={onTouchStart}
  ontouchmove={onTouchMove}
  ontouchend={onTouchEnd}
  ontouchcancel={onTouchEnd}
>
  <div
    bind:this={inner}
    class="absolute top-0 left-0 will-change-transform"
    style="transform: translate3d({-pan.x}px, {-pan.y}px, 0)"
  >
    <div bind:this={host}></div>
  </div>

  {#if panning || fitFrom !== null}
    <!-- Truncation must never be a mystery: 55 of 200 columns with nothing said
         looks exactly like an agent that stopped writing halfway across. The
         count is the honest part and is shown in every state; the trailing word
         is the ACTION a tap performs, so the chip never reads as a label that
         happens to be tappable. -->
    {#if canFit || fitFrom !== null}
      <button
        type="button"
        class="absolute top-1 right-1 min-h-9 rounded bg-canvas/80 px-2 py-1 text-sm text-faint"
        aria-label={fitFrom !== null
          ? `Back to ${fitFrom} point text`
          : `Zoom out to ${fit.size} point text to see ${fit.complete ? "the whole grid" : "more of it"}. This changes only this phone's view; the pane keeps its size.`}
        onclick={toggleFit}
        ontouchstart={swallow}
        ontouchmove={swallow}
        ontouchend={swallow}
      >
        {shown}/{cols} cols · {fitFrom !== null ? "reset" : "fit"}
      </button>
    {:else}
      <div
        class="pointer-events-none absolute top-1 right-1 rounded bg-canvas/80 px-1.5 py-0.5 text-sm text-faint"
      >
        {shown}/{cols} cols · pan
      </div>
    {/if}
  {/if}
</div>
