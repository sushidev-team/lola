<script lang="ts">
  import Chevron from "$lib/components/Chevron.svelte";
  import { tick, type Snippet } from 'svelte';
  import type { ReviewPackage, PRReadinessData } from '@bindings/internal/protocol';
  import { tooltip } from '$lib/tooltip';
  import { reviewOverview, reviewConfidence, riskWeight, type OverviewItem, type ReviewChange, type RiskLevel } from '$lib/reviewoverview';
  import Button from '../Button.svelte';
  import Icon from '../Icon.svelte';
  import Skeleton from '../Skeleton.svelte';
  import SSHRich from '../ssh/SSHRich.svelte';
  import ReviewThumbnail from './ReviewThumbnail.svelte';
  let { pkg, facts, sessionId, freshness, checking = false, onScreen, checks, feedback, onAssessRisks, assessment, onAssessConfidence, confidenceRequest }: {
    pkg:ReviewPackage; facts?:PRReadinessData; sessionId:string; freshness:'current'|'stale'|'unknown'; onScreen:(index:number)=>void;
    checking?:boolean; checks?:Snippet; feedback?:Snippet; onAssessRisks?:()=>void; assessment?:{state:string;message:string};
    onAssessConfidence?:()=>void; confidenceRequest?:{state:string;message:string};
  } = $props();
  const confidenceID = $props.id();
  let confidenceOpen = $state(false);
  let confidenceRoot: HTMLElement;
  let confidencePanel = $state<HTMLDivElement>();
  async function toggleConfidence() {
    confidenceOpen = !confidenceOpen;
    if (confidenceOpen) { await tick(); confidencePanel?.focus(); }
  }
  function closeConfidence(event: KeyboardEvent) {
    if (confidenceOpen && event.key === 'Escape') {
      event.preventDefault(); event.stopPropagation();
      confidenceOpen = false;
      confidenceRoot?.querySelector<HTMLButtonElement>('.confidence-info button')?.focus();
    }
  }
  function dismissConfidence(event: PointerEvent) {
    if (confidenceOpen && !confidenceRoot?.contains(event.target as Node)) confidenceOpen = false;
  }
  $effect(() => { pkg.id; confidenceOpen = false; });
  const overview = $derived(reviewOverview(pkg));
  const confidence = $derived(reviewConfidence(pkg,facts));
  const confidenceAction = $derived(confidenceRequest?.state === 'sending' ? 'Requesting self-assessment' : confidenceRequest?.state === 'accepted' ? 'Self-assessment requested' : confidenceRequest?.state === 'error' ? 'Retry self-assessment' : 'Ask agent for self-assessment');
  const screens = $derived(pkg.screens ?? []);
  const unrated = $derived(!overview.riskReportProvided || overview.risks.some(r => r.likelihood === 'unknown' || r.impact === 'unknown'));
</script>

