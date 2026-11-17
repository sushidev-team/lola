<script lang="ts">
  import { nav } from "$lib/nav.svelte";
  import SessionEmbed from "./SessionEmbed.svelte";
  import { reflowGridRows } from "$lib/reflow";
</script>

<!-- Keep the workspace and its terminal mounted across sidebar/focus/layout changes. -->
<div class="grid min-h-0 min-w-0 grid-cols-[minmax(0,1fr)] grid-rows-[minmax(0,1fr)] bg-canvas" data-session-embed {@attach reflowGridRows}>
  <SessionEmbed sessionId={nav.focusedTerm || nav.selectedId} />
</div>
