<script lang="ts">
  import { syntaxTokens } from "$lib/syntax";
  let { text = "", language = "", path = "" }: { text?: string; language?: string; path?: string } = $props();
  const tokens = $derived(syntaxTokens(text, language, path));
  const colors = { plain: "", kw: "text-magenta", str: "text-good", com: "text-faint", num: "text-orange", flag: "text-info", var: "text-orange", cmd: "text-accent-ink", path: "text-ink-2" };
</script>
{#each tokens as token, i (i)}{#if token.kind === "plain"}{token.text}{:else}<span class={colors[token.kind]} data-syntax={token.kind}>{token.text}</span>{/if}{/each}
