<script lang="ts">
  // The hover card's body for one session's usage (UsageMark): the count as
  // the headline, then a label/value grid — today, size against history, the
  // burn rate while burning, and the estimate. Rows come from usageRows() so
  // the wording is tested without a DOM.
  import type { UsageInfo } from "@bindings/internal/protocol";
  import { usageHeadline, usageRows } from "$lib/usage";

  let { usage }: { usage: UsageInfo } = $props();
  const rows = $derived(usageRows(usage));
</script>

<div class="num mb-1.5 text-base font-medium text-ink">{usageHeadline(usage)}</div>
<dl class="grid grid-cols-[auto_1fr] gap-x-4 gap-y-1">
  {#each rows as r (r.label)}
    <dt class="text-faint">{r.label}</dt>
    <dd class="num {r.tone === 'hot' ? 'text-orange' : 'text-ink'}">
      {r.value}{#if r.note}<span class="ml-1 text-faint">{r.note}</span>{/if}
    </dd>
  {/each}
</dl>
