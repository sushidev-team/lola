<script lang="ts">
  // The strip with the pane menu hosted beside it, the way the Terminal segment
  // and the app's sheet host arrange them: the strip asks, the router opens
  // `paneMenu`, the sheet reads the shared model.
  import PaneTabs from "./PaneTabs.svelte";
  import PaneMenu from "@mobile/sheets/paneMenu.svelte";
  import { router } from "@mobile/lib/router.svelte";

  import type { ComponentProps } from "svelte";

  let { ...props }: ComponentProps<typeof PaneTabs> = $props();
</script>

<PaneTabs {...props} onmenu={() => router.open("paneMenu")} />
{#if router.sheet === "paneMenu"}
  <PaneMenu />
{/if}
