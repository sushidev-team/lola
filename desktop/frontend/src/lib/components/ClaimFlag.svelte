<script lang="ts">
  import type { SessionInfo } from "$lib/store.svelte";
  import { hasMismatch } from "$lib/board";

  // The compact half of the claim audit: a warning beside a row (list, kanban
  // card) whose agent claimed something the evidence does not back — "tests
  // pass" with no test run in its transcript, or with CI failing. Unlike the
  // plan chip it survives a PR, because a false "tests pass" matters most then.
  // Not a <button> (the kanban card already is one); the sentences are the
  // tooltip, and the session sidebar (BoardPanel) shows them in full.
  let { session }: { session: SessionInfo } = $props();
  const b = $derived(session.board);
</script>

{#if hasMismatch(b)}
  <span
    class="inline-flex items-center gap-1 text-sm whitespace-nowrap text-warn"
    title={`lola could not confirm the agent's claim:\n${b.mismatches!.join("\n")}`}
    ><span aria-hidden="true">⚠</span> Unverified claim</span
  >
{/if}
