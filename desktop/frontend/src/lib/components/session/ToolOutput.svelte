<script lang="ts">
  import { outputLines, outputSourcePath } from "$lib/toolOutput";
  import { sourceReadPaths } from "$lib/toolActivity";
  import Button from "../Button.svelte";
  import SyntaxText from "../SyntaxText.svelte";
  let { text = "", command = "", sourcePath = "" }: { text?: string; command?: string; sourcePath?: string } = $props();
  const reads = $derived(sourceReadPaths(command));
  const path = $derived(outputSourcePath(sourcePath) || outputSourcePath(command, text));
  const lines = $derived(outputLines(text, path, !!sourcePath || reads.length > 0));
  const source = $derived(lines.some(line => !!line.path));
  let expanded = $state(false);
  const visible = $derived(expanded ? lines : lines.slice(0, 10));
</script>

<pre class="cv-tool-output selectable font-mono text-sm text-ink-2" data-tool-output data-source={source ? "" : undefined}>{#each visible as line, i}{#if i > 0}{"\n"}{/if}<span class="cv-output-line" data-source-line={line.path ? "" : undefined} data-code={line.path && (line.number || line.separator) ? "" : undefined} data-output-kind={line.kind || undefined} data-diff={['add', 'del', 'hunk'].includes(line.kind) ? line.kind : undefined}>{#if 'diagnosticPrefix' in line}<span class="cv-output-diagnostic">{line.diagnosticPrefix}</span><span class="cv-output-diagnostic-message">{line.message}</span>{:else}{@const named = !!line.path && line.number.startsWith(`${line.path}:`) || !!line.path && line.number.startsWith(`${line.path}-`)}{#if named}<span class="cv-output-file">{line.number.slice(0, line.path.length + 1)}</span>{/if}{#if line.number || line.separator}<span class="cv-output-number">{named ? line.number.slice(line.path.length + 1) : line.number}{line.separator}</span>{/if}<span class="cv-output-code"><SyntaxText text={line.text} language="text" path={line.path} /></span>{/if}</span>{/each}</pre>

{#if lines.length > 10}
  <Button size="xs" aria-expanded={expanded} onclick={() => expanded = !expanded}>{expanded ? 'Show less' : `Show all ${lines.length} lines`}</Button>
{/if}
