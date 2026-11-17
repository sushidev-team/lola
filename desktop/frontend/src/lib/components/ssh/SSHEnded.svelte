<script lang="ts">
  // A direct session whose every host has ended has nobody left to talk to, so this says what can
  // still happen next, in place of the composer. Anywhere else the agent is still there and the
  // composer stays: a new connection is on the host bar.
  import { store } from "$lib/store.svelte";
  import { ssh } from "$lib/ssh.svelte";
  import { terms, AGENT } from "$lib/terms.svelte";
  import { SSH_ENDED_OUTLIVES } from "$lib/ssh";
  import Button from "../Button.svelte";

  let { sessionId }: { sessionId: string } = $props();

  const view = $derived(ssh.view(sessionId));
  const session = $derived(store.sessionById(sessionId));
  const direct = $derived(session?.location === "ssh");
  const conn = $derived(view.active());
  const ran = $derived((conn?.commandsRun ?? 0) > 0);
</script>

<div class="grid gap-2 rounded-[10px] border border-edge bg-panel p-3" data-ended>
  <div class="grid gap-0.5">
    <p class="text-ink">Connection closed. The record stays until you delete this tab.</p>
    {#if ran}<p class="text-sm text-faint">{SSH_ENDED_OUTLIVES}</p>{/if}
  </div>
  <div class="flex flex-wrap gap-2">
    <Button variant="secondary" size="sm" onclick={() => ssh.connectHost(sessionId)}>New connection</Button>
    {#if direct}
      <Button size="sm" onclick={() => store.askKill(sessionId)}>End session…</Button>
    {:else}
      <Button size="sm" onclick={() => terms.select(sessionId, AGENT)}>Back to Agent</Button>
    {/if}
  </div>
</div>
