<script lang="ts">
  import type { Rollout } from "@bindings/internal/goalpolicy";
  let { rollout, members }: { rollout: Rollout; members: {id: string; repo: string; target: string}[] } = $props();
</script>

<section aria-label="Reviewed rollout contract" class="space-y-2 text-sm">
  <h4 class="font-medium text-ink">Reviewed rollout contract</h4>
  <p class="text-faint">Preserve compatibility between merges. Final merge and deployment decisions stay with you.</p>
  <ul class="list-disc space-y-1 pl-5 text-ink">{#each rollout.compatibility ?? [] as requirement}<li class="selectable whitespace-pre-wrap break-words">{requirement}</li>{/each}</ul>
  <h5 class="pt-1 font-medium text-ink">Human merge order</h5>
  <ol aria-label="Human merge order" class="list-decimal space-y-1 pl-5 text-faint">{#each rollout.merge_order ?? [] as id}
    {@const member = members.find(m => m.id === id)}
    <li class="selectable break-words"><span class="text-ink">{id}</span>{#if member}{` · ${member.repo || id} → ${member.target}`}{/if}</li>
  {/each}</ol>
  <p class="text-faint">Skip repositories with no changes; confirm the intermediate combinations before merging.</p>
</section>
