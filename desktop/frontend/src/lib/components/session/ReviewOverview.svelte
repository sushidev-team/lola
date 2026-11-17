<script lang="ts">
  import Chevron from "$lib/components/Chevron.svelte";
  import type { Snippet } from 'svelte';
  import type { ReviewPackage, PRReadinessData } from '@bindings/internal/protocol';
  import { reviewOverview, riskWeight, type OverviewItem, type ReviewChange, type RiskLevel } from '$lib/reviewoverview';
  import Button from '../Button.svelte';
  import Icon from '../Icon.svelte';
  import ReviewConfidence from './ReviewConfidence.svelte';
  import SSHRich from '../ssh/SSHRich.svelte';
  import ReviewThumbnail from './ReviewThumbnail.svelte';
  import { tooltip } from '$lib/tooltip';
  import { touchSurface } from '$lib/touchsurface';
  let { pkg, facts, sessionId, freshness, checking = false, onScreen, checks, feedback, onAssessRisks, assessment, confidenceRequest, split = false }: {
    pkg:ReviewPackage; facts?:PRReadinessData; sessionId:string; freshness:'current'|'stale'|'unknown'; onScreen:(index:number)=>void;
    checking?:boolean; checks?:Snippet; feedback?:Snippet; onAssessRisks?:()=>void; assessment?:{state:string;message:string};
    confidenceRequest?:{state:string;message:string};
    // The desktop workspace shows screenshots and checks beside the overview, so `split` leaves them out of it.
    split?:boolean;
  } = $props();
  // Phone: flat layout, no card chrome.
  const phone = touchSurface();
  const overview = $derived(reviewOverview(pkg));
  const screens = $derived(pkg.screens ?? []);
  const unrated = $derived(!overview.riskReportProvided || overview.risks.some(r => r.likelihood === 'unknown' || r.impact === 'unknown'));
  const riskHints = { likelihood: { low: 'Not expected to happen in normal use.', medium: 'Could happen in some situations.', high: 'Expected to happen unless addressed.', unknown: 'The agent did not rate this.' }, impact: { low: 'Small, easy to recover from.', medium: 'Noticeable, but contained.', high: 'Serious if it happens.', unknown: 'The agent did not rate this.' } } as const;
  const riskWords = { likelihood: { low: 'Unlikely', medium: 'Possible', high: 'Likely' }, impact: { low: 'Minor', medium: 'Moderate', high: 'Severe' } } as const;
</script>

