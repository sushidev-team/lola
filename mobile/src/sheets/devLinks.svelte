<script lang="ts">
  import { store } from "$lib/store.svelte";
  import DevLinksSheet from "@mobile/lib/components/DevLinksSheet.svelte";
  import { can } from "@mobile/lib/tiers.svelte";
  import { openExternal } from "@mobile/lib/openurl";
  import { router } from "@mobile/lib/router.svelte";

  // The session's dev servers as this phone can reach them (`?sheet=devLinks`).
  // Links open on the phone; a link the browser refuses leaves the sheet up.

  const session = $derived(store.sessionById(router.top.name === "session" ? router.top.id : ""));
  const forwards = $derived(session?.devForwards ?? []);

  /** An empty sheet offers to start the server only when there is one to start and this phone may. */
  const canStart = $derived((session?.devCommands?.length ?? 0) > 0 && can("dev"));

  let notice = $state("");

  function start(): void {
    if (!session) return;
    void store.dev(session.id, true);
    router.close();
  }

  async function open(url: string): Promise<void> {
    if (await openExternal(url)) router.close();
    else notice = "Couldn't open that link.";
  }
</script>

<DevLinksSheet {forwards} {notice} onstart={canStart ? start : undefined} onopen={(url) => void open(url)} onclose={() => router.close()} />
