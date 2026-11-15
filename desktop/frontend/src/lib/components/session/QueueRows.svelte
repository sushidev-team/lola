<script lang="ts">
  import Icon from "$lib/components/Icon.svelte";
  // What lola holds for the agent, directly above the composer. Unboxed, so it never reads as a
  // second composer. Delivered means it shows up in the story, so a row leaving here says as much
  // as anything could: lola cannot know a message was read.
  import { ssh } from "$lib/ssh.svelte";
  import { store } from "$lib/store.svelte";
  import { talk } from "$lib/talk.svelte";
  import { queuedItems } from "$lib/composer";
  import { youAttachments } from "$lib/composerpaste";
  import Button from "../Button.svelte";
  import { touchSurface } from "$lib/touchsurface";

  let { sessionId }: { sessionId: string } = $props();

  const view = $derived(ssh.view(sessionId));
  const rows = $derived(queuedItems(view.items));
  const held = $derived(!!talk.state(sessionId)?.queueHeld);
  let sending = $state(false);
  // The phone's tray is one header row until it is opened.
  let expanded = $state(false);
  // On the phone the row text is the edit target; the desktop keeps selectable text plus Edit.
  const phone = touchSurface();
  const reason = $derived(view.queueWhy || talk.state(sessionId)?.deferred || "");
  async function sendNow() {
    if (sending) return;
    sending = true;
    view.queueWhy = "";
    try { await ssh.sendHeld(sessionId); } finally { sending = false; }
  }
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

{#if rows.length && phone}
  <!-- Send now only releases the queue to the agent through the same gate as any message (it
       defers, never stops a turn), so there is nothing to confirm. -->
  <div class="mx-0 grid rounded-[14px] bg-edge-soft px-3" data-queue-rows data-queue-phone>
    <div class="flex min-h-11 min-w-0 items-center justify-between gap-2" data-queue-send>
      <button type="button" class="flex min-h-11 min-w-0 items-center gap-1.5 text-left" aria-expanded={expanded} onclick={() => (expanded = !expanded)} data-queue-toggle>
        <span class="text-micro uppercase text-faint" data-queue-title>{held ? "Held" : "Queued"} · {rows.length}</span>
        <span class="text-faint transition-transform motion-reduce:transition-none {expanded ? 'rotate-90' : ''}" aria-hidden="true"><Icon name="chevron-right" size={14} /></span>
      </button>
      <button type="button" class="min-h-11 shrink-0 px-1 text-subhead text-accent disabled:opacity-40" disabled={!store.alive || sending} onclick={sendNow}>Send now</button>
    </div>
    <!-- The rows are in place while expanded; the keyboard being up folds them again (screen CSS). -->
    {#if expanded}
      <div class="grid" data-queue-body>
        {#if attached}
          <p class="pb-1 text-footnote text-faint" data-queue-reason>Leave the terminal to send here.</p>
        {:else if reason}
          <p class="pb-1 text-footnote text-faint" data-queue-reason role="status">{reason}</p>
        {/if}
        {#each rows as it (it.id)}
          <div class="grid min-h-11 min-w-0 grid-cols-[minmax(0,1fr)_44px] items-center border-t border-edge" data-queue-row={it.id}>
            <button type="button" class="selectable min-h-11 min-w-0 truncate text-left text-callout text-ink" title={it.human?.text} disabled={!free || sending} onclick={() => edit(it.id, it.human?.text ?? "")} data-queue-text>{youAttachments(it.human?.text ?? "").text}</button>
            <button type="button" class="grid size-11 place-items-center text-faint disabled:opacity-40" disabled={sending} aria-label="Recall this message" title="Recall this message" onclick={() => ssh.recall(sessionId, it.id)}><Icon name="x" size={16} /></button>
          </div>
        {/each}
      </div>
    {/if}
  </div>
{:else if rows.length}
  <!-- The label sits in the rows' own gutter, the column a tool row's verb is in, so the text lines
       up with the targets above and a header row cannot float away from what it names. -->
  <div class="grid" data-queue-rows>
    {#each rows as it, i (it.id)}
      <div class="grid h-6 min-w-0 grid-cols-[56px_minmax(0,1fr)_auto_auto] items-center gap-x-2 text-sm" data-queue-row={it.id}>
        <span class="text-faint"><span data-desk-only>{i === 0 ? (held ? "Held" : "Queued") : ""}</span><span hidden data-phone-only data-queue-clock><Icon name="clock" size={16} /></span></span>
        <!-- On the phone the text is the edit target; the 20pt Edit button is not drawn there. -->
        {#if phone}
          <button type="button" class="selectable min-w-0 truncate text-left text-ink-2" title={it.human?.text} disabled={!free || sending} onclick={() => edit(it.id, it.human?.text ?? "")} data-queue-text>{youAttachments(it.human?.text ?? "").text}</button>
        {:else}
          <span class="selectable truncate text-ink-2" title={it.human?.text} data-queue-text>{youAttachments(it.human?.text ?? "").text}</span>
        {/if}
        <Button size="xs" class="h-5!" data-queue-edit disabled={!free || sending} title={free ? "Back to the field" : "The field has text in it"} onclick={() => edit(it.id, it.human?.text ?? "")}>Edit</Button>
        <Button size="xs" icon class="h-5! w-5!" disabled={sending} aria-label="Recall this message" title="Recall this message" onclick={() => ssh.recall(sessionId, it.id)}><Icon name="x" size={14} /></Button>
      </div>
    {/each}
    <div class="flex min-w-0 items-center gap-2 pl-16" data-queue-send>
      <span hidden data-phone-only data-queue-title>{held ? "Held" : "Queued"} · {rows.length}</span>
      <Button size="xs" variant="accent" loading={sending} disabled={!store.alive} onclick={sendNow}>Send now</Button>
      {#if attached}
        <span class="text-sm text-faint" data-queue-reason>Leave the terminal to send here.</span>
      {:else if reason}
        <span class="text-sm text-faint" data-queue-reason role="status">{reason}</span>
      {/if}
    </div>
  </div>
{/if}
