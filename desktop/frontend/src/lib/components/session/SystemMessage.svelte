<script lang="ts">
  import { disclosureMotion } from "$lib/disclosureMotion";
  import Chevron from "../Chevron.svelte";
  import SyntaxText from "../SyntaxText.svelte";
  let { text, label = "Automated instruction", legacy = false, reveal = false }: { text: string; label?: string; legacy?: boolean; reveal?: boolean } = $props();
  const summaryOnly = $derived(!legacy && text.trim().replace(/^lola\s+/i, "").toLowerCase() === label.trim().toLowerCase());
</script>

{#if summaryOnly}
  <div class="cv-system-message cv-system-static" data-lola-instruction><span class="cv-system-brand">Lola</span><span>{label}</span></div>
{:else}
<details use:disclosureMotion class="cv-system-message" open={reveal} data-lola-instruction>
  <summary><Chevron class="cv-disclosure-chevron" />
    <span class="cv-system-brand">Lola</span>
    <span>{label}</span>
    {#if legacy}<span class="cv-system-source" title="Recognized from Lola’s review-workflow template in an older terminal transcript; no send receipt is available.">recognized template</span>{/if}
  </summary>
  <div class="selectable whitespace-pre-wrap break-words text-sm text-ink-2"><SyntaxText {text} language={legacy ? "sh" : "text"} /></div>
</details>
{/if}
