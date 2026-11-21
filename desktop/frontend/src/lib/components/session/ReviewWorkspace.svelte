<script lang="ts">
  import { tick, untrack } from 'svelte';
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
  import ReviewConfidence from './ReviewConfidence.svelte';
  import BeforeMerge from './BeforeMerge.svelte';
  import OverviewChecks from './OverviewChecks.svelte';
  import ReviewCanvas from './ReviewCanvas.svelte';
  import ReviewChanges from './ReviewChanges.svelte';
  import ReviewSkeleton from './ReviewSkeleton.svelte';
  import ReviewVerification from './ReviewVerification.svelte';
  import { touchSurface } from '$lib/touchsurface';
  import ReviewCapture from './ReviewCapture.svelte';
  import ReviewScreensPhone from './ReviewScreensPhone.svelte';
  import { reviewCommentContext } from '$lib/reviewnote';

  // `embedded` mounts the workspace without its own modal (the phone's review sheet): the sheet draws the tabs
  // and "Mark reviewed" from `onstatus`, so there is one set of controls. `labels` is the phone's one-line
  // verification verdict. All three default to the desktop's behaviour.
  // `can` answers whether this client may send a command (the phone's tiers); a control it may not
  // send is not drawn. On a touch surface Verify and Capture are never drawn, whatever the tier:
  // both run a command line or agent-written code on the Mac, which is no thing to author or audit
  // on a phone. Their results stay readable.
  type Status = { tabs: { id: string; label: string }[]; canMark: boolean; reviewed: boolean; marking: boolean; mark: () => void };
  let { sessionId, embedded = false, tab = $bindable('material'), onstatus, labels, can = () => true }: {
    sessionId: string; embedded?: boolean; tab?: string; onstatus?: (s: Status) => void; labels?: { verified: string; notVerified: string }; can?: (cmd: string) => boolean;
  } = $props();
  const phone = touchSurface();
  // Desktop: overview and screenshots share one screen, so there is no separate Screenshots tab.
  const split = $derived(!embedded && !phone);
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
  // The desktop keeps Checks & feedback beside the work at all times; the phone's sheet draws its own.
  let panelOpen = $state(untrack(() => !embedded));
  const widths = { left: { key: "lola.review.left", def: 300, min: 220 }, aside: { key: "lola.review.aside", def: 300, min: 260 } };
  const stored = (k: string, def: number) => { try { const n = Number(localStorage.getItem(k)); return n > 0 ? n : def; } catch { return def; } };
  let leftW = $state(stored(widths.left.key, widths.left.def));
  let asideW = $state(stored(widths.aside.key, widths.aside.def));
  let splitEl = $state<HTMLElement>();
  let layoutEl = $state<HTMLElement>();
  function save(k: string, n: number) { try { localStorage.setItem(k, String(n)); } catch { /* per-viewer convenience only */ } }
  // Measured sizes keep a saved or dragged width inside the layout it is shown in, however the window or the checks panel changes.
  let splitW = $state(0);
  let layoutW = $state(0);
  const clampLeft = (n: number) => Math.max(widths.left.min, Math.min(n, (splitW || Infinity) - 360));
  const clampAside = (n: number) => Math.max(widths.aside.min, Math.min(n, 560, (layoutW || Infinity) - 600));
  const leftEff = $derived(clampLeft(leftW));
  const asideEff = $derived(clampAside(asideW));
  $effect(() => {
    if (typeof ResizeObserver === 'undefined') return;
    const watch = (el: HTMLElement | undefined, set: (n: number) => void) => { if (!el) return; set(el.clientWidth); const o = new ResizeObserver(() => set(el.clientWidth)); o.observe(el); return () => o.disconnect(); };
    const a = watch(splitEl, n => splitW = n), b = watch(layoutEl, n => layoutW = n);
    return () => { a?.(); b?.(); };
  });
  function drag(event: PointerEvent, side: "left" | "aside") {
    const host = side === "left" ? splitEl : layoutEl;
    if (!host || event.button !== 0) return;
    event.preventDefault();
    const handle = event.currentTarget as HTMLElement;
    handle.setPointerCapture(event.pointerId);
    const rect = host.getBoundingClientRect();
    const move = (e: PointerEvent) => { if (side === "left") leftW = clampLeft(e.clientX - rect.left); else asideW = clampAside(rect.right - e.clientX); };
    const done = () => { handle.removeEventListener("pointermove", move); handle.removeEventListener("pointerup", done); handle.removeEventListener("pointercancel", done); save(widths[side].key, side === "left" ? leftW : asideW); };
    handle.addEventListener("pointermove", move); handle.addEventListener("pointerup", done); handle.addEventListener("pointercancel", done);
  }
  function nudge(event: KeyboardEvent, side: "left" | "aside") {
    const step = event.key === "ArrowLeft" ? -16 : event.key === "ArrowRight" ? 16 : 0;
    if (event.key === "Home") { event.preventDefault(); if (side === "left") leftW = widths.left.def; else asideW = widths.aside.def; save(widths[side].key, side === "left" ? leftW : asideW); return; }
    if (!step) return;
    event.preventDefault();
    if (side === "left") leftW = clampLeft(leftEff + step); else asideW = clampAside(asideEff - step);
    save(widths[side].key, side === "left" ? leftW : asideW);
  }
  let canvasScreen = $state(0);
  let captureOpen = $state(false);
  let captureBusy = $state(false);
  const captureHost = $derived(!phone && can('reviewPackageCapture') && !!session && !session.goalId && session.location !== 'ssh' && (!session.sandbox || session.sandbox === 'none'));
  const canVerify = $derived(!phone && can('reviewPackageVerify') && !!session && !session.goalId && (!session.sandbox || session.sandbox === 'none'));
  const canComment = $derived(!!session && can('feedback'));
  let canvas = $state<ReviewCanvas>();
  const packageIdentity = $derived(current?.id);
  // A fresh capture names the screen to show in the package it created; any other package starts at its first.
  let showScreen = -1;
  $effect(() => { packageIdentity; canvasScreen = showScreen >= 0 ? showScreen : 0; showScreen = -1; captureOpen = false; captureBusy = false; });
  $effect(() => { if (split && tab === 'visuals') tab = 'material'; });
  let subject = $state('Write-up');
  let draft = $state('');
  let notice = $state('');
  const draftKey = $derived(`lola.review.draft.${target}.${current?.id ?? ''}`);
  const notes = $derived(feedback.noteFor(sessionId));
  const count = $derived(feedback.count(sessionId));
  const f = $derived(feed.view(sessionId));
  const view = $derived(ssh.view(sessionId));
  const tests = $derived(sessionActivity({feed:f.items,items:view.items,scope:view.scope,alive:false}).filter(e => e.kind === 'tests'));
  // Phone version labels: relative day + HH:MM, no seconds ("Today 22:28", "Yesterday 18:04", "14 Nov, 18:04").
  function phoneStamp(at: string): string {
    const d = new Date(at), now = new Date();
    const day = (x: Date) => new Date(x.getFullYear(), x.getMonth(), x.getDate()).getTime();
    const gap = Math.round((day(now) - day(d)) / 86400000);
    const hm = d.toLocaleTimeString(undefined, { hour: '2-digit', minute: '2-digit', hour12: false });
    if (gap === 0) return `Today ${hm}`;
    if (gap === 1) return `Yesterday ${hm}`;
    return `${d.toLocaleDateString(undefined, { day: 'numeric', month: 'short' })}, ${hm}`;
  }
  const phoneVersion = (p: Package, i: number) => (i === 0 ? 'Latest · ' : '') + phoneStamp(p.createdAt);
  const versionAria = $derived.by(() => {
    const i = packages.findIndex(p => p.id === current?.id);
    if (i < 0 || !current) return 'Review version';
    const full = new Date(current.createdAt).toLocaleString(undefined, { day: 'numeric', month: 'short', year: 'numeric', hour: '2-digit', minute: '2-digit', hour12: false });
    return `Version: ${i === 0 ? 'Latest, ' : ''}${full.replace(',', '')}`;
  });
  const freshness = $derived(current ? reviewFreshness(current,facts) : 'unknown');
  const captureReason = $derived(!current ? 'Prepare review material first' : !captureHost ? 'Capture requires a local host checkout' : freshness === 'stale' ? 'Prepare review material for the current revision first' : 'Capture a page or element into this review');
  const sections = $derived(current ? reviewSections(current.body) : []);
  const tabList = $derived([{id:'material',label:split ? 'Review' : 'Overview'}, ...(!split && (current?.screens?.length || (embedded && captureHost)) ? [{id:'visuals',label:'Screenshots'}] : []), ...(session ? [{id:'code',label:phone ? 'Changes' : 'Code changes'}] : []), ...(comparable ? [{id:'since',label:'Since review'}] : [])]);
  $effect(() => { onstatus?.({ tabs: tabList, canMark: !!current && reviewSupported && can('reviewPackageMark'), reviewed: reviewed?.package === current?.id, marking, mark: markReviewed }); });
  const requests = new SvelteMap<string, { state: 'sending' | 'accepted' | 'error'; message: string }>();
  const request = $derived(requests.get(target));
  const canRequest = $derived(can('agentSay') && !!session && !session.goalId && !['dead', 'exited', 'shell', 'orphaned'].includes(session.agentState ?? ''));

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
    const onFocus = () => { void refresh(); };
    window.addEventListener('focus', onFocus);
    return () => { generation++; clearInterval(timer); window.removeEventListener('focus', onFocus); };
  });
  async function captured(result: Package, index: number) {
    if (result.session !== current?.session) return;
    generation++; loading = false; materialLoading = false; checking = false;
    showScreen = index;
    packages = [result,...packages.filter(p => p.id !== result.id)]; selected = result.id;
    captureOpen = false; tab = split ? 'material' : 'visuals'; reviewCode = false;
    await tick(); canvasScreen = index;
  }
  async function closeCapture() {
    captureOpen = false; await tick();
    document.querySelector<HTMLButtonElement>('[data-review-capture]')?.focus();
  }
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
    return reviewCommentContext(current, section, screenIndex);
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
  async function sendAll() { if (draft.trim()) addComment(); await send(); }
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

