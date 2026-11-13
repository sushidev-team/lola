<script lang="ts">
  import { onDestroy, onMount, untrack } from "svelte";
  import { disclosureSlide } from "$lib/disclosureMotion";
  import ToolOutput from "./ToolOutput.svelte";

  let { text = "", running = false, command = "", sourcePath = "", expanded = false }: { text?: string; running?: boolean; command?: string; sourcePath?: string; expanded?: boolean } = $props();
  // The current tool may supply its bounded recorded output, with its excerpt as a fallback.
  // An empty result is already conveyed by the tool's completed status.
  const fragment = $derived(text.trim() === "(Bash completed with no output)" ? "" : text.trim().slice(0, 128 * 1024));
  let shown = $state(untrack(() => fragment));
  let pending = "";
  let timer: ReturnType<typeof setTimeout> | undefined;
  $effect(() => {
    pending = fragment;
    if (!running) {
      clearTimeout(timer);
      timer = undefined;
      shown = pending;
    } else if (!timer) {
      timer = setTimeout(() => { shown = pending; timer = undefined; }, 160);
    }
  });
  let start = $state(0);
  let hovered = $state(false);
  let focused = $state(false);
  let reduced = $state(false);
  let hidden = $state(false);
  let previous = "";
  const lastStart = $derived(Math.max(0, shown.split('\n').length - 4));
  $effect(() => {
    const next = shown;
    untrack(() => {
      // Appended output continues from our place; a replacement starts at its beginning.
      if (!next.startsWith(previous)) start = 0;
      previous = next;
      start = Math.min(start, Math.max(0, next.split('\n').length - 4));
    });
  });
  $effect(() => {
    if (expanded || hovered || focused || reduced || hidden || start >= lastStart) return;
    const advance = setTimeout(() => start = Math.min(start + 1, lastStart), start === 0 ? 500 : 140);
    return () => clearTimeout(advance);
  });
  onMount(() => {
    const preference = window.matchMedia('(prefers-reduced-motion: reduce)');
    const updateMotion = () => reduced = preference.matches;
    const updateVisibility = () => hidden = document.hidden;
    updateMotion();
    updateVisibility();
    preference.addEventListener('change', updateMotion);
    document.addEventListener('visibilitychange', updateVisibility);
    return () => {
      preference.removeEventListener('change', updateMotion);
      document.removeEventListener('visibilitychange', updateVisibility);
    };
  });
  onDestroy(() => clearTimeout(timer));
</script>

{#if shown}
  <div class="cv-tool-preview selectable font-mono" class:cv-tool-preview-expanded={expanded} data-preview={expanded ? undefined : ""} data-preview-surface role="group" aria-label="Tool output" onpointerenter={() => hovered = true} onpointerleave={() => hovered = false} onfocusin={() => focused = true} onfocusout={() => focused = false} transition:disclosureSlide|global>
    <ToolOutput text={shown} {command} {sourcePath} preview={!expanded} previewStart={start} full={expanded} />
  </div>
{/if}

<style>
  /* User-requested in-place expansion: bounded to 100–320px, on disclosure only. */
  .cv-tool-preview :global(.cv-tool-output) {
    transition: max-height 200ms cubic-bezier(0.16, 1, 0.3, 1);
  }
  @media (prefers-reduced-motion: reduce) {
    .cv-tool-preview :global(.cv-tool-output) { transition: none; }
  }
</style>
