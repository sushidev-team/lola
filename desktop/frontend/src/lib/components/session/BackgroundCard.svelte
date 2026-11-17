<script lang="ts">
  import type { AgentPrompt } from "@bindings/internal/protocol";
  import type { Snippet } from "svelte";
  import { talk } from "$lib/talk.svelte";
  import { ssh } from "$lib/ssh.svelte";
  import Button from "../Button.svelte";
  import Icon from "../Icon.svelte";

  let { sessionId, prompt, disabled = false, pager }: {
    sessionId: string;
    prompt: AgentPrompt;
    disabled?: boolean;
    pager?: Snippet;
  } = $props();

  const pending = $derived(!!talk.backgroundPending[sessionId]);
  const error = $derived(talk.backgroundErrors[sessionId] ?? "");
</script>

<section data-card="background" aria-label="Command running" class="grid min-h-0 gap-3 overflow-y-auto rounded-[10px] border border-edge bg-panel px-3 py-3">
  <div class="flex min-w-0 items-center gap-2">
    <Icon name="terminal" size={16} class="shrink-0 text-cyan" />
    <h3 class="font-medium text-ink">Command running</h3>
    <span class="ml-auto flex shrink-0 items-center gap-1">
      {#if pager}{@render pager()}{/if}
      <Button size="xs" class="text-ink-2!" onclick={() => ssh.showAgentTerminal(sessionId)}>Terminal</Button>
    </span>
  </div>
  <pre class="selectable max-h-32 overflow-y-auto whitespace-pre-wrap font-mono text-sm text-ink [overflow-wrap:anywhere]">{(prompt.lines ?? []).join("\n")}</pre>
  <div class="flex flex-wrap items-center gap-x-4 gap-y-2">
    <p class="min-w-0 flex-1 text-sm text-ink-2">Let Claude continue while this command runs.</p>
    <Button size="sm" variant="secondary" loading={pending} {disabled} onclick={() => talk.background(sessionId, prompt.hash)}>
      {pending ? "Moving to background…" : "Move to background"}
    </Button>
  </div>
  {#if error}<p class="text-sm text-orange" role="status">{error}</p>{/if}
</section>