{#snippet localChecks()}
  <details class="local-checks"><summary><Chevron class="disclosure-chevron" />Local checks</summary><div class="mt-3"><OverviewChecks {session} {tests} onJump={jump}/></div></details>
{/snippet}
{#snippet checks()}
  {#if reviewSession && !embedded && number}<BeforeMerge session={reviewSession} reviewLayout/>
  {:else if !embedded && !phone}<section class="no-pr" aria-label="PR status"><h3><Icon name="git-pull-request" size={14}/>No pull request yet</h3><p>Review works on the checkout. CI, reviewers, conflicts and merge status appear here once a PR is open.</p></section>{/if}
  {#if current}{#key current.id}<ReviewVerification pkg={current} {freshness} quiet={split} extra={session && !phone ? localChecks : undefined} {labels} canRun={canVerify} canRecover={can('reviewPackageRecover')}/>{/key}{/if}
  {#if !current && session && !phone}{@render localChecks()}{/if}
{/snippet}
{#snippet reviewFeedback()}
  <section aria-label="Review feedback" class="review-feedback">
    <div class="feedback-heading"><h2><Icon name="send" size={16}/>Feedback</h2></div>
    {#if canComment && current}
      <Select aria-label="Comment section" value={subject} onchange={e => { subject = e.currentTarget.value; editDraft(draft); }}><option value="Write-up">Whole write-up</option>{#each sections as section,i}<option value={`Section ${i+1}: ${section.title}`}>{section.title}</option>{/each}</Select>
      {#if subject !== 'Write-up' && draft}<p class="text-sm text-ink-2">Restored draft: {subject}</p>{/if}
      <textarea aria-label="Review comment" class="review-input" rows="2" maxlength="3000" value={draft} oninput={e => editDraft(e.currentTarget.value)} placeholder="What needs changing or explaining?"></textarea>
      {#if draft.trim()}<Button size="sm" variant="secondary" onclick={addComment}><Icon name="plus" size={14}/>Add to feedback</Button>{/if}
    {:else if !session}<p class="text-sm text-faint">Session ended. Review material is retained; feedback is unavailable.</p>{/if}
    {#if count}
      {#if feedback.draftsFor(sessionId).length}<p class="text-sm text-ink-2">{feedback.draftsFor(sessionId).length} code comments</p>{/if}
      {#if notes}<details><summary><Chevron class="disclosure-chevron" />Inspect feedback draft</summary><textarea aria-label="Feedback draft" class="review-input mt-2" rows="6" value={notes} oninput={e => feedback.setNote(sessionId,e.currentTarget.value)}></textarea></details>{/if}
    {/if}
    {#if can('feedback')}<div class="send-row"><span>{count ? `${count} ready` : 'Nothing to send yet'}</span><Button size="sm" variant="primary" loading={feedback.isSending(sessionId)} disabled={!session || (!count && !draft.trim())} aria-label="Send feedback to agent" onclick={sendAll}><Icon name="arrow-up" size={14}/>Send</Button></div>{/if}
    {#if notice}<p role="status" class="text-sm text-ink-2">{notice}</p>{/if}
  </section>
{/snippet}

{#snippet material()}
        {#if error}<p role="alert" class="mb-5 text-warn">Could not load review material. {error} It re-reads when this window regains focus; an older daemon may need restarting.</p>{/if}
        {#if current}
          {#if comparable}<p role="status" class="mb-5 text-sm text-info">This package differs from your last review. <Button size="xs" onclick={() => { tab = 'since'; reviewCode = false; }}>See changes since review</Button></p>{/if}
          {#if current.id !== packages[0]?.id}<p class="mb-5 text-sm text-info">A newer review package is available. <Button size="xs" onclick={() => selected = packages[0].id}>View latest package</Button></p>{/if}
          <ReviewOverview {split} pkg={current} {facts} {sessionId} {freshness} {checking} onScreen={index => { canvasScreen = index; if (!split) tab = 'visuals'; }} {checks} feedback={reviewFeedback} onAssessRisks={canRequest ? () => requestMaterial(true) : undefined} {assessment} {confidenceRequest}/>
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
{/snippet}

{#snippet body()}
  <div class="review-toolbar" class:embedded>
    {#if !embedded}<div class="min-w-0">
      <p class="truncate font-medium text-ink">{session?.title || session?.issue || sessionId}</p>
    </div>{/if}
    <div class="flex min-w-0 flex-wrap items-center gap-2">
      {#if lanes.length}<Select aria-label="Review repository" value={lane?.id} onchange={e => app = e.currentTarget.value}>{#each lanes as a}<option value={a.id}>{a.label}</option>{/each}</Select>{/if}
      {#if embedded && !phone && packages.length}<span class="text-sm text-faint">Version:</span>{/if}
      {#if packages.length}<Select aria-label={phone ? versionAria : 'Review version'} value={current?.id} onchange={e => selected = e.currentTarget.value}>{#each packages as p,i}<option value={p.id} data-selected-label={phone ? phoneVersion(p, i) : i === 0 ? 'Latest' : undefined} data-description={`Commit ${p.head.slice(0,8)}`}>{#if phone}{phoneVersion(p, i)}{:else}{i === 0 ? 'Latest · ' : ''}{new Date(p.createdAt).toLocaleString(undefined,{dateStyle:'medium',timeStyle:'medium'})}{/if}</option>{/each}</Select>{/if}
      {#if !embedded && current && reviewSupported && can('reviewPackageMark')}<Button size="sm" variant="secondary" loading={marking} disabled={reviewed?.package === current.id || !!error} title="Remember this package as inspected. This does not approve or merge the PR." onclick={markReviewed}>{reviewed?.package === current.id ? 'Reviewed' : 'Mark reviewed'}</Button>{/if}
      <Button size="sm" icon aria-label="Refresh review" title="Refresh review" loading={loading} onclick={refresh}><Icon name="refresh-cw" size={14} /></Button>
    </div>
    {#if markError}<p role="alert" class="w-full text-sm text-warn">{markError} Reopen the review and retry.</p>{/if}
  </div>
  <div class="review-layout" class:panel-open={panelOpen && (tab !== 'material' || !current)}>
    <div class="review-main" class:capturing={captureOpen}>
      <div class="review-tabs" class:embedded>{#if !embedded}<Tabs tabs={[{id:'material',label:'Overview'}, ...(current?.screens?.length ? [{id:'visuals',label:'Screenshots'}] : []), ...(session ? [{id:'code',label:'Code changes'}] : []), ...(comparable ? [{id:'since',label:'Since review'}] : [])]} active={tab} onSelect={id => { tab = id; reviewCode = false; }} />{/if}<div class="review-tab-actions">{#if (!embedded || tab === 'visuals') && !phone && can('reviewPackageCapture')}<Button size="sm" icon variant="secondary" data-review-capture title={captureReason} aria-label="Capture screenshot" aria-expanded={captureOpen} disabled={captureBusy || !current || !captureHost || freshness === 'stale'} onclick={() => { captureOpen = !captureOpen; }}><Icon name="image" size={14}/></Button>{/if}{#if tab !== 'material' || !current}<Button size="sm" title={tab === 'visuals' ? 'Checks & feedback · B' : 'Checks & feedback'} aria-keyshortcuts={tab === 'visuals' ? 'b' : undefined} selected={panelOpen} aria-expanded={panelOpen} onclick={() => panelOpen = !panelOpen}>Checks &amp; feedback{count ? ` · ${count}` : ''}</Button>{/if}</div></div>
      {#if captureOpen && current}{#key current.id}<ReviewCapture pkg={current} bind:busy={captureBusy} initialURL={session?.devUrls?.[0] || current.screens?.[0]?.url || ''} initialScreen={tab === 'visuals' ? canvasScreen : -1} onCaptured={captured} onClose={closeCapture}/>{/key}{/if}
      {#if tab === 'code' && session}
        <div class="review-code grid min-h-0 grid-rows-[minmax(0,1fr)]"><DiffView {sessionId} /></div>
      {:else if tab === 'since' && comparable && previous && current}
        {#if reviewCode && session}<div class="review-code grid min-h-0 grid-rows-[auto_minmax(0,1fr)]"><div class="px-4 py-2 text-xs text-faint"><Button size="xs" onclick={() => reviewCode = false}>Back to material comparison</Button><p>Exact published heads: {previous.head.slice(0,12)} to {current.head.slice(0,12)}. Uncommitted changes are excluded.</p></div><DiffView sessionId={target} reviewRange={{from:previous.id,to:current.id}}/></div>
        {:else}<div class="review-material"><ReviewChanges before={previous} after={current} onCode={session ? () => reviewCode = true : undefined}/></div>{/if}
      {:else if phone && tab === 'visuals' && current?.screens?.length}
        <div class="review-material">{#key current.id}<ReviewScreensPhone pkg={current} note={notes} {canComment} onComment={addScreenComment} />{/key}</div>
      {:else if embedded && tab === 'visuals' && !current?.screens?.length}
        <div class="review-material"><p class="text-sm text-faint">{phone ? 'No screenshots in this version.' : 'No screenshots yet. Capture one from the running app.'}</p></div>
      {:else if tab === 'visuals' && current?.screens?.length}
        <div class="review-visuals">
          {#key current.id}<ReviewCanvas bind:this={canvas} onToggleFeedback={() => panelOpen = !panelOpen} pkg={current} {freshness} bind:index={canvasScreen} {canComment} onComment={addScreenComment} onDeleteComment={deleteScreenComment} />{/key}
        </div>
      {:else}
        <div class="review-material">{@render material()}</div>
      {/if}
    </div>
    {#if panelOpen && (tab !== 'material' || !current)}<aside class="review-aside" aria-label="Review actions">
      {@render checks()}
      {@render reviewFeedback()}
    </aside>{/if}
  </div>
{/snippet}

{#snippet versionPicker()}
  {#if packages.length}<Select aria-label="Review version" value={current?.id} onchange={e => selected = e.currentTarget.value}>{#each packages as p,i}<option value={p.id} data-selected-label={i === 0 ? 'Latest' : undefined} data-description={`Commit ${p.head.slice(0,8)}`}>{i === 0 ? 'Latest · ' : ''}{new Date(p.createdAt).toLocaleString(undefined,{dateStyle:'medium',timeStyle:'medium'})}</option>{/each}</Select>{/if}
{/snippet}

{#snippet deskHeader()}
    <header class="desk-head" aria-label="Review toolbar">
      <div class="head-title">
        <strong class="truncate" title={session?.title || session?.issue || sessionId}>{session?.title || session?.issue || sessionId}</strong>
        {#if number}<span class="chip"><Icon name="git-pull-request" size={12}/>PR #{number}</span>{:else}<span class="chip muted">No PR yet</span>{/if}
        {#if current}<span class="chip" data-fresh={freshness} title={freshness === 'current' ? 'Matches the PR head GitHub reports' : freshness === 'stale' ? 'The branch moved on since this was published' : 'Cannot tell whether this matches the branch'}>{freshness === 'current' ? 'Current' : freshness === 'stale' ? 'Outdated' : 'Unverified'}</span>{/if}
      </div>
      <div class="head-tabs"><Tabs tabs={tabList} active={tab} onSelect={id => { tab = id; reviewCode = false; }} /></div>
      <div class="head-actions">
        {#if lanes.length}<Select aria-label="Review repository" value={lane?.id} onchange={e => app = e.currentTarget.value}>{#each lanes as a}<option value={a.id}>{a.label}</option>{/each}</Select>{/if}
        {@render versionPicker()}
        {#if can('reviewPackageCapture') && captureHost}<Button size="sm" variant="secondary" data-review-capture title={captureReason} aria-label="Capture screenshot" aria-expanded={captureOpen} disabled={captureBusy || !current || !captureHost || freshness === 'stale'} onclick={() => { captureOpen = !captureOpen; }}><Icon name="image" size={14}/>Capture</Button>{/if}
        {#if canRequest && current}<Button size="sm" variant="secondary" aria-label={confidenceRequest?.state === 'accepted' ? 'Update requested' : 'Update review'} title={confidenceRequest?.state === 'accepted' ? 'Update requested' : 'Ask the agent to update the review and its confidence'} loading={confidenceRequest?.state === 'sending'} disabled={confidenceRequest?.state === 'accepted'} onclick={() => requestMaterial(false,true)}><Icon name={confidenceRequest?.state === 'accepted' ? 'check' : 'refresh-cw'} size={14} />{confidenceRequest?.state === 'accepted' ? 'Requested' : 'Update review'}</Button>{/if}
        <span class="toolbar-divider"></span>
        <Button size="sm" title="Checks & feedback · B" aria-label="Checks & feedback" aria-keyshortcuts="b" selected={panelOpen} aria-expanded={panelOpen} onclick={() => panelOpen = !panelOpen}><Icon name="panel-right" size={14}/>{count ? count : ''}</Button>
      </div>
    </header>
{/snippet}

{#snippet deskBody()}
  <div class="desk-body">
  {#if markError}<p role="alert" class="head-error text-sm text-warn">{markError} Reopen the review and retry.</p>{/if}
  <div class="review-layout desk" class:panel-open={panelOpen} bind:this={layoutEl} style:--aside-w={`${asideEff}px`}>
    <div class="review-main" class:capturing={captureOpen}>
      {#if captureOpen && current}{#key current.id}<ReviewCapture pkg={current} bind:busy={captureBusy} initialURL={session?.devUrls?.[0] || current.screens?.[0]?.url || ''} initialScreen={-1} onCaptured={captured} onClose={closeCapture}/>{/key}{/if}
      {#if tab === 'code' && session}
        <div class="review-code grid min-h-0 grid-rows-[minmax(0,1fr)]"><DiffView {sessionId} /></div>
      {:else if tab === 'since' && comparable && previous && current}
        {#if reviewCode && session}<div class="review-code grid min-h-0 grid-rows-[auto_minmax(0,1fr)]"><div class="px-4 py-2 text-xs text-faint"><Button size="xs" onclick={() => reviewCode = false}>Back to material comparison</Button><p>Exact published heads: {previous.head.slice(0,12)} to {current.head.slice(0,12)}. Uncommitted changes are excluded.</p></div><DiffView sessionId={target} reviewRange={{from:previous.id,to:current.id}}/></div>
        {:else}<div class="review-material"><ReviewChanges before={previous} after={current} onCode={session ? () => reviewCode = true : undefined}/></div>{/if}
      {:else}
        <div class="review-split" class:solo={!current} bind:this={splitEl} style:--left-w={`${leftEff}px`}>
          <div class="split-left review-material">{@render material()}</div>
          {#if current}
            <!-- svelte-ignore a11y_no_noninteractive_tabindex, a11y_no_noninteractive_element_interactions -->
            <div class="resizer" role="separator" aria-orientation="vertical" aria-label="Resize material column" tabindex="0" aria-valuenow={leftEff} style:left={`${leftEff - 4}px`} onpointerdown={e => drag(e, "left")} onkeydown={e => nudge(e, "left")} ondblclick={() => { leftW = widths.left.def; save(widths.left.key, leftW); }}></div>
            <section class="split-right" aria-label="Screenshots">
              {#if current && reviewSupported && can('reviewPackageMark')}<div class="mark-overlay"><Button size="sm" variant={reviewed?.package === current.id ? 'secondary' : 'primary'} loading={marking} disabled={reviewed?.package === current.id || !!error} title="Remember this package as inspected. This does not approve or merge the PR." onclick={markReviewed}>{#if reviewed?.package === current.id}<Icon name="check" size={14}/>{/if}{reviewed?.package === current.id ? 'Reviewed' : 'Mark reviewed'}</Button></div>{/if}
              {#if current.screens?.length || current.pages?.length}
                {#key current.id}<ReviewCanvas bind:this={canvas} compact onToggleFeedback={() => panelOpen = !panelOpen} pkg={current} {freshness} bind:index={canvasScreen} {canComment} onComment={addScreenComment} onDeleteComment={deleteScreenComment} />{/key}
              {:else}
                <div class="no-shots"><Icon name="image" size={28}/><h2>No screenshots yet</h2><p>{captureHost ? 'Capture the running app to compare it with what shipped, and mark up what needs to change.' : 'The agent has not published screenshots for this version.'}</p>{#if captureHost && can('reviewPackageCapture') && freshness !== 'stale'}<Button size="sm" variant="primary" onclick={() => captureOpen = true}><Icon name="image" size={14}/>Capture screenshot</Button>{/if}</div>
              {/if}
            </section>
          {/if}
        </div>
      {/if}
    </div>
    <!-- svelte-ignore a11y_no_noninteractive_tabindex, a11y_no_noninteractive_element_interactions -->
    {#if panelOpen}<div class="resizer" role="separator" aria-orientation="vertical" aria-label="Resize checks column" tabindex="0" aria-valuenow={asideEff} style:right={`${asideEff - 4}px`} onpointerdown={e => drag(e, "aside")} onkeydown={e => nudge(e, "aside")} ondblclick={() => { asideW = widths.aside.def; save(widths.aside.key, asideW); }}></div>
    <aside class="review-aside desk-aside" aria-label="Review actions">
      <div class="aside-scroll">{#if current}<ReviewConfidence pkg={current} {facts} {checking} {confidenceRequest}/>{/if}{@render checks()}<div class="aside-feedback">{@render reviewFeedback()}</div></div>
    </aside>{/if}
  </div>
  </div>
{/snippet}

{#if embedded}
  <div class="review-embedded grid min-w-0 grid-cols-[minmax(0,1fr)]" data-review-embedded>{@render body()}</div>
{:else}
  <Modal title="Review" headerContent={split ? deskHeader : undefined} onkey={e => { if (tab === 'visuals' || (split && tab === 'material')) canvas?.handleKey(e); }} fullscreen onClose={() => nav.closeOverlay()} bodyClass={split ? 'grid min-h-0 grid-rows-[minmax(0,1fr)]' : 'grid min-h-0 grid-rows-[auto_minmax(0,1fr)]'}>
    {#if split}{@render deskBody()}{:else}{@render body()}{/if}
  </Modal>
{/if}

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
  .review-tab-actions { display:flex; align-items:center; gap:8px; flex-shrink:0; }
  .review-main.capturing { grid-template-rows:auto auto minmax(0,1fr); }
  .review-visuals { min-height:0; overflow:hidden; }
  .review-material { width:100%; min-width:0; }
  .review-main { display:grid; grid-template-rows:auto minmax(0,1fr); min-width:0; min-height:0; }
  .review-material { overflow:auto; padding:40px 40px 64px; background:var(--color-canvas); }
  .review-feedback { display:grid; gap:8px; }
  .send-row { display:flex; align-items:center; justify-content:space-between; gap:8px; font-size:12px; color:var(--color-ink-2); }
  .feedback-heading { display:flex; align-items:center; justify-content:space-between; gap:12px; }
  .feedback-heading h2 { display:flex; align-items:center; gap:8px; font-size:14px; font-weight:600; }
  .review-feedback summary,.local-checks summary { cursor:pointer; color:var(--color-ink-2); font-size:12px; }
  .local-checks { margin-top:16px; }
  .review-toolbar > div:last-child { max-width:100%; }
  .review-toolbar.embedded { padding:0 0 12px; justify-content:flex-start; background:transparent; }
  /* Phone: the version is a left-aligned accent pull-down ("Latest · Today 22:28 ⌄"); the closed control
     shows only the selected label (Select's overlay), the native text stays transparent. */
  :global([data-phone-desk]) .review-toolbar.embedded { justify-content:flex-start; padding:0 4px 0 12px; margin:8px 0 14px; border:1px solid var(--color-edge-soft); border-radius:9px; background:var(--color-canvas); }
  :global([data-phone-desk]) .review-toolbar.embedded > div:last-child { width:100%; flex-wrap:nowrap; }
  :global([data-phone-desk]) .review-toolbar.embedded :global(.lola-select) { min-width:0; max-width:100%; }
  :global([data-phone-desk]) .review-toolbar.embedded :global(.lola-field) { min-height:44px; border:0; background:transparent; padding:0 20px 0 0; font-size:15px; }
  :global([data-phone-desk]) .review-toolbar.embedded :global(.select-value) { inset:0 20px 0 0; color:var(--color-accent); font-size:15px; font-weight:400; }
  :global([data-phone-desk]) .review-toolbar.embedded :global(.lola-select-caret) { width:12px; height:12px; right:0; color:var(--color-accent); }
  :global([data-phone-desk]) .review-toolbar.embedded :global([aria-label="Refresh review"]) { margin-left:auto; min-width:44px; min-height:44px; }
  .review-tabs.embedded { padding:0 0 8px; border:0; background:transparent; justify-content:flex-end; }
  .review-embedded .review-material { padding:0; background:transparent; overflow:visible; }
  .review-embedded .review-layout { display:block; min-width:0; overflow:visible; }
  .review-embedded .review-main { display:block; }
  .review-embedded .review-aside { overflow:visible; border-left:0; border-top:1px solid var(--color-edge-soft); padding:16px 0; }
  .review-embedded .review-visuals { height:60vh; min-height:360px; }
  .review-embedded .review-code { height:60vh; }
  .review-toolbar :global(.lola-select) { width:auto; max-width:350px; }
  .review-toolbar :global(select) { max-width:350px; }
  /* Desktop header: the Git toolbar's 48px bar, title left, views centred, actions right. */
  .desk-head { display:grid; grid-template-columns:minmax(0,1fr) auto minmax(0,1fr); align-items:center; gap:12px; min-height:32px; padding:0; }
  .head-title { display:flex; align-items:center; gap:8px; min-width:0; }
  .head-title strong { font-size:13px; font-weight:600; color:var(--color-ink); min-width:0; }
  .chip { display:inline-flex; align-items:center; gap:4px; flex-shrink:0; padding:2px 8px; border-radius:999px; font-size:11px; line-height:16px; color:var(--color-ink-2); background:color-mix(in srgb,var(--color-ink) 7%,transparent); }
  .chip.muted { color:var(--color-faint); }
  .chip[data-fresh=current] { color:var(--color-ok,var(--color-accent-ink)); background:color-mix(in srgb,var(--color-ok,var(--color-accent)) 14%,transparent); }
  .chip[data-fresh=stale] { color:var(--color-warn); background:color-mix(in srgb,var(--color-warn) 14%,transparent); }
  .head-tabs :global([role=tablist]) { margin-bottom:0; border:0; gap:2px; padding:3px; border-radius:9px; background:color-mix(in srgb,var(--color-ink) 6%,transparent); }
  .head-tabs :global([role=tab]) { margin:0; border:0; border-radius:7px; padding:5px 14px; font-size:12px; color:var(--color-ink-2); }
  .head-tabs :global([role=tab][aria-selected=true]) { background:var(--color-panel); color:var(--color-ink); box-shadow:0 1px 2px #0003; }
  .head-tabs :global([role=tab]:hover) { color:var(--color-ink); }
  .head-actions { display:flex; align-items:center; justify-content:flex-end; gap:8px; min-width:0; }
  .head-actions :global(.lola-select) { width:auto; max-width:170px; }
  .toolbar-divider { width:1px; height:20px; background:var(--color-edge); margin:0 2px; }
  .head-error { padding:8px 14px; border-bottom:1px solid var(--color-edge); }
  .desk-body { display:flex; flex-direction:column; min-height:0; min-width:0; }
  .review-layout.desk { position:relative; flex:1; min-height:0; grid-template-columns:minmax(0,1fr); }
  .review-layout.desk.panel-open { grid-template-columns:minmax(0,1fr) var(--aside-w,300px); }
  .desk .review-main { grid-template-rows:minmax(0,1fr); }
  .desk .review-main.capturing { grid-template-rows:auto minmax(0,1fr); }
  .review-split { position:relative; display:grid; grid-template-columns:var(--left-w,300px) minmax(0,1fr); min-height:0; min-width:0; }
  .review-split.solo { grid-template-columns:minmax(0,1fr); }
  .split-left.review-material { padding:20px 20px 40px; border-right:1px solid var(--color-edge); min-height:0; }
  .review-split.solo .split-left.review-material { border-right:0; padding:40px; }
  .resizer { position:absolute; z-index:8; top:0; bottom:0; width:8px; cursor:col-resize; touch-action:none; }
  .resizer::after { content:""; position:absolute; top:0; bottom:0; left:3px; width:2px; border-radius:1px; background:transparent; transition:background .12s; }
  .resizer:hover::after, .resizer:focus-visible::after { background:var(--color-accent); }
  .resizer:focus-visible { outline:none; }
  .split-right { position:relative; min-width:0; min-height:0; overflow:hidden; background:var(--color-canvas); }
  .mark-overlay { position:absolute; z-index:5; left:50%; bottom:14px; transform:translateX(-50%); border-radius:8px; box-shadow:0 6px 20px #0006; }
  .no-shots { display:grid; justify-items:center; align-content:center; gap:10px; height:100%; padding:32px; text-align:center; color:var(--color-faint); }
  .no-shots h2 { font-size:15px; font-weight:600; color:var(--color-ink); }
  .no-shots p { max-width:38ch; font-size:13px; line-height:1.6; color:var(--color-ink-2); }
  .desk-aside { display:flex; flex-direction:column; gap:0; padding:0; overflow:hidden; }
  .aside-scroll { flex:1 1 0; min-height:0; overflow:auto; display:grid; align-content:start; gap:10px; padding:10px 10px 0; }
  .aside-feedback { position:sticky; bottom:0; z-index:2; margin:0 -10px; padding:10px 10px 12px; border-top:1px solid var(--color-edge); background:color-mix(in srgb,var(--color-info) 6%,var(--color-canvas)); }
  .no-pr { display:grid; gap:6px; padding:14px; border:1px dashed var(--color-edge); border-radius:10px; }
  .no-pr h3 { display:flex; align-items:center; gap:8px; font-size:13px; font-weight:600; color:var(--color-ink); }
  .no-pr p { font-size:12px; line-height:1.6; color:var(--color-ink-2); }
  @media(max-width:1180px) { .review-split .resizer { display:none; } .review-split { grid-template-columns:minmax(0,1fr); overflow:auto; } .split-left.review-material { overflow:visible; border-right:0; } .split-right { min-height:560px; } }
  @media(max-width:900px) { .desk-head { grid-template-columns:minmax(0,1fr) auto; } .head-tabs { grid-row:2; grid-column:1 / -1; padding-bottom:8px; } }
  .review-aside { display:grid; align-content:start; gap:28px; min-width:0; overflow:auto; padding:24px 20px; border-left:1px solid var(--color-edge); background:var(--color-canvas); }
  .review-input { width:100%; resize:vertical; padding:12px; border:1px solid color-mix(in srgb,var(--color-edge) 70%,transparent); border-radius:8px; background:var(--color-canvas); color:var(--color-ink); font-size:13px; line-height:20px; }
  .review-input:focus { border-color:var(--color-accent); outline:none; }
  .feedback-heading h2 :global(svg) { color:var(--color-info); }
  .empty-review { max-width:48ch; padding-top:24px; }
  @media(max-width:1000px) { .review-material { padding:24px 20px; } }
  @media(max-width:740px) { .review-layout > .resizer { display:none; } .review-layout { display:block; overflow:auto; } .review-main { display:grid; min-height:0; } .review-visuals { height:calc(100dvh - 210px); min-height:500px; } .review-tabs { flex-wrap:wrap; padding-right:8px; gap:4px; } .review-material { overflow:visible; } .review-code { height:70vh; } .review-aside { overflow:visible; border-left:0; border-top:1px solid var(--color-edge); } .review-toolbar { padding:12px 16px; } }
</style>
