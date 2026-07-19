<script lang="ts">
  import type { SessionInfo } from "$lib/store.svelte";
  let { session }: { session: Pick<SessionInfo, "prNumber" | "checks" | "review"> } = $props();
</script>

{#if session.prNumber > 0}
  <span class="inline-flex items-center gap-1 whitespace-nowrap text-xs">
    <span class="text-magenta">#{session.prNumber}</span>
    {#if session.checks === "pass"}<span class="text-good" title="checks pass">✓</span>
    {:else if session.checks === "fail"}<span class="text-bad" title="checks failed">✕ci</span>
    {:else if session.checks === "pending"}<span class="text-warn" title="checks running">⧗</span>{/if}
    {#if session.review === "APPROVED"}<span class="text-good" title="approved">✓rev</span>
    {:else if session.review === "CHANGES_REQUESTED"}<span class="text-bad" title="changes requested">✕rev</span>{/if}
  </span>
{:else}
  <span class="text-faint">—</span>
{/if}
