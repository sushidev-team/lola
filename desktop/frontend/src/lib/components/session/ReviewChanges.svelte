<script lang="ts">
  import type { ReviewPackage } from '@bindings/internal/protocol';
  import { reviewChanges } from '$lib/reviewchanges';
  import Chevron from '../Chevron.svelte';
  import Button from '../Button.svelte';
  import SSHRich from '../ssh/SSHRich.svelte';
  import ReviewScreenshot from './ReviewScreenshot.svelte';
  let { before, after, onCode }: {before:ReviewPackage;after:ReviewPackage;onCode?:()=>void} = $props();
  const changes = $derived(reviewChanges(before,after));
</script>

<section class="review-changes" aria-label="Changes since review">
  <header><h2>Since your last review</h2><p><code>{before.head.slice(0,12)}</code> to <code>{after.head.slice(0,12)}</code></p></header>
  {#if new Date(after.createdAt) < new Date(before.createdAt)}<p class="text-warn">You selected an older package. This comparison runs from your reviewed package back to that version.</p>{/if}
  {#if changes.headChanged}<p>The code revision changed. Checks for the earlier revision do not verify this one.</p>{:else}<p>Same code revision; the published review material changed.</p>{/if}
  {#if changes.baseChanged}<p class="text-warn">The base revision also changed. Compare the full PR before finishing your review.</p>{/if}
  {#if onCode}<div><Button size="sm" variant="secondary" onclick={onCode}>Compare code revisions</Button></div>{/if}
  {#if changes.titleChanged}<p>Title changed from “{before.title}” to “{after.title}”.</p>{/if}
  <h3>Write-up</h3>
  {#each changes.sections as row}
    <details><summary><Chevron class="disclosure-chevron" />{row.after?.title ?? row.before?.title}<span>{!row.before ? 'Added' : !row.after ? 'Removed' : 'Updated'}</span></summary>
      <div class="comparison">
        <div><h4>Reviewed</h4>{#if row.before}<SSHRich sessionId={before.session} text={row.before.body}/>{:else}<p>Not in the reviewed package.</p>{/if}</div>
        <div><h4>Selected version</h4>{#if row.after}<SSHRich sessionId={after.session} text={row.after.body}/>{:else}<p>Removed from this package.</p>{/if}</div>
      </div>
    </details>
  {:else}<p>No write-up sections changed.</p>{/each}
  <h3>Screenshots</h3>
  {#each changes.screens as row}
    <details><summary><Chevron class="disclosure-chevron" />{row.after?.title ?? row.before?.title}<span>{!row.before ? 'Added' : !row.after ? 'Removed' : 'Updated'}</span></summary>
      <div class="comparison">
        {#each [{pkg:before,screen:row.before,label:'Reviewed'},{pkg:after,screen:row.after,label:'Selected version'}] as item}
          <div><h4>{item.label}</h4>{#if item.screen}
            <ReviewScreenshot sessionId={item.pkg.session} packageId={item.pkg.id} artifact={item.screen.after} label={`${item.label}: ${item.screen.title}`}/>
            <p>{item.screen.caption}</p><p>{item.screen.url || 'No page URL'} · {item.screen.viewport || 'No viewport'}</p>
          {:else}<p>No screenshot in this package.</p>{/if}</div>
        {/each}
      </div>
      {#if row.before && row.after && row.before.after === row.after.after}<p class="image-note">After-image bytes are unchanged; baseline or capture metadata changed.</p>{/if}
      {#if row.before?.before !== row.after?.before}
        <h4>Changed baseline captures</h4>
        <div class="comparison">
          {#each [{pkg:before,screen:row.before,label:'Reviewed baseline'},{pkg:after,screen:row.after,label:'Selected baseline'}] as item}
            <div>{#if item.screen?.before}<ReviewScreenshot sessionId={item.pkg.session} packageId={item.pkg.id} artifact={item.screen.before} label={`${item.label}: ${item.screen.title}`}/>{:else}<p>{item.label}: no baseline supplied.</p>{/if}</div>
          {/each}
        </div>
      {/if}
    </details>
  {:else}<p>No screenshot pairs changed.</p>{/each}
  <p>{changes.unchangedScreens} unchanged screenshot {changes.unchangedScreens === 1 ? 'pair' : 'pairs'}. Captures and revision identities are agent-provided.</p>
</section>

<style>
  .review-changes { display:grid; gap:16px; max-width:1200px; margin:auto; color:var(--color-ink-2); font-size:13px; line-height:1.6; }
  h2 { color:var(--color-ink); font-size:22px; font-weight:600; } h3 { color:var(--color-ink); font-size:16px; font-weight:600; margin-top:16px; }
  h4 { color:var(--color-ink); font-weight:500; margin-bottom:12px; }
  details { border-bottom:1px solid var(--color-edge); min-width:0; }
  summary { display:flex; align-items:center; gap:8px; padding:12px 0; cursor:pointer; list-style:none; color:var(--color-ink); overflow-wrap:anywhere; }
  summary::-webkit-details-marker { display:none; } summary::marker { content:''; }
  summary span { margin-left:auto; font-size:12px; color:var(--color-ink-2); }
  summary:focus-visible { outline:2px solid var(--color-accent); outline-offset:3px; border-radius:4px; }
  details[open] > summary > :global(.disclosure-chevron) { transform:rotate(90deg); }
  .comparison { display:grid; grid-template-columns:repeat(2,minmax(0,1fr)); gap:24px; padding:12px 0 24px; }
  .comparison > div { min-width:0; overflow-wrap:anywhere; }
  .comparison :global(pre) { max-width:100%; overflow:auto; }
  .image-note { margin-bottom:16px; }
  @media(max-width:740px) { .comparison { grid-template-columns:minmax(0,1fr); } }
</style>
