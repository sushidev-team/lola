<script lang="ts">
  import { updates } from "$lib/update.svelte";
  import { nav } from "$lib/nav.svelte";
  import Button from "./Button.svelte";

  // The version, on its own line at the foot of the sidebar's scrolling body —
  // deliberately NOT in the utility row below it. That row is for acting on the
  // daemon (status, restart/stop) and on the app (help, settings); the version
  // only REPORTS, and sitting among four controls it read as a fifth one while
  // crowding the row it did not belong to.
  //
  // It is still a real control — clicking it opens the update overlay, and an
  // available update promotes it to an accent chip — because the alternative
  // (plain text plus a separate "check for updates" entry somewhere) is a worse
  // trade than one quiet button that happens to also be a label.
</script>

<!-- Bottom of the scrolling body: after Activity, above the utility row's
     border. It is the last row of that grid, so it stays put while Activity
     scrolls above it. -->
<div class="flex min-w-0 items-center px-3 pt-2 pb-2.5">
  {#if updates.available}
    <Button
      variant="secondary"
      size="xs"
      class="num shrink-0 border-accent! text-accent-ink!"
      title="update available: v{updates.info?.latestVersion}"
      onclick={() => nav.openOverlay("update")}
    >
      <span aria-hidden="true">↑</span> Update
    </Button>
  {:else}
    <Button size="xs" class="num shrink-0" title="check for updates" onclick={() => nav.openOverlay("update")}>
      v{updates.version}
    </Button>
  {/if}
</div>
