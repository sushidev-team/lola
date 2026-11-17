<script lang="ts">
  import { tick } from 'svelte';
  import type { ReviewPackage, PRReadinessData } from '@bindings/internal/protocol';
  import { tooltip } from '$lib/tooltip';
  import { reviewConfidence } from '$lib/reviewoverview';
  import Button from '../Button.svelte';
  import Icon from '../Icon.svelte';
  import Skeleton from '../Skeleton.svelte';
  import { touchSurface } from '$lib/touchsurface';

  let { pkg, facts, checking = false, onAssessConfidence, confidenceRequest, onUpdateMaterial, materialRequest }: {
    pkg: ReviewPackage; facts?: PRReadinessData; checking?: boolean;
    onAssessConfidence?: () => void; confidenceRequest?: { state: string; message: string }; onUpdateMaterial?: () => void; materialRequest?: { state: string; message: string };
  } = $props();
  const id = $props.id();
  // Phone: no "what is confidence" panel, and a score that cannot be computed says nothing, so it is not drawn.
  const phone = touchSurface();
  let open = $state(false);
  let root = $state<HTMLElement>();
  let panel = $state<HTMLDivElement>();
  const confidence = $derived(reviewConfidence(pkg, facts));
  const confLabel = $derived(confidenceRequest?.state === 'accepted' ? 'Requested' : confidenceRequest?.state === 'error' ? 'Retry confidence' : 'Update confidence');
  const matLabel = $derived(materialRequest?.state === 'accepted' ? 'Requested' : materialRequest?.state === 'error' ? 'Retry review' : 'Update review');
  const hint = $derived(confidenceRequest?.message || materialRequest?.message || '');
  const hintState = $derived(confidenceRequest?.message ? confidenceRequest.state : materialRequest?.state);
  const score = $derived(confidence.score);
  const tone = $derived(checking || score === null ? 'none' : confidence.failed ? 'bad' : score >= 75 ? 'good' : 'warn');
  const observed = $derived(confidence.evidence === null ? null : Math.round(confidence.evidence));
  async function toggle() { open = !open; if (open) { await tick(); panel?.focus(); } }
  function close() { open = false; root?.querySelector<HTMLButtonElement>('.info button')?.focus(); }
  function onKey(event: KeyboardEvent) {
    if (open && event.key === 'Escape') { event.preventDefault(); event.stopPropagation(); close(); }
  }
  function onPointer(event: PointerEvent) {
    if (open && !root?.contains(event.target as Node)) open = false;
  }
  $effect(() => { pkg.id; open = false; });
</script>

<svelte:window onkeydowncapture={onKey} onpointerdowncapture={onPointer} />

