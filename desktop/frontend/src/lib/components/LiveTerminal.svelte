<script lang="ts">
  import { onMount, onDestroy } from "svelte";
  import { Events } from "@wailsio/runtime";
  import { Terminal } from "@xterm/xterm";
  import { FitAddon } from "@xterm/addon-fit";
  import { WebglAddon } from "@xterm/addon-webgl";
  import { TermService } from "@bindings/desktop";
  import { TERM_FONT, appearance, termFontLoaded, termFontReady } from "$lib/theme-runtime.svelte";

  // A live, interactive terminal attached to a session's tmux pane. WebGL is
  // reserved for these focused terminals (the ~16-context ceiling means the grid
  // tiles use cheap DOM snapshots instead). Bytes arrive base64-encoded on
  // pty:<name> and keystrokes go back through TermService.Write.
  //
  // There is deliberately NO fontSize prop. Every font option comes from
  // TERM_FONT, which is the single place the cell arithmetic lives: the size,
  // lineHeight and letterSpacing are a matched SET that lands the cell on
  // 8.0000 x 17.0000 css px (Ghostty's measured cell for Hack@13) using whole
  // device pixels at dpr 1 and 2. Overriding the size alone breaks that — the
  // old `focused ? 14 : 12` did exactly that, giving 14px glyphs an 8px pitch
  // (their own advance is 8.4287), which is why the terminal read as
  // simultaneously bigger and more cramped than Ghostty.
  // onExit fires when the attached tmux session ends on its OWN — the shell
  // exited, or an attach hit an already-gone session (tmux's client exits, the
  // PTY EOFs, the backend emits pty:<name>:exit). A deliberate Detach does NOT
  // fire it. SessionEmbed passes it only for a shell tab, to retire that tab.
  // autofocus grabs the keyboard for this terminal once it opens, so a shell tab
  // (or a fullscreen embed) can be typed into immediately without a click. Left
  // off for the compact agent pane, which remounts as the selection moves and
  // must not steal keys from the sessions list.
  let {
    name,
    webgl = true,
    interactive = true,
    onExit,
    autofocus = false,
  }: {
    name: string;
    webgl?: boolean;
    interactive?: boolean;
    onExit?: () => void;
    autofocus?: boolean;
  } = $props();

  let host: HTMLDivElement;
  let term: Terminal | undefined;
  let fit: FitAddon | undefined;
  let gl: WebglAddon | undefined;
  let off: (() => void) | undefined;
  let offExit: (() => void) | undefined;
  let ro: ResizeObserver | undefined;
  let resizeTimer: ReturnType<typeof setTimeout> | undefined;
  let disposed = false;
  // The Attach round-trip, tracked so teardown can Detach the SAME session
  // exactly once and never before it was attached (see boot() and onDestroy).
  let attach: Promise<unknown> | undefined;
  let detachDone = false;

  function detachOnce() {
    if (detachDone) return;
    detachDone = true;
    TermService.Detach(name).catch(() => {});
  }

  function b64ToBytes(b64: string): Uint8Array {
    const bin = atob(b64);
    const bytes = new Uint8Array(bin.length);
    for (let i = 0; i < bin.length; i++) bytes[i] = bin.charCodeAt(i);
    return bytes;
  }

  // Force the one re-measure xterm will not do on its own. Only reachable when
  // the bounded font wait in boot() expired and Hack landed after open().
  //
  // Assigning a DIFFERENT family and then the real one straight back is what
  // makes it happen: OptionsService.ts:132 fires an option-change event only on
  // a real value change, and CharSizeService.ts:34 re-measures only on a
  // fontFamily/fontSize change — so the second assignment runs measure() again,
  // now with the loaded face. Nothing paints between the two writes (rendering
  // is rAF-batched), so the intermediate family is never seen.
  //
  // clearTextureAtlas() is required on top: atlases are cached by config and
  // SHARED between terminals with the same one (CharAtlasCache.ts:57-65), so
  // toggling back can hand us another terminal's atlas — still full of
  // fallback-face glyphs — instead of building a fresh one.
  //
  // Re-fitting is required too: cols/rows were derived from the old cell size,
  // and FitAddon.fit() no-ops unless the proposed geometry differs
  // (FitAddon.ts:44). It is the same fit() the ResizeObserver calls, runs at
  // most once per terminal, and is idempotent — so it cannot race the observer
  // into a different geometry than the observer would settle on anyway.
  function remeasureFont(): void {
    if (!term) return;
    term.options.fontFamily = "monospace";
    term.options.fontFamily = TERM_FONT.fontFamily;
    gl?.clearTextureAtlas();
    fit?.fit();
    term.refresh(0, term.rows - 1);
  }

  onMount(() => {
    void boot();
  });

  async function boot() {
    // Wait for Hack BEFORE open(), because open() is the only place xterm
    // measures the cell (Terminal.ts:570 -> CharSizeService.measure()). Opening
    // early does not merely delay the correct metrics, it LOSES them: the
    // service re-measures only on a fontFamily/fontSize change
    // (CharSizeService.ts:34) or after a real resize (Terminal.ts:1214), so a
    // terminal opened on the fallback face keeps the fallback's cell forever.
    // The old document.fonts.ready handler here cleared the atlas and re-fit,
    // which repainted the glyphs but never re-measured — the cell stayed wrong.
    //
    // Waiting is preferred over forcing a re-measure after the fact because it
    // is the path with no invalidation to get right: one measurement, taken
    // once, with the real font. The cost is a few ms of empty host div for a
    // locally vendored woff2. remeasureFont() above is kept only for the case
    // the wait gives up (see termFontReady), and is deliberately the exception.
    const ready = await termFontReady();
    if (disposed) return;

    term = new Terminal({
      scrollback: 1500,
      // Font metrics — Hack at the exact size/lineHeight/letterSpacing that
      // reproduce Ghostty's cell. See TERM_FONT in theme-runtime.svelte.ts for
      // the arithmetic and why each number cannot be changed independently.
      ...TERM_FONT,
      cursorBlink: interactive,
      disableStdin: !interactive,
      // Colours come from the live Catppuccin flavor. `background` is that
      // flavor's `base`, which is ALSO the value of --color-panel (see toTokens
      // in catppuccin.ts) — so the terminal and the surface it sits on are the
      // same colour by construction, and the wrapper in SessionEmbed paints
      // bg-panel so the gutter around it matches too. That replaces the old
      // hand-picked #111927: back when the chrome was dark navy and the
      // terminal was Mocha, the two palettes could only be reconciled by
      // pasting the panel colour into the terminal theme. Now one palette
      // drives both, so there is nothing left to reconcile.
      //
      // Why this still matters beyond looks: agents probe the terminal
      // background with OSC 11 to theme their own input box. Because the
      // background is a real, opaque colour that is genuinely painted (the
      // WebGL RectangleRenderer clears with theme.background) and genuinely
      // matches what surrounds it, the probe answers with a colour that is
      // actually on screen — and it now follows the flavor instead of being
      // frozen to one navy. allowTransparency stays false (in TERM_FONT): a
      // transparent background makes WebGL composite text thin and washed out,
      // and would leave the OSC-11 answer describing a colour nothing paints.
      theme: appearance.term,
    });
    fit = new FitAddon();
    term.loadAddon(fit);
    term.open(host);

    if (webgl) {
      try {
        gl = new WebglAddon();
        gl.onContextLoss(() => {
          gl?.dispose();
          gl = undefined; // fall back to the DOM renderer if the context is evicted
        });
        term.loadAddon(gl);
      } catch {
        gl = undefined;
      }
    }

    fit.fit();
    const { cols, rows } = term;

    if (interactive) {
      term.onData((d) => TermService.Write(name, d));
      term.onResize(({ cols, rows }) => TermService.Resize(name, cols, rows));
      // Grab the keyboard now so a shell tab / fullscreen embed is typeable
      // without a click — the reported "I start typing and it goes nowhere".
      if (autofocus) term.focus();
    }

    off = Events.On(`pty:${name}`, (e: { data: string }) => {
      term?.write(b64ToBytes(e.data));
    });

    // A shell tab asks to be told when its session ends so it can close itself.
    // Subscribe only when wanted (the agent pane passes no onExit): its own tmux
    // ending means the session died, which the store already reflects elsewhere.
    if (onExit) {
      offExit = Events.On(`pty:${name}:exit`, () => {
        if (!disposed) onExit?.();
      });
    }

    attach = TermService.Attach(name, cols, rows).catch(() => {
      term?.writeln("\x1b[31m[ could not attach to session ]\x1b[0m");
    });

    ro = new ResizeObserver(() => {
      clearTimeout(resizeTimer);
      resizeTimer = setTimeout(() => fit?.fit(), 60);
    });
    ro.observe(host);

    // We opened without a confirmed font, so the cell may be the fallback's.
    // Recover if Hack lands late. If it never loads at all this resolves false
    // and nothing happens — a fallback cell beats a terminal that keeps
    // rebuilding itself.
    if (!ready) {
      void termFontLoaded().then((ok) => {
        if (ok && !disposed) remeasureFont();
      });
    }
  }

  // Re-theme a LIVE terminal when the flavor changes. Assigning options.theme is
  // sufficient AND complete — deliberately no clearTextureAtlas() here, which is
  // the opposite of the font case in remeasureFont(). Verified in node_modules:
  //
  //   ThemeService.ts:116  onSpecificOptionChange('theme') -> _setTheme()
  //   ThemeService.ts:178  fires onChangeColors
  //   WebglRenderer.ts:78  onChangeColors -> _handleColorChange()
  //                        -> _refreshCharAtlas() + _clearModel(true)
  //   RenderService.ts:104 onChangeColors -> _fullRefresh()
  //
  // and _refreshCharAtlas calls acquireTextureAtlas, whose cache key includes
  // every ansi rgba plus fg/bg (CharAtlasUtils.configEquals) — so new colours
  // resolve to a DIFFERENT atlas instance, which is swapped in automatically and
  // the old one disposed. Calling clearTextureAtlas() on top would throw away a
  // freshly built atlas and force a redundant re-rasterisation of every glyph.
  //
  // remeasureFont() is the opposite case and DOES need the explicit clear: a
  // late font does not change the cache key at all (same colours, same size,
  // same family once toggled back), so acquireTextureAtlas can hand back an
  // atlas still full of fallback-face glyphs. Different failure, different fix.
  //
  // Assigning the same reference is a no-op: OptionsService's setter compares
  // with !== before firing (OptionsService.ts:132), and appearance.term is a
  // $derived whose identity is stable until the flavor id actually changes.
  //
  // `term` is undefined until boot() finishes waiting for the font, and this
  // effect does not re-run on that assignment (plain let, not $state). That is
  // fine and not worth a rune: the Terminal is CONSTRUCTED with the current
  // appearance.term, so a flavor changed during the wait is already applied by
  // the time there is a terminal to apply it to.
  $effect(() => {
    const theme = appearance.term;
    if (term) term.options.theme = theme;
  });

  onDestroy(() => {
    // Also the cancellation flag for boot(): unmounting during the font wait
    // must not leave a terminal attached to a tmux pane nobody is watching.
    disposed = true;
    off?.();
    offExit?.();
    ro?.disconnect();
    clearTimeout(resizeTimer);
    // Detach only a session we actually attached, and only AFTER Attach settles.
    // Tearing down during the pre-Attach font wait leaves `attach` undefined
    // (nothing to detach); tearing down while Attach is in flight defers the
    // Detach until it resolves so it can't overtake the Attach and leak the
    // pane. detachOnce() guards against a double Detach on the normal path.
    if (attach) void attach.finally(detachOnce);
    gl?.dispose();
    term?.dispose();
  });
</script>

<div bind:this={host} class="term-live h-full w-full overflow-hidden"></div>
