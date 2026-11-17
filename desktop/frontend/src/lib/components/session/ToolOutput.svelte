<script lang="ts">
  import { flip } from "svelte/animate";
  import { outputLines, outputSourcePath } from "$lib/toolOutput";
  import { sourceReadPaths } from "$lib/toolActivity";
  import Button from "../Button.svelte";
  import SyntaxText from "../SyntaxText.svelte";
  let { text = "", command = "", sourcePath = "", preview = false, previewStart = 0, full = false }: { text?: string; command?: string; sourcePath?: string; preview?: boolean; previewStart?: number; full?: boolean } = $props();
  const reads = $derived(sourceReadPaths(command));
  const path = $derived(outputSourcePath(sourcePath) || outputSourcePath(command, text));
  const lines = $derived(outputLines(text, path, !!sourcePath || reads.length > 0));
  const source = $derived(lines.some(line => !!line.path));
  let expanded = $state(false);
  const visible = $derived.by(() => {
    const excerpt = preview ? lines.slice(previewStart, previewStart + 4) : expanded || full ? lines : lines.slice(0, 10);
    if (!preview) return excerpt;
    // Deeply nested numbered source should use the available width. Preserve relative
    // indentation, line numbers and the original text in the full-output view.
    const code = lines.filter(line => line.path && line.number && line.text.trim());
    const indent = code.length ? Math.min(...code.map(line => line.text.match(/^[ \t]*/)?.[0].length ?? 0)) : 0;
    return excerpt.map(line => line.path && line.number ? { ...line, text: line.text.slice(indent) } : line);
  });
  function moveLine(node: HTMLElement, rects: Parameters<typeof flip>[1]) {
    const motion = preview && !!node.animate && !window.matchMedia?.('(prefers-reduced-motion: reduce)').matches;
    return flip(node, rects, { duration: motion ? 140 : 0 });
  }
</script>

<!-- svelte-ignore a11y_no_noninteractive_tabindex (Scrollable excerpts need keyboard focus in WebKit.) -->
<pre class="cv-tool-output selectable font-mono text-sm text-ink-2" data-tool-output data-source={source ? "" : undefined} role={preview ? "region" : undefined} aria-label={preview ? "Tool output excerpt" : undefined} tabindex={preview ? 0 : undefined}>{#each visible as line, i (`${preview ? previewStart + i : i}:${line.text}`)}<span class="cv-output-entry" class:cv-output-preview-entry={preview} animate:moveLine><span class="cv-output-line" style:--preview-line={preview && previewStart === 0 ? i : 0} data-source-line={line.path ? "" : undefined} data-code={line.path && (line.number || line.separator) ? "" : undefined} data-output-kind={line.kind || undefined} data-diff={['add', 'del', 'hunk'].includes(line.kind) ? line.kind : undefined}>{#if 'diagnosticPrefix' in line}<span class="cv-output-diagnostic">{line.diagnosticPrefix}</span><span class="cv-output-diagnostic-message">{line.message}</span>{:else}{@const named = !!line.path && line.number.startsWith(`${line.path}:`) || !!line.path && line.number.startsWith(`${line.path}-`)}{#if named}<span class="cv-output-file">{line.number.slice(0, line.path.length + 1)}</span>{/if}{#if line.number || line.separator}<span class="cv-output-number">{named ? line.number.slice(line.path.length + 1) : line.number}{line.separator}</span>{/if}<span class="cv-output-code"><SyntaxText text={line.text} language="text" path={line.path} /></span>{/if}</span>{#if !preview && i < visible.length - 1}{"\n"}{/if}</span>{/each}</pre>

{#if !preview && !full && lines.length > 10}
  <Button size="xs" aria-expanded={expanded} onclick={() => expanded = !expanded}>{expanded ? 'Show less' : `Show all ${lines.length} lines`}</Button>
{/if}
