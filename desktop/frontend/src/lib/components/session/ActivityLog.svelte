<script lang="ts">
  import type { Snippet } from "svelte";
  import { slide } from "svelte/transition";
  import { cubicOut } from "svelte/easing";
  import { feed } from "$lib/feed.svelte";
  import type { StoryRow } from "$lib/turns";
  import Chevron from "../Chevron.svelte";

  let { sessionId, id, rows, failed, children }: {
    sessionId: string; id: string; rows: StoryRow[]; failed: number; children: Snippet;
  } = $props();
  const f = $derived(feed.view(sessionId));
  const open = $derived(f.expanded.has(id));
  const counts = $derived.by(() => {
    const tools = rows.flatMap(row => row.kind === "run" ? row.items : row.kind === "tool" ? [row] : []);
    return [
      { kind: "run", label: "commands", n: tools.filter(t => t.tool.category === "run").length },
      { kind: "edit", label: "edits", n: tools.filter(t => t.tool.category === "edit").length },
      { kind: "read", label: "reads", n: tools.filter(t => t.tool.category === "read").length },
      { kind: "other", label: "calls", n: tools.filter(t => !["run", "edit", "read"].includes(t.tool.category)).length },
      { kind: "note", label: "updates", n: rows.filter(row => row.kind === "prose").length },
    ].filter(c => c.n > 0);
  });
  const contentId = $props.id();
  let animated = $state(false);
  function toggle() { animated = true; if (open) f.expanded.delete(id); else f.expanded.add(id); }
  function reveal(node: HTMLElement) {
    const motion = !!node.animate && !window.matchMedia?.('(prefers-reduced-motion: reduce)').matches;
    return slide(node, { duration: motion ? 200 : 0, easing: cubicOut });
  }
</script>

<div class="cv-activity cv-call-group" class:cv-call-group-open={open} class:cv-call-group-animated={animated} data-work={id}>
  <div class="cv-call-group-head">
    <button type="button" class="cv-activity-toggle" aria-label="{open ? 'Hide' : 'Show'} activity: {counts.map(c => `${c.n} ${c.label}`).join(', ')}{failed ? `, ${failed} failed` : ''}" aria-expanded={open} aria-controls={contentId} onclick={toggle} data-work-fold>
      <svg class="cv-activity-icon" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="m3 8 9-5 9 5-9 5zM3 12l9 5 9-5M3 16l9 5 9-5" /></svg>
      <span class="cv-activity-counts">
        {#each counts as count (count.kind)}
          <span class="cv-activity-chip" data-category={count.kind}>
            <svg viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.3" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
              {#if count.kind === "run"}<path d="m3 4 4 4-4 4M9 12h4" />
              {:else if count.kind === "edit"}<path d="m3 10 7-7 3 3-7 7-4 1zM8 5l3 3" />
              {:else if count.kind === "read"}<circle cx="7" cy="7" r="4"/><path d="m10 10 4 4" />
              {:else if count.kind === "note"}<path d="M2 3h12v8H7l-4 3v-3H2zM5 6h6M5 8h4" />
              {:else}<path d="m6 3-4 5 4 5M10 3l4 5-4 5" />{/if}
            </svg>
            <strong>{count.n}</strong> {count.n === 1 ? count.label.slice(0, -1) : count.label}
          </span>
        {/each}
        {#if failed}<span class="cv-activity-failures">{failed} failed</span>{/if}
      </span>
      <Chevron {open} />
    </button>
    <span class="cv-stack-sheet cv-stack-sheet-first" aria-hidden="true"></span>
    <span class="cv-stack-sheet cv-stack-sheet-second" aria-hidden="true"></span>
  </div>
  {#if open}
    <div id={contentId} class="cv-activity-log" transition:reveal>
      {@render children()}
    </div>
  {/if}
</div>
