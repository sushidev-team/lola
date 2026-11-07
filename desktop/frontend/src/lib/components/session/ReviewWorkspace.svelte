<script lang="ts">
  import { DaemonService } from '@bindings/desktop';
  import type { ReviewPackage as Package, PRReadinessData } from '@bindings/internal/protocol';
  import { store } from '$lib/store.svelte';
  import { nav } from '$lib/nav.svelte';
  import { feedback } from '$lib/feedback.svelte';
  import { feed } from '$lib/feed.svelte';
  import { ssh } from '$lib/ssh.svelte';
  import { sessionActivity } from '$lib/sessionactivity';
  import Modal from '../Modal.svelte';
  import Button from '../Button.svelte';
  import Select from '../Select.svelte';
  import Tabs from '../Tabs.svelte';
  import DiffView from '../DiffView.svelte';
  import SSHRich from '../ssh/SSHRich.svelte';
  import BeforeMerge from './BeforeMerge.svelte';
  import OverviewChecks from './OverviewChecks.svelte';
  import ReviewScreenshot from './ReviewScreenshot.svelte';

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
  function chooseSubject(value: string) {
    subject = value;
    editDraft(draft);
  }
  function addComment() {
    if (!current || !draft.trim()) return;
    const screen = current.screens?.find((s,i) => subject === `Screen ${i+1}: ${s.title}`);
    const images = screen ? `Screenshots: before ${screen.before || 'unavailable'}, after ${screen.after}\n` : '';
    const context = `Review package ${current.id}\nRepository: ${current.repo || 'unspecified'}${current.pr ? ` PR #${current.pr}` : ''}\nBase: ${current.base}\nHead: ${current.head}\nSection: ${subject}\n${images}`;
    const combined = [notes, context + draft.trim()].filter(Boolean).join('\n\n');
    // The existing feedback renderer caps a general note at 4000 runes.
    if ([...combined].length > 4000) { notice = 'This batch is full. Send it before adding another comment.'; return; }
    feedback.setNote(sessionId, combined);
    editDraft(''); notice = 'Comment added to review feedback.';
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
      <p class="text-xs text-faint">Inspect the material, compare the code, then send your feedback.</p>
    </div>
    <div class="flex min-w-0 flex-wrap items-center gap-2">
      {#if lanes.length}<Select aria-label="Review repository" value={lane?.id} onchange={e => app = e.currentTarget.value}>{#each lanes as a}<option value={a.id}>{a.label}</option>{/each}</Select>{/if}
      {#if packages.length}<Select aria-label="Review version" value={current?.id} onchange={e => selected = e.currentTarget.value}>{#each packages as p,i}<option value={p.id}>{i === 0 ? 'Latest · ' : ''}{p.repo} · {p.head.slice(0,8)} · {new Date(p.createdAt).toLocaleString()}</option>{/each}</Select>{/if}
      <Button size="sm" loading={loading} onclick={refresh}>Refresh review</Button>
    </div>
  </div>
  <div class="review-layout">
    <div class="review-main">
      <Tabs tabs={session ? [{id:'material',label:'Review material'},{id:'code',label:'Code changes'}] : [{id:'material',label:'Review material'}]} active={session ? tab : 'material'} onSelect={id => tab = id} />
      {#if tab === 'code' && session}
        <div class="review-code grid min-h-0 grid-rows-[auto_minmax(0,1fr)]"><p class="px-4 py-2 text-xs text-faint">Live checkout changes. These may differ from the retained review revision.</p><DiffView {sessionId} /></div>
      {:else}
        <div class="review-material">
          {#if error}<p role="alert" class="mb-5 text-warn">Could not load review material. {error} Try Refresh review; an older daemon may need restarting.</p>{/if}
          {#if current}
            {#if current.id !== packages[0]?.id}<p class="mb-5 text-sm text-info">A newer review package is available. <Button size="xs" onclick={() => selected = packages[0].id}>View latest package</Button></p>{/if}
            <header class="mb-7">
              <h1 class="text-xl font-semibold text-ink">{current.title}</h1>
              <p class="mt-2 text-sm text-ink-2">Agent-provided write-up and screenshots · {new Date(current.createdAt).toLocaleString()}</p>
              <p class="mt-2 text-sm {freshness === 'stale' ? 'text-warn' : 'text-faint'}" role="status">{freshness === 'stale' ? 'Outdated material — the current PR differs from this package.' : freshness === 'current' ? 'Package head matches the current PR. Captures and claims are agent-provided.' : 'Freshness unknown — the current PR head could not be confirmed.'}</p>
              <p class="mt-2 break-all font-mono text-xs text-faint">{current.base.slice(0,12)} → {current.head.slice(0,12)}</p>
            </header>
            <article class="review-writeup selectable" aria-label="Agent review write-up"><SSHRich {sessionId} text={current.body} /></article>
            {#each current.screens ?? [] as screen,i (`${current.id}:${i}`)}
              <section class="review-screen" aria-label={screen.title}>
                <div class="mb-3 flex flex-wrap items-center justify-between gap-2"><h2 class="text-lg text-ink">{screen.title}</h2><Button size="xs" onclick={() => { chooseSubject(`Screen ${i+1}: ${screen.title}`); document.getElementById('review-comment')?.focus(); }}>Comment on screen</Button></div>
                {#if screen.caption}<p class="mb-3 selectable text-prose text-ink-2">{screen.caption}</p>{/if}
                {#if screen.viewport || screen.url}<p class="mb-3 break-words text-xs text-faint">{[screen.viewport,screen.url].filter(Boolean).join(' · ')}</p>{/if}
                <div class="review-pair">
                  {#if screen.before}<ReviewScreenshot sessionId={current.session} packageId={current.id} artifact={screen.before} label={`${screen.title} — Before`} />{:else}<div class="missing-before"><p>Before capture unavailable</p><p class="mt-1 text-xs">No baseline image was provided.</p></div>{/if}
                  <ReviewScreenshot sessionId={current.session} packageId={current.id} artifact={screen.after} label={`${screen.title} — After`} />
                </div>
              </section>
            {/each}
          {:else if loading}<p role="status" class="text-faint">Loading review material…</p>
          {:else if !error}<div class="empty-review"><h1 class="text-xl font-semibold text-ink">No review material yet</h1><p class="mt-3 text-prose text-ink-2">Ask the agent to publish what changed, how it was verified, and before/after screenshots for visual changes.</p><p class="mt-4 break-words font-mono text-sm text-faint">lola review-package publish manifest.json</p><p class="mt-3 text-sm text-faint">Code changes and PR checks are available without a package.</p></div>{/if}
        </div>
      {/if}
    </div>
    <aside class="review-aside" aria-label="Review actions">
      {#if reviewSession}<BeforeMerge session={reviewSession} />{/if}
      {#if session}<OverviewChecks {session} {tests} onJump={jump} />{/if}
      <section>
        <h2 class="cv-overview-heading mb-3">Review feedback</h2>
        {#if !session}<p class="text-sm text-faint">This session has ended. Its review material is retained; sending feedback and live checkout actions are unavailable.</p>{/if}
        {#if current && session}
          <label for="review-subject" class="mb-1 block text-sm text-ink-2">Comment on</label>
          <Select id="review-subject" aria-label="Comment on" value={subject} onchange={e => chooseSubject(e.currentTarget.value)}><option value="Write-up">Write-up</option>{#each current.screens ?? [] as screen,i}<option value={`Screen ${i+1}: ${screen.title}`}>{screen.title}</option>{/each}</Select>
          <textarea id="review-comment" aria-label="Review comment" class="review-input mt-2" rows="4" maxlength="3000" value={draft} oninput={e => editDraft(e.currentTarget.value)} placeholder="What needs changing or explaining?"></textarea>
          <Button size="sm" variant="secondary" class="mt-2" disabled={!draft.trim()} onclick={addComment}>Add to feedback</Button>
        {/if}
        {#if count}
          <p class="mt-4 text-sm text-ink-2">{feedback.draftsFor(sessionId).length} code comments{notes ? ' · Material feedback included' : ''}</p>
          {#if notes}<details class="mt-2"><summary class="cursor-pointer text-sm text-ink-2">Inspect feedback draft</summary><textarea aria-label="Feedback draft" class="review-input mt-2" rows="8" maxlength="4000" value={notes} oninput={e => feedback.setNote(sessionId,e.currentTarget.value)}></textarea></details>{/if}
          <Button size="sm" variant="primary" class="mt-3" loading={feedback.isSending(sessionId)} disabled={!session} onclick={send}>Send feedback to agent</Button>
        {:else}<p class="mt-3 text-sm text-faint">Comments collect here before you send them. Code comments share the same batch.</p>{/if}
        {#if notice}<p role="status" class="mt-3 text-sm text-ink-2">{notice}</p>{/if}
      </section>
    </aside>
  </div>
</Modal>

<style>
  .review-toolbar { display:flex; flex-wrap:wrap; align-items:center; justify-content:space-between; gap:12px; padding:14px 24px; border-bottom:1px solid var(--color-edge); }
  .review-layout { display:grid; grid-template-columns:minmax(0,1fr) 330px; min-height:0; }
  .review-main { display:grid; grid-template-rows:auto minmax(0,1fr); min-width:0; min-height:0; }
  .review-material { overflow:auto; padding:28px 32px 64px; }
  .review-writeup { max-width:72ch; font-size:14px; line-height:22px; }
  .review-screen { margin-top:36px; padding-top:24px; border-top:1px solid var(--color-edge); }
  .review-pair { display:grid; grid-template-columns:repeat(2,minmax(0,1fr)); gap:20px; }
  .missing-before { align-self:center; padding:32px 16px; text-align:center; color:var(--color-faint); }
  .review-aside { display:grid; align-content:start; gap:28px; min-width:0; overflow:auto; padding:24px 20px; border-left:1px solid var(--color-edge); background:var(--color-canvas); }
  .review-input { width:100%; resize:vertical; padding:10px; border:1px solid var(--color-edge); border-radius:6px; background:var(--color-panel); color:var(--color-ink); font-size:13px; line-height:20px; }
  .review-input:focus-visible { outline:2px solid var(--color-accent); outline-offset:2px; }
  .empty-review { max-width:60ch; padding-top:24px; }
  @media(max-width:1000px) { .review-pair { grid-template-columns:minmax(0,1fr); } .review-material { padding:24px 20px; } }
  @media(max-width:740px) { .review-layout { display:block; overflow:auto; } .review-main { display:block; min-height:0; } .review-material { overflow:visible; } .review-code { height:70vh; } .review-aside { overflow:visible; border-left:0; border-top:1px solid var(--color-edge); } .review-toolbar { padding:12px 16px; } }
</style>
