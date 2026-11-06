<script lang="ts">
  // What lola could not read of the agent's log. The agent is fine either way, and the composer
  // does not depend on any of this, so each sentence says so and carries the way to the rest.
  import { store } from "$lib/store.svelte";
  import { ssh } from "$lib/ssh.svelte";
  import { agentName } from "$lib/ssh";
  import { talk } from "$lib/talk.svelte";
  import Button from "../Button.svelte";

  let { sessionId, health }: { sessionId: string; health: string } = $props();

  const agent = $derived(agentName(store.sessionById(sessionId)?.agent));
  const unreadable = $derived(health === "unreadable" || health === "unsupported");
  // What the log's reader is called: the plan's sentence names the program, not the model.
  const program = $derived(agent === "Claude" ? "Claude Code" : agent);
  // The daemon answered "unknown cmd": nothing is wrong with the log, and blaming it sends a person
  // looking in the wrong place. The app can be newer than a daemon that has not restarted.
  const outdated = $derived(talk.unsupported(sessionId));
</script>

<div class="cv-band">
  {#if health === "partial"}
    <p class="flex h-6 items-center gap-2 text-sm text-faint" data-feed-notice="partial">
      Some of this conversation could not be read. The terminal has all of it.
      <Button size="xs" onclick={() => ssh.showAgentPane(sessionId)}>Terminal <span class="text-faint" aria-hidden="true">⌘\</span></Button>
    </p>
  {:else if outdated}
    <p class="flex items-center gap-2 py-2 text-sm text-faint" data-feed-notice="outdated">
      <span><span class="text-warn">The running daemon is older than this app.</span> Restart it to read this conversation. Your agent keeps running.</span>
      <Button variant="secondary" size="xs" onclick={() => store.restartDaemon()}>Restart</Button>
      <Button size="xs" onclick={() => ssh.showAgentPane(sessionId)}>Terminal</Button>
    </p>
  {:else if unreadable}
    <p class="flex items-center gap-2 py-2 text-sm text-faint" data-feed-notice="unreadable">
      <span><span class="text-warn">This {program} version's log isn't readable.</span> Your agent is fine.</span>
      <Button variant="secondary" size="xs" onclick={() => ssh.showAgentPane(sessionId)}>Terminal</Button>
    </p>
  {/if}
</div>
