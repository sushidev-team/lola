<script lang="ts">
  import { DaemonService } from '@bindings/desktop';
  import { tick } from 'svelte';
  import type { ReviewPackage, ReviewCheck, ReviewVerifyArgs } from '@bindings/internal/protocol';
  import Button from '../Button.svelte';
  import Chevron from '../Chevron.svelte';

  let { pkg, canRun = false, freshness = 'unknown' }: { pkg: ReviewPackage; canRun?: boolean; freshness?: string } = $props();
  let checks = $state<ReviewCheck[]>([]);
  let loading = $state(false);
  let busy = $state(false);
  let error = $state('');
  let editing = $state(false);
  let name = $state('review');
  let command = $state('');
  let image = $state('');
  let seconds = $state(300);
  let memory = $state(2048);
  let reviewed = $state<ReviewVerifyArgs>();
  let uncertain = $state(false);
  let generation = 0;
  let root = $state<HTMLElement>();
  const key = $derived(`lola.review.verify.${pkg.session}.${pkg.id}`);
  const active = $derived(checks.some(c => !c.stopped));

  async function focus(selector: string) {
    await tick();
    root?.querySelector<HTMLElement>(selector)?.focus();
  }
  function edit() { editing = true; reviewed = undefined; save(); void focus('.check-form input'); }
  function close() { editing = false; save(); void focus('[data-setup-check]'); }

  function save() {
    try { localStorage.setItem(key,JSON.stringify({name,command,image,seconds,memory,reviewed,uncertain})); } catch { /* In-memory draft remains usable. */ }
  }
  async function refresh() {
    const token = ++generation;
    const session = pkg.session, id = pkg.id;
    loading = true;
    try {
      const result = await DaemonService.ReviewPackageChecks(session,id);
      if (token !== generation) return;
      checks = result.checks ?? [];
      error = '';
      if (uncertain) {
        const recorded = checks.find(c => c.id === reviewed?.id);
        // A successful read resolves ambiguity. If absent, keep the same frozen
        // request identity for an explicit retry, so a delayed write cannot repeat it.
        if (recorded) { reviewed = undefined; editing = false; }
        uncertain = false; save();
      }
    } catch (err) { if (token === generation) error = String(err); }
    finally { if (token === generation) loading = false; }
  }
  $effect(() => {
    const storageKey = key;
    generation++; checks = []; error = ''; reviewed = undefined; uncertain = false; editing = false;
    name='review'; command=''; image=''; seconds=300; memory=2048;
    try {
      const d = JSON.parse(localStorage.getItem(storageKey) ?? '{}');
      if (typeof d.command === 'string') command = d.command;
      if (typeof d.image === 'string') image = d.image;
      if (typeof d.name === 'string') name = d.name;
      if (typeof d.seconds === 'number') seconds = d.seconds;
      if (typeof d.memory === 'number') memory = d.memory;
      if (d.reviewed?.package === pkg.id && typeof d.reviewed?.id === 'string') { reviewed = d.reviewed; uncertain = !!d.uncertain; editing = true; }
    } catch { /* Invalid storage starts a fresh form. */ }
    void refresh();
    return () => { generation++; };
  });
  $effect(() => {
    if (!checks.some(c => c.state === 'running')) return;
    const timer = setInterval(() => { if (!loading && !busy) void refresh(); },3000);
    return () => clearInterval(timer);
  });
  function review(event: SubmitEvent) {
    event.preventDefault();
    reviewed = {package:pkg.id,id:crypto.randomUUID().replaceAll('-',''),spec:{id:name,command,image,timeout_seconds:seconds,memory_mib:memory}};
    error = ''; save(); void focus('.review-command h4');
  }
  async function run() {
    if (!reviewed || busy || uncertain) return;
    const request = $state.snapshot(reviewed);
    const token = generation;
    busy = true; uncertain = true; save();
    try {
      await DaemonService.ReviewPackageVerify(pkg.session,request);
      if (token !== generation) return;
      uncertain = false; reviewed = undefined; editing = false; save();
      await refresh();
      void focus('h3');
    } catch (err) { if (token === generation) { error = `${String(err)} Refresh checks before retrying.`; save(); } }
    finally { busy = false; }
  }
  async function recover(check: ReviewCheck) {
    const token = generation;
    busy = true;
    try { await DaemonService.ReviewPackageRecover(pkg.session,pkg.id,check.id); if (token === generation) await refresh(); }
    catch (err) { if (token === generation) error = String(err); }
    finally { busy = false; }
  }
</script>

