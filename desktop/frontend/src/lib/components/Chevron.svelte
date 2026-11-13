<script lang="ts">
  import Icon from "$lib/components/Icon.svelte";
  // The one disclosure mark. A geometric ▸ is about 4px wide at text size and reads as a speck, so
  // rows did not look like they opened. Drawn, in currentColor, and a real sibling element rather
  // than a pseudo-element, which WebKit does not reliably paint on a control. It turns to say what
  // happened: closed points right, open points down, and a menu chip passes `open`, since a menu
  // always opens downward.
  let { open = false, class: cls = "" }: { open?: boolean; class?: string } = $props();
</script>

<Icon name="chevron-right" class="inline-block size-3.5 shrink-0 transition-transform duration-150 motion-reduce:transition-none {open ? 'rotate-90' : ''} {cls}" data-chevron />
