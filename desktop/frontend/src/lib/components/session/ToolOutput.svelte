<script lang="ts">
  import { outputLines, outputSourcePath } from "$lib/toolOutput";
  import SyntaxText from "../SyntaxText.svelte";
  let { text = "", command = "", sourcePath = "" }: { text?: string; command?: string; sourcePath?: string } = $props();
  const path = $derived(sourcePath || outputSourcePath(command));
  const lines = $derived(outputLines(text, path, !!sourcePath));
  const source = $derived(lines.some(line => !!line.path));
</script>

<pre class="cv-tool-output selectable font-mono text-sm text-ink-2" data-tool-output data-source={source ? "" : undefined}>{#each lines as line, i}{#if i > 0}{"\n"}{/if}{#if line.number}<span class="cv-output-number">{line.number}{line.separator}</span>{/if}<SyntaxText text={line.text} language="text" path={line.path} />{/each}</pre>