<svelte:window onkeydowncapture={closeConfidence} onpointerdowncapture={dismissConfidence} />

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
{#snippet level(value: RiskLevel)}
  <span class="risk-level" data-level={value}><span class="level-bars" aria-hidden="true">{#each [1,2,3] as n}<i class:filled={n <= riskWeight[value]}></i>{/each}</span>{value === 'unknown' ? 'Not rated' : value}</span>
{/snippet}

<div class="overview">
  <div class="change-column">
    <header class="review-intro">
      <h1>{overview.title}</h1>
      <p class="summary-copy">{overview.summary}</p>
      {#if freshness === 'stale'}<div class="intro-meta"><span class="stale" role="status"><Icon name="circle-alert" size={14}/>Outdated material</span></div>{/if}
    </header>
      <section class="review-panel features" aria-label="Changes in this review">
        <div class="section-title"><h2>What changed<span class="section-count">{overview.changes.length}</span></h2></div>
        {#if overview.changes.length}{@render features(overview.changes.slice(0,4))}{:else}<p class="empty-copy">No feature breakdown provided.</p>{/if}
        {#if overview.changes.length > 4}<details class="additional"><summary><Chevron class="disclosure-chevron" />{overview.changes.length-4} more {overview.changes.length === 5 ? 'change' : 'changes'}</summary>{@render features(overview.changes.slice(4))}</details>{/if}
      </section>
      {#if screens.length}<section class="review-panel visual-review" aria-label="Screenshot previews">
        <div class="section-title"><h2>See the changes<span class="section-count">{screens.length}</span></h2><Button size="xs" onclick={() => onScreen(0)}>View all<Icon name="arrow-right" size={14}/></Button></div>
        <div class="screenshot-grid">{#each screens.slice(0,4) as screen,i}<button class="screen-preview" onclick={() => onScreen(i)} aria-label={`Review screenshot: ${screen.title}`}>
          <ReviewThumbnail {pkg} artifact={screen.after} label={`Preview: ${screen.title}`}/>
          <span class="preview-caption"><span>{screen.title}</span><Icon name="arrow-right" size={14}/></span>
        </button>{/each}</div>
      </section>{/if}
      <details class="additional source-details"><summary><Chevron class="disclosure-chevron" />Source &amp; revision</summary><div class="source-copy"><code>{pkg.base.slice(0,12)} → {pkg.head.slice(0,12)}</code><p>Write-up, risk estimates, and screenshots are agent-provided.</p></div>{#if overview.details.length}{@render entries(overview.details,'')}{/if}</details>
  </div>
  <div class="decision-column">
    <div class="readiness">
      <section class="confidence" aria-label="Review confidence" bind:this={confidenceRoot}>
        <div class="confidence-label"><div class="confidence-heading"><h2><Icon name="shield-check" size={17}/>Confidence</h2><span class="confidence-info"><Button icon size="xs" aria-label="About confidence" aria-haspopup="dialog" aria-expanded={confidenceOpen} aria-controls={confidenceID} onclick={toggleConfidence}><Icon name="info" size={15}/></Button></span></div><span class:has-issues={!checking && confidence.failed > 0}>{#if checking}<span role="status" aria-label="Loading confidence"><span class="sr-only">Loading confidence…</span><Skeleton width="90px" height="18px"/></span>{:else}{confidence.label}{/if}</span></div>
        {#if checking}<div class="score"><Skeleton width="100px" height="48px"/></div>
        {:else if confidence.score !== null}<strong class="score">{confidence.score}<small>/100</small></strong>{/if}
        {#if confidenceOpen}
        <div id={confidenceID} class="confidence-popover" role="dialog" aria-label="About confidence" tabindex="-1" bind:this={confidencePanel}>
        <div class="confidence-popover-heading"><h3>How confidence is calculated</h3><Button icon size="xs" aria-label="Close confidence details" onclick={() => { confidenceOpen = false; confidenceRoot.querySelector<HTMLButtonElement>('.confidence-info button')?.focus(); }}><Icon name="x" size={14}/></Button></div>
        {#if checking}
          <div class="confidence-breakdown" aria-hidden="true"><Skeleton height="40px"/></div>
          <div class="score-explanation"><Skeleton width="65%" height="18px"/></div>
        {:else}
        <dl class="confidence-breakdown">
          <div><dt>Observed results <small>70%</small></dt><dd>{#if confidence.evidence === null}Pending{:else}<span class="confidence-number">{Math.round(confidence.evidence)}/100</span>{/if}</dd></div>
          <div><dt>Agent self-confidence <small>30%</small></dt><dd>
            {#if confidence.agent}<span class="confidence-number">{confidence.agent.score}/100</span>{confidence.current ? '' : ' · unverified'}
            {:else}
              <span class="confidence-trigger" use:tooltip={onAssessConfidence ? confidenceAction : 'Resume the agent to request a self-assessment'}>
                <Button icon size="xs" variant="ghost" class="text-magenta!" aria-label={confidenceAction} loading={confidenceRequest?.state === 'sending'} disabled={!onAssessConfidence || confidenceRequest?.state === 'accepted'} onclick={onAssessConfidence}>
                  {#if confidenceRequest?.state !== 'sending'}<Icon name={confidenceRequest?.state === 'accepted' ? 'check' : confidenceRequest?.state === 'error' ? 'refresh-cw' : 'sparkles'} size={16}/>{/if}
                </Button>
              </span>
            {/if}
          </dd></div>
        </dl>
        {#if confidence.current && confidence.pending}<p class="confidence-note">{confidence.pending} {confidence.pending === 1 ? 'result is' : 'results are'} pending or unavailable; excluded from the score.</p>{/if}
        {#if confidence.failed}<p class="confidence-note has-issues">{confidence.failed} {confidence.failed === 1 ? 'observed issue lowers' : 'observed issues lower'} confidence.</p>{/if}
        {#if !confidence.agent && confidenceRequest?.message}<div class="confidence-request"><p class="assessment-message" role={confidenceRequest.state === 'error' ? 'alert' : 'status'}>{confidenceRequest.message}</p></div>{/if}
        <div class="score-explanation">
          {#if !confidence.current}<p>This review hasn’t been matched to the current PR. Live PR checks are shown below.</p>{/if}
          {#if confidence.agent}<p><strong>Agent’s rationale:</strong> {confidence.agent.basis}</p>{:else}<p>The agent hasn’t provided a valid self-assessment for this package. A combined score needs both observed results and an explicit self-assessment.</p>{/if}
          <dl>{#each confidence.signals as signal}<div><dt>{signal.label}</dt><dd>{signal.value}</dd></div>{/each}</dl>
          <p>70% observed results + 30% agent self-confidence. Observed results are the share of known signals that pass, weighted equally. Failures, conflicts, unresolved threads, and requested changes lower that share. Pending or unavailable signals are excluded. Approval does not change confidence.</p>
          <p>This is an assessment of the current revision, not a probability of correctness or permission to merge. The score is provisional while evidence is incomplete.</p>
          {#if facts?.observedAt}<p>Observed on GitHub {new Date(facts.observedAt).toLocaleString()}. CI revision: {facts.checksHead?.slice(0,12) || 'unconfirmed'}.</p>{/if}
        </div>
        {/if}
        </div>
        {/if}
      </section>
      {#if checks}<div class="checks">{@render checks()}</div>{/if}
    </div>
      <section class="review-panel risks" aria-label="Reported risks">
        <div class="section-title"><h2>Risks &amp; follow-up<span class="section-count">{overview.risks.length}</span></h2></div><p class="risk-attribution">Agent assessment</p>
        {#if overview.risks.length}
          <div class="risk-columns" aria-hidden="true"><span>Risk</span><span>Likelihood</span><span>Impact</span></div>
          {#each overview.risks as risk}<details class="entry risk-entry" class:priority={risk.priority >= 6}>
            <summary><span class="risk-title"><Icon name="chevron-right" size={14}/>{risk.title}</span><span class="risk-rating" aria-label={`Likelihood: ${risk.likelihood}`}><span class="rating-label" aria-hidden="true">Likelihood</span>{@render level(risk.likelihood)}</span><span class="risk-rating" aria-label={`Impact: ${risk.impact}`}><span class="rating-label" aria-hidden="true">Impact</span>{@render level(risk.impact)}</span></summary>
            <div class="risk-detail">
              {#if risk.description}<div><h3>What could happen</h3><p>{risk.description}</p></div>{/if}
              {#if risk.assess}<div><h3>How to assess it</h3><p>{risk.assess}</p></div>{/if}
            </div>
          </details>{/each}
        {:else}<p class="empty-copy">{overview.riskReportProvided ? 'No unresolved risks reported.' : 'Risks have not been assessed.'}</p>{/if}
        {#if unrated && onAssessRisks}<Button size="xs" variant="secondary" loading={assessment?.state === 'sending'} disabled={assessment?.state === 'accepted'} onclick={onAssessRisks}><Icon name="sparkles" size={13}/>{assessment?.state === 'accepted' ? 'Assessment requested' : assessment?.state === 'error' ? 'Retry assessment' : 'Ask agent to assess risks'}</Button>{/if}
        {#if assessment?.message}<p class="assessment-message" role={assessment.state === 'error' ? 'alert' : 'status'}>{assessment.message}</p>{/if}
      </section>
      {#if feedback}<div class="review-panel feedback">{@render feedback()}</div>{/if}
  </div>
</div>

<style>
  summary::marker { content:""; }
  summary > :global(.disclosure-chevron) { margin-right:6px; vertical-align:-2px; color:var(--color-ink-2); }
  details[open] > summary > :global(.disclosure-chevron) { transform:rotate(90deg); }
  .overview { display:grid; grid-template-columns:minmax(0,1fr) minmax(370px,.7fr); gap:40px; align-items:start; max-width:1400px; margin:0 auto; min-width:0; }
  .change-column,.decision-column { min-width:0; display:grid; gap:32px; }
  .review-intro { min-width:0; padding:4px 0 0; }
  h1 { margin:0 0 16px; max-width:32ch; font-size:30px; line-height:1.2; letter-spacing:-.025em; font-weight:600; color:var(--color-ink); text-wrap:balance; overflow-wrap:anywhere; }
  .summary-copy { max-width:65ch; color:var(--color-ink-2); font-size:14px; line-height:1.65; margin:0; }
  .intro-meta { margin-top:16px; font-size:12px; }
  .stale { display:inline-flex; align-items:center; gap:6px; color:var(--color-warn); }
  h2 { display:flex; align-items:center; gap:10px; font-size:14px; font-weight:600; color:var(--color-ink); }
  .review-panel { min-width:0; }
  .section-title { display:flex; align-items:center; justify-content:space-between; gap:12px; margin-bottom:16px; }
  .section-count { font-size:12px; font-weight:400; font-variant-numeric:tabular-nums; color:var(--color-ink-2); }
  .readiness { min-width:0; padding:24px; border:1px solid color-mix(in srgb,var(--color-accent) 22%,var(--color-edge)); border-radius:16px; background:var(--color-panel); }
  .confidence { position:relative; display:grid; grid-template-columns:minmax(0,1fr) auto; gap:12px; padding-bottom:20px; border-bottom:1px solid color-mix(in srgb,var(--color-edge) 60%,transparent); }
  .confidence-label { display:grid; align-content:center; gap:6px; }
  .confidence-heading { display:flex; align-items:center; gap:4px; }
  .confidence-label h2 { font-size:13px; }
  .confidence-popover { position:absolute; top:calc(100% - 10px); right:0; z-index:30; width:min(480px,calc(100vw - 48px)); max-height:min(60vh,600px); overflow:auto; display:grid; gap:14px; padding:20px; border:1px solid var(--color-edge); border-radius:10px; background:var(--color-panel); color:var(--color-ink); box-shadow:0 12px 32px rgb(0 0 0 / .25); }
  .confidence-popover-heading { display:flex; align-items:center; justify-content:space-between; gap:12px; }
  .confidence-popover-heading h3 { margin:0; font-size:13px; font-weight:600; }
  .confidence-popover:focus { outline:none; }
  .confidence-label h2 :global(svg) { color:var(--color-magenta); }
  .confidence-label > span { color:var(--color-ink-2); font-size:12px; }
  .score,.confidence-number { font-family:var(--font-mono); font-variant-numeric:lining-nums tabular-nums slashed-zero; }
  .score { align-self:center; white-space:nowrap; font-size:44px; line-height:1.1; letter-spacing:-.04em; font-weight:700; color:var(--color-magenta); }
  .score small { font-size:14px; font-weight:400; letter-spacing:0; color:var(--color-ink-2); margin-left:6px; }
  .confidence-trigger { align-self:center; display:inline-flex; }
  .confidence-breakdown { grid-column:1/-1; margin:0; display:grid; gap:8px; font-size:12px; }
  .confidence-breakdown > div { display:flex; align-items:center; justify-content:space-between; gap:12px; }
  .confidence-breakdown dt { color:var(--color-ink-2); }
  .confidence-breakdown small { margin-left:4px; font-size:11px; }
  .confidence-breakdown dd { margin:0; text-align:right; font-variant-numeric:tabular-nums; }
  .confidence-note { grid-column:1/-1; margin:0; color:var(--color-ink-2); font-size:12px; line-height:1.5; }
  .confidence-request { grid-column:1/-1; min-width:0; }
  .confidence .has-issues { color:var(--color-warn); }
  .score-explanation { grid-column:1/-1; font-size:12px; color:var(--color-ink-2); }
  .score-explanation dl { margin:12px 0 8px; } .score-explanation dl > div { display:flex; justify-content:space-between; gap:12px; margin:8px 0; }
  .score-explanation p { line-height:1.6; }
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
  .source-details { margin:0; } .source-copy { font-size:12px; line-height:1.6; margin:12px 0; }
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
  .risk-columns,.risk-entry > summary { display:grid; grid-template-columns:minmax(0,1fr) 84px 84px; gap:14px; }
  .risk-columns { padding:0 0 12px; font-size:12px; color:var(--color-ink-2); border-bottom:1px solid var(--color-edge); }
  .risk-entry > summary { padding:18px 0; align-items:center; }
  .risk-title { display:flex; gap:8px; align-items:baseline; overflow-wrap:anywhere; font-size:13px; line-height:1.55; }
  .risk-title :global(svg) { position:relative; top:2px; }
  .risk-rating { min-width:0; }
  .rating-label { display:none; }
  .risk-level { display:inline-flex; align-items:center; gap:6px; padding:5px 7px; border-radius:6px; font-size:12px; line-height:16px; text-transform:capitalize; white-space:nowrap; color:var(--color-ink-2); background:color-mix(in srgb,var(--color-ink) 5%,transparent); }
  .level-bars { display:inline-flex; align-items:end; gap:2px; height:12px; }
  .level-bars i { width:3px; height:4px; border-radius:1px; background:color-mix(in srgb,var(--color-ink-2) 22%,transparent); }
  .level-bars i:nth-child(2) { height:8px; } .level-bars i:nth-child(3) { height:12px; }
  .level-bars i.filled { background:currentColor; }
  [data-level=high] { color:var(--color-warn); background:color-mix(in srgb,var(--color-warn) 12%,transparent); }
  [data-level=medium] { color:var(--color-ink); }
  .risk-detail { display:grid; gap:16px; padding:0 0 20px 22px; }
  .risk-detail h3 { font-size:12px; font-weight:500; color:var(--color-ink-2); margin:0 0 6px; }
  @container (max-width:440px) {
    .risk-columns { display:none; }
    .risk-attribution { margin-bottom:16px; }
    .risk-entry:first-of-type { border-top:1px solid var(--color-edge); }
    .risk-entry > summary { grid-template-columns:minmax(0,1fr) minmax(0,1fr); gap:12px; }
    .risk-title { grid-column:1/-1; font-size:14px; }
    .risk-rating { display:flex; flex-direction:column; align-items:flex-start; gap:6px; padding-left:22px; }
    .rating-label { display:block; font-size:12px; color:var(--color-ink-2); }
  }
  .risk-detail p { font-size:13px; line-height:1.6; color:var(--color-ink); max-width:65ch; overflow-wrap:anywhere; }
  .risks > :global(button) { margin-top:16px; }
  .assessment-message { color:var(--color-ink-2); font-size:12px; line-height:1.5; margin-top:12px; }
  .feedback { padding:20px; border-radius:12px; background:color-mix(in srgb,var(--color-info) 6%,var(--color-panel)); }
  @media(min-width:1200px) { .screenshot-grid { grid-template-columns:repeat(4,minmax(0,1fr)); gap:16px; } }
  @media(max-width:1100px) { .overview { gap:32px; grid-template-columns:minmax(0,1fr) 330px; } h1 { font-size:26px; } .readiness { padding:20px; } }
  @media(max-width:800px) { .overview { grid-template-columns:minmax(0,1fr); gap:32px; max-width:620px; } .change-column,.decision-column { display:contents; } .readiness { order:1; } .features { order:2; } .visual-review { order:3; } .risks { order:4; } .feedback { order:5; } .source-details { order:6; } }
  @media(max-width:440px) { .risks { padding:20px 16px; } .features { padding:20px 16px; } .features h2 { font-size:20px; } .feature-description { margin-right:0; } h1 { font-size:24px; } .screenshot-grid { gap:16px 12px; } .screen-preview :global(.thumbnail) { padding:6px; border-radius:8px; } .preview-caption { font-size:12px; } .readiness { padding:18px; } }
  @media(prefers-reduced-motion:reduce) { .entry > summary :global(svg),.screen-preview :global(.thumbnail),.feature-heading > :global(svg) { transition:none; } }
</style>
