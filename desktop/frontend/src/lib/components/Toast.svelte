<script lang="ts">
  import { store } from "$lib/store.svelte";

  // The transient action result ("killed eng-123", "config reloaded", an error).
  // It used to live in the footer, where a message appearing and vanishing made
  // a permanent row twitch and pushed the hint text around. As a corner toast it
  // costs no layout at all.
  //
  // No timer here: store.setFlash already clears itself after 4s, and a second
  // timer would only be a second thing to get out of sync.
  //
  // z-45 sits above the z-40 modal backdrop — an action fired from inside a
  // guarded config form still reports — but below SessionMenu's z-50 popup.
</script>

<!-- The live region is ALWAYS in the DOM and only its contents change. A
     role="status" created together with its text is commonly missed by screen
     readers — the region has to exist before the announcement is written into
     it. The wrapper is inert and invisible when there is no flash. -->
<div
  class="fixed right-4 bottom-4 z-[45] max-w-sm empty:hidden"
  role="status"
  aria-live="polite"
>
  {#if store.flash}
    <div
      class="selectable rounded-md border border-edge bg-panel px-3 py-2 text-sm shadow-lg"
      class:text-good={store.flash.kind === "good"}
      class:text-warn={store.flash.kind === "warn"}
      class:text-bad={store.flash.kind === "bad"}
    >
      {store.flash.text}
    </div>
  {/if}
</div>
