<script lang="ts">
  import type { Snippet } from 'svelte';
  import type { ReviewPackage, PRReadinessData } from '@bindings/internal/protocol';
  import { reviewOverview, reviewConfidence, riskWeight, type OverviewItem, type RiskLevel } from '$lib/reviewoverview';
  import Button from '../Button.svelte';
  import Icon from '../Icon.svelte';
  import SSHRich from '../ssh/SSHRich.svelte';
  import ReviewThumbnail from './ReviewThumbnail.svelte';
  let { pkg, facts, sessionId, freshness, onScreen, checks, feedback, onAssessRisks, assessment }: {
    pkg:ReviewPackage; facts?:PRReadinessData; sessionId:string; freshness:'current'|'stale'|'unknown'; onScreen:(index:number)=>void;
    checks?:Snippet; feedback?:Snippet; onAssessRisks?:()=>void; assessment?:{state:string;message:string};
  } = $props();
  const overview = $derived(reviewOverview(pkg));
  const confidence = $derived(reviewConfidence(pkg,facts));
  const screens = $derived(pkg.screens ?? []);
  const unrated = $derived(!overview.riskReportProvided || overview.risks.some(r => r.likelihood === 'unknown' || r.impact === 'unknown'));
</script>

{#snippet entries(items: OverviewItem[], empty: string)}
  {#if items.length}<div class="entries">{#each items as item}<details class="entry">
    <summary><Icon name="chevron-right" size={14}/><span>{item.title}</span></summary>
    <div class="entry-detail selectable"><SSHRich {sessionId} text={item.body}/></div>
  </details>{/each}</div>{:else}<p class="empty-copy">{empty}</p>{/if}
{/snippet}
{#snippet level(value: RiskLevel)}
  <span class="risk-level" data-level={value}><span class="level-bars" aria-hidden="true">{#each [1,2,3] as n}<i class:filled={n <= riskWeight[value]}></i>{/each}</span>{value === 'unknown' ? 'Not rated' : value}</span>
{/snippet}

<div class="overview">
  <div class="change-column">
    <header class="review-intro">
      <h1>{overview.title}</h1>
      <p class="summary-copy">{overview.summary}</p>
      <div class="intro-meta"><span class:stale={freshness !== 'current'} role="status"><Icon name={freshness === 'current' ? 'git-pull-request' : 'circle-alert'} size={14}/>{freshness === 'current' ? 'Matches current PR' : freshness === 'stale' ? 'Outdated material' : 'Freshness unknown'}</span></div>
      {#if screens.length}<Button variant="secondary" onclick={() => onScreen(0)}><Icon name="image" size={15}/>Explore {screens.length} {screens.length === 1 ? 'screen' : 'screens'}<Icon name="arrow-right" size={14}/></Button>{/if}
    </header>
      <section aria-label="Changes in this review">
        <div class="section-title"><h2><Icon name="layers" size={17}/>What changed</h2><span>{overview.changes.length}</span></div>
        {@render entries(overview.changes.slice(0,4),'No feature breakdown provided.')}
        {#if overview.changes.length > 4}<details class="additional"><summary>{overview.changes.length-4} more {overview.changes.length === 5 ? 'change' : 'changes'}</summary>{@render entries(overview.changes.slice(4),'')}</details>{/if}
      </section>
      {#if screens.length}<section class="visual-review" aria-label="Screenshot previews">
        <div class="section-title"><h2><Icon name="image" size={17}/>See the changes</h2><Button size="xs" onclick={() => onScreen(0)}>View all<Icon name="arrow-right" size={14}/></Button></div>
        <div class="screenshot-grid">{#each screens.slice(0,4) as screen,i}<button class="screen-preview" onclick={() => onScreen(i)} aria-label={`Review screenshot: ${screen.title}`}>
          <ReviewThumbnail {pkg} artifact={screen.after} label={`Preview: ${screen.title}`}/>
          <span class="preview-caption"><span>{screen.title}</span><Icon name="arrow-right" size={14}/></span>
        </button>{/each}</div>
      </section>{/if}
      <details class="additional source-details"><summary>Source &amp; revision</summary><div class="source-copy"><code>{pkg.base.slice(0,12)} → {pkg.head.slice(0,12)}</code><p>Write-up, risk estimates, and screenshots are agent-provided.</p></div>{#if overview.details.length}{@render entries(overview.details,'')}{/if}</details>
  </div>
  <div class="decision-column">
    <div class="readiness">
      <section class="confidence" aria-label="Review confidence">
        <div class="confidence-label"><h2><Icon name="shield-check" size={17}/>Review confidence</h2><span>{confidence.score === null ? 'Revision unconfirmed' : `${confidence.confirmed} / 5 signals`}</span></div>
        <strong class="score">{confidence.score ?? '—'}{#if confidence.score !== null}<small>/100</small>{/if}</strong>
        <div class="signal-chart" role="img" aria-label={confidence.score === null ? 'Confidence not scored: revision unconfirmed' : `Evidence coverage: ${confidence.score} out of 100`}>
          {#each confidence.signals as signal}<span class:confirmed={signal.confirmed} class:failed={signal.failed} title={`${signal.label}: ${signal.value}`}></span>{/each}
        </div>
        <details class="score-explanation"><summary>Evidence behind this score</summary><dl>{#each confidence.signals as signal}<div><dt>{signal.label}</dt><dd>{signal.value}</dd></div>{/each}</dl><p>20 points per confirmed signal for this revision. Evidence coverage, not a probability of correct code. Agent claims do not increase it.</p></details>
      </section>
      {#if checks}<div class="checks">{@render checks()}</div>{/if}
    </div>
      <section class="risks" aria-label="Reported risks">
        <div class="section-title"><h2><Icon name="circle-alert" size={17}/>Risks &amp; follow-up</h2><span title="Ordered by likelihood × impact. Unrated risks precede lower-rated risks.">Agent assessment</span></div>
        {#if overview.risks.length}
          <div class="risk-columns" aria-hidden="true"><span>Risk</span><span>Likelihood</span><span>Impact</span></div>
          {#each overview.risks as risk}<details class="entry risk-entry" class:priority={risk.priority >= 6}>
            <summary><span class="risk-title"><Icon name="chevron-right" size={14}/>{risk.title}</span><span aria-label={`Likelihood: ${risk.likelihood}`}>{@render level(risk.likelihood)}</span><span aria-label={`Impact: ${risk.impact}`}>{@render level(risk.impact)}</span></summary>
            <div class="entry-detail selectable"><SSHRich {sessionId} text={risk.body}/></div>
          </details>{/each}
        {:else}<p class="empty-copy">{overview.riskReportProvided ? 'No unresolved risks reported.' : 'Risks have not been assessed.'}</p>{/if}
        {#if unrated && onAssessRisks}<Button size="xs" variant="secondary" loading={assessment?.state === 'sending'} disabled={assessment?.state === 'accepted'} onclick={onAssessRisks}><Icon name="sparkles" size={13}/>{assessment?.state === 'accepted' ? 'Assessment requested' : assessment?.state === 'error' ? 'Retry assessment' : 'Ask agent to assess risks'}</Button>{/if}
        {#if assessment?.message}<p class="assessment-message" role={assessment.state === 'error' ? 'alert' : 'status'}>{assessment.message}</p>{/if}
      </section>
      {#if feedback}<div class="feedback">{@render feedback()}</div>{/if}
  </div>
</div>

<style>
  .overview { display:grid; grid-template-columns:minmax(0,1.2fr) minmax(360px,1fr); gap:48px; align-items:start; min-width:0; }
  .review-intro { min-width:0; padding:8px 0; }
  h1 { margin:0 0 16px; max-width:30ch; font-size:28px; line-height:1.25; font-weight:600; color:var(--color-ink); text-wrap:balance; overflow-wrap:anywhere; }
  .summary-copy { max-width:60ch; color:var(--color-ink-2); font-size:15px; line-height:1.6; margin-bottom:20px; }
  .intro-meta { display:flex; align-items:center; flex-wrap:wrap; gap:16px; color:var(--color-ink-2); font-size:11px; margin-bottom:20px; }
  .intro-meta > span:first-child { display:flex; align-items:center; gap:6px; }
  .stale { color:var(--color-warn); }
  h2 { display:flex; align-items:center; gap:8px; font-size:14px; font-weight:600; color:var(--color-ink); }
  .readiness { min-width:0; background:var(--color-canvas); border:1px solid var(--color-edge); border-radius:8px; padding:20px; }
  .confidence { display:grid; grid-template-columns:minmax(0,1fr) auto; gap:12px 16px; }
  .confidence-label { display:grid; align-content:center; gap:6px; }
  .confidence-label > span { color:var(--color-ink-2); font-size:11px; }
  .score { font-size:32px; line-height:1.2; font-weight:600; font-variant-numeric:tabular-nums; color:var(--color-ink); }
  .score small { font-size:12px; font-weight:400; color:var(--color-ink-2); margin-left:4px; }
  .signal-chart { display:flex; gap:4px; height:5px; grid-column:1/-1; }
  .signal-chart span { flex:1; background:var(--color-edge); border-radius:2px; }
  .signal-chart .confirmed { background:var(--color-good); } .signal-chart .failed { background:var(--color-warn); }
  .score-explanation { grid-column:1/-1; font-size:11px; color:var(--color-ink-2); }
  .score-explanation dl { margin:12px 0 8px; } .score-explanation dl > div { display:flex; justify-content:space-between; gap:12px; margin:6px 0; }
  .score-explanation p { max-width:65ch; line-height:1.6; }
  .checks { margin-top:20px; }
  .section-title { display:flex; align-items:center; justify-content:space-between; gap:12px; margin-bottom:12px; }
  .section-title > span { color:var(--color-ink-2); font-size:11px; }
  .change-column,.decision-column { min-width:0; display:grid; gap:32px; }
  summary { cursor:pointer; }
  summary:focus-visible,.screen-preview:focus-visible { outline:2px solid var(--color-accent); outline-offset:3px; }
  .entries { border-top:1px solid var(--color-edge); }
  .entry { border-bottom:1px solid var(--color-edge); }
  .entry > summary { display:flex; align-items:center; gap:10px; list-style:none; padding:16px 0; font-size:13px; line-height:1.5; color:var(--color-ink); }
  .entry > summary::-webkit-details-marker { display:none; }
  .entry > summary :global(svg) { flex-shrink:0; color:var(--color-ink-2); transition:transform 150ms ease-out; }
  .entry[open] > summary :global(svg) { transform:rotate(90deg); }
  .entry > summary:hover { color:var(--color-accent); }
  .entry-detail { padding:0 0 18px 24px; max-width:75ch; font-size:13px; overflow-wrap:anywhere; }
  .entry-detail :global(pre) { max-width:100%; overflow:auto; }
  .empty-copy { color:var(--color-ink-2); padding:8px 0 16px; font-size:12px; }
  .additional { margin-top:12px; font-size:12px; color:var(--color-ink-2); }
  .additional > summary { margin-bottom:12px; }
  .source-details { margin-top:0; } .source-copy { font-size:11px; line-height:1.6; margin:12px 0; }
  .screenshot-grid { display:grid; grid-template-columns:repeat(2,minmax(0,1fr)); gap:16px; }
  .screen-preview { text-align:left; min-width:0; border:1px solid var(--color-edge); border-radius:6px; overflow:hidden; background:var(--color-canvas); cursor:pointer; transition:border-color 150ms; }
  .screen-preview:hover { border-color:var(--color-accent); }
  .preview-caption { display:flex; justify-content:space-between; align-items:center; gap:12px; padding:12px; font-size:12px; color:var(--color-ink); }
  .preview-caption > span { overflow-wrap:anywhere; } .preview-caption :global(svg) { flex-shrink:0; }
  .risk-columns,.risk-entry > summary { display:grid; grid-template-columns:minmax(0,1fr) 84px 84px; gap:12px; }
  .risk-columns { font-size:10px; color:var(--color-ink-2); padding:0 0 8px; border-bottom:1px solid var(--color-edge); }
  .risk-title { display:flex; gap:8px; align-items:center; overflow-wrap:anywhere; }
  .risk-entry.priority .risk-title { color:var(--color-warn); }
  .risk-level { display:flex; gap:6px; align-items:center; font-size:11px; text-transform:capitalize; color:var(--color-ink-2); white-space:nowrap; }
  .level-bars { display:flex; gap:2px; align-items:end; height:12px; }
  .level-bars i { width:3px; height:5px; border-radius:1px; background:var(--color-edge); }
  .level-bars i:nth-child(2) { height:8px; } .level-bars i:nth-child(3) { height:11px; }
  .level-bars i.filled { background:currentColor; }
  [data-level=high] { color:var(--color-warn); } [data-level=medium] { color:var(--color-ink); }
  .risks > :global(button) { margin-top:16px; }
  .assessment-message { color:var(--color-ink-2); font-size:12px; line-height:1.5; margin-top:12px; }
  .feedback { border-top:1px solid var(--color-edge); padding-top:24px; }
  @media(min-width:1700px) { .screenshot-grid { grid-template-columns:repeat(4,minmax(0,1fr)); } }
  @media(max-width:1100px) { .overview { gap:28px; grid-template-columns:minmax(0,1fr) minmax(340px,1fr); } h1 { font-size:24px; } }
  @media(max-width:800px) { .overview { grid-template-columns:minmax(0,1fr); gap:28px; } .review-intro { padding-top:0; } }
  @media(max-width:440px) { .risk-columns,.risk-entry > summary { grid-template-columns:minmax(0,1fr) 70px 70px; gap:8px; } .risk-level { gap:4px; font-size:10px; } .readiness { padding:16px; } .screenshot-grid { gap:12px; } }
  @media(prefers-reduced-motion:reduce) { .entry > summary :global(svg),.screen-preview { transition:none; } }
</style>
