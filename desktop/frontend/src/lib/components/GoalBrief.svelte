<script lang="ts">
  import type { BriefContract } from "@bindings/internal/goal";
  let { brief, label }: { brief: BriefContract; label: string } = $props();
</script>
<section aria-label={label} class="min-w-0 space-y-2 text-sm">
  <h5 class="font-medium text-ink">{label}</h5>
  <p class="selectable break-words text-ink">{brief.title}</p>
  <p class="selectable whitespace-pre-wrap break-words text-faint">{brief.outcome}</p>
  <ul class="list-disc space-y-1 pl-5 text-faint">{#each brief.acceptance ?? [] as item}<li class="selectable break-words">{item}</li>{/each}</ul>
  {#if brief.non_goals?.length}<p class="text-ink">Outside this goal</p><ul class="list-disc space-y-1 pl-5 text-faint">{#each brief.non_goals as item}<li class="selectable break-words">{item}</li>{/each}</ul>{/if}
</section>