{#snippet entries(items: OverviewItem[], empty: string)}
  {#if items.length}<div class="entries">{#each items as item}<details class="entry">
    <summary><Icon name="chevron-right" size={14}/><span>{item.title}</span></summary>
    <div class="entry-detail selectable"><SSHRich {sessionId} text={item.body}/></div>
  </details>{/each}</div>{:else}<p class="empty-copy">{empty}</p>{/if}
{/snippet}
{#snippet features(items: ReviewChange[])}
  <div class="feature-grid">{#each items as item}<details class="feature-item">
    <summary>
      <span class="feature-heading"><span class="feature-icon"><Icon name="layers" size={17}/></span><span class="feature-name">{item.title}</span><Icon name="chevron-down" size={14}/></span>
      {#if item.description}<span class="feature-description">{item.description}</span>{/if}
    </summary>
    <div class="feature-detail">
      {#if item.parts.length}<ul class="feature-parts">{#each item.parts as part}<li><Icon name="minus" size={13}/><span>{part}</span></li>{/each}</ul>{/if}
    </div>
  </details>{/each}</div>
{/snippet}
{#snippet level(value: RiskLevel, kind: 'likelihood' | 'impact')}
  <span class="risk-level" data-level={value} use:tooltip={{ heading: `${kind === 'impact' ? 'Impact' : 'Likelihood'}: ${value === 'unknown' ? 'Not rated' : riskWords[kind][value]}`, text: riskHints[kind][value], delay: 120 }}><span class="level-bars" aria-hidden="true">{#each [1,2,3] as n}<i class:filled={n <= riskWeight[value]}></i>{/each}</span>{value === 'unknown' ? 'Not rated' : riskWords[kind][value]}</span>
{/snippet}

<div class="overview" class:split>
  <div class="change-column">
    <header class="review-intro">
      {#if split}<h1 class="sr-only">{overview.title}</h1>{:else}<h1>{overview.title}</h1>
      <p class="summary-copy">{overview.summary}</p>{/if}
      {#if freshness === 'stale' && !split}<div class="intro-meta"><span class="stale" role="status"><Icon name="circle-alert" size={14}/>Outdated material</span></div>{/if}
    </header>
      {#if !(phone && overview.changes.length === 0)}<section class="review-panel features" aria-label="Changes in this review">
        <div class="section-title"><h2>What changed<span class="section-count">{overview.changes.length}</span></h2></div>
        {#if split && overview.summary}<p class="summary-copy lead">{overview.summary}</p>{/if}
        {#if overview.changes.length}{@render features(overview.changes.slice(0,4))}{:else}<p class="empty-copy">No feature breakdown provided.</p>{/if}
        {#if overview.changes.length > 4}<details class="additional"><summary><Chevron class="disclosure-chevron" />{overview.changes.length-4} more {overview.changes.length === 5 ? 'change' : 'changes'}</summary>{@render features(overview.changes.slice(4))}</details>{/if}
      </section>{/if}
      {#if screens.length && !phone && !split}<section class="review-panel visual-review" aria-label="Screenshot previews">
        <div class="section-title"><h2>See the changes<span class="section-count">{screens.length}</span></h2><Button size="xs" onclick={() => onScreen(0)}>View all<Icon name="arrow-right" size={14}/></Button></div>
        <div class="screenshot-grid">{#each screens.slice(0,4) as screen,i}<button class="screen-preview" onclick={() => onScreen(i)} aria-label={`Review screenshot: ${screen.title}`}>
          <ReviewThumbnail {pkg} artifact={screen.after} label={`Preview: ${screen.title}`}/>
          <span class="preview-caption"><span>{screen.title}</span><Icon name="arrow-right" size={14}/></span>
        </button>{/each}</div>
      </section>{/if}
  </div>
  <div class="decision-column">
    {#if !split}<div class="readiness">
      <ReviewConfidence {pkg} {facts} {checking} {confidenceRequest}/>
      {#if checks}<div class="checks">{@render checks()}</div>{/if}
    </div>{/if}
      <section class="review-panel risks" aria-label="Reported risks">
        <div class="section-title"><h2>Risks &amp; follow-up<span class="section-count">{overview.risks.length}</span></h2></div><p class="risk-attribution">Agent assessment</p>
        {#if overview.risks.length}
          <div class="risk-columns" aria-hidden="true"><span>Risk</span><span>Likelihood</span><span>Impact</span></div>
          {#each overview.risks as risk}<details class="entry risk-entry" class:priority={risk.priority >= 6}>
            <summary><span class="risk-title"><Icon name="chevron-right" size={14}/>{risk.title}</span><span class="risk-rating" aria-label={`Likelihood: ${risk.likelihood}`}>{@render level(risk.likelihood, 'likelihood')}</span><span class="risk-rating" aria-label={`Impact: ${risk.impact}`}>{@render level(risk.impact, 'impact')}</span></summary>
            <div class="risk-detail">
              {#if risk.description}<div><h3>If it goes wrong</h3><p>{risk.description}</p></div>{/if}
              {#if risk.assess}<div><h3>How to check</h3><p>{risk.assess}</p></div>{/if}
            </div>
          </details>{/each}
        {:else}<p class="empty-copy">{overview.riskReportProvided ? 'No unresolved risks reported.' : 'Risks have not been assessed.'}</p>{/if}
        {#if unrated && onAssessRisks}<Button size="xs" variant="secondary" loading={assessment?.state === 'sending'} disabled={assessment?.state === 'accepted'} onclick={onAssessRisks}><Icon name="sparkles" size={13}/>{assessment?.state === 'accepted' ? 'Assessment requested' : assessment?.state === 'error' ? 'Retry assessment' : 'Ask agent to assess risks'}</Button>{/if}
        {#if assessment?.message}<p class="assessment-message" role={assessment.state === 'error' ? 'alert' : 'status'}>{assessment.message}</p>{/if}
      </section>
      {#if feedback && !split}<div class="review-panel feedback">{@render feedback()}</div>{/if}
  </div>
</div>

<style>
  summary::marker { content:""; }
  summary > :global(.disclosure-chevron) { margin-right:6px; vertical-align:-2px; color:var(--color-ink-2); }
  details[open] > summary > :global(.disclosure-chevron) { transform:rotate(90deg); }
  .overview { display:grid; grid-template-columns:minmax(0,1fr) minmax(370px,.7fr); gap:40px; align-items:start; max-width:1400px; margin:0 auto; min-width:0; }
  /* Split: one scrolling column of cards; the screenshots and checks live beside it. */
  .overview.split { grid-template-columns:minmax(0,1fr); gap:20px; max-width:none; }
  .overview.split .change-column,.overview.split .decision-column { gap:20px; }
  .overview.split .decision-column { display:contents; }
  .overview.split .change-column { display:contents; }
  .overview.split .review-intro { order:0; } .overview.split .features { order:2; } .overview.split .risks { order:3; }
  .overview.split h1 { font-size:22px; max-width:none; margin-bottom:10px; }
  .overview.split .features,.overview.split .risks { padding:14px; border-radius:12px; }
  .overview.split .risks h2 { font-size:14px; } .overview.split .risks .section-count { min-width:20px; height:20px; font-size:11px; }
  .overview.split .risk-attribution { margin-bottom:8px; }
  .overview.split .risk-columns { padding-bottom:8px; }
  .overview.split .risk-entry > summary { padding:9px 0; }
  .overview.split .features h2,.overview.split .risks h2 { font-size:16px; }
  .change-column,.decision-column { min-width:0; display:grid; gap:32px; }
  .review-intro { min-width:0; padding:4px 0 0; }
  h1 { margin:0 0 16px; max-width:32ch; font-size:30px; line-height:1.2; letter-spacing:-.025em; font-weight:600; color:var(--color-ink); text-wrap:balance; overflow-wrap:anywhere; }
  .summary-copy.lead { margin:0 0 14px; font-size:13px; line-height:1.55; }
  .summary-copy { max-width:65ch; color:var(--color-ink-2); font-size:14px; line-height:1.65; margin:0; }
  .intro-meta { margin-top:16px; font-size:12px; }
  .stale { display:inline-flex; align-items:center; gap:6px; color:var(--color-warn); }
  h2 { display:flex; align-items:center; gap:10px; font-size:14px; font-weight:600; color:var(--color-ink); }
  .review-panel { min-width:0; }
  .section-title { display:flex; align-items:center; justify-content:space-between; gap:12px; margin-bottom:16px; }
  .section-count { font-size:12px; font-weight:400; font-variant-numeric:tabular-nums; color:var(--color-ink-2); }
  .readiness { min-width:0; }
  .checks { margin-top:20px; }
  summary { cursor:pointer; list-style:none; }
  summary::-webkit-details-marker { display:none; }
  summary:focus-visible,.screen-preview:focus-visible { outline:2px solid var(--color-accent); outline-offset:4px; border-radius:4px; }
  .features { container-type:inline-size; padding:24px; border-radius:16px; background:var(--color-panel); border:1px solid color-mix(in srgb,var(--color-edge) 60%,transparent); }
  .features .section-title { margin-bottom:20px; }
  .features h2 { font-size:20px; line-height:1.3; letter-spacing:-.015em; font-weight:600; gap:12px; }
  .features .section-count { display:inline-flex; align-items:center; justify-content:center; min-width:25px; height:25px; padding:0 7px; border-radius:7px; background:var(--color-accent-fill); color:var(--color-accent-ink); font-size:12px; font-weight:500; letter-spacing:0; }
  .feature-grid { display:grid; grid-template-columns:minmax(0,1fr); }
  .feature-item { min-width:0; border-top:1px solid color-mix(in srgb,var(--color-edge) 55%,transparent); }
  .feature-item > summary { padding:20px 0; }
  .feature-heading { position:relative; padding-left:44px; display:flex; align-items:center; gap:12px; font-size:15px; line-height:1.45; font-weight:500; color:var(--color-ink); }
  .feature-name { flex:1; overflow-wrap:anywhere; }
  .feature-icon { position:absolute; left:0; top:-5px; display:inline-flex; align-items:center; justify-content:center; width:32px; height:32px; flex-shrink:0; border-radius:9px; color:var(--color-accent-ink); background:color-mix(in srgb,var(--color-accent) 10%,transparent); }
  .feature-heading > :global(svg) { color:var(--color-ink-2); transition:transform 160ms ease-out; }
  .feature-item[open] .feature-heading > :global(svg) { transform:rotate(180deg); }
  .feature-item > summary:hover .feature-heading { color:var(--color-accent-ink); }
  .feature-description { display:block; color:var(--color-ink-2); font-size:13px; line-height:1.65; margin:4px 26px 0 44px; max-width:65ch; overflow-wrap:anywhere; }
  .feature-detail { padding:0 26px 4px 44px; font-size:13px; overflow-wrap:anywhere; }
  .feature-parts { display:grid; gap:10px; padding:0; margin:0 0 16px; list-style:none; }
  .feature-parts li { display:flex; gap:8px; align-items:baseline; line-height:1.5; color:var(--color-ink); }
  .feature-parts li :global(svg) { flex-shrink:0; color:var(--color-accent); }
  .feature-detail :global(pre),.risk-detail :global(pre),.entry-detail :global(pre) { max-width:100%; overflow:auto; }
  .entries { border-top:1px solid var(--color-edge); }
  .entry { border-bottom:1px solid color-mix(in srgb,var(--color-edge) 55%,transparent); }
  .entry > summary { display:flex; align-items:center; gap:12px; padding:16px 0; font-size:13px; line-height:1.5; color:var(--color-ink); }
  .entry > summary :global(svg) { flex-shrink:0; color:var(--color-ink-2); transition:transform 160ms ease-out; }
  .entry[open] > summary :global(svg) { transform:rotate(90deg); }
  .entry > summary:hover,.entry[open] > summary { color:var(--color-accent-ink); }
  .entry-detail { padding:0 0 18px 26px; max-width:75ch; font-size:13px; overflow-wrap:anywhere; }
  .empty-copy { color:var(--color-ink-2); padding:8px 0 16px; font-size:13px; }
  .additional { margin-top:12px; font-size:12px; color:var(--color-ink-2); }
  .additional > summary { margin-bottom:12px; }
  .screenshot-grid { display:grid; grid-template-columns:repeat(2,minmax(0,1fr)); gap:20px; }
  .screen-preview { align-self:start; text-align:left; min-width:0; border-radius:12px; cursor:pointer; background:transparent; }
  .screen-preview :global(.thumbnail) { display:flex; border:1px solid color-mix(in srgb,var(--color-info) 20%,var(--color-edge)); border-radius:12px; padding:12px; background:color-mix(in srgb,var(--color-info) 9%,var(--color-panel)); transition:border-color 160ms ease-out,background 160ms ease-out; }
  .screen-preview :global(.thumbnail img) { min-width:0; min-height:0; height:auto; object-fit:contain; }
  .screen-preview:hover :global(.thumbnail) { border-color:var(--color-accent); background:color-mix(in srgb,var(--color-info) 15%,var(--color-panel)); }
  .preview-caption { display:flex; justify-content:space-between; align-items:center; gap:12px; padding:12px 2px 0; font-size:12px; line-height:1.5; color:var(--color-ink-2); }
  .preview-caption > span { overflow-wrap:anywhere; } .preview-caption :global(svg) { flex-shrink:0; color:var(--color-accent-ink); }
  .screen-preview:hover .preview-caption { color:var(--color-ink); }
  .risks { container-type:inline-size; padding:24px; border-radius:16px; background:var(--color-panel); border:1px solid color-mix(in srgb,var(--color-edge) 60%,transparent); }
  .risks .section-title { margin-bottom:6px; }
  .risks h2 { font-size:20px; line-height:1.3; font-weight:600; letter-spacing:-.015em; gap:12px; }
  .risks .section-count { display:inline-flex; align-items:center; justify-content:center; min-width:25px; height:25px; padding:0 7px; border-radius:7px; background:var(--color-accent-fill); color:var(--color-accent-ink); font-size:12px; font-weight:500; letter-spacing:0; }
  .risk-attribution { font-size:12px; color:var(--color-ink-2); margin-bottom:24px; }
  .risk-columns,.risk-entry > summary { display:grid; grid-template-columns:minmax(0,1fr) 104px 104px; gap:14px; }
  .risk-columns { padding:0 0 12px; font-size:12px; color:var(--color-ink-2); border-bottom:1px solid var(--color-edge); }
  .risk-entry > summary { padding:18px 0; align-items:center; }
  .risk-title { display:flex; gap:8px; align-items:baseline; overflow-wrap:anywhere; font-size:13px; line-height:1.55; }
  .risk-title :global(svg) { position:relative; top:2px; }
  .risk-rating { min-width:0; }
  .risk-level { box-sizing:border-box; width:100%; display:flex; align-items:center; gap:6px; padding:5px 8px; border-radius:6px; font-size:12px; line-height:16px; white-space:nowrap; color:var(--color-ink-2); background:color-mix(in srgb,var(--color-ink) 5%,transparent); }
  .level-bars { display:inline-flex; align-items:end; gap:2px; height:12px; }
  .level-bars i { width:3px; height:4px; border-radius:1px; background:color-mix(in srgb,var(--color-ink-2) 22%,transparent); }
  .level-bars i:nth-child(2) { height:8px; } .level-bars i:nth-child(3) { height:12px; }
  .level-bars i.filled { background:currentColor; }
  [data-level=high] { color:var(--color-warn); background:color-mix(in srgb,var(--color-warn) 12%,transparent); }
  [data-level=medium] { color:var(--color-ink); }
  .risk-detail { display:grid; gap:10px; margin:0 0 14px 22px; padding-left:12px; border-left:2px solid color-mix(in srgb,var(--color-edge) 80%,transparent); }
  .risk-detail h3 { font-size:11px; font-weight:500; letter-spacing:.02em; color:var(--color-faint); margin:0 0 2px; }
  @container (max-width:440px) {
    .risk-columns { display:none; }
    .risk-attribution { margin-bottom:8px; }
    .risk-entry:first-of-type { border-top:1px solid var(--color-edge); }
    .risk-entry > summary { grid-template-columns:minmax(0,134px) minmax(0,112px) minmax(0,1fr); gap:8px 8px; padding:10px 0 12px; }
    .risk-title { grid-column:1/-1; font-size:13px; line-height:1.4; }
    .risk-rating { min-width:0; }
    .risk-rating:nth-of-type(2) { padding-left:22px; }
    .risk-level { padding:2px 6px; font-size:11px; line-height:14px; }
    .risk-detail { gap:8px; margin-bottom:10px; }
  }
  .risk-detail p { margin:0; font-size:13px; line-height:1.5; color:var(--color-ink-2); max-width:65ch; overflow-wrap:anywhere; }
  .risks > :global(button) { margin-top:16px; }
  .assessment-message { color:var(--color-ink-2); font-size:12px; line-height:1.5; margin-top:12px; }
  .feedback { padding:20px; border-radius:12px; background:color-mix(in srgb,var(--color-info) 6%,var(--color-panel)); }
  @media(min-width:1200px) { .screenshot-grid { grid-template-columns:repeat(4,minmax(0,1fr)); gap:16px; } }
  @media(max-width:1100px) { .overview { gap:32px; grid-template-columns:minmax(0,1fr) 330px; } h1 { font-size:26px; } }
  @media(max-width:800px) { .overview { grid-template-columns:minmax(0,1fr); gap:32px; max-width:620px; } .change-column,.decision-column { display:contents; } .readiness { order:1; } .features { order:2; } .visual-review { order:3; } .risks { order:4; } .feedback { order:5; } }
  @media(max-width:440px) { .risks { padding:20px 16px; } .features { padding:20px 16px; } .features h2 { font-size:20px; } .feature-description { margin-right:0; } h1 { font-size:24px; } .screenshot-grid { gap:16px 12px; } .screen-preview :global(.thumbnail) { padding:6px; border-radius:8px; } .preview-caption { font-size:12px; } }
  /* Phone: one flat column. No card inside a card, headings no larger than the 28/34 title tier. */
  :global([data-phone-desk]) .readiness,:global([data-phone-desk]) .features,:global([data-phone-desk]) .risks,:global([data-phone-desk]) .feedback { padding:0; border:0; border-radius:0; background:transparent; }
  :global([data-phone-desk]) h1 { max-width:none; font-size:28px; line-height:34px; margin-bottom:8px; }
  :global([data-phone-desk]) .features h2,:global([data-phone-desk]) .risks h2 { font-size:17px; line-height:22px; font-weight:600; letter-spacing:0; gap:8px; }
  :global([data-phone-desk]) .features .section-count,:global([data-phone-desk]) .risks .section-count { min-width:0; height:auto; padding:0; border-radius:0; background:none; color:var(--color-faint); font-size:13px; line-height:18px; font-weight:400; }
  :global([data-phone-desk]) .summary-copy { font-size:15px; line-height:22px; }
  :global([data-phone-desk]) .overview { gap:24px; max-width:none; }
  @media(prefers-reduced-motion:reduce) { .entry > summary :global(svg),.screen-preview :global(.thumbnail),.feature-heading > :global(svg) { transition:none; } }
</style>
