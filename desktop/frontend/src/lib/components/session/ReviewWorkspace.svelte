<script lang="ts">
  import { DaemonService, TalkService } from '@bindings/desktop';
  import { SvelteMap } from 'svelte/reactivity';
  import type { ReviewPackage as Package, PRReadinessData } from '@bindings/internal/protocol';
  import { store } from '$lib/store.svelte';
  import { nav } from '$lib/nav.svelte';
  import { feedback } from '$lib/feedback.svelte';
  import { feed } from '$lib/feed.svelte';
  import { ssh } from '$lib/ssh.svelte';
  import { sessionActivity } from '$lib/sessionactivity';
  import Modal from '../Modal.svelte';
  import Button from '../Button.svelte';
  import Icon from '../Icon.svelte';
  import Select from '../Select.svelte';
  import Tabs from '../Tabs.svelte';
  import DiffView from '../DiffView.svelte';
  import ReviewWriteup from './ReviewWriteup.svelte';
  import BeforeMerge from './BeforeMerge.svelte';
  import OverviewChecks from './OverviewChecks.svelte';
  import ReviewCanvas from './ReviewCanvas.svelte';

  let { sessionId }: { sessionId: string } = $props();
  const session = $derived(store.sessionById(sessionId));
  let app = $state('');
  const lanes = $derived(session?.stack ? (session.apps ?? []).map(a => ({ id: a.name, label: a.label || a.name, number: a.number ?? 0, url:a.url ?? '' })) : []);
  const lane = $derived(lanes.find(a => a.id === app) ?? lanes.find(a => a.id === session?.focusApp) ?? lanes[0]);
  const target = $derived(lane ? `${sessionId}~${lane.id}` : sessionId);
  const number = $derived(lane?.number ?? session?.prNumber ?? 0);
  // Pin readiness actions to the selected member, never the stack's aggregate PR.
  const reviewSession = $derived(session && lane ? { ...session, id:target, stack:false, apps:[], prNumber:number, prUrl:lane.url } : session);
  let packages = $state<Package[]>([]);
  let selected = $state('');
  const current = $derived(packages.find(p => p.id === selected) ?? packages[0]);
  let facts = $state<PRReadinessData>();
  let loading = $state(false);
  let error = $state('');
  let generation = 0;
  let tab = $state('material');
  let panelOpen = $state(false);
  let canvasScreen = $state(0);
  const packageIdentity = $derived(current?.id);
  $effect(() => { packageIdentity; canvasScreen = 0; });
  let subject = $state('Write-up');
  let draft = $state('');
  let notice = $state('');
  const draftKey = $derived(`lola.review.draft.${target}.${current?.id ?? ''}`);
  const notes = $derived(feedback.noteFor(sessionId));
  const count = $derived(feedback.count(sessionId));
  const f = $derived(feed.view(sessionId));
  const view = $derived(ssh.view(sessionId));
  const tests = $derived(sessionActivity({feed:f.items,items:view.items,scope:view.scope,alive:false}).filter(e => e.kind === 'tests'));
  const freshness = $derived(!current || !facts ? 'unknown' : current.head !== facts.head || current.repo !== facts.repo || (current.pr > 0 && current.pr !== facts.number) ? 'stale' : 'current');
  const requests = new SvelteMap<string, { state: 'sending' | 'accepted' | 'error'; message: string }>();
  const request = $derived(requests.get(target));
  const canRequest = $derived(!!session && !session.goalId && !['dead', 'exited', 'shell', 'orphaned'].includes(session.agentState ?? ''));

  async function requestMaterial() {
    const key = target;
    if (!canRequest || requests.get(key)?.state === 'sending' || requests.get(key)?.state === 'accepted') return;
    const scope = lane ? ` for stack member ${JSON.stringify(lane.id)}${number ? ` (PR #${number})` : ''}` : number ? ` for PR #${number}` : ' for this session';
    const text = `Prepare and publish review material in Lola${scope}: a concise write-up using ## Summary (one sentence), ## Changes (at most three short bullets), ## Verification (commands and results), and ## Risks (only unresolved items). Reuse screenshots and test results already collected during implementation and testing when they still describe the reviewed revision. Do not rerun checks or recreate captures solely to format this package. Capture or verify only missing or stale evidence. Put extended technical detail under ## Details. Include before/after screenshots for visual changes where available; explain any missing baseline. Use the actual PR base and head revisions. Run lola review-package publish --help for the manifest format, then publish the package${lane ? ` with --app ${JSON.stringify(lane.id)}` : ''}. This request is to document the current changes; do not modify application code or create a new PR.`;
    requests.set(key, { state: 'sending', message: '' });
    try {
      // Use the conversation's safe send path, independently of unsent review comments.
      const result = await TalkService.Say({ session: sessionId, text });
      requests.set(key, { state: 'accepted', message: result.agentGone
        ? 'Request saved. Resume the agent in the conversation to continue.'
        : result.delivered ? 'Request sent. Review material will appear here once published.'
        : `Request queued. ${result.why || 'Lola will send it when the agent can receive it.'}` });
    } catch (err) {
      requests.set(key, { state: 'error', message: `Could not send the request. ${String(err)}` });
    }
  }

  async function refresh() {
    const version = ++generation, sid = target, pr = number;
    loading = true; error = ''; facts = undefined;
    const [material, status] = await Promise.allSettled([
      DaemonService.ReviewPackages(sid),
      pr ? DaemonService.PRReadiness(sid, pr) : Promise.resolve(undefined),
    ]);
    if (version !== generation) return;
    if (material.status === 'fulfilled') {
      packages = material.value.packages ?? [];
      // A new publication must not replace the revision someone is reviewing.
      if (!selected || !packages.some(p => p.id === selected)) selected = packages[0]?.id ?? '';
    }
    else error = String(material.reason);
    if (status.status === 'fulfilled') facts = status.value;
    loading = false;
  }
  $effect(() => {
    target; number;
    packages = []; selected = ''; notice = '';
    void refresh();
    const timer = setInterval(() => { if (document.visibilityState !== 'hidden') void refresh(); }, 60000);
    return () => { generation++; clearInterval(timer); };
  });
  $effect(() => {
    const key = draftKey;
    subject = 'Write-up'; draft = '';
    try {
      const saved = JSON.parse(localStorage.getItem(key) ?? '{}');
      if (typeof saved.text === 'string') draft = saved.text;
      if (typeof saved.subject === 'string') subject = saved.subject;
    } catch { /* unavailable storage leaves an empty draft */ }
  });
  function editDraft(value: string) {
    draft = value;
    try { localStorage.setItem(draftKey, JSON.stringify({text:value,subject})); } catch { /* draft remains in memory */ }
  }
  function queueComment(section: string, text: string, screenIndex?: number): boolean {
    if (!current || !text.trim()) return false;
    const screen = screenIndex === undefined ? undefined : current.screens?.[screenIndex];
    const images = screen ? `Page: ${screen.url || 'unspecified'}; viewport: ${screen.viewport || 'unspecified'}\nScreenshots: before ${screen.before || 'unavailable'}, after ${screen.after}\n` : '';
    const context = `Review package ${current.id}\nRepository: ${current.repo || 'unspecified'}${current.pr ? ` PR #${current.pr}` : ''}\nBase: ${current.base}\nHead: ${current.head}\nSection: ${section}\n${images}`;
    const combined = [notes, context + text.trim()].filter(Boolean).join('\n\n');
    // The existing feedback renderer caps a general note at 4000 runes.
    if ([...combined].length > 4000) { notice = 'This batch is full. Send it before adding another comment.'; return false; }
    feedback.setNote(sessionId, combined);
    notice = 'Comment added to review feedback.';
    return true;
  }
  function addComment() {
    const index = current?.screens?.findIndex((s,i) => subject === `Screen ${i+1}: ${s.title}`);
    if (queueComment(subject, draft, index !== undefined && index >= 0 ? index : undefined)) { subject = 'Write-up'; editDraft(''); }
  }
  function addScreenComment(index: number, text: string): boolean {
    return queueComment(`Screen ${index+1}: ${current?.screens?.[index]?.title}`, text, index);
  }
  async function send() {
    const sent = await feedback.send(sessionId);
    notice = sent ? 'Feedback accepted. Lola delivers it when the agent is ready.' : 'Feedback was not accepted. Your draft is kept; check the error and retry.';
  }
  function jump(id: string) {
    view.filter = 'everything'; view.cursor = id;
    view.jump = {block:`item:${id}`, n:(view.jump?.n ?? 0)+1};
    nav.closeOverlay();
  }
</script>

<Modal title="PR review" fullscreen onClose={() => nav.closeOverlay()} bodyClass="grid min-h-0 grid-rows-[auto_minmax(0,1fr)]">
  <div class="review-toolbar">
    <div class="min-w-0">
      <p class="truncate font-medium text-ink">{session?.title || session?.issue || sessionId}</p>
    </div>
    <div class="flex min-w-0 flex-wrap items-center gap-2">
      {#if lanes.length}<Select aria-label="Review repository" value={lane?.id} onchange={e => app = e.currentTarget.value}>{#each lanes as a}<option value={a.id}>{a.label}</option>{/each}</Select>{/if}
      {#if packages.length}<Select aria-label="Review version" value={current?.id} onchange={e => selected = e.currentTarget.value}>{#each packages as p,i}<option value={p.id}>{i === 0 ? 'Latest · ' : ''}{p.head.slice(0,8)} · {new Date(p.createdAt).toLocaleDateString()}</option>{/each}</Select>{/if}
      <Button size="sm" icon aria-label="Refresh review" title="Refresh review" loading={loading} onclick={refresh}><Icon name="refresh-cw" size={14} /></Button>
    </div>
  </div>
  <div class="review-layout" class:panel-open={panelOpen}>
    <div class="review-main">
      <div class="review-tabs"><Tabs tabs={[{id:'material',label:'Overview'}, ...(current?.screens?.length ? [{id:'visuals',label:'Screenshots'}] : []), ...(session ? [{id:'code',label:'Code changes'}] : [])]} active={tab} onSelect={id => tab = id} /><Button size="sm" selected={panelOpen} aria-expanded={panelOpen} onclick={() => panelOpen = !panelOpen}>Checks &amp; feedback{count ? ` · ${count}` : ''}</Button></div>
      {#if tab === 'code' && session}
        <div class="review-code grid min-h-0 grid-rows-[auto_minmax(0,1fr)]"><p class="px-4 py-2 text-xs text-faint">Live checkout changes. These may differ from the retained review revision.</p><DiffView {sessionId} /></div>
      {:else if tab === 'visuals' && current?.screens?.length}
        <div class="review-visuals">
          {#key current.id}<ReviewCanvas pkg={current} {freshness} bind:index={canvasScreen} canComment={!!session} onComment={addScreenComment} />{/key}
        </div>
      {:else}
        <div class="review-material">
          {#if error}<p role="alert" class="mb-5 text-warn">Could not load review material. {error} Try Refresh review; an older daemon may need restarting.</p>{/if}
          {#if current}
            {#if current.id !== packages[0]?.id}<p class="mb-5 text-sm text-info">A newer review package is available. <Button size="xs" onclick={() => selected = packages[0].id}>View latest package</Button></p>{/if}
            <header class="package-heading">
              <h1 class="text-lg font-semibold text-ink">{current.title}</h1>
              <div class="package-meta">
                <span role="status" class={freshness === 'stale' ? 'text-warn' : 'text-ink-2'}>{freshness === 'stale' ? 'Outdated material' : freshness === 'current' ? 'Matches current PR' : 'Freshness unknown'}</span>
                <span>Agent-provided · {new Date(current.createdAt).toLocaleString()}</span>
                <details><summary>Revision details</summary><p class="mt-2">{current.base.slice(0,12)} → {current.head.slice(0,12)}</p><p class="mt-1">{freshness === 'stale' ? 'The PR differs from this package.' : freshness === 'current' ? 'Package head matches the current PR. Captures and claims are agent-provided.' : 'The current PR head could not be confirmed.'}</p></details>
              </div>
            </header>
            <ReviewWriteup {sessionId} body={current.body} />
            {#if session}<details class="writeup-comment"><summary>Comment on write-up</summary>{#if subject !== 'Write-up' && draft}<p class="mt-2">Restored draft: {subject}</p>{/if}<textarea aria-label="Review comment" class="review-input mt-2" rows="3" maxlength="3000" value={draft} oninput={e => editDraft(e.currentTarget.value)} placeholder="What needs changing or explaining?"></textarea><Button variant="secondary" class="mt-2" disabled={!draft.trim()} onclick={addComment}>Add to feedback</Button></details>{/if}
            {#if current.screens?.length}<Button variant="primary" class="mt-5" onclick={() => tab = 'visuals'}>Explore {current.screens.length} {current.screens.length === 1 ? 'screen' : 'screens'}</Button>{/if}
          {:else if loading}<p role="status" class="text-faint">Loading review material…</p>
          {:else if !error}
            <div class="empty-review">
              <h1 class="text-lg font-semibold text-ink">No review material yet</h1>
              <p class="mt-2 text-prose text-ink-2">Get a summary of the changes, verification results, and screenshots from the agent.</p>
              <div class="mt-5 flex flex-wrap items-center gap-2">
                {#if canRequest}
                  <Button variant="primary" size="md" loading={request?.state === 'sending'} disabled={request?.state === 'accepted'} onclick={requestMaterial}>{request?.state === 'accepted' ? 'Review requested' : request?.state === 'error' ? 'Retry request' : 'Ask agent to prepare review'}</Button>
                {/if}
                {#if session}<Button variant="secondary" size="md" onclick={() => tab = 'code'}>View code changes</Button>{/if}
              </div>
              {#if request?.message}<p role={request.state === 'error' ? 'alert' : 'status'} class="mt-3 text-sm text-ink-2">{request.message}</p>
              {:else if !canRequest}<p class="mt-3 text-sm text-faint">{session ? 'Resume the coding agent in the conversation to request review material.' : 'This session ended without publishing review material.'}</p>{/if}
            </div>
          {/if}
        </div>
      {/if}
    </div>
    {#if panelOpen}<aside class="review-aside" aria-label="Review actions">
      {#if reviewSession}<BeforeMerge session={reviewSession} />{/if}
      {#if session}<OverviewChecks {session} {tests} onJump={jump} />{/if}
      <section>
        <h2 class="cv-overview-heading mb-3">Review feedback</h2>
        {#if !session}<p class="text-sm text-faint">This session has ended. Its review material is retained; sending feedback and live checkout actions are unavailable.</p>{/if}
        {#if count}
          <p class="mt-4 text-sm text-ink-2">{feedback.draftsFor(sessionId).length} code comments{notes ? ' · Material feedback included' : ''}</p>
          {#if notes}<details class="mt-2"><summary class="cursor-pointer text-sm text-ink-2">Inspect feedback draft</summary><textarea aria-label="Feedback draft" class="review-input mt-2" rows="8" maxlength="4000" value={notes} oninput={e => feedback.setNote(sessionId,e.currentTarget.value)}></textarea></details>{/if}
          <Button size="sm" variant="primary" class="mt-3" loading={feedback.isSending(sessionId)} disabled={!session} onclick={send}>Send feedback to agent</Button>
        {:else}<p class="mt-3 text-sm text-faint">Pin a comment on a screenshot, mark an area, or comment on the write-up. Review your batch here before sending.</p>{/if}
        {#if notice}<p role="status" class="mt-3 text-sm text-ink-2">{notice}</p>{/if}
      </section>
    </aside>{/if}
  </div>
</Modal>

<style>
  .review-toolbar { display:flex; flex-wrap:wrap; align-items:center; justify-content:space-between; gap:12px; padding:14px 24px; border-bottom:1px solid var(--color-edge); }
  .review-layout { display:grid; grid-template-columns:minmax(0,1fr); min-height:0; }
  .review-layout.panel-open { grid-template-columns:minmax(0,1fr) 330px; }
  .review-tabs :global([role=tablist]) { margin-bottom:0; border-bottom:0; }
  .review-tabs { display:flex; align-items:center; justify-content:space-between; gap:12px; padding-right:16px; border-bottom:1px solid var(--color-edge); }
  .review-visuals { min-height:0; overflow:hidden; }
  .review-material { max-width:1000px; width:100%; margin-inline:auto; }
  .review-main { display:grid; grid-template-rows:auto minmax(0,1fr); min-width:0; min-height:0; }
  .review-material { overflow:auto; padding:28px 32px 64px; }
  .package-heading { margin-bottom:20px; }
  .package-meta { display:flex; flex-wrap:wrap; align-items:baseline; gap:8px 16px; margin-top:8px; font-size:12px; color:var(--color-faint); }
  .package-meta summary,.writeup-comment summary { cursor:pointer; }
  .writeup-comment { padding:12px 0; color:var(--color-ink-2); font-size:13px; }
  .review-toolbar > div:last-child { max-width:100%; }
  .review-toolbar :global(.lola-select) { width:auto; max-width:350px; }
  .review-toolbar :global(select) { max-width:350px; }
  .review-aside { display:grid; align-content:start; gap:28px; min-width:0; overflow:auto; padding:24px 20px; border-left:1px solid var(--color-edge); background:var(--color-canvas); }
  .review-input { width:100%; resize:vertical; padding:10px; border:1px solid var(--color-edge); border-radius:6px; background:var(--color-panel); color:var(--color-ink); font-size:13px; line-height:20px; }
  .empty-review { max-width:48ch; padding-top:24px; }
  @media(max-width:1000px) { .review-material { padding:24px 20px; } }
  @media(max-width:740px) { .review-layout { display:block; overflow:auto; } .review-main { display:grid; min-height:0; } .review-visuals { height:calc(100dvh - 210px); min-height:500px; } .review-tabs { flex-wrap:wrap; padding-right:8px; gap:4px; } .review-material { overflow:visible; } .review-code { height:70vh; } .review-aside { overflow:visible; border-left:0; border-top:1px solid var(--color-edge); } .review-toolbar { padding:12px 16px; } }
</style>
