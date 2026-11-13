<script lang="ts">
  import Icon from "$lib/components/Icon.svelte";
  import { nav } from "$lib/nav.svelte";
  import ActivityFeed from "./ActivityFeed.svelte";
  function read() { try { return localStorage.getItem("lola.activityOpen") === "1"; } catch { return false; } }
  let open = $state(read());
  function toggle() { open=!open; try { localStorage.setItem("lola.activityOpen",open?"1":"0"); } catch {} }
</script>
<section class="min-w-0 border-t border-edge/60 px-3 py-2" aria-label="Activity">
  <button class="flex w-full items-center gap-2 rounded px-2 py-1 text-sm text-faint hover:bg-sel" aria-expanded={open} onclick={toggle}>
    <Icon name="chevron-right" class="size-3 transition-transform {open ? 'rotate-90' : ''}" />Activity
  </button>
  {#if open}<div class="max-h-40 overflow-auto px-2 pt-2"><ActivityFeed /></div><button class="mt-2 px-2 text-sm text-accent-ink" onclick={() => nav.openOverlay("activity")}>View all activity</button>{/if}
</section>
