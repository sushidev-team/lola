<script lang="ts">
  import Skeleton from "./Skeleton.svelte";
  import Icon from "./Icon.svelte";
  import { store } from "$lib/store.svelte";
  import { gitTargetProject } from "$lib/workspacenav";
  // The project Git is about to open on, so the title is right in the first frame.
  const title = $derived.by(() => { const p = gitTargetProject(); return p ? store.displayNameFor(p.name) : ""; });
  // What the Git workspace looks like before it has mounted: a toolbar, the branch rail and the
  // history. Drawn from plain placeholders so it paints in the same frame as the switch, while the
  // real workspace loads behind it.
  const rail = [[70, 80, 52, 66], [60, 74], [56]];
  const rows = [76, 56, 88, 65, 48, 80, 60, 72, 52, 84, 62, 70];
</script>

<div class="grid h-full grid-rows-[44px_minmax(0,1fr)]" role="status" aria-label="Loading Git workspace">
  <div class="flex items-center gap-3 border-b border-edge/70 px-4"><span class="flex size-7 items-center justify-center text-faint"><Icon name="chevron-left" size={16} /></span>{#if title}<h1 class="truncate text-base font-semibold text-ink">{title}</h1>{:else}<Skeleton width="120px" height="14px" />{/if}<span class="ml-auto flex gap-2"><Skeleton width="64px" height="24px" /><Skeleton width="64px" height="24px" /><Skeleton width="72px" height="24px" /></span></div>
  <div class="grid min-h-0 grid-cols-[260px_minmax(0,1fr)]">
    <div class="grid content-start gap-5 border-r border-edge/70 p-4">
      {#each rail as group}
        <div class="grid gap-2.5"><Skeleton width="48px" height="10px" />{#each group as w}<div class="flex items-center gap-2"><Skeleton width="12px" height="12px" /><Skeleton width="{w}%" height="11px" /></div>{/each}</div>
      {/each}
    </div>
    <div class="grid content-start gap-3.5 p-4">
      {#each rows as w, i}
        <div class="flex items-center gap-4"><Skeleton width="18px" height="18px" /><Skeleton width="{w}%" height="11px" /><span class="ml-auto"><Skeleton width="{40 + (i % 3) * 14}px" height="11px" /></span></div>
      {/each}
    </div>
  </div>
</div>
