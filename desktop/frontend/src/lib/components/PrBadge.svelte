<script lang="ts">
  import type { SessionInfo } from "$lib/store.svelte";

  // `status` is optional so the non-table call sites keep the full badge. Pass it
  // wherever the status pill is visible on the same row: the delivery axis is
  // DERIVED from these very facts (DeriveDelivery in internal/state), so a
  // `ci failed` pill next to a `✕ci` glyph is one fact printed twice in two
  // notations. Suppressing the implied glyph leaves the badge saying only what
  // the pill does not — most usefully "✓" on a review_pending row, which is CI
  // green while a human sits on it, something no status word covers.
  let {
    session,
    status = "",
  }: { session: Pick<SessionInfo, "prNumber" | "checks" | "review">; status?: string } = $props();

  const checksImplied = $derived(
    (status === "ci_failed" && session.checks === "fail") ||
      (status === "ci_pending" && session.checks === "pending"),
  );
  const reviewImplied = $derived(
    (status === "approved" && session.review === "APPROVED") ||
      (status === "changes_requested" && session.review === "CHANGES_REQUESTED"),
  );
</script>

{#if session.prNumber > 0}
  <!-- `num`: the PR number sits in a table column and must not reflow its
       neighbours when a wider one arrives on the next observer push. -->
  <span class="num inline-flex items-center gap-1 align-middle whitespace-nowrap text-sm">
    <span class="text-magenta">#{session.prNumber}</span>
    {#if !checksImplied}
      {#if session.checks === "pass"}<span class="text-good" title="checks pass">✓</span>
      {:else if session.checks === "fail"}<span class="text-bad" title="checks failed">✕ci</span>
      {:else if session.checks === "pending"}<span class="text-warn" title="checks running">⧗</span>{/if}
    {/if}
    {#if !reviewImplied}
      {#if session.review === "APPROVED"}<span class="text-good" title="approved">✓rev</span>
      {:else if session.review === "CHANGES_REQUESTED"}<span class="text-bad" title="changes requested">✕rev</span>{/if}
    {/if}
  </span>
{:else}
  <span class="text-sm text-faint">—</span>
{/if}
