<script lang="ts">
  import ModelSettings from "$lib/components/session/ModelSettings.svelte";
  import EmptyState from "@mobile/lib/components/EmptyState.svelte";
  import Sheet from "@mobile/lib/components/Sheet.svelte";
  import { copy } from "@mobile/lib/copy";
  import { router } from "@mobile/lib/router.svelte";
  import { can } from "@mobile/lib/tiers.svelte";

  // The shared model controls as an inset grouped list; they apply only while the agent rests (spec §5.3).
  // ModelSettings draws nothing for an agent with no settings, so it reports that (`empty`) and this
  // sheet says so instead of opening blank. Its `[data-phone-desk]` scope is what rescales the shared text.

  const id = $derived(router.top.name === "session" ? router.top.id : "");
  let empty = $state(false);
</script>

<Sheet label={copy.model.title} title={copy.model.title} onclose={() => router.close()}>
  <div data-phone-desk class="pb-2">
    <ModelSettings sessionId={id} layout="list" locked={!can("agentSetting")} bind:empty />
  </div>
  {#if empty}
    <EmptyState size="sheet" icon="cpu" title={copy.model.unavailable.title} body={copy.model.unavailable.body} />
  {/if}
</Sheet>
