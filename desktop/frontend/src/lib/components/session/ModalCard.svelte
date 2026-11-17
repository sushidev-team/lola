<script lang="ts">
  // A screen in the agent's pane that only takes keys. It has no field, ever: typed prose is
  // swallowed by the widget and the Enter answers the dialog, which is the opposite of a composer.
  // Fallback when the live terminal is unavailable; it never guesses approval keys.
  import type { AgentPrompt } from "@bindings/internal/protocol";
  import type { Snippet } from "svelte";
  import { store } from "$lib/store.svelte";
  import { ssh, sshClock } from "$lib/ssh.svelte";
  import { agentName } from "$lib/ssh";
  import { elapsedWords } from "$lib/sessionview";
  import { toMillis } from "$lib/time";
  import { touchSurface } from "$lib/touchsurface";
  import Button from "../Button.svelte";

  const MAX_LINES = 8;
  // The phone has no Dismiss and no Esc: one full-width button moves the screen to its Terminal segment.
  const phone = touchSurface();
  const PHONE_LINES = 6;

  let { sessionId, prompt, pager }: { sessionId: string; prompt: AgentPrompt; pager?: Snippet } = $props();

  const agent = $derived(agentName(store.sessionById(sessionId)?.agent));
  const view = $derived(ssh.view(sessionId));
  const since = $derived(toMillis(prompt.since as unknown as string));
  const open = $derived(Number.isFinite(since) && since > 0 ? elapsedWords(sshClock.now - since) : "");

  function onKey(e: KeyboardEvent) {
    if (e.key !== "Escape") return;
    e.preventDefault();
    e.stopPropagation();
    view.composerFocus++;
  }
</script>

{#if phone}
  <section data-card="modal" tabindex="-1" aria-label="Choose in terminal" class="ssh-fade-in grid gap-3 rounded-[14px] border bg-panel p-4 outline-none" style="border-color: color-mix(in srgb, var(--color-warn) 40%, transparent)">
    <div class="flex min-w-0 items-baseline gap-2">
      <h3 class="min-w-0 flex-1 text-headline text-warn">Choose in terminal</h3>
      {#if open}<span class="num shrink-0 text-footnote text-faint">{open}</span>{/if}
    </div>
    <div class="overflow-hidden rounded-lg bg-crust p-3 font-mono text-[13px] leading-[18px] text-ink-2" data-modal-lines>
      {#each (prompt.lines ?? []).slice(0, PHONE_LINES) as line, i (i)}
        <p class="selectable truncate whitespace-pre">{line || " "}</p>
      {/each}
    </div>
    <button type="button" class="grid h-[50px] w-full place-items-center rounded-xl bg-primary-fill text-callout font-semibold text-on-primary active:opacity-80" onclick={() => ssh.showAgentTerminal(sessionId)}>Open terminal</button>
  </section>
{:else}
<!-- svelte-ignore a11y_no_noninteractive_element_interactions, a11y_no_noninteractive_tabindex -->
<section data-card="modal" tabindex="-1" aria-label="{agent} opened a screen that only takes keys" class="ssh-fade-in grid gap-2 rounded-[10px] border border-orange bg-panel px-3 py-2 outline-none focus-visible:outline-2 focus-visible:outline-offset-1 focus-visible:outline-orange" onkeydown={onKey}>
  <div class="flex min-w-0 items-center gap-2">
    <h3 class="min-w-0 truncate font-medium text-orange">{agent} opened a screen that only takes keys</h3>
    <span class="ml-auto flex shrink-0 items-center gap-2 text-sm text-faint">
      {#if open}<span class="num">open {open}</span>{/if}
      {#if pager}{@render pager()}{/if}
    </span>
  </div>
  <div class="overflow-hidden font-mono text-sm text-faint" data-modal-lines>
    {#each (prompt.lines ?? []).slice(0, MAX_LINES) as line, i (i)}
      <p class="selectable truncate whitespace-pre">{line || " "}</p>
    {/each}
  </div>
  <div class="flex justify-end">
    <Button variant="primary" size="sm" onclick={() => ssh.showAgentTerminal(sessionId)}>Open terminal <span class="text-faint" data-chord>⌘\</span></Button>
  </div>
</section>
{/if}
