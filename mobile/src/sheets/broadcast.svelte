<script lang="ts">
  import { displayFor } from "$lib/theme";
  import { store } from "$lib/store.svelte";
  import ListGroup from "@mobile/lib/components/ListGroup.svelte";
  import Sheet from "@mobile/lib/components/Sheet.svelte";
  import { copy, fill } from "@mobile/lib/copy";
  import { router } from "@mobile/lib/router.svelte";

  // One message to every live agent of the session's project (spec §5.3). The
  // daemon types it where an agent rests and queues it elsewhere.

  const id = $derived(router.top.name === "session" ? router.top.id : "");
  const project = $derived(store.sessionById(id)?.project ?? "");
  /**
   * Agents the message would reach, as the daemon counts them (`broadcastTarget`): the agent is alive
   * (not gone, a shell or orphaned) and its PR is not already merged or closed. No project = all projects.
   */
  const agents = $derived(
    store.sessions.filter((s) => {
      if (project && s.project !== project) return false;
      const d = displayFor(s.agentState);
      return d !== "gone" && d !== "shell" && d !== "orphaned" && s.delivery !== "merged" && s.delivery !== "closed";
    }).length,
  );
  let message = $state("");
  let busy = $state(false);

  async function send(): Promise<void> {
    if (busy || !project || agents === 0 || !message.trim()) return;
    busy = true;
    const r = await store.broadcast(project, message.trim());
    busy = false;
    if (r) router.close();
  }
</script>

<Sheet
  label={copy.broadcast.title}
  title={copy.broadcast.title}
  dirty={message.trim() !== ""}
  action={{ label: copy.broadcast.send, onclick: () => void send(), disabled: busy || agents === 0 || !message.trim() }}
  onclose={() => router.close()}
>
  <form class="flex flex-col gap-2 pb-2" onsubmit={(e) => { e.preventDefault(); void send(); }}>
    <p class="px-gutter text-subhead text-faint">{agents === 0 ? copy.broadcast.none : fill(copy.broadcast.meta, { n: agents })}</p>
    <ListGroup>
      <textarea
        class="min-h-24 text-body text-ink outline-none placeholder:text-placeholder"
        aria-label={copy.broadcast.title}
        placeholder={copy.broadcast.placeholder}
        bind:value={message}
        disabled={busy}
      ></textarea>
    </ListGroup>
  </form>
</Sheet>
