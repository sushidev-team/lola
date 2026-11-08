<script lang="ts">
  import { DaemonService } from '@bindings/desktop';
  import { readPRReadiness } from '$lib/prreadiness';
  import { reviewWorkspace } from '$lib/reviewworkspace.svelte';
  import { nav } from '$lib/nav.svelte';
  import type { PRReadinessData } from '@bindings/internal/protocol';
  import type { SessionInfo } from '$lib/store.svelte';
  import { store } from '$lib/store.svelte';
  import { confirm } from '$lib/confirm.svelte';
  import { appLabel } from '$lib/stackapps';
  import { tooltip } from '$lib/tooltip';
  import Icon from '../Icon.svelte';
  import Skeleton from '../Skeleton.svelte';
  import type { IconName } from '$lib/icons';
  import Button from '../Button.svelte';
  import Tabs from '../Tabs.svelte';
  import Select from '../Select.svelte';
  import { untrack } from 'svelte';
  import { slide } from 'svelte/transition';

  let { session, reviewLayout = false }: { session: SessionInfo; reviewLayout?: boolean } = $props();
  const prs = $derived(session.stack
    ? (session.apps ?? []).filter(a => a.number).map(a => ({ id: `${session.id}~${a.name}`, number: a.number!, label: appLabel(a), url: a.url ?? '' }))
    : session.prNumber ? [{ id: session.id, number: session.prNumber, label: 'Pull request', url: session.prUrl ?? '' }] : []);
  let chosen = $state('');
  const selected = $derived(prs.find(p => p.id === chosen) ?? prs.find(p => p.id === `${session.id}~${session.focusApp}`) ?? prs[0]);
  const identity = $derived(selected ? `${selected.id}:${selected.number}` : '');
  let loadedFor = $state('');
  let data = $state<PRReadinessData | null>(null);
  let loading = $state(false);
  let error = $state('');
  let notice = $state('');
  let sending = $state(false);
  let accepted = $state(false);
  let merging = $state(false);
  let mergeRequested = $state(false);
  const mergeMethodKey = 'lola.mergeMethod';
  function readMergeMethod(): string {
    try {
      const value = localStorage.getItem(mergeMethodKey);
      return value === 'squash' || value === 'merge' || value === 'rebase' ? value : '';
    } catch { return ''; }
  }
  let chosenMethod = $state(readMergeMethod());
  function chooseMergeMethod(value: string) {
    chosenMethod = value;
    try { localStorage.setItem(mergeMethodKey, value); } catch { /* best effort */ }
  }
  let viewGeneration = 0;
  let expandedReviewers = $state<string[]>([]);
  let collapsedReviewers = $state<string[]>([]);
  let generation = 0;
  const current = $derived(loadedFor === identity ? data : null);
  const threads = $derived(current?.threads ?? []);
  const reviewerGroups = $derived.by(() => {
    const groups = new Map<string, typeof threads>();
    for (const thread of threads) {
      const author = thread.author || 'Unknown reviewer';
      groups.set(author, [...(groups.get(author) ?? []), thread]);
    }
    return Array.from(groups, ([author, items]) => ({ author, items }));
  });
  const terminal = $derived(current?.state === 'MERGED' || current?.state === 'CLOSED');
  const mergeMethod = $derived(current?.mergeMethod ? chosenMethod || current.mergeMethod : '');
  const mergeLabel = $derived(({squash:'Squash and merge',merge:'Merge pull request',rebase:'Rebase and merge'} as Record<string,string>)[mergeMethod]);
  const readyToMerge = $derived(!!mergeLabel && current?.mergeBlocked === '');
  const canMerge = $derived(!!current && !!mergeLabel && !!current.head && current.state === 'OPEN' && !error && !loading && !merging && !mergeRequested);
  const mergeHint = $derived(!mergeLabel ? 'Restart the daemon to enable merging.' : error ? 'Refresh GitHub status before merging.' : current?.mergeBlocked ? `Merge pull request… ${current.mergeBlocked}` : mergeLabel);
  type Signal = { label: string; value: string; icon: IconName; tone: string };
  const signals = $derived.by((): Signal[] => {
    if (!current) return [];
    return [
      { label: 'CI', value: ({pass:'Passed',fail:'Failed',pending:'Running',none:'No checks'}[current.checks] ?? 'Unknown'), icon: current.checks === 'pass' ? 'check' : current.checks === 'fail' ? 'x' : current.checks === 'pending' ? 'loader-circle' : 'circle', tone: tone(current.checks) },
      { label: 'Review', value: ({APPROVED:'Approved',CHANGES_REQUESTED:'Changes requested',REVIEW_REQUIRED:'Required'}[current.review] ?? 'No review'), icon: current.review === 'APPROVED' ? 'check' : current.review === 'CHANGES_REQUESTED' ? 'circle-alert' : 'shield-check', tone: current.review === 'APPROVED' ? 'good' : current.review === 'CHANGES_REQUESTED' ? 'bad' : 'faint' },
      { label: 'Branch', value: current.mergeable === 'CONFLICTING' ? 'Conflicts' : current.mergeState === 'BEHIND' ? 'Behind base' : current.mergeable === 'MERGEABLE' ? 'No conflicts' : 'Checking', icon: current.mergeable === 'CONFLICTING' ? 'git-compare-arrows' : current.mergeable === 'MERGEABLE' ? 'check' : 'circle', tone: current.mergeable === 'CONFLICTING' ? 'bad' : current.mergeState === 'BEHIND' ? 'warn' : current.mergeable === 'MERGEABLE' ? 'good' : 'faint' },
      { label: 'Comments', value: threads.length ? `${threads.length} unresolved` : 'All resolved', icon: threads.length ? 'message-square-text' : 'check', tone: threads.length ? 'warn' : 'good' },
    ];
  });
  const next = $derived.by(() => {
    if (!current || terminal) return '';
    if (current.draft) return 'Draft · mark ready on GitHub when ready for review.';
    if (current.mergeable === 'CONFLICTING') return 'Resolve branch conflicts before merging.';
    if (current.checks === 'fail') return 'Fix failing CI checks.';
    if (threads.length) return 'Validate and address the open feedback.';
    if (current.review === 'CHANGES_REQUESTED') return 'Address the requested changes and ask for another review.';
    if (current.checks === 'pending') return '';
    if (current.mergeState === 'BEHIND') return 'Update the branch with the latest base.';
    if (current.review === 'REVIEW_REQUIRED') return 'Request the required review.';
    if (current.mergeState === 'BLOCKED') return 'GitHub has additional merge requirements.';
    if (current.mergeState === 'CLEAN') return current.mergeBlocked || 'No blockers reported.';
    return 'Confirm remaining merge requirements on GitHub.';
  });
  function tone(value: string) { return value === 'pass' ? 'good' : value === 'fail' ? 'bad' : value === 'pending' ? 'info' : 'faint'; }
  function open(url: string) {
    try { const u = new URL(url); if (u.protocol === 'https:' && u.hostname === 'github.com') store.openURL(u.href); } catch { /* invalid API URL: don't navigate */ }
  }
  async function refresh() {
    if (!selected) return;
    const target = selected, key = identity, version = ++generation;
    loading = true;
    try {
      const result = await readPRReadiness(target.id, target.number);
      if (version !== generation || key !== identity) return;
      data = result; loadedFor = key; error = '';
    } catch (e) { if (version === generation && key === identity) error = String(e); }
    finally { if (version === generation) loading = false; }
  }
  $effect(() => {
    const key = identity;
    if (!key) return;
    expandedReviewers = []; collapsedReviewers = []; notice = ''; error = ''; data = null; loadedFor = ''; accepted = false; mergeRequested = false;
    untrack(() => void refresh());
    const timer = setInterval(() => { if (document.visibilityState !== 'hidden') void refresh(); }, 60000);
    return () => { clearInterval(timer); generation++; viewGeneration++; };
  });
  async function address() {
    if (!selected || sending) return;
    const target = selected, key = identity;
    sending = true; notice = '';
    try {
      await DaemonService.AddressPRFeedback(target.id, target.number);
      if (key === identity) accepted = true;
    } catch (e) { if (key === identity) notice = String(e); }
    finally { sending = false; }
  }
  function askMerge() {
    if (!selected || !current || !canMerge) return;
    const target = selected, key = identity, view = viewGeneration;
    const { repo, head } = current;
    const method = mergeMethod;
    if (readyToMerge) {
      void merge(target, key, view, repo, head, method, false);
      return;
    }
    confirm.ask({
      title: `Merge PR #${target.number} anyway?`,
      body: current.mergeBlocked,
      detail: `${mergeLabel} ${repo}#${target.number}? GitHub will still enforce branch protections.`,
      confirmLabel: mergeLabel,
      onConfirm: () => {
        if (view !== viewGeneration || key !== identity || !canMerge || current?.head !== head || current?.repo !== repo || mergeMethod !== method) return;
        void merge(target, key, view, repo, head, method, true);
      },
    });
  }
  async function merge(target: {id:string;number:number}, key: string, view: number, repo: string, head: string, method: string, confirmUnready: boolean) {
    merging = true; notice = '';
    try {
      await DaemonService.MergePR(target.id, target.number, repo, head, method, confirmUnready);
      if (view !== viewGeneration || key !== identity) return;
      mergeRequested = true;
      notice = 'Merge requested. GitHub status will update shortly.';
    } catch (e) {
      if (view === viewGeneration && key === identity) notice = `Could not merge: ${String(e)}`;
    } finally {
      if (view === viewGeneration && key === identity) await refresh();
      merging = false;
    }
  }
  function disclose(node: Element) {
    return slide(node, { duration: typeof node.animate !== 'function' || window.matchMedia('(prefers-reduced-motion: reduce)').matches ? 0 : 160 });
  }
