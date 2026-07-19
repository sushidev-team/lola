<script lang="ts">
  import { store, type SessionInfo } from "$lib/store.svelte";
  import { nav } from "$lib/nav.svelte";
  import { isAttention, reactingText } from "$lib/theme";
  import StatusPill from "./StatusPill.svelte";
  import PrBadge from "./PrBadge.svelte";

  let { rows, dense = false }: { rows: SessionInfo[]; dense?: boolean } = $props();
</script>

<div class="min-w-0 text-xs">
  <table class="w-full border-separate border-spacing-0">
    <thead class="sticky top-0 bg-panel/90 backdrop-blur">
      <tr class="text-left text-[10px] tracking-wider text-faint uppercase">
        <th class="w-4 py-1 pl-2"></th>
        <th class="py-1 pr-2">Issue</th>
        {#if !dense}<th class="py-1 pr-2">Title</th>{/if}
        <th class="py-1 pr-2">Project</th>
        <th class="py-1 pr-2">Status</th>
        <th class="py-1 pr-2">PR</th>
        {#if !dense}<th class="py-1 pr-2">Reacting</th>{/if}
        <th class="py-1 pr-2 text-right">Age</th>
      </tr>
    </thead>
    <tbody>
      {#each rows as s (s.id)}
        {@const sel = nav.selectedId === s.id}
        <tr
          class="cursor-pointer border-b border-edge/30 hover:bg-sel/60"
          class:bg-sel={sel}
          onclick={() => nav.select(s.id)}
          ondblclick={() => nav.toggleFocusTerm(s.id)}
        >
          <td class="py-1 pl-2 text-center">
            {#if sel}<span class="font-bold text-accent">›</span>
            {:else if isAttention(s.status) && s.status === "needs_input"}<span class="text-warn">!</span>{/if}
          </td>
          <td class="py-1 pr-2 font-medium whitespace-nowrap" class:text-accent={sel}>{s.issue || s.id.slice(0, 8)}</td>
          {#if !dense}
            <td class="max-w-[22rem] truncate py-1 pr-2 text-faint">{s.title}</td>
          {/if}
          <td class="py-1 pr-2 whitespace-nowrap text-faint">{s.project}</td>
          <td class="py-1 pr-2"><StatusPill status={s.status} /></td>
          <td class="py-1 pr-2"><PrBadge session={s} /></td>
          {#if !dense}
            <td class="py-1 pr-2 whitespace-nowrap {reactingText(s.reacting)}">{s.reacting}</td>
          {/if}
          <td class="py-1 pr-2 text-right whitespace-nowrap text-faint tabular-nums">{s.age}</td>
        </tr>
      {/each}
    </tbody>
  </table>

  {#if rows.length === 0}
    <div class="px-3 py-6 text-center text-faint">no sessions observed</div>
  {/if}
</div>
