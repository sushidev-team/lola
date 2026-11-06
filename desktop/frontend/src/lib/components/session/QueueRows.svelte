<script lang="ts">
  // What lola holds for the agent, directly above the composer. Unboxed, so it never reads as a
  // second composer. Delivered means it shows up in the story, so a row leaving here says as much
  // as anything could: lola cannot know a message was read.
  import { ssh } from "$lib/ssh.svelte";
  import { talk } from "$lib/talk.svelte";
  import { queuedItems } from "$lib/composer";
  import { youAttachments } from "$lib/composerpaste";
  import Button from "../Button.svelte";

  let { sessionId }: { sessionId: string } = $props();

  const view = $derived(ssh.view(sessionId));
  const rows = $derived(queuedItems(view.items));
  const held = $derived(!!talk.state(sessionId)?.queueHeld);
  const free = $derived(!view.agentDraft.trim());
  // The daemon's own reason, in the one case a person can do something about by moving.
  const attached = $derived(/detach this session's live terminals/.test(talk.state(sessionId)?.deferred ?? ""));

  async function edit(id: string, text: string) {
    if (!free) return;
    if (await ssh.recall(sessionId, id)) {
      view.agentDraft = text;
      view.runDefault = false;
      view.composerFocus++;
    }
  }
</script>

{#if rows.length}
  <!-- The label sits in the rows' own gutter, the column a tool row's verb is in, so the text lines
       up with the targets above and a header row cannot float away from what it names. -->
  <div class="grid" data-queue-rows>
    {#each rows as it, i (it.id)}
      <div class="grid h-6 min-w-0 grid-cols-[56px_minmax(0,1fr)_auto_auto] items-center gap-x-2 text-sm" data-queue-row={it.id}>
        <span class="text-faint">{i === 0 ? (held ? "Held" : "Queued") : ""}</span>
        <span class="selectable truncate text-ink-2" title={it.human?.text}>{youAttachments(it.human?.text ?? "").text}</span>
        <Button size="xs" class="h-5!" disabled={!free} title={free ? "Back to the field" : "The field has text in it"} onclick={() => edit(it.id, it.human?.text ?? "")}>Edit</Button>
        <Button size="xs" icon class="h-5! w-5!" aria-label="Recall this message" title="Recall this message" onclick={() => ssh.recall(sessionId, it.id)}>×</Button>
      </div>
    {/each}
    {#if attached}
      <p class="h-5 truncate pl-16 text-sm text-faint" data-queue-reason>waits until you leave the terminal</p>
    {:else if rows.length > 1 && !held}
      <p class="h-5 truncate pl-16 text-sm text-faint">sends together when it rests</p>
    {/if}
  </div>
{/if}
