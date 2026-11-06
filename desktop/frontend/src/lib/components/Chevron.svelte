<script lang="ts">
  // The one disclosure mark. A geometric ▸ is about 4px wide at text size and reads as a speck, so
  // rows did not look like they opened. Drawn, in currentColor, and a real sibling element rather
  // than a pseudo-element, which WebKit does not reliably paint on a control. It turns to say what
  // happened: closed points right, open points down, and a menu chip passes `open`, since a menu
  // always opens downward.
  let { open = false, class: cls = "" }: { open?: boolean; class?: string } = $props();
</script>

<svg class="inline-block size-2.5 shrink-0 transition-transform duration-150 motion-reduce:transition-none {open ? 'rotate-90' : ''} {cls}" viewBox="0 0 10 10" fill="none" stroke="currentColor" stroke-width="1.25" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true" data-chevron><path d="M3.5 1.5 7 5 3.5 8.5" /></svg>