</script>

{#if selected}
<section data-before-merge class:review-layout={reviewLayout} aria-label="PR status">
  <header>
    <h3 class="cv-overview-heading">PR status</h3>
    {#if nav.overlay !== 'pr-review'}<Button size="xs" onclick={() => reviewWorkspace.open(session.id)}>Review PR</Button>{/if}
    <button type="button" class="pr-link" onclick={() => open(current?.url || selected.url)} aria-label="Open pull request #{selected.number}"><Icon name="git-pull-request" size={13} />#{selected.number}<Icon name="external-link" size={11} /></button>
    <button type="button" class="refresh" disabled={loading} onclick={refresh} aria-label="Refresh PR status" use:tooltip={'Refresh PR status'}><Icon name="refresh-cw" size={13} /></button>
  </header>
  {#if prs.length > 1}<Tabs tabs={prs.map(p => ({ id:p.id, label:p.label }))} active={selected.id} onSelect={id => chosen = id} />{/if}
  {#if error}<p class="error text-warn" role="status">{current ? 'Status may be outdated. ' : ''}Could not refresh GitHub. <button type="button" onclick={refresh} title={error}>Retry</button></p>{/if}
  {#if !current && !error}
    <div role="status" aria-label="Loading PR status">
      <span class="sr-only">Loading PR status…</span>
      <div class="signals" aria-hidden="true">
        {#each ['CI','Review','Branch','Comments'] as label}
          <div class="signal"><Skeleton width="15px" height="15px"/><span class="signal-label">{label}</span><span class="signal-value"><Skeleton width="76px"/></span></div>
        {/each}
      </div>
      <p class="next"><Skeleton width="52%" height="18px"/></p>
      {#if session.location !== 'ssh'}<div class="pr-actions loading-actions"><Skeleton height={reviewLayout ? '34px' : '26px'}/><Skeleton height={reviewLayout ? '34px' : '26px'}/></div>{/if}
    </div>
  {:else if terminal}<p class="terminal"><Icon name={current?.state === 'MERGED' ? 'check' : 'circle-minus'} size={15} /><span>{current?.state === 'MERGED' ? 'Merged' : 'Closed without merging'}</span></p>
  {:else if current}
    <div class="signals" aria-label="Merge checks">
      {#each signals as signal}<div class="signal" use:tooltip={`${signal.label}: ${signal.value}`}><Icon name={signal.icon} size={15} class={`text-${signal.tone} ${signal.icon === 'loader-circle' ? 'merge-spinner' : ''}`} /><span class="signal-label">{signal.label}</span><span class="signal-value text-{signal.tone}">{signal.value}</span></div>{/each}
    </div>
    {#if next && next !== 'Validate and address the open feedback.'}<p class="next">{next}</p>{/if}
    {#if threads.length}
      <div class="reviewers">
        {#each reviewerGroups as group (group.author)}
          <section class="reviewer-group" aria-label={`Feedback from ${group.author}`}>
            <h4 class="reviewer-heading"><button type="button" class="reviewer-toggle" aria-expanded={!collapsedReviewers.includes(group.author)} onclick={() => collapsedReviewers = collapsedReviewers.includes(group.author) ? collapsedReviewers.filter(author => author !== group.author) : [...collapsedReviewers, group.author]}><Icon name={collapsedReviewers.includes(group.author) ? 'chevron-right' : 'chevron-down'} size={13} /><span class="reviewer-name">{group.author}</span><span class="count" aria-label={`${group.items.length} unresolved threads`}>{group.items.length}</span></button></h4>
            {#if !collapsedReviewers.includes(group.author)}
            <div transition:disclose>
            <ul class="threads">
              {#each (expandedReviewers.includes(group.author) ? group.items : group.items.slice(0, 3)) as thread (thread.id)}
                <li><button type="button" onclick={() => open(thread.url || current!.url)} aria-label={`Open review thread: ${thread.preview || thread.path}`}>
                  <span class="preview">{thread.preview || 'Open conversation on GitHub'}</span>
                  <span class="thread-context"><span class="path" use:tooltip={thread.path}>{thread.path.split('/').pop()}{thread.line ? `:${thread.line}` : ''}</span>{#if thread.outdated}<span class="outdated">Outdated</span>{/if}<Icon name="external-link" size={10} class="thread-link" /></span>
                </button></li>
              {/each}
            </ul>
            {#if group.items.length > 3}
              <button type="button" class="more" aria-expanded={expandedReviewers.includes(group.author)} onclick={() => expandedReviewers = expandedReviewers.includes(group.author) ? expandedReviewers.filter(author => author !== group.author) : [...expandedReviewers, group.author]}>{expandedReviewers.includes(group.author) ? 'Show fewer' : `Show ${group.items.length - 3} more`}</button>
            {/if}
            </div>
            {/if}
          </section>
        {/each}
      </div>
      {#if session.location !== 'ssh'}
        <div class="feedback-action"><Button variant="secondary" size="xs" disabled={sending || accepted || !!error} onclick={address} title="Ask the agent to validate findings, fix valid issues, and resolve addressed threads after verification"><Icon name="shield-check" size={13} />{sending ? 'Sending…' : accepted ? 'Requested' : 'Ask agent to address'}</Button></div>
      {/if}
    {/if}
    {#if session.location !== 'ssh'}
      <div class="pr-actions">
        <div class="merge-split" class:ready={readyToMerge}>
          <span use:tooltip={mergeHint}><Button class="rounded-r-none!" variant={readyToMerge ? 'primary' : 'secondary'} size="xs" icon={!readyToMerge && !merging && !mergeRequested} disabled={!canMerge} loading={merging} onclick={askMerge} aria-label={merging ? 'Merging…' : mergeRequested ? 'Merge requested' : readyToMerge ? mergeLabel : 'Merge pull request'}>{#if !merging}<Icon name="git-merge" size={13} />{/if}{#if merging}Merging…{:else if mergeRequested}Merge requested{:else if readyToMerge}{mergeLabel}{/if}</Button></span>
          <span class="merge-method" use:tooltip={'Choose merge method'}>
            <Select aria-label="Merge method" minMenuWidth={220} value={mergeMethod} disabled={!canMerge} onchange={(event) => chooseMergeMethod(event.currentTarget.value)}>
              <option value="squash">Squash and merge</option>
              <option value="merge">Create merge commit</option>
              <option value="rebase">Rebase and merge</option>
            </Select>
          </span>
        </div>
        {#if !threads.length && current.review !== 'APPROVED'}<Button variant="secondary" size="xs" onclick={() => store.review(selected.id)}><Icon name="shield-check" size={13} />Request agent review</Button>{/if}
      </div>
      {#if !mergeLabel}<p class="muted">Restart the daemon to enable merging.</p>{/if}
    {/if}
    {#if session.mergeQueue}<p class="muted">Merge queue · position {session.mergeQueue.position}</p>{/if}
    {#if session.overlaps?.length}<p class="overlaps"><Icon name="layers" size={12} /><span>Overlaps · {session.overlaps.map(o => o.issue || o.session).join(', ')}</span></p>{/if}
  {/if}
  {#if notice && !(terminal && mergeRequested)}<p class="notice" role="status">{notice}</p>{/if}
</section>
{/if}

<style>
  [data-before-merge] { container-type:inline-size; }
  header { display:flex;align-items:center;gap:8px;margin-bottom:12px; }
  header h3 { margin-right:auto; }
  .pr-link,.refresh { display:inline-flex;align-items:center;gap:4px;color:var(--color-faint);font-size:12px; }
  .pr-link:hover,.refresh:hover,.more:hover { color:var(--color-accent); }
  .refresh { padding:3px; } .refresh:disabled { opacity:.4; }
  button:focus-visible { outline:2px solid var(--color-accent);outline-offset:3px;border-radius:3px; }
  .signals { display:grid;grid-template-columns:repeat(4,minmax(0,1fr));gap:8px;padding:10px 0;border-top:1px solid var(--color-edge);border-bottom:1px solid var(--color-edge); }
  .signal { min-width:0;display:flex;flex-direction:column;align-items:flex-start;gap:4px; }
  @media (prefers-reduced-motion: no-preference) {
    .signal :global(.merge-spinner) { animation:merge-spin 1.4s linear infinite; }
  }
  @keyframes merge-spin { to { transform:rotate(360deg); } }
  .signal-label { white-space:nowrap;font-size:11px;color:var(--color-ink-2); }
  .signal-value { font-size:10px;line-height:14px;overflow-wrap:normal; }
  @container (max-width: 330px) {
    .signals { grid-template-columns:repeat(2,minmax(0,1fr));gap:12px 16px; }
    .signal { display:grid;grid-template-columns:16px minmax(0,1fr);gap:3px 6px;align-items:center; }
    .signal-value { grid-column:2; }
  }
  .next { margin:10px 0;font-size:12px;line-height:18px;color:var(--color-ink-2); }
  .reviewers { display:grid;gap:16px;margin-top:14px; }
  .reviewer-heading { color:var(--color-ink-2);font-size:11px;line-height:16px;font-weight:500; }
  .reviewer-toggle { display:flex;align-items:center;gap:7px;width:100%;padding:3px 0;text-align:left; }
  .reviewer-toggle:hover { color:var(--color-accent); }
  .reviewer-toggle[aria-expanded="true"] { margin-bottom:3px; }
  .reviewer-name { min-width:0;overflow:hidden;text-overflow:ellipsis;white-space:nowrap; }
  .count { color:var(--color-faint);font-size:10px;font-weight:400;font-variant-numeric:tabular-nums; }
  .threads { margin-left:6px;padding-left:13px;border-left:1px solid var(--color-edge); }
  .threads li + li { margin-top:4px; }
  .threads button { display:block;width:100%;text-align:left;padding:6px 0;border-radius:3px; }
  .threads button:hover .preview { color:var(--color-accent); }
  .threads button:hover :global(.thread-link),.threads button:focus-visible :global(.thread-link) { opacity:1; }
  .thread-context { display:flex;align-items:center;gap:6px;font-size:10px;line-height:15px;color:var(--color-faint);margin-top:4px; }
  .path { min-width:0;overflow:hidden;text-overflow:ellipsis;white-space:nowrap; }
  .outdated { flex-shrink:0;font-size:9px; }
  :global(.thread-link) { flex-shrink:0;opacity:0; }
  .preview { display:-webkit-box;-webkit-box-orient:vertical;-webkit-line-clamp:2;line-clamp:2;overflow:hidden;overflow-wrap:anywhere;font-size:12px;line-height:18px; }
  .more { display:block;font-size:11px;color:var(--color-faint);margin:5px 0 0 20px;padding:3px 0; }
  .feedback-action { margin-top:14px;padding-top:12px;border-top:1px solid color-mix(in srgb,var(--color-edge) 60%,transparent); }
  .loading-actions > :global(.skeleton) { flex:1; }
  .pr-actions { display:flex;flex-wrap:wrap;gap:8px;margin-top:12px; }
  .merge-split { display:inline-flex;align-items:stretch; }
  .merge-method { display:block;width:24px;flex-shrink:0; }
  .merge-method :global(.lola-field) { min-height:24px;height:24px;padding:0;border-radius:0 6px 6px 0;color:transparent; }
  .merge-method :global(.lola-select-caret) { right:5px;color:var(--color-ink); }
  .merge-split.ready .merge-method :global(.lola-field) { background:var(--color-accent-fill);border-color:transparent;border-left-color:var(--color-edge); }
  .merge-split.ready .merge-method :global(.lola-field:enabled:hover) { background:var(--color-accent-fill-hover); }
  .merge-split.ready .merge-method :global(.lola-select-caret) { color:var(--color-accent-ink); }
  .overlaps { display:flex;align-items:center;gap:6px;margin-top:12px;font-size:10px;line-height:16px;color:var(--color-faint); }
  .muted,.notice,.error { font-size:11px;line-height:17px;margin-top:8px; } .muted,.notice { color:var(--color-faint); }
  .error button { text-decoration:underline; } .terminal { display:flex;align-items:center;gap:7px;color:var(--color-faint);font-size:12px; }
  .review-layout .signals { grid-template-columns:minmax(0,1fr); gap:14px; padding:6px 0 16px; border:0; }
  .review-layout .signal { display:grid; grid-template-columns:18px minmax(0,1fr) auto; gap:8px; align-items:center; }
  .review-layout .signal-label,.review-layout .signal-value { font-size:12px; line-height:18px; }
  .review-layout .signal-value { grid-column:auto; text-align:right; }
  .review-layout .next { margin:0 0 16px; font-size:12px; }
  .review-layout .pr-actions { display:grid; gap:10px; }
  .review-layout .merge-split { display:flex; }
  .review-layout .merge-split > span:first-child { flex:1; min-width:0; }
  .review-layout .pr-actions :global(button) { min-height:34px; width:100%; font-size:12px; }
  .review-layout .merge-split.ready :global(button) { background:var(--color-accent); color:var(--color-on-accent); }
  .review-layout .merge-split.ready :global(button:enabled:hover) { background:color-mix(in srgb,var(--color-accent) 85%,var(--color-ink)); }
  .review-layout .merge-method { width:32px; }
  .review-layout .merge-method :global(.lola-field) { height:34px; }
  .review-layout .merge-split.ready .merge-method :global(.lola-field) { background:var(--color-accent); border-left-color:color-mix(in srgb,var(--color-on-accent) 20%,transparent); }
  .review-layout .merge-split.ready .merge-method :global(.lola-field:enabled:hover) { background:color-mix(in srgb,var(--color-accent) 85%,var(--color-ink)); }
  .review-layout .merge-split.ready .merge-method :global(.lola-select-caret) { color:var(--color-on-accent); }
  .review-layout .merge-method :global(.lola-select-caret) { right:9px; }
  .review-layout .overlaps { font-size:12px; }
</style>
