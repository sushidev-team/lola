<script lang="ts">
  import { DaemonService } from '@bindings/desktop';
  import { tick } from 'svelte';
  import type { ReviewPackage, ReviewCheck, ReviewVerifyArgs } from '@bindings/internal/protocol';
  import Button from '../Button.svelte';
  import Chevron from '../Chevron.svelte';
  import Icon from '../Icon.svelte';
  import { touchSurface } from '$lib/touchsurface';

  // `labels` is the phone's one-line verdict ("Verified" / "Not verified"); without it, or off the phone, the full heading shows.
  // `canRecover` (default true) draws "Reconcile interrupted check"; the phone passes its tier answer.
  let { pkg, canRun = false, canRecover = true, freshness = 'unknown', labels }: { pkg: ReviewPackage; canRun?: boolean; canRecover?: boolean; freshness?: string; labels?: { verified: string; notVerified: string } } = $props();
  const phone = touchSurface();
  let checks = $state<ReviewCheck[]>([]);
  const verdict = $derived(freshness === 'current' && checks.length > 0 && checks.every(c => c.state === 'pass'));
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
  // Phone, nothing run yet: one "No checks / Add check" row replaces the verdict header plus a second row.
  const bare = $derived(phone && !!labels && !loading && !error && !checks.length && !(canRun && editing));

  async function focus(selector: string) {
    await tick();
    root?.querySelector<HTMLElement>(selector)?.focus();
  }
  function edit() { editing = true; reviewed = undefined; save(); void focus('.check-form textarea'); }
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
  {#if !bare}<header class="verification-heading">
    {#if phone && labels}<div><h3 tabindex="-1" aria-label="Independent verification"><Icon name="shield-check" size={15}/>{verdict ? labels.verified : labels.notVerified}</h3></div>
    {:else}<div><h3 tabindex="-1"><Icon name="shield-check" size={15}/>Independent verification</h3>
      <p class="revision"><code>{pkg.head.slice(0,12)}</code><span class:outdated={freshness === 'stale'}>{freshness === 'current' ? 'Current revision' : freshness === 'stale' ? 'Older revision' : 'Revision not confirmed'}</span></p>
    </div>{/if}
    {#if !phone}<Button size="sm" icon aria-label="Refresh checks" title="Refresh checks" loading={loading} disabled={busy} onclick={refresh}><Icon name="refresh-cw" size={14}/></Button>{/if}
  </header>{/if}
  {#if freshness === 'stale'}<p class="context-note outdated">Checks apply to this version, not the current PR.</p>{/if}
  {#if error}<p role="alert" class="message text-warn">{error}</p>{/if}
  {#if uncertain}<p role="status" class="message text-warn">The request may have been accepted. Refresh checks before sending again.</p>{/if}
  {#if checks.length}<div class="check-results">
    {#each checks as check (check.id)}
      <details class="check-result">
        <summary><Chevron class="disclosure-chevron"/><span class="check-name">{check.spec.id}</span><span class="check-state" class:text-good={check.state === 'pass'} class:text-bad={check.state === 'fail'}>{check.state === 'pass' ? 'Passed' : check.state === 'fail' ? 'Failed' : check.state === 'running' ? 'Running' : check.state === 'interrupted' ? 'Interrupted' : 'Unavailable'}</span></summary>
        <p class="explanation">{check.spec.timeout_seconds}s · {check.spec.memory_mib || 2048} MiB · {new Date(check.startedAt).toLocaleString()}</p>
        <pre>{check.spec.command}</pre><p class="receipt-image">{check.spec.image}</p>
        {#if check.error}<p class="explanation text-warn">{check.error}</p>{/if}
        {#if check.output}<pre aria-label={`Output from ${check.spec.id}`}>{check.output}</pre>{/if}
        {#if canRecover && !check.stopped && check.state !== 'running'}<Button size="sm" loading={busy} onclick={() => recover(check)}>Reconcile interrupted check</Button>{/if}
      </details>
    {/each}
  </div>{/if}
  {#if canRun && editing}
    {#if reviewed}
      <div class="review-command">
        <h4 tabindex="-1">Review check authorization</h4>
        <pre class="command-preview">{reviewed.spec.command}</pre>
        <dl class="authorization-facts">
          <div><dt>Check</dt><dd>{reviewed.spec.id}</dd></div>
          <div><dt>Image</dt><dd><code>{reviewed.spec.image}</code></dd></div>
          <div><dt>Limits</dt><dd>{reviewed.spec.timeout_seconds} seconds · {reviewed.spec.memory_mib} MiB</dd></div>
          <div><dt>Access</dt><dd>No network or host credentials</dd></div>
        </dl>
        <p class="explanation">Runs the committed revision in an isolated container. The image must contain its dependencies. Results do not approve or merge the PR.</p>
        <div class="verification-actions"><Button size="sm" disabled={busy || uncertain} onclick={edit}>Edit check</Button><Button size="sm" variant="primary" loading={busy} disabled={uncertain || active} onclick={run}>Authorize and run</Button></div>
      </div>
    {:else}
      <form onsubmit={review} oninput={save} class="check-form">
        <div class="check-inputs">
          <label>Command<textarea required maxlength="16000" rows="2" bind:value={command} placeholder="npm test"></textarea></label>
          <label>Container image<input required bind:value={image} placeholder="sha256:…" aria-describedby="check-image-help"/></label>
          <p id="check-image-help" class="explanation">A local image with your test dependencies. Runs offline.</p>
        </div>
        <details class="check-options">
          <summary><Chevron class="disclosure-chevron"/><span>Options</span><span class="options-value">{seconds}s · {memory} MiB</span></summary>
          <div class="options-fields">
            <label>Check name<input required pattern={'[a-zA-Z0-9][a-zA-Z0-9._\\-]{0,127}'} maxlength="128" bind:value={name}/></label>
            <div class="check-limits"><label>Timeout (seconds)<input type="number" required min="1" max="3600" step="1" bind:value={seconds}/></label><label>Memory (MiB)<input type="number" required min="256" max="8192" step="1" bind:value={memory}/></label></div>
          </div>
        </details>
        <div class="verification-actions"><Button size="sm" onclick={close}>Cancel</Button><Button size="sm" variant="secondary" type="submit" disabled={active}>Review check<Icon name="arrow-right" size={14}/></Button></div>
      </form>
    {/if}
  {:else if !loading && !error && phone && !checks.length}
    <div class="phone-setup" data-phone-setup><span class="text-subtext" style="font-size:15px;line-height:20px">No checks</span>{#if canRun}<button type="button" class="text-accent" style="font-size:15px;font-weight:400" data-setup-check disabled={active} onclick={edit}>Add check</button>{/if}</div>
  {:else if !loading && !error}
    <div class="setup-row"><span class="explanation">{checks.length ? 'Run another check on this revision.' : 'No checks yet.'}</span>{#if canRun}<Button size="sm" variant="secondary" data-setup-check disabled={active} onclick={edit}>Set up check</Button>{/if}</div>
  {/if}
</section>

<style>
  :global([data-phone-desk]) .verification { margin-top:0; padding-top:0; border-top:0; }
  :global([data-phone-desk]) .verification-heading { align-items:center; }
  :global([data-phone-desk]) .verification-heading h3 { font-size:17px; line-height:22px; font-weight:600; }
  :global([data-phone-desk]) .verification-heading :global(button) { min-width:44px; min-height:44px; }
  .verification { min-width:0; margin-top:20px; padding-top:16px; border-top:1px solid var(--color-edge); }
  .verification-heading { display:flex; justify-content:space-between; align-items:flex-start; gap:12px; }
  h3,h4 { color:var(--color-ink); font-size:13px; font-weight:600; line-height:20px; }
  h3 { display:flex; align-items:center; gap:8px; }
  h3 :global(svg) { color:var(--color-ink-2); }
  .revision { display:flex; flex-wrap:wrap; align-items:center; gap:8px; margin-top:4px; font-size:11px; line-height:16px; color:var(--color-ink-2); }
  .revision code { color:var(--color-ink); }
  .revision span { border-left:1px solid var(--color-edge); padding-left:8px; }
  .context-note { margin-top:8px; font-size:12px; line-height:18px; }
  .outdated,.explanation.text-warn { color:var(--color-warn); }
  .message { margin-top:12px; font-size:12px; line-height:18px; overflow-wrap:anywhere; }
  .explanation { font-size:12px; line-height:18px; color:var(--color-ink-2); }
  .setup-row { display:flex; align-items:center; justify-content:space-between; flex-wrap:wrap; gap:12px; margin-top:16px; }
  .check-results { margin-top:12px; }
  .check-result { border-bottom:1px solid var(--color-edge); padding:12px 0; }
  summary { display:flex; gap:8px; align-items:center; cursor:pointer; font-size:12px; line-height:20px; list-style:none; }
  .check-name { min-width:0; overflow-wrap:anywhere; flex:1; }
  .check-state { margin-left:auto; flex-shrink:0; }
  summary::-webkit-details-marker { display:none; }
  details[open] > summary > :global(.disclosure-chevron) { transform:rotate(90deg); }
  .check-result > :not(summary) { margin-top:8px; }
  pre { font-size:12px; line-height:18px; white-space:pre-wrap; overflow-wrap:anywhere; max-height:240px; overflow:auto; color:var(--color-ink); }
  .receipt-image { font-size:11px; line-height:17px; overflow-wrap:anywhere; color:var(--color-ink-2); }
  .check-form,.review-command { display:grid; gap:16px; margin-top:16px; }
  .check-inputs { display:grid; gap:12px; }
  .check-inputs > p { margin-top:-8px; }
  label { display:grid; gap:6px; font-size:12px; color:var(--color-ink-2); min-width:0; }
  input,textarea { min-width:0; width:100%; border:1px solid var(--color-edge); border-radius:6px; padding:8px; background:var(--color-canvas); color:var(--color-ink); font-size:12px; line-height:18px; }
  textarea { resize:vertical; font-family:var(--font-mono); }
  input:focus-visible,textarea:focus-visible,summary:focus-visible { outline:2px solid var(--color-accent); outline-offset:2px; }
  .check-options { border-top:1px solid var(--color-edge); border-bottom:1px solid var(--color-edge); padding:8px 0; }
  .options-value { margin-left:auto; color:var(--color-ink-2); font-size:11px; }
  .options-fields { display:grid; gap:12px; margin:12px 0 4px; }
  .check-limits { display:grid; grid-template-columns:repeat(2,minmax(0,1fr)); gap:12px; }
  .verification-actions { display:flex; justify-content:space-between; align-items:center; flex-wrap:wrap; gap:8px; }
  .command-preview { padding:12px; border:1px solid var(--color-edge); border-radius:6px; background:var(--color-canvas); }
  .authorization-facts { display:grid; gap:8px; font-size:12px; line-height:18px; }
  .authorization-facts > div { display:grid; grid-template-columns:44px minmax(0,1fr); gap:12px; }
  dt { color:var(--color-ink-2); } dd { overflow-wrap:anywhere; min-width:0; }
  .phone-setup { display:flex; align-items:center; justify-content:space-between; gap:12px; min-height:44px; }
  .phone-setup button { min-height:44px; min-width:44px; padding-left:12px; }
  .phone-setup button:disabled { opacity:.4; }
</style>
