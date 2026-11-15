<script lang="ts">
  import { store } from "$lib/store.svelte";
  import ListGroup from "@mobile/lib/components/ListGroup.svelte";
  import ListRow from "@mobile/lib/components/ListRow.svelte";
  import Sheet from "@mobile/lib/components/Sheet.svelte";
  import { copy } from "@mobile/lib/copy";
  import { router } from "@mobile/lib/router.svelte";

  // The coding agents, the current one checked and not selectable. The shared
  // confirm asks before anything moves.

  const KINDS = ["claude", "codex", "opencode"] as const;
  const id = $derived(router.top.name === "session" ? router.top.id : "");
  const current = $derived(store.sessionById(id)?.agent || "claude");

  function pick(kind: string): void {
    router.close();
    store.askSwitchAgent(id, kind);
  }
</script>

<Sheet label={copy.session.menu.switchAgent} title={copy.session.menu.switchAgent} onclose={() => router.close()}>
  <ListGroup>
    {#each KINDS as kind (kind)}
      <ListRow
        label={copy.agents[kind]}
        current={kind === current}
        onclick={() => pick(kind)}
      />
    {/each}
  </ListGroup>
</Sheet>
