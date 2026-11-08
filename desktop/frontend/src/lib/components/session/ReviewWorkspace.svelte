<script lang="ts">
  import Chevron from "$lib/components/Chevron.svelte";
  import { DaemonService, TalkService } from '@bindings/desktop';
  import { SvelteMap } from 'svelte/reactivity';
  import type { ReviewPackage as Package, PRReadinessData, ReviewRecord } from '@bindings/internal/protocol';
  import { reviewFreshness } from '$lib/reviewchanges';
  import { reviewSections } from '$lib/reviewmaterial';
  import { readPRReadiness } from '$lib/prreadiness';
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
  import ReviewOverview from './ReviewOverview.svelte';
  import BeforeMerge from './BeforeMerge.svelte';
  import OverviewChecks from './OverviewChecks.svelte';
  import ReviewCanvas from './ReviewCanvas.svelte';
  import ReviewChanges from './ReviewChanges.svelte';
  import ReviewSkeleton from './ReviewSkeleton.svelte';
  import ReviewVerification from './ReviewVerification.svelte';

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
  let reviews = $state<Partial<Record<string,ReviewRecord>>>({});
  const reviewScope = $derived(current?.session ?? target);
  const reviewed = $derived(reviews[reviewScope]);
  let reviewSupported = $state(false);
  const previous = $derived(packages.find(p => p.id === reviewed?.package));
  const comparable = $derived(previous && current && previous.id !== current.id && previous.session === current.session && previous.repo === current.repo && previous.pr === current.pr);
  let marking = $state(false);
  let markError = $state('');
  let reviewCode = $state(false);
  let facts = $state<PRReadinessData>();
  let loading = $state(true);
  let materialLoading = $state(true);
  let checking = $state(false);
  let error = $state('');
  let generation = 0;
  let reviewWrite = 0;
  let tab = $state('material');
  let panelOpen = $state(false);
  let canvasScreen = $state(0);
  let canvas = $state<ReviewCanvas>();
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
  const freshness = $derived(current ? reviewFreshness(current,facts) : 'unknown');
  const sections = $derived(current ? reviewSections(current.body) : []);
  const requests = new SvelteMap<string, { state: 'sending' | 'accepted' | 'error'; message: string }>();
  const request = $derived(requests.get(target));
  const canRequest = $derived(!!session && !session.goalId && !['dead', 'exited', 'shell', 'orphaned'].includes(session.agentState ?? ''));

  const assessmentKey = $derived(`${target}:risks:${current?.id ?? ''}`);
  const assessment = $derived(requests.get(assessmentKey));
  const confidenceKey = $derived(`${target}:confidence:${current?.id ?? ''}`);
  const confidenceRequest = $derived(requests.get(confidenceKey));
  async function requestMaterial(assessRisks = false, assessConfidence = false) {
    const key = assessConfidence ? confidenceKey : assessRisks ? assessmentKey : target;
    if (!canRequest || requests.get(key)?.state === 'sending' || requests.get(key)?.state === 'accepted') return;
    const scope = lane ? ` for stack member ${JSON.stringify(lane.id)}${number ? ` (PR #${number})` : ''}` : number ? ` for PR #${number}` : ' for this session';
    const text = `${assessConfidence ? `Add an explicit self-assessment to review package ${current?.id}, retaining its screenshots and evidence. ` : ''}${assessRisks ? `Assess risks in review package ${current?.id}, retaining its screenshots and evidence. ` : ''}Prepare and publish review material in Lola${scope}: a concise write-up using ## Summary (one sentence), ## Changes (at most three bullets, each starting with a bold 3–8 word user-visible outcome; under each bullet add an indented New: sentence of at most 16 words describing the new capability. Add an indented Includes: list with 2–4 nested bullets describing user-facing sub-features, not classes or files. Put technical detail under Details:; keep filenames out of labels), ## Verification (commands and results), and ## Risks (only unresolved items; each bullet starts with a bold concrete failure consequence, not a topic label (for example: New users cannot finish registration, not Mail delivery), followed by Likelihood: low|medium|high|unknown; Impact: low|medium|high|unknown; Outcome: one short plain-language sentence; Assess: one concrete next step to check the risk (not a claim it was performed); Basis: a brief rationale. Estimate likelihood and severity separately from evidence; use unknown when unsupported, never invent probabilities). Include ## Agent confidence with two separate lines: Score: N/100 (an integer from 0 to 100 expressing your confidence that this revision meets the requested behavior) and Basis: a brief rationale naming verification performed and remaining uncertainty. Use 0-39 for substantial doubts, 40-69 for meaningful unverified behavior, 70-89 for strong evidence with bounded uncertainty, and 90-100 only for thorough relevant verification with no known gaps. This is your self-assessment, not a measured probability. Do not lower it solely because human approval is pending. Reuse screenshots and test results already collected during implementation and testing when they still describe the reviewed revision. Do not rerun checks or recreate captures solely to format this package. Capture or verify only missing or stale evidence. Put extended technical detail under ## Details. Include before/after screenshots for visual changes where available; explain any missing baseline. Use the actual PR base and head revisions. Run lola review-package publish --help for the manifest format, then publish the package${lane ? ` with --app ${JSON.stringify(lane.id)}` : ''}. This request is to document the current changes; do not modify application code or create a new PR.`;
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
    const version = ++generation, sid = target, pr = number, write = reviewWrite;
    loading = true; materialLoading = true; checking = !!pr; error = ''; facts = undefined;
    // Retained material is local; don't hold it behind a GitHub round trip.
    await Promise.all([
      DaemonService.ReviewPackages(sid).then(material => {
        if (version !== generation) return;
        packages = material.packages ?? [];
        if (write === reviewWrite) reviews = material.reviewed ?? {};
        reviewSupported = material.reviewTracking === true;
        // A new publication must not replace the revision someone is reviewing.
        if (!selected || !packages.some(p => p.id === selected)) selected = packages[0]?.id ?? '';
      }, err => { if (version === generation) error = String(err); })
        .finally(() => { if (version === generation) materialLoading = false; }),
      (pr ? readPRReadiness(sid, pr) : Promise.resolve(undefined)).then(status => {
        if (version === generation) facts = status;
      }, () => { /* Unavailable GitHub facts remain unverified. */ })
        .finally(() => { if (version === generation) checking = false; }),
    ]);
    if (version === generation) loading = false;
  }
  $effect(() => {
    target; number;
    packages = []; selected = ''; notice = ''; reviews = {}; markError = ''; reviewCode = false; reviewSupported = false;
    void refresh();
    const timer = setInterval(() => { if (document.visibilityState !== 'hidden') void refresh(); }, 60000);
    return () => { generation++; clearInterval(timer); };
  });
  async function markReviewed() {
    if (!current || marking) return;
    const sid = target, scope = reviewScope, packageID = current.id;
    marking = true; markError = '';
    try {
      const result = await DaemonService.ReviewPackageMark(scope,packageID,reviewed?.package ?? '');
      if (sid === target) {
        // Older reads cannot undo this inspection; GitHub can still finish.
        reviewWrite++; reviews = {...reviews,[scope]:result}; reviewCode = false;
        if (tab === 'since') tab = 'material';
      }
    } catch (err) {
      if (sid === target) markError = `Could not save your review. ${String(err)}`;
    } finally { marking = false; }
  }
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
  function commentContext(section: string, screenIndex?: number): string {
    if (!current) return "";
    const screen = screenIndex === undefined ? undefined : current.screens?.[screenIndex];
    const images = screen ? `Page: ${screen.url || 'unspecified'}; viewport: ${screen.viewport || 'unspecified'}${screen.selector ? `; element: ${screen.selector}` : ''}\nScreenshots: before ${screen.before || 'unavailable'}, after ${screen.after}\n` : '';
    return `Review package ${current.id}\nRepository: ${current.repo || 'unspecified'}${current.pr ? ` PR #${current.pr}` : ''}\nBase: ${current.base}\nHead: ${current.head}\nSection: ${section}\n${images}`;
  }
  function queueComment(section: string, text: string, screenIndex?: number, previousText?: string): boolean {
    if (!current || !text.trim()) return false;
    const context = commentContext(section, screenIndex);
    const previous = previousText ? context + previousText.trim() : '';
    // Replace only this annotation's exact pending entry. Once sent (or manually
    // changed in the batch), retain that history and add the latest text instead.
    const combined = previous && notes.includes(previous)
      ? notes.replace(previous, () => context + text.trim())
      : [notes, context + text.trim()].filter(Boolean).join('\n\n');
    feedback.setNote(sessionId, combined);
    notice = 'Comment added to review feedback.';
    return true;
  }
  function addComment() {
    const index = current?.screens?.findIndex((s,i) => subject === `Screen ${i+1}: ${s.title}`);
    if (queueComment(subject, draft, index !== undefined && index >= 0 ? index : undefined)) { subject = 'Write-up'; editDraft(''); }
  }
  function addScreenComment(index: number, text: string, previous?: string): boolean {
    return queueComment(`Screen ${index+1}: ${current?.screens?.[index]?.title}`, text, index, previous);
  }
  function deleteScreenComment(index: number, text: string) {
    const context = commentContext(`Screen ${index+1}: ${current?.screens?.[index]?.title}`, index);
    if (!context) return;
    // Only remove the exact pending entry, never other comments or sent history.
    feedback.setNote(sessionId, notes.replace(context + text.trim(), '').trim());
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

{#snippet checks()}
  {#if reviewSession}<BeforeMerge session={reviewSession} reviewLayout/>{/if}
  {#if current}{#key current.id}<ReviewVerification pkg={current} {freshness} canRun={!!session && !session.goalId && (!session.sandbox || session.sandbox === 'none')}/>{/key}{/if}
  {#if session}<details class="local-checks"><summary><Chevron class="disclosure-chevron" />Local checks</summary><div class="mt-3"><p class="mb-3 text-xs text-faint">Not verified for this version.</p><OverviewChecks {session} {tests} onJump={jump}/></div></details>{/if}
{/snippet}
{#snippet reviewFeedback()}
  <section aria-label="Review feedback" class="review-feedback">
    <div class="feedback-heading"><h2><Icon name="send" size={16}/>Feedback</h2>{#if count}<span>Ready to send</span>{/if}</div>
    {#if session && current}
      <Select aria-label="Comment section" value={subject} onchange={e => { subject = e.currentTarget.value; editDraft(draft); }}><option value="Write-up">Whole write-up</option>{#each sections as section,i}<option value={`Section ${i+1}: ${section.title}`}>{section.title}</option>{/each}</Select>
      {#if subject !== 'Write-up' && draft}<p class="text-sm text-ink-2">Restored draft: {subject}</p>{/if}
      <textarea aria-label="Review comment" class="review-input" rows="2" maxlength="3000" value={draft} oninput={e => editDraft(e.currentTarget.value)} placeholder="What needs changing or explaining?"></textarea>
      {#if draft.trim()}<Button size="sm" variant="secondary" onclick={addComment}><Icon name="plus" size={14}/>Add to feedback</Button>{/if}
    {:else if !session}<p class="text-sm text-faint">Session ended. Review material is retained; feedback is unavailable.</p>{/if}
    {#if count}
      {#if feedback.draftsFor(sessionId).length}<p class="text-sm text-ink-2">{feedback.draftsFor(sessionId).length} code comments</p>{/if}
      {#if notes}<details><summary><Chevron class="disclosure-chevron" />Inspect feedback draft</summary><textarea aria-label="Feedback draft" class="review-input mt-2" rows="6" value={notes} oninput={e => feedback.setNote(sessionId,e.currentTarget.value)}></textarea></details>{/if}
      <div><Button size="sm" variant="primary" loading={feedback.isSending(sessionId)} disabled={!session} onclick={send}><Icon name="arrow-up" size={14}/>Send feedback to agent</Button></div>
    {/if}
    {#if notice}<p role="status" class="text-sm text-ink-2">{notice}</p>{/if}
  </section>
{/snippet}

<Modal title="PR review" onkey={e => { if (tab === 'visuals') canvas?.handleKey(e); }} fullscreen onClose={() => nav.closeOverlay()} bodyClass="grid min-h-0 grid-rows-[auto_minmax(0,1fr)]">
  <div class="review-toolbar">
    <div class="min-w-0">
      <p class="truncate font-medium text-ink">{session?.title || session?.issue || sessionId}</p>
    </div>
    <div class="flex min-w-0 flex-wrap items-center gap-2">
      {#if lanes.length}<Select aria-label="Review repository" value={lane?.id} onchange={e => app = e.currentTarget.value}>{#each lanes as a}<option value={a.id}>{a.label}</option>{/each}</Select>{/if}
      {#if packages.length}<Select aria-label="Review version" value={current?.id} onchange={e => selected = e.currentTarget.value}>{#each packages as p,i}<option value={p.id} data-selected-label={i === 0 ? 'Latest' : undefined} data-description={`Commit ${p.head.slice(0,8)}`}>{i === 0 ? 'Latest · ' : ''}{new Date(p.createdAt).toLocaleString(undefined,{dateStyle:'medium',timeStyle:'medium'})}</option>{/each}</Select>{/if}
      {#if current && reviewSupported}<Button size="sm" variant="secondary" loading={marking} disabled={reviewed?.package === current.id || !!error} title="Remember this package as inspected. This does not approve or merge the PR." onclick={markReviewed}>{reviewed?.package === current.id ? 'Reviewed' : 'Mark reviewed'}</Button>{/if}
      <Button size="sm" icon aria-label="Refresh review" title="Refresh review" loading={loading} onclick={refresh}><Icon name="refresh-cw" size={14} /></Button>
    </div>
    {#if markError}<p role="alert" class="w-full text-sm text-warn">{markError} Refresh review and retry.</p>{/if}
  </div>
  <div class="review-layout" class:panel-open={panelOpen && (tab !== 'material' || !current)}>
    <div class="review-main">
      <div class="review-tabs"><Tabs tabs={[{id:'material',label:'Overview'}, ...(current?.screens?.length ? [{id:'visuals',label:'Screenshots'}] : []), ...(session ? [{id:'code',label:'Code changes'}] : []), ...(comparable ? [{id:'since',label:'Since review'}] : [])]} active={tab} onSelect={id => { tab = id; reviewCode = false; }} />{#if tab !== 'material' || !current}<Button size="sm" title={tab === 'visuals' ? 'Checks & feedback · B' : 'Checks & feedback'} aria-keyshortcuts={tab === 'visuals' ? 'b' : undefined} selected={panelOpen} aria-expanded={panelOpen} onclick={() => panelOpen = !panelOpen}>Checks &amp; feedback{count ? ` · ${count}` : ''}</Button>{/if}</div>
      {#if tab === 'code' && session}
        <div class="review-code grid min-h-0 grid-rows-[auto_minmax(0,1fr)]"><p class="px-4 py-2 text-xs text-faint">Live checkout changes. These may differ from the retained review revision.</p><DiffView {sessionId} /></div>
      {:else if tab === 'since' && comparable && previous && current}
        {#if reviewCode && session}<div class="review-code grid min-h-0 grid-rows-[auto_minmax(0,1fr)]"><div class="px-4 py-2 text-xs text-faint"><Button size="xs" onclick={() => reviewCode = false}>Back to material comparison</Button><p>Exact published heads: {previous.head.slice(0,12)} to {current.head.slice(0,12)}. Uncommitted changes are excluded.</p></div><DiffView sessionId={target} reviewRange={{from:previous.id,to:current.id}}/></div>
        {:else}<div class="review-material"><ReviewChanges before={previous} after={current} onCode={session ? () => reviewCode = true : undefined}/></div>{/if}
      {:else if tab === 'visuals' && current?.screens?.length}
        <div class="review-visuals">
          {#key current.id}<ReviewCanvas bind:this={canvas} onToggleFeedback={() => panelOpen = !panelOpen} pkg={current} {freshness} bind:index={canvasScreen} canComment={!!session} onComment={addScreenComment} onDeleteComment={deleteScreenComment} />{/key}
        </div>
      {:else}
        <div class="review-material">
          {#if error}<p role="alert" class="mb-5 text-warn">Could not load review material. {error} Try Refresh review; an older daemon may need restarting.</p>{/if}
          {#if current}
            {#if comparable}<p role="status" class="mb-5 text-sm text-info">This package differs from your last review. <Button size="xs" onclick={() => { tab = 'since'; reviewCode = false; }}>See changes since review</Button></p>{/if}
            {#if current.id !== packages[0]?.id}<p class="mb-5 text-sm text-info">A newer review package is available. <Button size="xs" onclick={() => selected = packages[0].id}>View latest package</Button></p>{/if}
            <ReviewOverview pkg={current} {facts} {sessionId} {freshness} {checking} onScreen={index => { canvasScreen = index; tab = 'visuals'; }} {checks} feedback={reviewFeedback} onAssessRisks={canRequest ? () => requestMaterial(true) : undefined} {assessment} onAssessConfidence={canRequest ? () => requestMaterial(false,true) : undefined} {confidenceRequest}/>
          {:else if materialLoading}<ReviewSkeleton/>
          {:else if !error}
            <div class="empty-review">
              <h1 class="text-lg font-semibold text-ink">No review material yet</h1>
              <p class="mt-2 text-prose text-ink-2">Get a summary of the changes, verification results, and screenshots from the agent.</p>
              <div class="mt-5 flex flex-wrap items-center gap-2">
                {#if canRequest}
                  <Button variant="primary" size="md" loading={request?.state === 'sending'} disabled={request?.state === 'accepted'} onclick={() => requestMaterial()}>{request?.state === 'accepted' ? 'Review requested' : request?.state === 'error' ? 'Retry request' : 'Ask agent to prepare review'}</Button>
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
    {#if panelOpen && (tab !== 'material' || !current)}<aside class="review-aside" aria-label="Review actions">
      {@render checks()}
      {@render reviewFeedback()}
    </aside>{/if}
  </div>
</Modal>

<style>
  summary { list-style:none; }
  summary::-webkit-details-marker { display:none; }
  summary::marker { content:""; }
  summary > :global(.disclosure-chevron) { margin-right:6px; vertical-align:-2px; color:var(--color-ink-2); }
  details[open] > summary > :global(.disclosure-chevron) { transform:rotate(90deg); }
  .review-toolbar { display:flex; flex-wrap:wrap; align-items:center; justify-content:space-between; gap:12px; padding:12px 28px; background:var(--color-canvas); }
  .review-layout { display:grid; grid-template-columns:minmax(0,1fr); min-height:0; }
  .review-layout.panel-open { grid-template-columns:minmax(0,1fr) 330px; }
  .review-tabs :global([role=tablist]) { margin-bottom:0; border:0; gap:4px; }
  .review-tabs :global([role=tab]) { margin:0; border:0; border-radius:7px; padding:7px 14px; font-size:12px; color:var(--color-ink-2); }
  .review-tabs :global([role=tab][aria-selected=true]) { background:var(--color-accent-fill); color:var(--color-accent-ink); }
  .review-tabs :global([role=tab]:hover) { color:var(--color-ink); }
  .review-tabs { display:flex; align-items:center; justify-content:space-between; gap:12px; padding:0 28px 12px; background:var(--color-canvas); border-bottom:1px solid color-mix(in srgb,var(--color-edge) 55%,transparent); }
  .review-visuals { min-height:0; overflow:hidden; }
  .review-material { width:100%; min-width:0; }
  .review-main { display:grid; grid-template-rows:auto minmax(0,1fr); min-width:0; min-height:0; }
  .review-material { overflow:auto; padding:40px 40px 64px; background:var(--color-canvas); }
  .review-feedback { display:grid; gap:12px; }
  .feedback-heading { display:flex; align-items:center; justify-content:space-between; gap:12px; }
  .feedback-heading h2 { display:flex; align-items:center; gap:8px; font-size:14px; font-weight:600; }
  .feedback-heading > span { color:var(--color-accent); font-size:11px; }
  .review-feedback summary,.local-checks summary { cursor:pointer; color:var(--color-ink-2); font-size:12px; }
  .local-checks { margin-top:16px; }
  .review-toolbar > div:last-child { max-width:100%; }
  .review-toolbar :global(.lola-select) { width:auto; max-width:350px; }
  .review-toolbar :global(select) { max-width:350px; }
  .review-aside { display:grid; align-content:start; gap:28px; min-width:0; overflow:auto; padding:24px 20px; border-left:1px solid var(--color-edge); background:var(--color-canvas); }
  .review-input { width:100%; resize:vertical; padding:12px; border:1px solid color-mix(in srgb,var(--color-edge) 70%,transparent); border-radius:8px; background:var(--color-canvas); color:var(--color-ink); font-size:13px; line-height:20px; }
  .review-input:focus { border-color:var(--color-accent); outline:none; }
  .feedback-heading h2 :global(svg) { color:var(--color-info); }
  .empty-review { max-width:48ch; padding-top:24px; }
  @media(max-width:1000px) { .review-material { padding:24px 20px; } }
  @media(max-width:740px) { .review-layout { display:block; overflow:auto; } .review-main { display:grid; min-height:0; } .review-visuals { height:calc(100dvh - 210px); min-height:500px; } .review-tabs { flex-wrap:wrap; padding-right:8px; gap:4px; } .review-material { overflow:visible; } .review-code { height:70vh; } .review-aside { overflow:visible; border-left:0; border-top:1px solid var(--color-edge); } .review-toolbar { padding:12px 16px; } }
</style>
