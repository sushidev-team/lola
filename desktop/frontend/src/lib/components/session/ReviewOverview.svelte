<script lang="ts">
  import Chevron from "$lib/components/Chevron.svelte";
  import type { Snippet } from 'svelte';
  import type { ReviewPackage, PRReadinessData } from '@bindings/internal/protocol';
  import { reviewOverview, riskWeight, type OverviewItem, type ReviewChange, type ReviewRisk, type RiskLevel } from '$lib/reviewoverview';
  import Button from '../Button.svelte';
  import Icon from '../Icon.svelte';
  import ReviewConfidence from './ReviewConfidence.svelte';
  import SSHRich from '../ssh/SSHRich.svelte';
  import ReviewThumbnail from './ReviewThumbnail.svelte';
  import { MAX_PREVIEW_TABS, preview, prepOf } from '$lib/preview.svelte';
  import { store } from '$lib/store.svelte';
  import { forwardedPageURL } from '$lib/devurl';
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
  const allPages = $derived(pkg.pages ?? []);
  // A phone opens a page through its session's forward of that server, or not at all.
  const forwards = $derived(store.sessions.find(s => s.id === sessionId)?.devForwards ?? []);
  const pages = $derived(phone ? allPages.filter(p => forwardedPageURL(p.url, forwards)) : allPages);
  function openPage(page:(typeof allPages)[number]) { if (phone) { const u = forwardedPageURL(page.url, forwards); if (u) void store.openURL(u); } else if (split) onScreen(screens.length + allPages.indexOf(page)); else preview.open(sessionId, page.url, { prep:prepOf(page), caption:page.caption }); }
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
{#snippet featureLabel(item: ReviewChange)}
  <span class="feature-heading">
    {#if !phone}<span class="feature-icon"><Icon name="layers" size={17}/></span>{/if}
    <span class="feature-name">{item.title}</span>
    {#if !phone || item.parts.length}<Icon name="chevron-down" size={14}/>{/if}
  </span>
  {#if item.description}<span class="feature-description">{item.description}</span>{/if}
{/snippet}
{#snippet features(items: ReviewChange[])}
  <div class="feature-grid">
    {#each items as item}
      {#if phone && !item.parts.length}
        <div class="feature-item feature-static">{@render featureLabel(item)}</div>
      {:else}
        <details class="feature-item">
          <summary>{@render featureLabel(item)}</summary>
          <div class="feature-detail">
            {#if item.parts.length}<ul class="feature-parts">{#each item.parts as part}<li><Icon name="minus" size={13}/><span>{part}</span></li>{/each}</ul>{/if}
          </div>
        </details>
      {/if}
    {/each}
  </div>
{/snippet}
{#snippet level(value: RiskLevel, kind: 'likelihood' | 'impact')}
  <span class="risk-level" data-level={value} use:tooltip={{ heading: `${kind === 'impact' ? 'Impact' : 'Likelihood'}: ${value === 'unknown' ? 'Not rated' : riskWords[kind][value]}`, text: riskHints[kind][value], delay: 120 }}><span class="level-bars" aria-hidden="true">{#each [1,2,3] as n}<i class:filled={n <= riskWeight[value]}></i>{/each}</span>{value === 'unknown' ? 'Not rated' : riskWords[kind][value]}</span>
{/snippet}

{#snippet readiness()}
  <div class="readiness">
    {#if phone}<div class="section-title"><h2>Verification<Icon name="shield-check" size={17}/></h2></div>{/if}
    <ReviewConfidence {pkg} {facts} {checking} {confidenceRequest}/>
    {#if checks}<div class="checks">{@render checks()}</div>{/if}
  </div>
{/snippet}
{#snippet riskDetail(risk: ReviewRisk)}
  {#if risk.description && risk.description.replace(/[.!]$/, '').toLowerCase() !== risk.title.toLowerCase()}<p>{risk.description}</p>{/if}
  {#if risk.assess}<div><h3>Next step</h3><p>{risk.assess}</p></div>{/if}
{/snippet}

<div class="overview" class:split class:phone>
  <div class="change-column">
    <header class="review-intro">
      {#if split}<h1 class="sr-only">{overview.title}</h1>{:else}<h1>{overview.title}</h1>
      <p class="summary-copy">{overview.summary}</p>{/if}
      {#if freshness === 'stale' && !split}<div class="intro-meta"><span class="stale" role="status"><Icon name="circle-alert" size={14}/>Outdated material</span></div>{/if}
    </header>
      {#if !(phone && overview.changes.length === 0)}<section class="review-panel features" aria-label="Changes in this review">
        <div class="section-title"><h2>What changed<span class="section-count">{#if phone}<Icon name="layers" size={14}/>{/if}{overview.changes.length}</span></h2></div>
        {#if split && overview.summary}<p class="summary-copy lead">{overview.summary}</p>{/if}
        {#if overview.changes.length}{@render features(overview.changes.slice(0,4))}{:else}<p class="empty-copy">No feature breakdown provided.</p>{/if}
        {#if overview.changes.length > 4}<details class="additional"><summary><Chevron class="disclosure-chevron" />{overview.changes.length-4} more {overview.changes.length === 5 ? 'change' : 'changes'}</summary>{@render features(overview.changes.slice(4))}</details>{/if}
      </section>{/if}
      {#if pages.length}<section class="review-panel live-pages" aria-label="Live pages">
        <div class="section-title"><h2>Open the running app<span class="section-count">{pages.length}</span></h2>{#if !phone && pages.length > 1}<Button size="xs" onclick={() => preview.openTabs(sessionId, pages.map(p => ({ url:p.url, title:p.title, caption:p.caption, prep:prepOf(p) })))}>Open all as tabs{pages.length > MAX_PREVIEW_TABS ? ` (first ${MAX_PREVIEW_TABS})` : ''}<Icon name="arrow-right" size={14}/></Button>{/if}</div>
        <ul class="page-list">{#each pages as page}<li><Button size="xs" onclick={() => openPage(page)} aria-label={`Open live page: ${page.title}`}><Icon name="arrow-right" size={14}/>{page.title}</Button>{#if page.caption}<span class="page-caption">{page.caption}</span>{/if}</li>{/each}</ul>
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
    {#if !split && !phone}{@render readiness()}{/if}
      <section class="review-panel risks" aria-label="Reported risks">
        <div class="section-title"><h2>Risks &amp; follow-up<span class="section-count">{#if phone}<Icon name="triangle-alert" size={14}/>{/if}{overview.risks.length}</span></h2></div><p class="risk-attribution">Agent assessment</p>
        {#if overview.risks.length}
          {#if phone}
            {#each overview.risks as risk}
              <article class="phone-risk">
                <h3>{risk.title}</h3>
                <dl class="risk-facts">
                  <div><dt>Likelihood</dt><dd>{@render level(risk.likelihood, 'likelihood')}</dd></div>
                  <div><dt>Impact</dt><dd>{@render level(risk.impact, 'impact')}</dd></div>
                </dl>
                <div class="risk-detail">{@render riskDetail(risk)}</div>
              </article>
            {/each}
          {:else}
            <div class="risk-columns" aria-hidden="true"><span>Risk</span><span>Likelihood</span><span>Impact</span></div>
            {#each overview.risks as risk}<details class="entry risk-entry" class:priority={risk.priority >= 6}>
              <summary><span class="risk-title"><Icon name="chevron-right" size={14}/>{risk.title}</span><span class="risk-rating" aria-label={`Likelihood: ${risk.likelihood}`}>{@render level(risk.likelihood, 'likelihood')}</span><span class="risk-rating" aria-label={`Impact: ${risk.impact}`}>{@render level(risk.impact, 'impact')}</span></summary>
              <div class="risk-detail">
                {@render riskDetail(risk)}
              </div>
            </details>{/each}
          {/if}
        {:else}<p class="empty-copy">{overview.riskReportProvided ? 'No unresolved risks reported.' : 'Risks have not been assessed.'}</p>{/if}
        {#if unrated && onAssessRisks}<Button size="xs" variant="secondary" loading={assessment?.state === 'sending'} disabled={assessment?.state === 'accepted'} onclick={onAssessRisks}><Icon name="sparkles" size={13}/>{assessment?.state === 'accepted' ? 'Assessment requested' : assessment?.state === 'error' ? 'Retry assessment' : 'Ask agent to assess risks'}</Button>{/if}
        {#if assessment?.message}<p class="assessment-message" role={assessment.state === 'error' ? 'alert' : 'status'}>{assessment.message}</p>{/if}
      </section>
      {#if phone && !split}{@render readiness()}{/if}
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
  .page-list { display:grid; gap:10px; padding:0; margin:0; list-style:none; }
  .page-list li { display:flex; flex-wrap:wrap; align-items:center; gap:4px 12px; min-width:0; }
  .page-caption { font-size:12px; color:var(--color-ink-2); overflow-wrap:anywhere; }
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
  /* Every section owns its header and body. Their text shares one gutter,
     including when an expanded change is taller than the visible sheet. */
  .overview.phone {
    --section-inset:12px;
    --section-line:var(--color-edge);
    --section-ground:var(--color-canvas);
    --section-header:var(--color-sel);
  }
  .overview.phone, .phone .change-column, .phone .decision-column {
    display:grid; grid-template-columns:minmax(0,1fr); gap:1rem; max-width:none;
  }
  .phone :is(.readiness,.features,.risks,.feedback) {
    order:initial; min-width:0; margin-inline:-.5rem; padding:0 var(--section-inset);
    border:1px solid var(--section-line); border-radius:12px; background:var(--section-ground);
  }
  .phone .review-intro { padding:.125rem calc(var(--section-inset) - .5rem + 1px) .375rem; }
  .phone h1 { max-width:none; margin:0 0 .375rem; font-size:1.5rem; line-height:1.2; font-weight:650; text-wrap:pretty; }
  .phone .summary-copy { font-size:var(--text-callout); line-height:1.5; }
  .phone .section-title, .phone .feedback :global(.feedback-heading) {
    position:sticky; top:0; z-index:1;
    min-height:44px; margin:0 calc(-1 * var(--section-inset)); padding:.5rem var(--section-inset);
    border-radius:11px 11px 0 0; border-bottom:1px solid var(--section-line); background:var(--section-header);
  }
  .phone .section-title h2, .phone .feedback :global(.feedback-heading h2) {
    display:flex; align-items:center; justify-content:space-between; gap:.5rem; width:100%;
    color:var(--color-ink); font-size:var(--text-headline); font-weight:650; line-height:1.3; letter-spacing:0;
  }
  .phone .section-count {
    display:inline-flex; align-items:center; gap:5px; min-width:0; height:auto; padding:2px 7px;
    border:1px solid var(--section-line); border-radius:6px; background:var(--section-ground);
    color:var(--color-subtext); font-size:var(--text-footnote); font-weight:600;
  }
  .phone .features .section-count :global(svg) { color:var(--color-accent); }
  .phone .risks .section-count :global(svg) { color:var(--color-subtext); }
  .phone .feature-grid > .feature-item:first-child { border-top:0; }
  .phone .feature-item > summary, .phone .feature-static { min-height:44px; padding:.75rem 0; }
  .phone .feature-heading { padding:0; gap:.75rem; font-weight:600; font-size:var(--text-callout); }
  .phone .feature-heading > :global(svg) { flex:none; }
  .phone .feature-description { margin:.25rem 0 0; max-width:none; font-size:var(--text-subhead); line-height:1.5; }
  .phone .feature-detail { padding:0; }
  .phone .feature-parts { gap:.375rem; margin:0 0 .875rem; font-size:var(--text-subhead); }
  .phone .additional { margin:0; border-top:1px solid var(--color-edge-soft); }
  .phone .additional > summary { display:flex; align-items:center; min-height:44px; margin:0; color:var(--color-accent); }
  .phone .risk-attribution { margin:.65rem 0 0; color:var(--color-faint); font-size:var(--text-footnote); }
  .phone-risk { padding:.5rem 0 .875rem; min-width:0; }
  .phone-risk + .phone-risk { padding-top:.875rem; border-top:1px solid var(--color-edge-soft); }
  .phone-risk > h3 { color:var(--color-ink); font-size:var(--text-callout); font-weight:600; line-height:1.45; }
  .risk-facts { display:flex; flex-wrap:wrap; gap:.375rem 1rem; margin:.5rem 0; font-size:var(--text-footnote); }
  .risk-facts > div { display:flex; align-items:center; flex-wrap:wrap; gap:.375rem; }
  .risk-facts dt { color:var(--color-faint); }
  .risk-facts .risk-level { padding:0; background:none; font-size:inherit; }
  .phone .risk-detail { margin:0; padding:0; border:0; }
  .phone .risk-detail h3 { font-size:var(--text-footnote); }
  .phone .risk-detail p { font-size:var(--text-subhead); }
  .phone .checks { margin:0; }
  .phone .readiness :global(.conf), .phone .readiness :global(.verification) {
    border:0; border-radius:0; background:transparent; margin:0; padding:.75rem 0;
  }
  .phone .readiness :global(.verification.bare) { padding:0; }
  .phone .readiness > :global(.conf + .checks) { border-top:1px solid var(--color-edge-soft); }
  .phone .feedback { padding-bottom:var(--section-inset); }
  .phone .feedback :global(.feedback-heading svg) { order:1; color:var(--color-subtext); }
  .phone .risks > :global(button), .phone .assessment-message { margin:0 0 .75rem; }
  @media(prefers-reduced-motion:reduce) { .entry > summary :global(svg),.screen-preview :global(.thumbnail),.feature-heading > :global(svg) { transition:none; } }
</style>
