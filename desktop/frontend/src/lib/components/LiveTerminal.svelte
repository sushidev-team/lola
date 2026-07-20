<script lang="ts">
  import { onMount, onDestroy } from "svelte";
  import { Events } from "@wailsio/runtime";
  import { Terminal } from "@xterm/xterm";
  import { FitAddon } from "@xterm/addon-fit";
  import { WebglAddon } from "@xterm/addon-webgl";
  import { TermService } from "@bindings/desktop";

  // A live, interactive terminal attached to a session's tmux pane. WebGL is
  // reserved for these focused terminals (the ~16-context ceiling means the grid
  // tiles use cheap DOM snapshots instead). Bytes arrive base64-encoded on
  // pty:<name> and keystrokes go back through TermService.Write.
  let {
    name,
    webgl = true,
    interactive = true,
    fontSize = 13,
  }: { name: string; webgl?: boolean; interactive?: boolean; fontSize?: number } = $props();

  let host: HTMLDivElement;
  let term: Terminal | undefined;
  let fit: FitAddon | undefined;
  let gl: WebglAddon | undefined;
  let off: (() => void) | undefined;
  let ro: ResizeObserver | undefined;
  let resizeTimer: ReturnType<typeof setTimeout> | undefined;

  function b64ToBytes(b64: string): Uint8Array {
    const bin = atob(b64);
    const bytes = new Uint8Array(bin.length);
    for (let i = 0; i < bin.length; i++) bytes[i] = bin.charCodeAt(i);
    return bytes;
  }

  onMount(() => {
    term = new Terminal({
      scrollback: 1500,
      // Hack — a hinted, highly legible terminal face. An INTEGER font size keeps
      // cell/glyph dimensions on whole device pixels so WebGL renders crisply
      // (a fractional size like 12.5 rounds unevenly and looks soft).
      fontFamily: '"Hack", "JetBrains Mono", ui-monospace, Menlo, monospace',
      fontSize,
      fontWeight: 400,
      fontWeightBold: 700,
      // 1.0 keeps cell heights on whole pixels; letterSpacing 0 keeps columns
      // pixel-aligned; allowTransparency:false so glyph edges blend onto the
      // opaque bg instead of to transparent (which renders text abnormally thin).
      lineHeight: 1.0,
      letterSpacing: 0,
      allowTransparency: false,
      cursorBlink: interactive,
      disableStdin: !interactive,
      theme: {
        background: "#0e1420",
        foreground: "#c3cbd6",
        cursor: "#57c7d6",
        selectionBackground: "#1b2634",
        black: "#0e1420",
        red: "#e0716f",
        green: "#5fd08a",
        yellow: "#e0b44a",
        blue: "#6ea8fe",
        magenta: "#c99bf0",
        cyan: "#57c7d6",
        white: "#c3cbd6",
        brightBlack: "#6b7686",
      },
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
    }

    off = Events.On(`pty:${name}`, (e: { data: string }) => {
      term?.write(b64ToBytes(e.data));
    });

    TermService.Attach(name, cols, rows).catch(() => {
      term?.writeln("\x1b[31m[ could not attach to session ]\x1b[0m");
    });

    ro = new ResizeObserver(() => {
      clearTimeout(resizeTimer);
      resizeTimer = setTimeout(() => fit?.fit(), 60);
    });
    ro.observe(host);

    // xterm measures the font and builds the WebGL glyph atlas at open(); if Hack
    // is still loading it uses fallback metrics/glyphs and the atlas stays stale.
    // Clear the atlas, re-fit, and repaint once the font is ready.
    document.fonts.ready.then(() => {
      gl?.clearTextureAtlas();
      fit?.fit();
      term?.refresh(0, (term?.rows ?? 1) - 1);
    });
  });

  onDestroy(() => {
    off?.();
    ro?.disconnect();
    clearTimeout(resizeTimer);
    TermService.Detach(name).catch(() => {});
    gl?.dispose();
    term?.dispose();
  });
</script>

<div bind:this={host} class="term-live h-full w-full overflow-hidden"></div>
