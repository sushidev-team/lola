<!--
  One QR code, drawn as a single SVG path.

  It is deliberately BLACK ON WHITE rather than themed. A QR is read by a camera
  measuring contrast, not by a person reading an interface, and every flavour
  this app ships would tint one of the two — Catppuccin latte's canvas is off
  white and mocha's would invert the code entirely, which many decoders refuse.
  So the white plate is part of the picture, and it carries the quiet zone with
  it: a code flush against a dark panel is materially harder to scan, and a
  quiet zone left to the call site is one that gets forgotten.

  The rendering is one `<path>` of ~1800 module squares rather than ~1800
  `<rect>` elements. A version 11 code is 3721 modules; that many DOM nodes in a
  WKWebView is a real cost for a static picture.
-->
<script lang="ts">
  import { encodeQR, toPath, viewBox, type ECCLevel } from "$lib/qr";

  let {
    /** The exact string a scanner should read back. */
    value,
    /** Rendered side length in CSS pixels. */
    size = 240,
    ecc = "M" as ECCLevel,
    /**
     * What a screen reader announces. The VALUE is never it: this component's
     * one caller renders a bearer key, and an accessibility tree is a place a
     * secret should not be either.
     */
    label = "QR code",
  }: {
    value: string;
    size?: number;
    ecc?: ECCLevel;
    label?: string;
  } = $props();

  // Encoding is pure and cheap (a few milliseconds at this size), so it is
  // derived rather than cached in an effect — and a failure is a rendered
  // state, not a thrown error that would take the settings tab down with it.
  const drawn = $derived.by(() => {
    try {
      const m = encodeQR(value, { ecc });
      return { path: toPath(m, 4), box: viewBox(m, 4), error: "" };
    } catch (e) {
      return { path: "", box: 0, error: e instanceof Error ? e.message : String(e) };
    }
  });
</script>

{#if drawn.error}
  <p class="text-sm text-bad">Could not draw a code: {drawn.error}</p>
{:else}
  <svg
    role="img"
    aria-label={label}
    width={size}
    height={size}
    viewBox="0 0 {drawn.box} {drawn.box}"
    shape-rendering="crispEdges"
    class="rounded-sm">
    <rect width={drawn.box} height={drawn.box} fill="#ffffff" />
    <path d={drawn.path} fill="#000000" />
  </svg>
{/if}
