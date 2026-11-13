<script lang="ts">
  import { onDestroy, untrack } from 'svelte';
  import { createAttachmentKey, fromAction } from 'svelte/attachments';
  import { tooltip } from '$lib/tooltip';
  import { disclosureSlide } from '$lib/disclosureMotion';
  import GitFilePath from './GitFilePath.svelte';
  import Button from './Button.svelte';
  import Skeleton from './Skeleton.svelte';
  import Checkbox from './Checkbox.svelte';
  import Icon from './Icon.svelte';
  import { confirm } from '$lib/confirm.svelte';
  import type { ProjectGitState } from '$lib/projectgit.svelte';
  import { folderFirstFiles, stagedFiles, unstagedFiles, type GitArea, type GitFile } from '$lib/projectgit';

  let { git, path = '', area = '', ref = '', limit = 200, oninspect, onact, oncommitted, oncontextfile }: {
    git: ProjectGitState; path?: string; area?: GitArea | ''; ref?: string; limit?: number;
    oninspect: (file: GitFile, staged: boolean) => void;
    onact: (action: 'stage' | 'unstage', paths: string[]) => void;
    oncommitted: () => void;
    oncontextfile?: (file: GitFile, staged: boolean, paths: string[], event: MouseEvent) => void;
  } = $props();
  const id = $props.id();
  let mounted = true;
  onDestroy(() => { mounted = false; });
  const data = $derived(git.snapshot);
  const initialLoading = $derived(!data && !git.error);
  const staged = $derived(folderFirstFiles(stagedFiles(data?.files ?? [])));
  const unstaged = $derived(folderFirstFiles(unstagedFiles(data?.files ?? [])));
  const conflicts = $derived((data?.files ?? []).some(file => file.conflict));
  const amendStale = $derived(git.amend && !!data && git.amendHead !== data.head);
  const canCommit = $derived(staged.length > 0 && !!git.subject.trim() && !conflicts && !git.busy && !git.draftingMessage && !data?.operation && !git.amendLoading && (!git.amend || (!!git.amendHead && !!data?.head && !amendStale)));
  const groups = $derived([{ title: 'Unstaged', entries: unstaged, staged: false }, { title: 'Staged', entries: staged, staged: true }]);
  let tree = $state(readTree());
  let collapsed = $state(new Set<string>());
  type GroupKey = 'staged' | 'unstaged';
  let selections = $state<Record<GroupKey, string[]>>({ staged: [], unstaged: [] });
  let anchors = $state<Record<GroupKey, string>>({ staged: '', unstaged: '' });
  let selectionOwner: ProjectGitState | undefined;
  const visibleStaged = $derived(tree ? visibleTree(staged, true) : staged);
  const visibleUnstaged = $derived(tree ? visibleTree(unstaged, false) : unstaged);
  const signingLabel = $derived(data?.signing?.enabled ? `${data.signing.format === 'ssh' ? 'SSH' : data.signing.format === 'x509' ? 'X.509' : 'GPG'} signing enabled` : 'Using your Git configuration');
  const signingTooltipKey = createAttachmentKey();
  const signingTooltip = { [signingTooltipKey]: fromAction(tooltip, () => ({ heading: signingLabel, text: 'Commits use your installed Git and repository signing configuration.', meta: data?.signing?.key ? `Signing key: ${data.signing.key}` : undefined, delay: 500 })) };
  $effect(() => {
    const state = git, currentStaged = staged, currentUnstaged = unstaged;
    untrack(() => {
      if (selectionOwner !== state) {
        selectionOwner = state; selections = { staged: [], unstaged: [] }; anchors = { staged: '', unstaged: '' };
        return;
      }
      for (const [key, entries] of [['staged', currentStaged], ['unstaged', currentUnstaged]] as const) {
        const paths = new Set(entries.map(file => file.path));
        selections[key] = selections[key].filter(path => paths.has(path));
        if (!paths.has(anchors[key])) anchors[key] = '';
      }
    });
  });
  function groupKey(staged: boolean): GroupKey { return staged ? 'staged' : 'unstaged'; }
  function selectedPaths(staged: boolean) { return selections[groupKey(staged)]; }
  function visibleTree(entries: GitFile[], staged: boolean, prefix = ''): GitFile[] {
    return folders(entries, prefix).flatMap(folder => {
      const path = prefix + folder + '/';
      return collapsed.has(`${staged}:${path}`) ? [] : visibleTree(entries.filter(file => file.path.startsWith(path)), staged, path);
    }).concat(entries.filter(file => !file.path.slice(prefix.length).includes('/')));
  }
  function selectFile(entry: GitFile, staged: boolean, event: MouseEvent | KeyboardEvent) {
    const key = groupKey(staged), order = staged ? visibleStaged : visibleUnstaged;
    const additive = event.metaKey || event.ctrlKey;
    if (event.shiftKey) {
      const start = order.findIndex(file => file.path === anchors[key]), end = order.findIndex(file => file.path === entry.path);
      const range = start < 0 || end < 0 ? [entry.path] : order.slice(Math.min(start, end), Math.max(start, end) + 1).map(file => file.path);
      selections[key] = additive ? [...new Set([...selections[key], ...range])] : range;
      if (start < 0) anchors[key] = entry.path;
    } else if (additive) {
      selections[key] = selections[key].includes(entry.path) ? selections[key].filter(path => path !== entry.path) : [...selections[key], entry.path];
      anchors[key] = entry.path;
    } else {
      selections[key] = [entry.path]; anchors[key] = entry.path;
      oninspect(entry, staged);
    }
  }
  function fileKey(event: KeyboardEvent, entry: GitFile, staged: boolean) {
    if (oncontextfile && (event.key === 'ContextMenu' || (event.shiftKey && event.key === 'F10'))) {
      event.preventDefault(); event.stopPropagation();
      const rect = (event.currentTarget as HTMLElement).getBoundingClientRect();
      (event.currentTarget as HTMLElement).dispatchEvent(new MouseEvent('contextmenu',{bubbles:true,clientX:rect.left+12,clientY:rect.bottom,cancelable:true}));
      return;
    }
    const key = groupKey(staged), order = staged ? visibleStaged : visibleUnstaged;
    if (event.key === 'Escape') { event.preventDefault(); event.stopPropagation(); selections[key] = []; anchors[key] = ''; return; }
    if ((event.key === ' ' || event.key === 'Enter') && (event.shiftKey || event.metaKey || event.ctrlKey)) { event.preventDefault(); event.stopPropagation(); selectFile(entry, staged, event); return; }
    if (!['ArrowDown', 'ArrowUp', 'Home', 'End'].includes(event.key)) return;
    const current = order.findIndex(file => file.path === entry.path);
    const next = event.key === 'Home' ? 0 : event.key === 'End' ? order.length - 1 : Math.max(0, Math.min(order.length - 1, current + (event.key === 'ArrowDown' ? 1 : -1)));
    if (!order[next]) return;
    event.preventDefault(); event.stopPropagation(); selectFile(order[next], staged, event);
    const root = (event.currentTarget as HTMLElement).closest('.stage-scroll');
    const target = [...(root?.querySelectorAll<HTMLButtonElement>('[data-file-path]') ?? [])].find(button => button.dataset.filePath === order[next].path);
    target?.focus(); target?.scrollIntoView?.({block:'nearest'});
  }
  function actionPaths(entry: GitFile, staged: boolean) { const selection = selectedPaths(staged); return selection.includes(entry.path) ? selection : [entry.path]; }
  function fileContext(event: MouseEvent, entry: GitFile, staged: boolean) {
    if (!oncontextfile) return;
    event.preventDefault(); event.stopPropagation();
    if (!selectedPaths(staged).includes(entry.path)) {selections[groupKey(staged)]=[entry.path];anchors[groupKey(staged)]=entry.path;}
    oncontextfile(entry,staged,actionPaths(entry,staged),event);
  }

  function readTree() { try { return localStorage.getItem('lola.git.files.tree') === '1'; } catch { return false; } }
  function setTree(value: boolean) { tree = value; try { localStorage.setItem('lola.git.files.tree', value ? '1' : '0'); } catch { /* retain in-memory choice */ } }
  function toggle(key: string) { const next = new Set(collapsed); if (next.has(key)) next.delete(key); else next.add(key); collapsed = next; }
  function folders(entries: GitFile[], prefix: string) { return [...new Set(entries.map(file => file.path.slice(prefix.length)).filter(path => path.includes('/')).map(path => path.slice(0, path.indexOf('/'))))].sort((a,b) => a.localeCompare(b)); }
  function status(file: GitFile, staged: boolean) { return file.conflict ? '!' : file.untracked ? '+' : (staged ? file.index : file.worktree).trim() || 'M'; }
  async function discardAll() {
    if (git.busy || !unstaged.length || conflicts || data?.operation) return;
    const state = git, historyRef = ref, count = limit, branch = data?.branch || 'detached HEAD';
    const plan = await state.prepareDiscard();
    if (!plan || !mounted || state !== git) return;
    const total = plan.tracked.length + plan.untracked.length;
    if (!total) { await state.load(historyRef, count); return; }
    confirm.ask({
      title: 'Discard all unstaged changes?',
      body: `Discard changes in ${total} ${total === 1 ? 'file' : 'files'} on ${branch}?`,
      detail: [plan.tracked.length ? `Restore ${plan.tracked.length} tracked ${plan.tracked.length === 1 ? 'file' : 'files'} to the staged version.` : '', plan.untracked.length ? `Permanently delete ${plan.untracked.length} untracked ${plan.untracked.length === 1 ? 'file' : 'files'}.` : '', 'Staged changes and ignored files are kept. This cannot be undone.'].filter(Boolean).join(' '),
      confirmLabel: 'Discard changes',
      onConfirm: () => {
        if (!mounted || state !== git) return;
        void state.discardUnstaged(plan.token, historyRef, count);
      },
    });
  }
  async function commit(state: ProjectGitState, amend: boolean, head: string, branch: string, count: number) {
    const ok = await state.act(amend ? 'amend' : 'commit', [], branch, count, head);
    if (ok && state === git) oncommitted();
  }
  async function submit(event: SubmitEvent) {
    event.preventDefault();
    if (!canCommit) return;
    const state = git, head = git.amendHead, message = git.message, branch = ref, count = limit;
    if (!state.amend) { void commit(state, false, '', branch, count); return; }
    const valid = () => mounted && state === git && state.message === message && state.amend && state.amendHead === head && state.snapshot?.head === head;
    const proceed = () => {
      if (!valid()) { state.error = 'The checkout or commit draft changed. Review it and try again.'; return; }
      void commit(state, true, head, branch, count);
    };
    const published = await state.amendNeedsConfirmation(head);
    if (published === null) return;
    if (!valid()) { state.error = 'The checkout or commit draft changed. Review it and try again.'; return; }
    if (!published) { proceed(); return; }
    confirm.ask({
      title: 'Amend pushed commit?',
      body: `Commit ${head.slice(0, 8)} is already on a remote branch. Amending rewrites shared history.`,
      confirmLabel: 'Amend commit',
      confirmVariant: 'primary',
      onConfirm: proceed,
    });
  }
