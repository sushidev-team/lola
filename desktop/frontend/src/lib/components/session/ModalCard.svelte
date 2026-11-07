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
  import Button from "../Button.svelte";

  const MAX_LINES = 8;

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
    <Button variant="primary" size="sm" onclick={() => ssh.showAgentTerminal(sessionId)}>Open terminal <span class="text-faint">⌘\</span></Button>
  </div>
</section>
