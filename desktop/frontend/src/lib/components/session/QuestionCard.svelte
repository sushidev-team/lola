<script lang="ts">
  // An agent asking a person to choose. The same shape as a permission, with the question to read
  // rather than a command to check.
  //
  // One press only answers a single choice. A multi-select takes several keys and a Submit, and
  // lola will not guess at that, so it shows the question and sends a person to the terminal. A
  // "Type something" row is the same: pressing it would leave the pane waiting for text.
  import type { AgentPrompt } from "@bindings/internal/protocol";
  import type { Snippet } from "svelte";
  import { store } from "$lib/store.svelte";
  import { ssh } from "$lib/ssh.svelte";
  import { agentName } from "$lib/ssh";
  import Button from "../Button.svelte";
  import ChoiceCard from "./ChoiceCard.svelte";

  let { sessionId, prompt, narrow = false, pager }: { sessionId: string; prompt: AgentPrompt; narrow?: boolean; pager?: Snippet } = $props();

  const agent = $derived(agentName(store.sessionById(sessionId)?.agent));
  const view = $derived(ssh.view(sessionId));
  const multi = $derived(prompt.kind === "question_multi");

  function onKey(e: KeyboardEvent) {
    if (e.key !== "Escape") return;
    e.preventDefault();
    e.stopPropagation();
    view.composerFocus++;
  }
</script>

{#if multi}
  <!-- svelte-ignore a11y_no_noninteractive_element_interactions, a11y_no_noninteractive_tabindex -->
  <section data-card="question_multi" tabindex="-1" aria-label="{agent} has a question" class="ssh-fade-in grid gap-2 rounded-[10px] border border-orange bg-panel px-3 py-2 outline-none focus-visible:outline-2 focus-visible:outline-offset-1 focus-visible:outline-orange" onkeydown={onKey}>
    <div class="flex min-w-0 items-center gap-2">
      <h3 class="min-w-0 truncate font-medium text-orange">{agent} has a question</h3>
      {#if pager}<span class="ml-auto">{@render pager()}</span>{/if}
    </div>
    <div class="min-h-0 overflow-y-auto" data-card-body>
      {#each prompt.lines ?? [] as line, i (i)}
        <p class="selectable text-prose [overflow-wrap:anywhere] whitespace-pre-wrap text-ink" data-prompt-line>{line || " "}</p>
      {/each}
      <p class="text-sm text-faint">It takes several choices, and lola only presses one key.</p>
    </div>
    <div class="flex justify-end">
      <Button variant="secondary" size="sm" onclick={() => ssh.showAgentTerminal(sessionId)}>Answer in terminal <span class="text-faint">⌘\</span></Button>
    </div>
  </section>
{:else}
  <ChoiceCard {sessionId} {prompt} title="{agent} has a question" reading {narrow} {pager} toTerminal={(l) => /^Type something/i.test(l)} />
{/if}
