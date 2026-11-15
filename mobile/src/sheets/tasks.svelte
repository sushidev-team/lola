<script lang="ts">
  import SessionTasks from "$lib/components/session/SessionTasks.svelte";
  import Sheet from "@mobile/lib/components/Sheet.svelte";
  import { copy } from "@mobile/lib/copy";
  import { router } from "@mobile/lib/router.svelte";

  // The shared task board (spec §5.3). The body keeps the 16pt gutter: nothing starts left of it.
  // The board's tabs, '+ Add' and empty block take their phone look from `[data-phone-desk]`.

  const id = $derived(router.top.name === "session" ? router.top.id : "");
</script>

<Sheet label={copy.tasks.title} title={copy.tasks.title} onclose={() => router.close()}>
  <div data-phone-desk class="px-gutter pb-2">
    <SessionTasks sessionId={id} emptyText={copy.tasks.empty.title} />
  </div>
</Sheet>

<style>
  /* 12px between the segmented control and "+ Add". The Add trigger lives inside SessionTasks
     (desktop code), so it cannot move to the sheet header from here; only its gap is ours. */
  :global([data-phone-desk] .task-toolbar .phone-add) {
    margin-left: 12px;
  }
</style>
