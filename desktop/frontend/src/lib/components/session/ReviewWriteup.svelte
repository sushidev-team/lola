<script lang="ts">
  import { reviewSections } from '$lib/reviewmaterial';
  import SSHRich from '../ssh/SSHRich.svelte';
  let { sessionId, body }: { sessionId: string; body: string } = $props();
  const sections = $derived(reviewSections(body));
</script>

<div class="writeup" aria-label="Agent review write-up">
  {#each sections as section}
    <details open={section.title.toLowerCase() === 'summary' && section.body.length < 500}>
      <summary>{section.title}</summary>
      <div class="section-body selectable"><SSHRich {sessionId} text={section.body} /></div>
    </details>
  {/each}
</div>

<style>
  .writeup { border-top:1px solid var(--color-edge); }
  details { border-bottom:1px solid var(--color-edge); }
  summary { padding:14px 0; cursor:pointer; color:var(--color-ink); font-weight:500; }
  .section-body { max-width:72ch; padding:0 0 20px 16px; font-size:13px; line-height:1.6; }
  summary:focus-visible { outline:2px solid var(--color-accent); outline-offset:2px; }
</style>
