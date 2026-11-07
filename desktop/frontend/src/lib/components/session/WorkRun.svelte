<script lang="ts">
  import { feed } from "$lib/feed.svelte";
  import type { RunRowData } from "$lib/turns";
  import { formatMs } from "$lib/ssh";
  import { tooltip } from "$lib/tooltip";
  import { slide } from "svelte/transition";
  import { cubicOut } from "svelte/easing";
  import Chevron from "../Chevron.svelte";
  import SyntaxText from "../SyntaxText.svelte";
  import ToolRow from "./ToolRow.svelte";
  import ToolPreview from "./ToolPreview.svelte";

  let { sessionId, row, chars = 72, readonly = false, nested = false }: {
    sessionId: string; row: RunRowData; chars?: number; readonly?: boolean; nested?: boolean;
  } = $props();
  const f = $derived(feed.view(sessionId));
  const open = $derived(!readonly && f.expanded.has(row.id));
  const label = $derived(row.category === 'run' ? 'commands' : row.category === 'read' ? 'file reads' : row.category === 'edit' ? 'edits' : 'tool calls');
  const previews = $derived(row.items.slice(-1).map(item => item.target));
  const hint = $derived(`${row.items.length} separate ${label}\n${row.items.slice(0, 3).map(item => item.target).join('\n')}${row.items.length > 3 ? '\n…' : ''}`);
  const id = $props.id();
  let animated = $state(false);
  function toggle() { animated = true; if (open) f.expanded.delete(row.id); else f.expanded.add(row.id); }
  function motionEnabled(node: HTMLElement) {
    return !!node.animate && !window.matchMedia?.('(prefers-reduced-motion: reduce)').matches;
  }
  function reveal(node: HTMLElement) {
    return slide(node, { duration: motionEnabled(node) ? 240 : 0, easing: cubicOut });
  }
  function unstack(node: HTMLElement) {
    return {
      duration: motionEnabled(node) ? 240 : 0,
      easing: cubicOut,
      css: (t: number, u: number) => `opacity: ${t * t}; transform: translateY(${-6 * u}px);`,
    };
  }

</script>

<div data-run={row.category} class="cv-call-group" class:cv-call-group-open={open} class:cv-call-group-animated={animated}>
  <div class="cv-call-group-head">
    <button class="cv-call-group-toggle" type="button" disabled={readonly} aria-expanded={open} aria-controls={id} onclick={toggle} use:tooltip={{ text: hint, language: row.category === "run" ? "sh" : "text" }}>
      <Chevron {open} />
      <span class="cv-call-group-label"><span class="cv-stack-count">{row.items.length}</span> {label}</span>
      <span class="cv-call-group-preview">
        {#each previews as preview, i}
          {#if i}<span class="cv-call-group-separator" aria-hidden="true">·</span>{/if}
          <span class="cv-call-preview font-mono"><SyntaxText text={preview} language={row.category === 'run' ? 'sh' : 'text'} /></span>
        {/each}
      </span>
      <span class="cv-call-group-meta">{#if row.failed}<span>{row.failed} failed</span>{/if}{#if row.durationMs}<span>{formatMs(row.durationMs)}</span>{/if}</span>
    </button>
    <span class="cv-stack-sheet cv-stack-sheet-first" aria-hidden="true"></span>
    <span class="cv-stack-sheet cv-stack-sheet-second" aria-hidden="true"></span>
  </div>
  {#if !open && !nested}<ToolPreview text={row.items.at(-1)?.tool.preview || row.items.at(-1)?.target || ""} />{/if}
  {#if open}
    <div {id} class="cv-call-group-items" transition:reveal>
      {#each row.items as item (item.id)}
        <div data-row={item.id} transition:unstack><ToolRow {sessionId} row={item} {chars} nested /></div>
      {/each}
    </div>
  {/if}
</div>