{#if !(phone && score === null)}
<section class="conf" data-tone={tone} aria-label="Review confidence" bind:this={root}>
  <div class="row">
    <div class="gauge">
      <span class="dial" style:--p={score !== null && !checking ? score : 0}></span>
      <span class="num">{#if checking}<span role="status" aria-label="Loading confidence"><span class="sr-only">Loading confidence…</span><Skeleton width="26px" height="16px"/></span>{:else if score !== null}<strong class="score">{score}</strong>{:else}–{/if}</span>
    </div>
    <div class="body">
      <div class="title"><h2>Confidence</h2>{#if !phone}<span class="info"><Button icon size="xs" aria-label="About confidence" aria-haspopup="dialog" aria-expanded={open} aria-controls={id} onclick={toggle}><Icon name="info" size={14}/></Button></span>{/if}</div>
      <p class="verdict" class:issues={!checking && confidence.failed > 0}>{#if checking}<Skeleton width="90px" height="14px"/>{:else}{confidence.label}{/if}</p>
    </div>
  </div>
  {#if !checking}<div class="parts">
    <span class="part" class:issues={confidence.failed > 0}><i>Checks</i><b>{observed === null ? '–' : observed}</b></span>
    <span class="part"><i>Agent</i><b>{confidence.agent ? confidence.agent.score : '–'}</b></span>
  </div>{/if}
  {#if !phone}<div class="actions">
    <span use:tooltip={onUpdateMaterial ? 'Ask the agent to refresh the write-up, screenshots and confidence' : 'Resume the agent to update the review'}><Button size="xs" variant="secondary" aria-label={materialRequest?.state === 'accepted' ? 'Review update requested' : matLabel} loading={materialRequest?.state === 'sending'} disabled={!onUpdateMaterial || materialRequest?.state === 'accepted'} onclick={onUpdateMaterial}><Icon name={materialRequest?.state === 'accepted' ? 'check' : 'refresh-cw'} size={12}/>{matLabel}</Button></span>
    <span use:tooltip={onAssessConfidence ? 'Ask the agent to re-assess its confidence' : 'Resume the agent to update confidence'}><Button size="xs" variant="secondary" aria-label={confidenceRequest?.state === 'accepted' ? 'Self-assessment requested' : confLabel} loading={confidenceRequest?.state === 'sending'} disabled={!onAssessConfidence || confidenceRequest?.state === 'accepted'} onclick={onAssessConfidence}><Icon name={confidenceRequest?.state === 'accepted' ? 'check' : 'activity'} size={12}/>{confLabel}</Button></span>
  </div>
  {#if hint}<p class="hint" role={hintState === 'error' ? 'alert' : 'status'}>{hint}</p>{/if}{/if}
  {#if open && !phone}
  <div {id} class="explain" role="dialog" aria-label="About confidence" tabindex="-1" bind:this={panel}>
    <div class="explain-head"><h3>How it is calculated</h3><Button icon size="xs" aria-label="Close confidence details" onclick={close}><Icon name="x" size={14}/></Button></div>
    {#if checking}<Skeleton height="40px"/>
    {:else}
    <dl class="split">
      <div><dt>Observed results <small>70%</small></dt><dd>{#if confidence.evidence === null}Pending{:else}{Math.round(confidence.evidence)}/100{/if}</dd></div>
      <div><dt>Agent self-confidence <small>30%</small></dt><dd>
        {#if confidence.agent}{confidence.agent.score}/100{confidence.current ? '' : ' · unverified'}
        {:else}Not assessed{/if}
      </dd></div>
    </dl>
    {#if !confidence.current}<p class="note">Not matched to the current PR yet.</p>{/if}
    {#if confidence.agent}<p class="note"><strong>Agent’s rationale:</strong> {confidence.agent.basis}</p>{/if}
    <dl class="signals">{#each confidence.signals as signal}<div><dt>{signal.label}</dt><dd>{signal.value}</dd></div>{/each}</dl>
    <p class="note">Failures lower the observed share; pending signals are left out. Approval never changes it, and it is not permission to merge.</p>
    {#if facts?.observedAt}<p class="note">Observed {new Date(facts.observedAt).toLocaleString()} · CI on {facts.checksHead?.slice(0,7) || 'unconfirmed'}</p>{/if}
    {/if}
  </div>
  {/if}
</section>
{/if}

<style>
  .conf { --tone:var(--color-ink-2); min-width:0; display:grid; gap:12px; padding:14px; border:1px solid var(--color-edge); border-radius:14px; background:var(--color-panel); }
  .conf[data-tone='good'] { --tone:var(--color-good); } .conf[data-tone='warn'] { --tone:var(--color-warn); } .conf[data-tone='bad'] { --tone:var(--color-bad); }
  .row { display:flex; align-items:center; gap:14px; min-width:0; }
  .gauge { position:relative; flex-shrink:0; width:56px; height:56px; }
  .dial { position:absolute; inset:0; border-radius:50%; background:conic-gradient(var(--tone) calc(var(--p) * 1%), color-mix(in srgb,var(--color-edge) 80%,transparent) 0); -webkit-mask:radial-gradient(farthest-side,transparent calc(100% - 5px),#000 calc(100% - 4px)); mask:radial-gradient(farthest-side,transparent calc(100% - 5px),#000 calc(100% - 4px)); }
  .num { position:absolute; inset:0; display:grid; place-items:center; font-family:var(--font-mono); font-variant-numeric:lining-nums tabular-nums slashed-zero; font-size:13px; color:var(--color-ink-2); }
  .num strong { font-size:17px; font-weight:700; letter-spacing:-.03em; color:var(--tone); }
  .body { min-width:0; display:grid; gap:3px; flex:1; }
  .title { display:flex; align-items:center; gap:2px; }
  h2 { font-size:12px; font-weight:500; color:var(--color-ink-2); }
  .verdict { font-size:14px; line-height:1.3; font-weight:600; color:var(--color-ink); }
  .verdict.issues,.issues b { color:var(--color-warn); }
  .parts { display:flex; flex-wrap:wrap; gap:6px; }
  .part { display:inline-flex; align-items:center; gap:6px; padding:3px 9px; border-radius:999px; background:color-mix(in srgb,var(--color-ink) 6%,transparent); font-size:11px; color:var(--color-ink-2); }
  .part i { font-style:normal; } .part b { font-family:var(--font-mono); font-weight:600; font-size:12px; color:var(--color-ink); font-variant-numeric:tabular-nums; }
  .explain { display:grid; gap:10px; padding-top:12px; border-top:1px solid color-mix(in srgb,var(--color-edge) 60%,transparent); font-size:12px; color:var(--color-ink-2); }
  .explain:focus { outline:none; }
  .explain-head { display:flex; align-items:center; justify-content:space-between; gap:12px; }
  .explain-head h3 { margin:0; font-size:12px; font-weight:600; color:var(--color-ink); }
  .actions { display:flex; flex-wrap:wrap; gap:6px; }
  .hint { margin:0; font-size:11px; line-height:1.4; color:var(--color-ink-2); }
  dl { margin:0; display:grid; gap:6px; }
  dl > div { display:flex; align-items:center; justify-content:space-between; gap:12px; }
  dt small { margin-left:4px; font-size:11px; } dd { margin:0; text-align:right; font-variant-numeric:tabular-nums; color:var(--color-ink); }
  .signals { padding-top:8px; border-top:1px dashed var(--color-edge); }
  .note { margin:0; line-height:1.5; }
</style>
