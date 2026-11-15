<script lang="ts">
  import { onDestroy, untrack } from "svelte";
  import PaneMenuSheet from "@mobile/lib/components/PaneMenuSheet.svelte";
  import { paneTabs } from "@mobile/lib/panetabs.svelte";
  import { router } from "@mobile/lib/router.svelte";
  import { can } from "@mobile/lib/tiers.svelte";
  import { PANE_KIND_AGENT } from "@mobile/wire";

  // One tab's menu (`?sheet=paneMenu`): rename on this phone, close on the Mac.
  //
  // The pane is fixed once resolved. A close clears the model's own `menuPane`,
  // and following that would swap this sheet to another pane.

  // The pane is fixed once it is resolved. Held tab first; else the pane the
  // route names (the session's own name is the agent pane's, but a link names no
  // tab and the session id is not always a pane name); else the agent pane; else
  // the first one. Resolved only after the inventory has loaded.
  let pane = $state(untrack(() => paneTabs.menuPane));
  const menuFor = $derived(paneTabs.panes.find((p) => p.name === pane));

  $effect(() => {
    if (pane !== "" || !paneTabs.loaded) return;
    const top = router.top;
    const named = top.name === "session" ? (top.pane ?? top.id) : "";
    const found =
      paneTabs.panes.find((p) => p.name === named) ??
      paneTabs.panes.find((p) => p.kind === PANE_KIND_AGENT) ??
      paneTabs.panes[0];
    if (found) pane = found.name;
  });

  // Closed only when the inventory is known AND the pane is truly gone (or there
  // is nothing to be the pane of).
  $effect(() => {
    if (!paneTabs.loaded) return;
    // The agent pane has no menu: it is the session itself.
    if (menuFor?.kind === PANE_KIND_AGENT) {
      router.close();
      return;
    }
    if (menuFor) return;
    if (pane !== "" || paneTabs.panes.length === 0) router.close();
  });
  onDestroy(() => {
    paneTabs.menuPane = "";
  });
</script>

{#if menuFor && menuFor.kind !== PANE_KIND_AGENT}
  <PaneMenuSheet
    name={menuFor.name}
    defaultName={paneTabs.defaultName(menuFor)}
    label={paneTabs.labels[menuFor.name] ?? ""}
    canClose={can("paneClose")}
    closing={paneTabs.closing === menuFor.name}
    onrename={(l) => {
      paneTabs.rename(menuFor, l);
      router.close();
    }}
    onclosepane={() => {
      void paneTabs.closePane(menuFor).then(() => router.close());
    }}
    ondismiss={() => router.close()}
  />
{/if}
