<script lang="ts">
  import type { Improvement } from "@bindings/internal/knowledge";
  let { proposal }: { proposal: Improvement } = $props();
</script>
<div class="min-w-0 space-y-2 text-sm">
  <h4 class="break-words font-medium text-ink">{proposal.topic}</h4>
  <p class="selectable whitespace-pre-wrap break-words text-ink">{proposal.problem}</p>
  <p class="break-words text-faint">{proposal.state} · {proposal.trigger} · {proposal.occurrences} distinct reporting sessions or attributable sources. Repetition is not independent proof.</p>
  <p class="break-words text-faint">Scope: {proposal.scope.project}{proposal.scope.goal_id ? ` · goal ${proposal.scope.goal_id}` : " · project"}. Paths: {(proposal.scope.paths ?? []).join(", ") || "all paths"}.</p>
  {#if proposal.trigger === "policy"}<p class="text-warn">This proposes a mandatory policy change. It requires normal owner/code review; triage does not authorize installing a rule.</p>{/if}
  {#each proposal.suggestions ?? [] as idea}<div class="space-y-1 pt-2"><h5 class="font-medium text-ink">{idea.kind === "regression_test" ? "Regression test" : idea.kind === "lint" ? "Lint rule" : "Reusable skill"}</h5><p class="selectable whitespace-pre-wrap break-words text-faint">{idea.change}</p><p class="selectable whitespace-pre-wrap break-words text-faint">Validation: {idea.validation}</p></div>{/each}
  {#if proposal.omitted_suggestions}<p class="text-faint">{proposal.omitted_suggestions} additional approaches are in the exact source lessons. Inspect them before choosing an implementation.</p>{/if}
  <p class="selectable break-all text-faint">Proposal {proposal.id}<br />Revision {proposal.revision}</p>
</div>
