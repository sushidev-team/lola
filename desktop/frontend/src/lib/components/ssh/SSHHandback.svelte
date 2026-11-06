<script lang="ts">
  // Handing back. Replaces the composer in place, not a modal: you need the terminal
  // and the stream in view while you decide what to say.
  //
  // Two equal buttons, both secondary, with no default. Whether what you did goes to
  // a model provider is an explicit choice each time, which is why this is not a
  // checkbox with a remembered state. No key chooses for you: ↩ in the note does
  // nothing and ⌘↩ is not bound. What is sent is exactly this text field, never a
  // fresh capture.
  import { tick } from "svelte";
  import { store } from "$lib/store.svelte";
  import { ssh } from "$lib/ssh.svelte";
  import {
    SSH_MAX_HANDBACK_BYTES,
    SSH_TRANSCRIPT_WARNING,
    agentName,
    byteLength,
    clockTime,
    formatBytes,
  } from "$lib/ssh";
  import Button from "../Button.svelte";

  let { sessionId }: { sessionId: string } = $props();

  const view = $derived(ssh.view(sessionId));
  const agent = $derived(agentName(store.sessionById(sessionId)?.agent));
  const h = $derived(view.handback);
  const bytes = $derived(byteLength(h?.transcript ?? ""));
  const over = $derived(bytes > SSH_MAX_HANDBACK_BYTES);
  const empty = $derived(!h?.loading && !(h?.transcript ?? "").trim());

  let noteEl = $state<HTMLTextAreaElement | null>(null);
  $effect(() => {
    if (h) void tick().then(() => noteEl?.focus());
  });

  function keep() {
    ssh.closeHandback(sessionId);
    view.terminalFocus++;
  }

  function onKey(e: KeyboardEvent) {
    if (e.key === "Escape") {
      e.preventDefault();
      e.stopPropagation();
      keep();
    }
  }
</script>

{#if h}
  <!-- svelte-ignore a11y_no_noninteractive_element_interactions -->
  <section class="ssh-fade-in grid gap-2 rounded-[10px] border border-edge bg-panel p-3" aria-label="Hand back to {agent}" onkeydown={onKey}>
    <h3 class="label text-faint">Hand back to {agent}</h3>
    <textarea
      bind:this={noteEl}
      bind:value={h.note}
      rows="2"
      class="w-full resize-none rounded border border-edge bg-canvas px-2 py-1.5 text-ink outline-none placeholder:text-placeholder focus:border-accent focus-visible:outline-none!"
      placeholder="Say what you did or what to do next (optional)"
      aria-label="Say what you did or what to do next"
    ></textarea>
    <div class="flex items-baseline gap-2 text-sm text-faint">
      <span>Your terminal{#if h.since}{" "}since {clockTime(h.since)}{/if}</span>
      <span class="num ml-auto {over ? 'text-warn' : ''}">{formatBytes(bytes)} of {formatBytes(SSH_MAX_HANDBACK_BYTES)}</span>
    </div>
    {#if h.loading}
      <div class="grid gap-1.5 rounded border border-edge/60 bg-canvas px-2 py-2" aria-label="Reading your terminal…">
        <span class="text-sm text-faint">Reading your terminal…</span>
        <span class="h-3 w-3/4 rounded bg-sel/40" aria-hidden="true"></span>
        <span class="h-3 w-1/2 rounded bg-sel/40" aria-hidden="true"></span>
      </div>
    {:else}
      <textarea
        bind:value={h.transcript}
        rows="8"
        spellcheck="false"
        class="w-full resize-y rounded border border-edge bg-canvas px-2 py-1.5 font-mono text-sm text-ink outline-none focus:border-accent focus-visible:outline-none!"
        aria-label="Your terminal, as it would be sent"
      ></textarea>
    {/if}
    <div class="grid gap-0.5 text-sm text-faint">
      {#if h.failed}<p class="text-warn">Could not read your terminal. You can still hand back with a note.</p>{/if}
      {#if h.truncated}<p class="text-warn">Your terminal history did not go back far enough. This is what is left, and some of your session may be missing.</p>{/if}
      {#if over}<p class="text-warn">Only the last {formatBytes(SSH_MAX_HANDBACK_BYTES)} can be sent.</p>{/if}
      <p>{SSH_TRANSCRIPT_WARNING}</p>
    </div>
    <div class="flex flex-wrap items-center gap-2">
      <Button size="sm" onclick={keep}>Keep driving</Button>
      <span class="ml-auto flex gap-2">
        <Button variant="secondary" size="sm" disabled={h.busy} onclick={() => ssh.handback(sessionId, false)}>Hand back without it</Button>
        <Button
          variant="secondary"
          size="sm"
          disabled={h.busy || h.loading || empty}
          onclick={() => ssh.handback(sessionId, true)}>{empty ? "Nothing to send" : "Hand back with this text"}</Button
        >
      </span>
    </div>
  </section>
{/if}
