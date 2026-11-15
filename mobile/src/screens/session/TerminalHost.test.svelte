<script lang="ts">
  // The segment with the two sheets it opens hosted beside it, the way App's
  // sheet host mounts them by name from `router.sheet`.
  import TerminalSegment from "./TerminalSegment.svelte";
  import PaneMenu from "@mobile/sheets/paneMenu.svelte";
  import ViewSettingsSheet from "@mobile/sheets/viewSettings.svelte";
  import { router } from "@mobile/lib/router.svelte";
</script>

<TerminalSegment id="lola-fe-42" />
{#if router.sheet === "paneMenu"}
  <PaneMenu />
{:else if router.sheet === "viewSettings"}
  <ViewSettingsSheet />
{/if}
