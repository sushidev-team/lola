<script lang="ts">
  import Sheet from "@mobile/lib/components/Sheet.svelte";
  import ViewSettings, { viewTextOnly } from "@mobile/lib/components/ViewSettings.svelte";
  import { router } from "@mobile/lib/router.svelte";
  import { terminalView } from "@mobile/lib/terminalgeometry.svelte";
  import { can } from "@mobile/lib/tiers.svelte";
  import { copy } from "@mobile/lib/copy";

  // Text size, fit width and the size pin for the attached pane
  // (`?sheet=viewSettings`). The terminal segment keeps `terminalView` current;
  // this sheet only reads it and drives the two actions the terminal registered.
  // "Fit the width" closes the sheet because it changes what is behind it.

  // A sheet holding only the stepper is named for it, and its row drops the label.
  const textOnly = $derived(viewTextOnly(terminalView.geom, can("paneResize")));
</script>

<Sheet label="View" title={textOnly ? copy.viewSettings.textSize : "View"} dismissLabel="Close view settings" onclose={() => router.close()}>
  <ViewSettings
    font={terminalView.font}
    geom={terminalView.geom}
    onfont={(size) => terminalView.actions?.setFont(size)}
    onfit={() => terminalView.actions?.toggleFit()}
    pinned={terminalView.pinActive}
    canPin={can("paneResize")}
    onpin={(on) => terminalView.setPin(on)}
    ondone={() => router.close()}
  />
</Sheet>