</script>

{#snippet fileRow(entry: GitFile, staged: boolean, depth: number)}
  {@const selection = selectedPaths(staged)}
  {@const targets = actionPaths(entry, staged)}
  <div class="stage-file" style={`padding-left:${depth * 12}px`}>
    <Button block selected={selection.includes(entry.path)} aria-pressed={selection.includes(entry.path)} class="stage-file-main" data-file-path={entry.path} title={entry.oldPath ? `${entry.oldPath} → ${entry.path}` : entry.path} aria-label={`Inspect ${staged ? 'staged' : 'unstaged'} ${entry.path}`} onclick={(event) => selectFile(entry, staged, event)} onkeydown={(event) => fileKey(event, entry, staged)} oncontextmenu={(event)=>fileContext(event,entry,staged)}>
      <span class={`file-status ${entry.conflict ? 'text-bad' : entry.untracked ? 'text-good' : 'text-orange'}`}>{status(entry, staged)}</span>
      <GitFilePath path={entry.path} leafOnly={tree}/>
    </Button>
    <Button size="xs" class={staged ? 'stage-action unstage-action' : 'stage-action'} disabled={!!git.busy} aria-label={targets.length > 1 ? `${staged ? 'Unstage' : 'Stage'} ${targets.length} selected files` : `${staged ? 'Unstage' : 'Stage'} ${entry.path}`} onclick={() => onact(staged ? 'unstage' : 'stage', targets)}>{staged ? 'Unstage' : 'Stage'} {targets.length > 1 ? `${targets.length} files` : 'file'}</Button>
  </div>
{/snippet}

{#snippet fileTree(entries: GitFile[], staged: boolean, prefix = '', depth = 0)}
  {#each folders(entries, prefix) as folder (folder)}
    {@const folderPath = prefix + folder + '/'}
    {@const key = `${staged}:${folderPath}`}
    {@const children = entries.filter(file => file.path.startsWith(folderPath))}
    {@const expanded = !collapsed.has(key)}
    <Button block class="file-folder" style={`padding-left:${5 + depth * 12}px`} aria-expanded={expanded} aria-label={`${staged ? 'Staged' : 'Unstaged'} folder ${folderPath.slice(0,-1)}`} onclick={() => toggle(key)}><Icon name="chevron-down" class={expanded ? 'folder-chevron' : 'folder-chevron collapsed'} size={12} /><Icon name="folder" size={14} /><span>{folder}</span><small>{children.length}</small></Button>
    {#if expanded}<div transition:disclosureSlide>{@render fileTree(children, staged, folderPath, depth + 1)}</div>{/if}
  {/each}
  {#each entries.filter(file => !file.path.slice(prefix.length).includes('/')) as entry (entry.path)}{@render fileRow(entry, staged, depth)}{/each}
{/snippet}

<div class="staging-panel">
  <div class="inspector-heading"><h3>Changes <span>{#if initialLoading}<Skeleton width="16px" height="12px"/>{:else}{data?.files.length ?? '—'}{/if}</span></h3><div class="view-switch" role="group" aria-label="Changed file layout"><Button size="xs" icon selected={!tree} aria-label="File list view" title="File list" onclick={() => setTree(false)}><Icon name="list" size={14} /></Button><Button size="xs" icon selected={tree} aria-label="File tree view" title="File tree" onclick={() => setTree(true)}><Icon name="folder" size={14} /></Button></div><Button size="xs" icon variant="danger" class="discard-all" aria-label="Discard all unstaged changes" title={data?.operation || conflicts ? 'Finish the current operation before discarding changes' : 'Discard all unstaged changes…'} disabled={!unstaged.length || !!git.busy || !!data?.operation || conflicts} loading={git.busy === 'prepare-discard' || git.busy === 'discard'} onclick={() => void discardAll()}><Icon name="trash-2" size={14} /></Button></div>
  <div class="staging-files">
    {#each groups as group (group.title)}
      <section class="stage-group" aria-label={`${group.title} files`}>
        <div class="stage-heading"><h4>{group.title}<span>{#if initialLoading}<Skeleton width="16px" height="12px"/>{:else}{data ? group.entries.length : '—'}{/if}</span></h4><div class="stage-group-actions">{#if selectedPaths(group.staged).length}<Button size="xs" class={group.staged ? 'unstage-selected' : 'stage-selected'} disabled={!!git.busy} aria-label={`${group.staged ? 'Unstage' : 'Stage'} ${selectedPaths(group.staged).length} selected files`} onclick={() => onact(group.staged ? 'unstage' : 'stage', selectedPaths(group.staged))}>{group.staged ? 'Unstage' : 'Stage'} {selectedPaths(group.staged).length}</Button>{/if}<Button size="xs" class={group.staged ? 'unstage-all' : 'stage-all'} disabled={!group.entries.length || !!git.busy} onclick={() => onact(group.staged ? 'unstage' : 'stage', group.entries.map(file => file.path))}>{group.staged ? 'Unstage all' : 'Stage all'}</Button></div></div>
        <div class="stage-scroll">
          {#if initialLoading}<div class="files-skeleton" aria-hidden="true">{#each Array(group.staged ? 3 : 7) as _, index}<div class="file-placeholder"><Skeleton width="12px" height="12px"/><Skeleton width={`${[85,66,76,58][index % 4]}%`} height="12px"/></div>{/each}</div>{/if}
          {#if tree}{@render fileTree(group.entries, group.staged)}{:else}{#each group.entries as entry (entry.path)}{@render fileRow(entry, group.staged, 0)}{/each}{/if}
          {#if data && !group.entries.length}<p class="stage-empty">{group.staged ? 'Stage files to include in your commit.' : 'No unstaged changes.'}</p>{/if}
        </div>
      </section>
    {/each}
  </div>
  <form class="commit-composer" onsubmit={submit}>
    <div class="composer-heading"><label for={`${id}-subject`}>Commit message</label><Button size="xs" variant="accent" loading={git.draftingMessage} disabled={!data || !staged.length || conflicts || !!data.operation || !!git.busy || git.draftingMessage} title="Draft a message from staged changes using Claude Haiku" aria-label="Generate commit message with AI" onclick={() => void git.draftCommitMessage()}><Icon name="sparkles" size={13}/>{git.draftingMessage ? 'Drafting…' : 'AI draft'}</Button></div>
    {#if git.draftError}<p class="draft-error" role="alert">{git.draftError}</p>{/if}
    <input id={`${id}-subject`} aria-label="Commit subject" placeholder={git.amendLoading ? 'Loading last commit…' : 'Summary'} bind:value={git.subject} disabled={!!git.busy || !data} maxlength="1000" />
    <textarea aria-label="Commit body" placeholder="Description (optional)" rows="3" bind:value={git.body} disabled={!!git.busy || !data} maxlength="19000"></textarea>
    <div class="commit-options"><label class="amend-option"><Checkbox checked={git.amend} onchange={(event) => void git.setAmend(event.currentTarget.checked)} disabled={!!git.busy || !!data?.operation || !data?.head} /><span>Amend last commit</span>{#if git.amend && git.amendHead}<code>{git.amendHead.slice(0, 8)}</code>{/if}</label><Button size="xs" icon class="signing-icon" aria-label={signingLabel} {...signingTooltip}><Icon name={data?.signing?.enabled ? 'shield-check' : 'info'} size={14} /></Button></div>
    {#if amendStale}<p class="conflict-note" role="alert">HEAD changed since you selected Amend. Turn Amend off, review the last commit, then enable it again.</p>{/if}
    <Button block variant="primary" loading={git.busy === 'commit' || git.busy === 'amend' || git.busy === 'check-amend'} disabled={!canCommit} type="submit"><Icon name="check" size={14} />{git.amend ? 'Amend' : 'Commit'} {staged.length ? `${staged.length} ${staged.length === 1 ? 'file' : 'files'}` : 'staged changes'}</Button>
  </form>
</div>

<style>
  .staging-panel { grid-row:1 / -1; display:grid; grid-template-rows:auto minmax(140px,1fr) auto; min-height:0; min-width:0; overflow:auto; }
  .inspector-heading { display:grid; grid-template-columns:minmax(0,1fr) auto minmax(0,1fr); align-items:center; min-height:36px; padding:4px 10px; border-bottom:1px solid var(--color-edge); }
  .inspector-heading h3 { display:flex; gap:8px; align-items:center; font-size:14px; font-weight:600; }
  .inspector-heading h3 span { font-size:12px; font-weight:400; color:var(--color-ink-2); }
  .inspector-heading h3 span,.stage-heading h4 span,.amend-option code { font-family:var(--font-mono); font-variant-numeric:tabular-nums; }
  .staging-panel :global(.file-folder > small) { font-family:var(--font-mono); font-variant-numeric:tabular-nums; }
  .view-switch { display:flex; gap:2px; }
  .inspector-heading :global(.discard-all) { justify-self:end; color:var(--color-bad); border:1px solid var(--color-bad); background:color-mix(in srgb,var(--color-bad) 16%,var(--color-canvas)); }
  .inspector-heading :global(.discard-all:enabled:hover) { background:color-mix(in srgb,var(--color-bad) 28%,var(--color-canvas)); }
  .staging-files { display:grid; grid-template-rows:minmax(60px,1fr) minmax(60px,1fr); min-height:0; }
  .stage-group { display:grid; grid-template-rows:auto minmax(0,1fr); min-width:0; min-height:0; }
  .stage-group + .stage-group { border-top:1px solid var(--color-edge); }
  .stage-heading { display:flex; align-items:center; justify-content:space-between; padding:4px 10px; }
  .stage-heading h4 { display:flex; align-items:center; gap:7px; font-size:13px; font-weight:600; }
  .stage-heading h4 span { color:var(--color-ink-2); font-size:12px; font-weight:400; }
  .stage-scroll { overflow:auto; padding:0 5px 5px; min-height:0; }
  .stage-file { position:relative; display:flex; align-items:center; min-width:0; }
  .stage-group-actions { display:flex; gap:2px; align-items:center; }
  .staging-panel :global(.stage-all),.staging-panel :global(.stage-selected),.staging-panel :global(.unstage-all),.staging-panel :global(.unstage-selected),.stage-file :global(.stage-action) { --stage-color:var(--color-good); color:white; border:1px solid var(--stage-color); background:color-mix(in srgb,var(--stage-color) 35%,#14171f); font-weight:600; transition:none; }
  .staging-panel :global(.unstage-all),.staging-panel :global(.unstage-selected),.stage-file :global(.unstage-action) { --stage-color:var(--color-bad); }
  .staging-panel :global(.stage-all:enabled:hover),.staging-panel :global(.stage-selected:enabled:hover),.staging-panel :global(.unstage-all:enabled:hover),.staging-panel :global(.unstage-selected:enabled:hover),.stage-file :global(.stage-action:enabled:hover) { color:white; background:color-mix(in srgb,var(--stage-color) 45%,#14171f); }
  .stage-file :global(.stage-action) { position:absolute; right:0; top:2px; height:24px; opacity:0; pointer-events:none; font-size:12px; font-weight:600; transition:none; }
  .stage-file:hover :global(.stage-action),.stage-file:focus-within :global(.stage-action) { opacity:1; pointer-events:auto; }
  .commit-options { display:flex; align-items:center; gap:6px; }
  .commit-options .amend-option { flex:1; min-width:0; }
  .commit-options :global(.signing-icon) { margin-left:auto; color:var(--color-ink-2); }
  .stage-file :global(.stage-file-main) { transition:none; flex:1; height:28px; gap:7px; text-align:left; min-width:0; padding:3px 5px; font-weight:400; }
  .stage-file :global(.stage-file-main:not([aria-pressed="true"])) { color:var(--color-ink-2); }
  .file-status { width:12px; flex-shrink:0; text-align:center; font-family:var(--font-mono); font-size:12px; }
  :global(.file-folder) { height:28px; gap:5px; font-size:13px; font-weight:400; }
  .staging-panel :global(.file-folder > span) { overflow:hidden; text-overflow:ellipsis; white-space:nowrap; }
  .staging-panel :global(.file-folder > small) { margin-left:auto; font-size:12px; color:var(--color-ink-2); }
  :global(.folder-chevron) { transition:transform 160ms ease-out; }
  :global(.folder-chevron.collapsed) { transform:rotate(-90deg); }
  .file-placeholder { display:flex; align-items:center; gap:7px; height:28px; padding:3px 5px; }
  .file-placeholder > :global(.skeleton:first-child) { flex-shrink:0; }
  .stage-empty { padding:6px 6px 12px; font-size:13px; color:var(--color-ink-2); line-height:1.6; }
  .commit-composer { display:grid; gap:7px; padding:10px; border-top:1px solid var(--color-edge); }
  .composer-heading { display:flex; align-items:center; justify-content:space-between; gap:8px; }
  .draft-error { color:var(--color-bad); font-size:12px; line-height:1.5; }
  .commit-composer label { font-size:13px; font-weight:500; }
  .commit-composer input,.commit-composer textarea { min-width:0; width:100%; border:1px solid var(--color-edge); border-radius:5px; padding:7px 9px; background:var(--color-panel); color:var(--color-ink); font-size:13px; line-height:1.5; outline:none; }
  .commit-composer textarea { resize:vertical; min-height:64px; max-height:140px; }
  .commit-composer input:focus,.commit-composer textarea:focus { border-color:var(--color-accent); }
  .commit-composer input::placeholder,.commit-composer textarea::placeholder { color:var(--color-ink-2); }
  .amend-option { display:flex; align-items:center; gap:7px; cursor:pointer; }
  .amend-option code { margin-left:auto; color:var(--color-ink-2); font-size:12px; font-weight:400; }
  .conflict-note { color:var(--color-warn); font-size:12px; }
  @media (prefers-reduced-motion: reduce) { :global(.folder-chevron),.stage-file :global(.stage-action) { transition:none; } }
  @media (hover:none) { .stage-file :global(.stage-action) { opacity:1; pointer-events:auto; } }
</style>