<section class="verification" aria-label="Independent verification" bind:this={root}>
  <div class="verification-heading"><h3 tabindex="-1">Independent verification</h3><Button size="xs" loading={loading} disabled={busy} onclick={refresh}>Refresh checks</Button></div>
  <p class="explanation">Commands run offline in a disposable container against <code>{pkg.head.slice(0,12)}</code>. Results do not approve or merge the PR.</p>
  {#if freshness !== 'current'}<p class="text-warn explanation">{freshness === 'stale' ? 'This package is stale. These results do not verify the current PR.' : 'Current PR revision is unknown. These results apply only to this package.'}</p>{/if}
  {#if error}<p role="alert" class="text-warn explanation">{error}</p>{/if}
  {#if uncertain}<p role="status" class="text-warn explanation">The request may have been accepted. Refresh checks to recover its receipt before sending again.</p>{/if}
  {#each checks as check (check.id)}
    <details class="check-result">
      <summary><Chevron class="disclosure-chevron"/><span class="check-name">{check.spec.id}</span><span class="check-state" class:text-good={check.state === 'pass'} class:text-bad={check.state === 'fail'}>{check.state === 'pass' ? 'Passed' : check.state === 'fail' ? 'Failed' : check.state === 'running' ? 'Running' : check.state === 'interrupted' ? 'Interrupted' : 'Unavailable'}</span></summary>
      <p class="explanation">{check.spec.timeout_seconds}s · {check.spec.memory_mib || 2048} MiB · {new Date(check.startedAt).toLocaleString()}</p>
      <pre>{check.spec.command}</pre><p class="receipt-image">{check.spec.image}</p>
      {#if check.error}<p class="explanation text-warn">{check.error}</p>{/if}
      {#if check.output}<pre aria-label={`Output from ${check.spec.id}`}>{check.output}</pre>{/if}
      {#if !check.stopped && check.state !== 'running'}<Button size="sm" loading={busy} onclick={() => recover(check)}>Reconcile interrupted check</Button>{/if}
    </details>
  {:else}{#if !loading && !error}<p class="explanation text-faint">No independent check has run for this package.</p>{/if}{/each}
  {#if canRun}
    {#if editing}
      {#if reviewed}
        <div class="review-command">
          <h4 tabindex="-1">Review check authorization</h4>
          <pre>{reviewed.spec.command}</pre>
          <p class="receipt-image">{reviewed.spec.image}</p>
          <p class="explanation">{reviewed.spec.timeout_seconds} seconds · {reviewed.spec.memory_mib} MiB · no network or host credentials. The image must already contain the toolchain and dependencies.</p>
          <div class="verification-actions"><Button size="sm" disabled={busy || uncertain} onclick={edit}>Edit check</Button><Button size="sm" variant="primary" loading={busy} disabled={uncertain || active} onclick={run}>Authorize and run</Button></div>
        </div>
      {:else}
        <form onsubmit={review} oninput={save} class="check-form">
          <label>Check name<input required pattern={'[a-zA-Z0-9][a-zA-Z0-9._\\-]{0,127}'} maxlength="128" bind:value={name}/></label>
          <label>Command<textarea required maxlength="16000" rows="3" bind:value={command} placeholder="npm test"></textarea></label>
          <label>Container image<input required bind:value={image} placeholder="sha256:…" aria-describedby="check-image-help"/></label>
          <p id="check-image-help" class="explanation">Use a locally available image pinned by its SHA-256 digest.</p>
          <div class="check-limits"><label>Timeout (seconds)<input type="number" required min="1" max="3600" step="1" bind:value={seconds}/></label><label>Memory (MiB)<input type="number" required min="256" max="8192" step="1" bind:value={memory}/></label></div>
          <div class="verification-actions"><Button size="sm" onclick={close}>Close</Button><Button size="sm" variant="secondary" type="submit" disabled={active}>Review check</Button></div>
        </form>
      {/if}
    {:else}<Button size="sm" variant="secondary" data-setup-check disabled={active} onclick={edit}>Set up check</Button>{/if}
  {/if}
</section>

<style>
  .verification { display:grid; gap:12px; margin-top:20px; min-width:0; }
  .verification-heading { display:flex; justify-content:space-between; align-items:center; flex-wrap:wrap; gap:8px; }
  h3,h4 { color:var(--color-ink); font-size:13px; font-weight:600; }
  .explanation { font-size:12px; line-height:18px; color:var(--color-ink-2); }
  .explanation.text-warn { color:var(--color-warn); }
  .check-result { border-bottom:1px solid var(--color-edge); padding-bottom:10px; }
  summary { display:flex; gap:8px; align-items:center; cursor:pointer; font-size:12px; list-style:none; }
  .check-name { min-width:0; overflow-wrap:anywhere; flex:1; }
  .check-state { margin-left:auto; flex-shrink:0; }
  summary::-webkit-details-marker { display:none; }
  details[open] > summary > :global(.disclosure-chevron) { transform:rotate(90deg); }
  .check-result > :not(summary) { margin-top:10px; }
  pre { font-size:12px; line-height:18px; white-space:pre-wrap; overflow-wrap:anywhere; max-height:240px; overflow:auto; color:var(--color-ink); }
  .receipt-image { font-size:11px; line-height:17px; overflow-wrap:anywhere; color:var(--color-faint); }
  .check-form,.review-command { display:grid; gap:12px; }
  label { display:grid; gap:5px; font-size:12px; color:var(--color-ink-2); min-width:0; }
  input,textarea { min-width:0; width:100%; border:1px solid var(--color-edge); border-radius:6px; padding:8px; background:var(--color-canvas); color:var(--color-ink); font-size:12px; }
  input:focus,textarea:focus { border-color:var(--color-accent); outline:none; }
  .check-limits { display:grid; grid-template-columns:repeat(2,minmax(0,1fr)); gap:12px; }
  .verification-actions { display:flex; justify-content:space-between; flex-wrap:wrap; gap:8px; }
</style>
