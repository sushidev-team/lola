<script lang="ts">
  import type { ReviewPackage, PRReadinessData } from '@bindings/internal/protocol';
  import { reviewOverview, reviewConfidence, type OverviewItem } from '$lib/reviewoverview';
  import Button from '../Button.svelte';
  import Icon from '../Icon.svelte';
  import SSHRich from '../ssh/SSHRich.svelte';
  import ReviewThumbnail from './ReviewThumbnail.svelte';
  let { pkg, facts, sessionId, freshness, onScreen, onChecks }: { pkg:ReviewPackage; facts?:PRReadinessData; sessionId:string; freshness:'current'|'stale'|'unknown'; onScreen:(index:number)=>void; onChecks:()=>void } = $props();
  const overview = $derived(reviewOverview(pkg));
  const confidence = $derived(reviewConfidence(pkg,facts));
  const screens = $derived(pkg.screens ?? []);
  const paired = $derived(screens.filter(s => s.before).length);
</script>

{#snippet entries(items: OverviewItem[], empty: string)}
  {#if items.length}<div class="entries">{#each items as item}<details class="entry">
    <summary><Icon name="chevron-right" size={14}/><span>{item.title}</span></summary>
    <div class="entry-detail selectable"><SSHRich {sessionId} text={item.body}/></div>
  </details>{/each}</div>{:else}<p class="empty-copy">{empty}</p>{/if}
{/snippet}

<div class="overview">
  <div class="overview-top">
    <header class="review-intro">
      <div class="intro-status"><span class:stale={freshness === 'stale'} role="status"><Icon name={freshness === 'current' ? 'git-pull-request' : 'circle-alert'} size={14}/>{freshness === 'current' ? 'Matches current PR' : freshness === 'stale' ? 'Outdated material' : 'Freshness unknown'}</span><span>{new Date(pkg.createdAt).toLocaleDateString()}</span></div>
      <h1>{overview.title}</h1>
      <p class="summary-copy">{overview.summary}</p>
      <div class="intro-actions">
        {#if screens.length}<Button variant="primary" onclick={() => onScreen(0)}><Icon name="image" size={15}/>Explore {screens.length} {screens.length === 1 ? 'screen' : 'screens'}<Icon name="arrow-right" size={14}/></Button>{/if}
        <Button variant="secondary" onclick={onChecks}><Icon name="list" size={14}/>Inspect checks</Button>
      </div>
      <details class="revision"><summary>Revision details</summary><div><code>{pkg.base.slice(0,12)} → {pkg.head.slice(0,12)}</code><p>{freshness === 'current' ? 'Package head matches the current PR.' : freshness === 'stale' ? 'The PR differs from this package.' : 'The current PR head could not be confirmed.'} Write-up and screenshots are agent-provided.</p></div></details>
      {#if screens.length}<section class="visual-review" aria-label="Screenshot previews">
    <div class="section-title"><h2><Icon name="image" size={17}/>See the changes</h2><Button onclick={() => onScreen(0)}>View all {screens.length}<Icon name="arrow-right" size={14}/></Button></div>
    <div class="capture-coverage"><div class="coverage-bar" role="img" aria-label={`${paired} before-and-after comparisons, ${screens.length-paired} after-only screenshots`}><span style:width={`${paired/screens.length*100}%`}></span></div><span>{paired} before / after · {screens.length-paired} after only</span></div>
    <div class="screenshot-grid">{#each screens.slice(0,4) as screen,i}<button class="screen-preview" onclick={() => onScreen(i)} aria-label={`Review screenshot: ${screen.title}`}>
      <ReviewThumbnail {pkg} artifact={screen.after} label={`Preview: ${screen.title}`}/>
      <span class="preview-caption"><span>{screen.title}</span><Icon name="arrow-right" size={15}/></span>
      <span class="preview-type">{screen.before ? 'Before & after' : 'After'}</span>
    </button>{/each}</div>
  </section>{/if}
    </header>
    <section class="confidence" aria-label="Review confidence">
      <div class="confidence-heading"><h2>Review confidence</h2><Icon name="shield-check" size={18}/></div>
      <div class="score-line"><strong>{confidence.score ?? '—'}</strong>{#if confidence.score !== null}<span>/ 100</span>{/if}<p>{confidence.score === null ? 'Awaiting matching PR evidence' : `${confidence.confirmed} of 5 signals confirmed`}</p></div>
      <div class="signal-chart" role="img" aria-label={confidence.score === null ? 'Confidence not scored: revision unconfirmed' : `Evidence coverage: ${confidence.score} out of 100`}>
        {#each confidence.signals as signal}<span class:confirmed={signal.confirmed} class:failed={signal.failed} title={`${signal.label}: ${signal.value}`}></span>{/each}
      </div>
      <dl class="signals">{#each confidence.signals as signal}<div><dt>{signal.label}</dt><dd class:confirmed={signal.confirmed} class:failed={signal.failed}><Icon name={signal.confirmed ? 'check' : signal.failed ? 'circle-alert' : 'minus'} size={13}/>{signal.value}</dd></div>{/each}</dl>
      <details class="score-explanation"><summary>Evidence coverage · how it’s scored</summary><p>Each confirmed signal contributes 20 points. Missing or failing signals contribute zero. This measures evidence coverage, not the probability of correct code. Agent claims and screenshots do not increase it.</p></details>
      {#if confidence.score !== null && facts?.mergeBlocked}<p class="merge-note"><Icon name="circle-alert" size={14}/>{facts.mergeBlocked}</p>{/if}
    </section>
  </div>

  <div class="review-sections">
    <section class="features" aria-label="Changes in this review">
      <div class="section-title"><h2><Icon name="layers" size={17}/>What changed</h2><span>{overview.changes.length} {overview.changes.length === 1 ? 'change' : 'changes'}</span></div>
      {@render entries(overview.changes,'No feature breakdown was provided.')}
      {#if overview.details.length}<details class="additional"><summary>More context <span>{overview.details.length}</span></summary>{@render entries(overview.details,'')}</details>{/if}
    </section>
    <div class="evidence-column">
      <section aria-label="Agent verification"><div class="section-title"><h2><Icon name="terminal" size={17}/>Verification</h2><span>Agent-reported</span></div>{@render entries(overview.verification,'No verification results reported.')}</section>
      <section aria-label="Reported risks"><div class="section-title"><h2><Icon name="circle-alert" size={17}/>Risks &amp; follow-up</h2><span>Agent-reported</span></div>{@render entries(overview.risks,'No risk assessment provided.')}</section>
    </div>
  </div>


</div>

<style>
  .overview { display:grid; gap:32px; min-width:0; }
  .overview-top { display:grid; grid-template-columns:minmax(0,1fr) minmax(300px,360px); gap:48px; align-items:start; }
  .review-intro { padding:8px 0; min-width:0; }
  .intro-status { display:flex; align-items:center; flex-wrap:wrap; gap:16px; color:var(--color-ink-2); font-size:12px; }
  .intro-status > span:first-child { display:flex; align-items:center; gap:6px; }
  .stale { color:var(--color-warn); }
  h1 { margin:18px 0 14px; max-width:38ch; font-size:26px; line-height:1.25; font-weight:600; color:var(--color-ink); overflow-wrap:anywhere; }
  .summary-copy { max-width:70ch; color:var(--color-ink-2); font-size:15px; line-height:1.6; }
  .intro-actions { display:flex; flex-wrap:wrap; gap:8px; margin:22px 0 18px; }
  .revision { color:var(--color-ink-2); font-size:11px; }
  .revision > div { padding-top:8px; max-width:70ch; line-height:1.6; }
  .revision code { color:var(--color-ink-2); }
  summary { cursor:pointer; }
  summary:focus-visible,.screen-preview:focus-visible { outline:2px solid var(--color-accent); outline-offset:3px; }
  .confidence { border:1px solid var(--color-edge); border-radius:8px; padding:18px 20px; background:var(--color-canvas); }
  .confidence-heading { display:flex; align-items:center; justify-content:space-between; gap:12px; }
  h2 { display:flex; align-items:center; gap:8px; font-size:14px; font-weight:600; color:var(--color-ink); }
  .confidence-heading > :global(svg) { color:var(--color-ink-2); }
  .score-line { display:flex; align-items:baseline; gap:6px; margin:12px 0; }
  .score-line strong { font-size:42px; line-height:1.1; font-weight:600; font-variant-numeric:tabular-nums; }
  .score-line > span { color:var(--color-ink-2); font-size:14px; }
  .score-line p { margin-left:auto; max-width:15ch; color:var(--color-ink-2); font-size:11px; line-height:1.5; }
  .signal-chart { display:flex; gap:4px; height:8px; margin-bottom:16px; }
  .signal-chart span { flex:1; background:var(--color-edge); border-radius:2px; }
  .signal-chart .confirmed { background:var(--color-good); }
  .signal-chart .failed { background:var(--color-warn); }
  .signals > div { display:flex; justify-content:space-between; gap:12px; padding:5px 0; font-size:12px; }
  dt { color:var(--color-ink-2); }
  dd { display:flex; align-items:center; gap:5px; color:var(--color-ink-2); text-align:right; }
  dd.confirmed { color:var(--color-good); } dd.failed { color:var(--color-warn); }
  .score-explanation { border-top:1px solid var(--color-edge); margin-top:12px; padding-top:10px; font-size:11px; color:var(--color-ink-2); }
  .score-explanation p { line-height:1.6; margin-top:8px; }
  .merge-note { display:flex; align-items:start; gap:6px; margin-top:10px; color:var(--color-warn); font-size:12px; }
  .review-sections { display:grid; grid-template-columns:minmax(0,1.6fr) minmax(280px,1fr); gap:40px; align-items:start; }
  .section-title { display:flex; flex-wrap:wrap; align-items:center; justify-content:space-between; gap:8px; margin-bottom:12px; }
  .section-title > span { color:var(--color-ink-2); font-size:11px; }
  .entries { border-top:1px solid var(--color-edge); }
  .entry { border-bottom:1px solid var(--color-edge); }
  .entry > summary { display:flex; align-items:start; gap:10px; list-style:none; padding:14px 0; font-size:13px; line-height:1.5; color:var(--color-ink); }
  .entry > summary::-webkit-details-marker { display:none; }
  .entry > summary :global(svg) { margin-top:3px; color:var(--color-ink-2); transition:transform 150ms ease-out; }
  .entry[open] > summary :global(svg) { transform:rotate(90deg); }
  .entry-detail { padding:0 0 18px 24px; max-width:78ch; font-size:13px; overflow-wrap:anywhere; }
  .entry-detail :global(pre) { max-width:100%; overflow:auto; }
  .empty-copy { color:var(--color-ink-2); border-top:1px solid var(--color-edge); padding-top:16px; font-size:12px; }
  .evidence-column { display:grid; gap:28px; min-width:0; }
  .features { min-width:0; }
  .additional { margin-top:18px; font-size:12px; color:var(--color-ink-2); }
  .additional > summary { margin-bottom:12px; } .additional > summary span { margin-left:8px; }
  .visual-review { margin-top:28px; }
  .capture-coverage { display:flex; align-items:center; gap:10px; color:var(--color-ink-2); font-size:11px; margin-bottom:16px; }
  .coverage-bar { display:flex; width:100px; height:5px; border-radius:2px; background:var(--color-edge); overflow:hidden; }
  .coverage-bar span { background:var(--color-accent); }
  .screenshot-grid { display:grid; grid-template-columns:repeat(4,minmax(0,1fr)); gap:16px; }
  .screen-preview { text-align:left; min-width:0; border:1px solid var(--color-edge); border-radius:6px; overflow:hidden; background:var(--color-canvas); cursor:pointer; transition:border-color 150ms; }
  .screen-preview:hover { border-color:var(--color-accent); }
  .preview-caption { display:flex; justify-content:space-between; gap:10px; padding:12px 12px 5px; font-size:12px; color:var(--color-ink); }
  .preview-caption > span { overflow-wrap:anywhere; }
  .preview-type { display:block; padding:0 12px 12px; font-size:11px; color:var(--color-ink-2); }
  @media(max-width:1000px) { .screenshot-grid { grid-template-columns:repeat(2,minmax(0,1fr)); } .overview-top { grid-template-columns:minmax(0,1fr) 300px; gap:24px; } .review-sections { gap:24px; grid-template-columns:minmax(0,1fr) minmax(250px,1fr); } h1 { font-size:22px; } }
  @media(max-width:740px) { .overview-top,.review-sections { grid-template-columns:minmax(0,1fr); gap:24px; } .screenshot-grid { grid-template-columns:repeat(2,minmax(0,1fr)); gap:10px; } .confidence { padding:16px; } }
  @media(prefers-reduced-motion:reduce) { .entry > summary :global(svg),.screen-preview { transition:none; } }
</style>
