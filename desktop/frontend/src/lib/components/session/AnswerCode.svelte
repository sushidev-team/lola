<script lang="ts">
  import Button from "../Button.svelte";
  import Chevron from "../Chevron.svelte";
  import Icon from "../Icon.svelte";
  import SyntaxText from "../SyntaxText.svelte";

  let { text, language }: { text: string; language?: string } = $props();
  const codeId = $props.id();
  const languageNames: Record<string, string> = { js: "JavaScript", javascript: "JavaScript", ts: "TypeScript", typescript: "TypeScript", sh: "Shell", bash: "Bash", py: "Python", python: "Python", json: "JSON" };
  const label = $derived(language ? languageNames[language.toLowerCase()] ?? language : "Code");
  const lines = $derived(text.split("\n").length);
  const foldable = $derived(lines > 8);
  let expanded = $state(false);
  let feedback = $state<"copied" | "failed" | null>(null);
  const message = $derived(feedback === "copied" ? "Code copied" : feedback === "failed" ? "Could not copy code" : "");

  async function copy() {
    try {
      await navigator.clipboard.writeText(text);
      feedback = "copied";
    } catch {
      feedback = "failed";
    }
  }

  $effect(() => {
    if (!feedback) return;
    const timer = setTimeout(() => { feedback = null; }, 2000);
    return () => clearTimeout(timer);
  });
</script>

<div class="relative min-w-0 rounded-lg border border-edge/70 bg-canvas">
  {#if foldable}
    <Button variant="bare" block aria-expanded={expanded} aria-controls={codeId} onclick={() => { expanded = !expanded; }}
      class="h-10! gap-2! px-3! pr-12! text-ink-2! enabled:hover:bg-sel/40">
      <Chevron open={expanded} />
      <span class="truncate font-medium text-ink">{label}</span>
      <span class="shrink-0 text-sm text-faint">{lines} lines</span>
    </Button>
  {/if}
  <div id={codeId} hidden={foldable && !expanded} class={foldable ? "border-t border-edge/60" : ""}>
    <pre class="selectable max-h-80 overflow-auto px-3.5 py-2.5 pr-11 font-mono text-sm leading-5 whitespace-pre-wrap [overflow-wrap:anywhere]" data-rich-code><SyntaxText {text} {language} /></pre>
  </div>
  <Button size="sm" icon class="absolute right-1.5 bottom-1.5 border border-edge/70 bg-panel text-ink-2!" aria-label="Copy code" title={message || "Copy code"} onclick={copy}>
    <Icon name={feedback === "copied" ? "check" : feedback === "failed" ? "circle-alert" : "copy"} class="size-3.5" />
  </Button>
  <span class="sr-only" role="status">{message}</span>
</div>
