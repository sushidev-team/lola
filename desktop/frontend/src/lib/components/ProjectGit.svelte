<script lang="ts">
  import { tick, untrack, onDestroy, type Snippet } from 'svelte';
  import Button from './Button.svelte';
  import Skeleton from './Skeleton.svelte';
  import MenuItem from './MenuItem.svelte';
  import GitFilePath from './GitFilePath.svelte';
  import ProjectGitStaging from './ProjectGitStaging.svelte';
  import ProjectGitActionMenu from './ProjectGitActionMenu.svelte';
  import ProjectGitWorktree from './ProjectGitWorktree.svelte';
  import ProjectGitPullRequest from './ProjectGitPullRequest.svelte';
  import ProjectGitPublish from './ProjectGitPublish.svelte';
  import ProjectGitNewBranch from './ProjectGitNewBranch.svelte';
  import { GitActions, type GitDestination } from '$lib/gitactions.svelte';
  import { gitCommitTooltip } from '$lib/gittooltip';
  import { confirm } from '$lib/confirm.svelte';
  import { nav } from '$lib/nav.svelte';
  import { disclosureMotion, disclosureSlide } from '$lib/disclosureMotion';
  import { gitColumns, readGitColumns, saveGitColumns, defaultGitColumns, type GitColumnID } from '$lib/gitcolumns';
  import GitProviderIcon from './GitProviderIcon.svelte';
  import Icon from './Icon.svelte';
  import ProjectGitDiff from './ProjectGitDiff.svelte';
  import GitAvatar from './GitAvatar.svelte';
  import { projectGitState, type ProjectGitState } from '$lib/projectgit.svelte';
  import { folderFirstFiles, workingGraphConnection, commitGraph, graphColor, graphLabelColor, graphPath, branchesByCommit, branchTree, type BranchNode, GRAPH_LANE_WIDTH, GRAPH_NODE_X, selectCommits, type GitArea, type GitFile, type GitRef, type GitRemote } from '$lib/projectgit';
  import { agoPhrase, exactTime } from '$lib/time';

  let { project, heading, actions, windowInset = false }: { project: string; heading?: Snippet; actions?: Snippet; windowInset?: boolean } = $props();
  const rowHeight = 34;
  let worktree = $state('');
  let ref = $state('');
  let query = $state('');
  let searchOpen = $state(false);
  let searchInput = $state<HTMLInputElement>();
  let columnMenu = $state(false);
  let columnOptions = $state<HTMLDivElement>();
  async function toggleColumnMenu() { columnMenu = !columnMenu; if (columnMenu) { await tick(); columnOptions?.querySelector<HTMLButtonElement>('[role=menuitemcheckbox]:not(:disabled)')?.focus(); } }
  function menuKey(event: KeyboardEvent) {
    const items = Array.from(columnOptions?.querySelectorAll<HTMLButtonElement>('[role^=menuitem]:not(:disabled)') ?? []);
    const index = items.indexOf(document.activeElement as HTMLButtonElement);
    if (['ArrowDown', 'ArrowUp', 'Home', 'End'].includes(event.key)) { event.preventDefault(); items[event.key === 'Home' ? 0 : event.key === 'End' ? items.length - 1 : (index + (event.key === 'ArrowDown' ? 1 : -1) + items.length) % items.length]?.focus(); }
    if (event.key === 'Escape') { event.stopPropagation(); columnMenu = false; columnOptions?.querySelector<HTMLButtonElement>('[aria-haspopup]')?.focus(); }
  }
  let columnPrefs = $state(readGitColumns());
  const shownColumns = $derived(gitColumns.filter(c => !columnPrefs.hidden.includes(c.id)));
  const columnTemplate = $derived(shownColumns.map(c => c.id === 'subject' && columnPrefs.widths.subject === undefined ? `minmax(${columnWidth(c.id)}px,1fr)` : `${columnWidth(c.id)}px`).join(' '));
  const detailsOffset = $derived(8 + shownColumns.slice(0, shownColumns.findIndex(column => column.id === 'subject')).reduce((sum, column) => sum + columnWidth(column.id) + 10, 0));
  const tableWidth = $derived(shownColumns.reduce((sum, c) => sum + columnWidth(c.id) + 10, 10));
  function columnWidth(id: GitColumnID) {
    const column = gitColumns.find(c => c.id === id)!;
    return id === 'graph' ? Math.max(columnPrefs.widths[id] ?? 0, search ? 40 : graphWidth) : columnPrefs.widths[id] ?? column.width;
  }
  function toggleColumn(id: GitColumnID) {
    if (id === 'subject') return;
    columnPrefs = { ...columnPrefs, hidden: columnPrefs.hidden.includes(id) ? columnPrefs.hidden.filter(c => c !== id) : [...columnPrefs.hidden, id] };
    saveGitColumns(columnPrefs);
  }
  function setColumnWidth(id: GitColumnID, width: number) {
    const c = gitColumns.find(c => c.id === id)!;
    columnPrefs = { ...columnPrefs, widths: { ...columnPrefs.widths, [id]: Math.round(Math.max(c.min, Math.min(c.max, width))) } };
    saveGitColumns(columnPrefs);
  }
  function resizeColumn(event: PointerEvent, id: GitColumnID) {
    if (event.button !== 0) return;
    event.preventDefault();
    const target = event.currentTarget as HTMLElement, start = event.clientX, width = target.parentElement?.getBoundingClientRect().width || columnWidth(id);
    target.setPointerCapture(event.pointerId);
    const move = (e: PointerEvent) => setColumnWidth(id, width + e.clientX - start);
    const finish = () => { target.removeEventListener('pointermove', move); target.removeEventListener('lostpointercapture', finish); };
    target.addEventListener('pointermove', move); target.addEventListener('lostpointercapture', finish);
  }
  type Sidebar = 'rail' | 'inspector';
  const sidebarDefaults = {rail:260, inspector:320};
  function readSidebarWidths() {
    try {
      const saved = JSON.parse(localStorage.getItem('lola.git.sidebar-widths') || '{}');
      return {rail:Number.isFinite(saved.rail) ? Math.max(180,Math.min(480,saved.rail)) : 260, inspector:Number.isFinite(saved.inspector) ? Math.max(240,Math.min(600,saved.inspector)) : 320};
    } catch { return {...sidebarDefaults}; }
  }
  let sidebarWidths = $state(readSidebarWidths());
  let workbenchWidth = $state(1200);
  let resizingSidebar = $state<Sidebar | ''>('');
  const availableWidth = $derived(workbenchWidth || 1200);
  const railMax = $derived(Math.max(180,Math.min(480,availableWidth - 560)));
  const railWidth = $derived(Math.min(sidebarWidths.rail,railMax));
  const inspectorMax = $derived(Math.max(240,Math.min(600,availableWidth - (availableWidth > 950 ? railWidth : 0) - 320)));
  const inspectorWidth = $derived(Math.min(sidebarWidths.inspector,inspectorMax));
  function setSidebarWidth(side: Sidebar, width: number) {
    sidebarWidths = {...sidebarWidths,[side]:Math.round(Math.max(side === 'rail' ? 180 : 240,Math.min(side === 'rail' ? railMax : inspectorMax,width)))};
    try { localStorage.setItem('lola.git.sidebar-widths',JSON.stringify(sidebarWidths)); } catch { /* retain the current layout */ }
  }
  function resizeSidebar(event: PointerEvent, side: Sidebar) {
    if (event.button !== 0) return;
    event.preventDefault();
    const target = event.currentTarget as HTMLElement, start = event.clientX, width = side === 'rail' ? railWidth : inspectorWidth;
    target.setPointerCapture(event.pointerId); resizingSidebar = side;
    const move = (e: PointerEvent) => setSidebarWidth(side,width + (e.clientX - start) * (side === 'rail' ? 1 : -1));
    const finish = () => { resizingSidebar = ''; target.removeEventListener('pointermove',move); target.removeEventListener('lostpointercapture',finish); };
    target.addEventListener('pointermove',move); target.addEventListener('lostpointercapture',finish);
  }
  function sidebarKey(event: KeyboardEvent, side: Sidebar) {
    if (!['ArrowLeft','ArrowRight','Home','End'].includes(event.key)) return;
    event.preventDefault(); event.stopPropagation();
    const width = side === 'rail' ? railWidth : inspectorWidth;
    setSidebarWidth(side,event.key === 'Home' ? (side === 'rail' ? 180 : 240) : event.key === 'End' ? (side === 'rail' ? railMax : inspectorMax) : width + (event.key === 'ArrowRight' ? 10 : -10) * (side === 'rail' ? 1 : -1));
  }
  async function openSearch() { searchOpen = true; columnMenu = false; await tick(); searchInput?.focus(); searchInput?.select(); }
  function closeSearch() { searchOpen = false; query = ''; }
  function searchKey(event: KeyboardEvent) {
    if (event.defaultPrevented || nav.overlay || confirm.request) return;
    if ((event.metaKey || event.ctrlKey) && event.key.toLowerCase() === 'f' && !nav.overlay && !confirm.request) { event.preventDefault(); void openSearch(); }
    if (event.key === 'Escape') { if (columnMenu) columnMenu = false; else if (searchOpen) closeSearch(); }
  }
  let lastSearchRequest = untrack(() => nav.gitSearchRequest);
  $effect(() => { if (nav.gitSearchRequest !== lastSearchRequest) { lastSearchRequest = nav.gitSearchRequest; void openSearch(); } });
  let limit = $state(200);
  let selected = $state<string[]>([]);
  let area = $state<GitArea | ''>('');
  let diffOpen = $state(false);
  let path = $state('');
  let activeFile = $state('');
  let showRail = $state(false);
  let draggedBranch = $state<GitRef | null>(null);
  let dropTarget = $state('');
  let newWorktree = $state<{git: ProjectGitState; branch: GitRef; historyRef: string; limit: number} | null>(null);
  function openNewWorktree(branch: GitRef) {
    if (git.busy || data?.operation) return;
    gitActions.close(); publishBranch = null; newBranch = null;
    newWorktree = {git, branch:{...branch}, historyRef:ref, limit};
  }
  let publishBranch = $state<{git: ProjectGitState; branch: GitRef; destination?: GitDestination; historyRef: string; limit: number} | null>(null);
  function openPublish(branch: GitRef, destination?: GitDestination) {
    if (git.busy || data?.operation) return;
    git.error = ''; newWorktree = null; newBranch = null; gitActions.close();
    publishBranch = {git, branch: {...branch}, destination, historyRef: ref, limit};
  }
  let newBranch = $state<{ git: ProjectGitState; start: { sha: string; label: string }; refs: GitRef[]; historyRef: string; limit: number } | null>(null);
  function openNewBranch(sha = data?.head ?? '', label = data?.branch || 'Detached HEAD') {
    if (!sha || git.busy || data?.operation) return;
    gitActions.close(); cancelBranchDrag();
    publishBranch = null; newWorktree = null;
    newBranch = { git, start: { sha, label }, refs: (data?.refs ?? []).map(branch => ({ ...branch })), historyRef: ref, limit };
  }
  let newPR = $state<{git:ProjectGitState;branch:GitRef;target?:GitRef;historyRef:string;limit:number}|null>(null);
  function openPullRequest(branch = data?.refs.find(r=>r.current&&!r.remote), target?:GitRef) {
    if(!branch||git.busy||data?.operation)return;
    gitActions.close();cancelBranchDrag();publishBranch=null;newWorktree=null;newBranch=null;
    newPR={git,branch:{...branch},target:target?{...target}:undefined,historyRef:ref,limit};
  }
  const gitActions = new GitActions(() => ({ git, ref, limit }), {
    viewHistory: chooseRef,
    createPullRequest: openPullRequest,
    openWorktree: (value) => chooseWorktree(value === configuredPath ? '' : value),
    viewCommit: showCommitDiff,
    createBranch: openNewBranch,
    publishBranch: openPublish,
    createWorktree: openNewWorktree,
    inspectFile,
    stageFiles: (paths, staged) => void act(staged ? 'unstage' : 'stage', paths),
    compareWorking: (sha) => {selected = [sha]; area = 'working'; diffOpen = true; path = ''; activeFile = '';},
    canCompareCommit: (sha) => selected.length === 1 && selected[0] !== sha,
    compareCommit: (sha) => { selected = selectCommits(selected, sha, true); area = 'commit'; diffOpen = false; path = ''; activeFile = ''; },
  });
  let cancelBranchDrag = () => {};
  let suppressClickUntil = 0;
  let workspace: HTMLDivElement;
  function copyBranchBadge(source: HTMLElement, rect: DOMRect) {
    const copy = source.cloneNode(true) as HTMLElement;
    const originals = [source, ...source.querySelectorAll('*')];
    const copies = [copy, ...copy.querySelectorAll('*')];
    // Preserve the rendered appearance when moving the copy outside its rail/
    // graph ancestors. A DOM clone carries no source event listeners.
    originals.forEach((original, index) => {
      const clone = copies[index] as HTMLElement | SVGElement;
      const style = getComputedStyle(original);
      for (const property of Array.from(style)) clone.style.setProperty(property, style.getPropertyValue(property), 'important');
      clone.style.setProperty('pointer-events', 'none', 'important');
      clone.style.setProperty('transition', 'none', 'important');
      clone.removeAttribute('id'); clone.removeAttribute('title'); clone.removeAttribute('aria-describedby');
    });
    copy.querySelectorAll('.ref-expanded').forEach(node => node.remove());
    copy.classList.add('branch-drag-ghost');
    copy.setAttribute('aria-hidden', 'true'); copy.inert = true;
    for (const [property, value] of Object.entries({ position:'fixed', margin:'0', width:`${rect.width}px`, height:`${rect.height}px`, 'box-sizing':'border-box', 'z-index':'10001' })) copy.style.setProperty(property, value, 'important');
    document.body.append(copy);
    return copy;
  }
  function clickAfterDrag(event: MouseEvent) {
    if (Date.now() >= suppressClickUntil) return false;
    event.preventDefault(); event.stopPropagation(); return true;
  }
  function pointerTarget(event: PointerEvent) {
    const hit = document.elementFromPoint(event.clientX, event.clientY);
    if (!hit || !workspace?.contains(hit)) return null;
    const name = hit.closest<HTMLElement>('[data-branch-ref]')?.dataset.branchRef;
    // Branch actions need an explicit ref; ancestry does not identify a target.
    return data?.refs.find(branch => branch.fullName === name);
  }
  function startBranchDrag(event: PointerEvent, branch?: GitRef) {
    if (!branch || git.busy || data?.operation || event.button !== 0 || event.isPrimary === false) return;
    cancelBranchDrag();
    const node = event.currentTarget as HTMLElement, pointer = event.pointerId;
    const source = (event.target instanceof Element ? event.target.closest<HTMLElement>('[data-branch-ref]') : null) ?? node;
    const sourceRect = source.getBoundingClientRect();
    const start = { x: event.clientX, y: event.clientY };
    let preview: HTMLElement | undefined;
    let active = false;
    const clear = () => {
      window.removeEventListener('pointermove', move, true);
      window.removeEventListener('pointerup', finish, true);
      window.removeEventListener('pointercancel', cancel, true);
      window.removeEventListener('keydown', escape, true);
      window.removeEventListener('blur', cancel);
      node.removeEventListener('lostpointercapture', cancel);
      if (node.hasPointerCapture?.(pointer)) node.releasePointerCapture(pointer);
      preview?.remove(); preview = undefined;
      draggedBranch = null; dropTarget = ''; cancelBranchDrag = () => {};
    };
    const cancel = () => { if (active) suppressClickUntil = Date.now() + 300; clear(); };
    const escape = (e: KeyboardEvent) => { if (e.key === 'Escape') { e.preventDefault(); e.stopPropagation(); cancel(); } };
    const move = (e: PointerEvent) => {
      if (e.pointerId !== pointer) return;
      if (!active && Math.hypot(e.clientX - start.x, e.clientY - start.y) < 5) return;
      if (!active) {
        active = true; draggedBranch = { ...branch }; gitActions.close();
        preview = copyBranchBadge(source, sourceRect);
        node.setPointerCapture?.(pointer);
        // Also dismiss delayed previews; no native drag session is created.
        node.dispatchEvent(new Event('dragstart', { bubbles: true, cancelable: true }));
        window.getSelection()?.removeAllRanges();
      }
      e.preventDefault();
      preview?.style.setProperty('left', `${sourceRect.left + e.clientX - start.x}px`, 'important');
      preview?.style.setProperty('top', `${sourceRect.top + e.clientY - start.y}px`, 'important');
      const target = pointerTarget(e);
      dropTarget = target && gitActions.canDrop(branch,target) ? target.fullName : '';
    };
    const finish = (e: PointerEvent) => {
      if (e.pointerId !== pointer) return;
      if (active && !git.busy) {
        e.preventDefault(); suppressClickUntil = Date.now() + 300;
        const target = pointerTarget(e);
        if (target && gitActions.canDrop(branch,target)) dropBranch(e, target);
      }
      clear();
    };
    cancelBranchDrag = cancel;
    window.addEventListener('pointermove', move, true);
    window.addEventListener('pointerup', finish, true);
    window.addEventListener('pointercancel', cancel, true);
    window.addEventListener('keydown', escape, true);
    window.addEventListener('blur', cancel);
    node.addEventListener('lostpointercapture', cancel);
  }
  onDestroy(() => cancelBranchDrag());
  function dropBranch(event: MouseEvent, branch?: GitRef) {
    if (!draggedBranch || !branch) return;
    const source = draggedBranch;
    if (source.fullName === branch.fullName) gitActions.openBranch(branch, event);
    else gitActions.dropBranches(source, branch, event);
  }
  function eventBranch(event: Event) {
    const name = event.target instanceof Element ? event.target.closest<HTMLElement>('[data-branch-ref]')?.dataset.branchRef : undefined;
    return data?.refs.find(branch => branch.fullName === name);
  }
  function keyboardContext(event: KeyboardEvent) {
    if (event.key !== 'ContextMenu' && !(event.shiftKey && event.key === 'F10')) return;
    event.preventDefault(); event.stopPropagation();
    const node = event.target as HTMLElement, rect = node.getBoundingClientRect();
    node.dispatchEvent(new MouseEvent('contextmenu', { bubbles:true, cancelable:true, clientX:rect.left + 12, clientY:rect.bottom }));
  }
  let collapsedFolders = $state<Set<string>>(new Set());
  let hoveredLane = $state<number | null>(null);
  let focusedLane = $state<number | null>(null);
  const tracedLane = $derived(hoveredLane ?? focusedLane);
  const git = $derived(projectGitState(project, worktree));
  const configuredPath = $derived(projectGitState(project, '').snapshot?.path ?? '');
  const data = $derived(git.snapshot);
  const initialLoading = $derived(!data && !git.error);
  const workingCounts = $derived.by(() => {
    const counts = { added: 0, modified: 0, deleted: 0, conflicts: 0 };
    for (const file of data?.files ?? []) {
      if (file.conflict) counts.conflicts++;
      else if (file.untracked || file.index === 'A') counts.added++;
      else if (file.index === 'D' || file.worktree === 'D') counts.deleted++;
      else counts.modified++;
    }
    return counts;
  });
  const localBranches = $derived(branchTree(data?.refs.filter(branch => !branch.remote) ?? []));
  const commits = $derived(data?.commits ?? []);
  const graph = $derived(commitGraph(commits));
  const graphBySHA = $derived(new Map(graph.map(row => [row.sha, row])));
  const workingConnection = $derived(!query.trim() && data?.files.length ? workingGraphConnection(graph, data.head) : null);
  const workingX = $derived(GRAPH_NODE_X + (workingConnection?.lane ?? 0) * GRAPH_LANE_WIDTH);
  const graphWidth = $derived(Math.max(64, workingConnection ? workingX + GRAPH_LANE_WIDTH : 0, ...graph.map(row => row.width * GRAPH_LANE_WIDTH + GRAPH_NODE_X)));
  const branchHistory = $derived(branchesByCommit(commits, data?.refs ?? []));
  const refsAt = $derived.by(() => {
    const result = new Map<string, GitRef[]>();
    for (const branch of [...(data?.refs ?? [])].sort((a, b) => Number(b.current) - Number(a.current) || Number(a.remote) - Number(b.remote) || a.name.localeCompare(b.name))) result.set(branch.sha, [...(result.get(branch.sha) ?? []), branch]);
    return result;
  });
  const remoteGroups = $derived.by(() => {
    const remotes = [...(data?.remotes ?? [])];
    const branches = data?.refs.filter(branch => branch.remote) ?? [];
    for (const branch of branches) {
      if (!remoteFor(branch, remotes)) {
        const name = branch.name.split('/')[0];
        if (!remotes.some(remote => remote.name === name)) remotes.push({ name, host: '', owner: '', repo: '', avatarUrl: '' });
      }
    }
    return remotes.map(remote => {
      const children = branches.filter(branch => remoteFor(branch, remotes)?.name === remote.name);
      return { ...remote, branches: children, tree: branchTree(children, remote.name + '/') };
    });
  });
  const search = $derived(query.trim().toLocaleLowerCase());
  const visible = $derived(search ? commits.filter(c => [c.subject, c.author, c.sha, ...(c.refs ?? [])].some(v => v.toLocaleLowerCase().includes(search))) : commits);
  const currentCommit = $derived(commits.find(c => c.sha === selected[selected.length - 1]));
  const containedBranches = $derived(currentCommit ? branchHistory.get(currentCommit.sha) ?? [] : []);
  const files = $derived(folderFirstFiles(git.diff?.files ?? []));
  const file = $derived(files.find(f => f.path === activeFile) ?? files[0]);
  const totals = $derived(files.reduce((sum, f) => ({ add: sum.add + f.additions, del: sum.del + f.deletions }), { add: 0, del: 0 }));
  const diffTitle = $derived(area === 'working' ? `${selected[0]?.slice(0,8)} → Working changes` : selected.length === 2 ? `${selected[0].slice(0, 8)} → ${selected[1].slice(0, 8)}` : area === 'commit' ? 'Commit changes' : area === 'staged' ? 'Staged changes' : 'Working changes');

  $effect(() => {
    project;
    untrack(() => { newPR = null; newWorktree = null; publishBranch = null; newBranch = null; gitActions.close(); cancelBranchDrag(); worktree = ''; ref = ''; query = ''; limit = 200; selected = []; area = ''; diffOpen = false; path = ''; activeFile = ''; });
  });
  $effect(() => {
    const state = git, branch = ref, count = limit;
    untrack(() => { if (newPR && newPR.git !== state) newPR = null; if (newWorktree && newWorktree.git !== state) newWorktree = null; if (publishBranch && publishBranch.git !== state) publishBranch = null; if (newBranch && newBranch.git !== state) newBranch = null; gitActions.close(); cancelBranchDrag(); selected = []; area = ''; diffOpen = false; path = ''; activeFile = ''; hoveredLane = null; focusedLane = null; state.clearDiff(); void state.load(branch, count); });
  });
  $effect(() => {
    const state = git, snapshot = data, selection = selected, chosenArea = area, chosenPath = path;
    untrack(() => {
      if (!snapshot || !chosenArea) { state.clearDiff(); return; }
      void state.loadDiff(chosenArea, chosenArea === 'working' || selection.length === 2 ? selection[0] : '', chosenArea === 'working' ? '' : selection.at(-1) ?? '', chosenPath);
    });
  });

  function chooseCommit(sha: string, event?: MouseEvent) {
    selected = selectCommits(selected, sha, !!(event?.metaKey || event?.ctrlKey));
    area = selected.length ? 'commit' : '';
    diffOpen = false;
    path = ''; activeFile = '';
  }
  function working() { selected = []; area = ''; diffOpen = false; path = ''; activeFile = ''; }
  function showCommitDiff(sha: string) { selected = [sha]; area = 'commit'; path = ''; activeFile = ''; diffOpen = true; }
  function inspectFile(file: GitFile, staged: boolean) {
    diffOpen = true;
    selected = []; area = staged ? 'staged' : file.untracked ? 'untracked' : 'unstaged'; path = file.path; activeFile = file.path;
  }
  async function act(action: 'stage' | 'unstage' | 'commit' | 'fetch' | 'pull' | 'push', paths: string[] = []) {
    const state = git;
    const ok = await state.act(action, paths, ref, limit);
    if (ok && state === git && ['stage', 'unstage', 'commit'].includes(action)) working();
  }
  function chooseWorktree(value: string) { worktree = value; ref = ''; limit = 200; query = ''; showRail = false; }
  function chooseRef(value: string) { ref = value; limit = 200; query = ''; showRail = false; }
  function historyKey(event: KeyboardEvent) {
    if (!['ArrowDown', 'ArrowUp', 'Home', 'End'].includes(event.key)) return;
    const buttons = Array.from(event.currentTarget instanceof HTMLElement ? event.currentTarget.querySelectorAll<HTMLButtonElement>('[data-commit]') : []);
    if (!buttons.length) return;
    const current = buttons.indexOf(document.activeElement as HTMLButtonElement);
    const next = event.key === 'Home' ? 0 : event.key === 'End' ? buttons.length - 1 : Math.max(0, Math.min(buttons.length - 1, current + (event.key === 'ArrowDown' ? 1 : -1)));
    event.preventDefault(); event.stopPropagation(); buttons[next].focus(); buttons[next].scrollIntoView?.({ block: 'nearest' });
  }
  function remoteFor(branch: GitRef, remotes = data?.remotes ?? []): GitRemote | undefined {
    return branch.remote ? remotes.filter(remote => branch.name.startsWith(`${remote.name}/`)).sort((a, b) => b.name.length - a.name.length)[0] : undefined;
  }
  function refColor(branch: GitRef) { const row = graphBySHA.get(branch.sha); return row ? graphColor(row.color) : 'var(--color-faint)'; }
  function branchDescription(branch: GitRef) { return `${branch.remote ? 'Remote' : 'Local'} branch: ${branch.name}${branch.current ? ' · active checkout' : ''}`; }
  function folderKey(scope: string, path: string) { return JSON.stringify([project, worktree, scope, path]); }
  function toggleFolder(key: string) {
    const next = new Set(collapsedFolders);
    if (next.has(key)) next.delete(key); else next.add(key);
    collapsedFolders = next;
  }
</script>

{#snippet branchNodes(nodes: BranchNode[], scope: 'local' | 'remote', depth: number)}
  {#each nodes as node (node.kind + ':' + node.path)}
    {#if node.kind === 'folder'}
      {@const key = folderKey(scope, node.path)}
      {@const expanded = !collapsedFolders.has(key)}
      {@const containsSelection = ref.startsWith(`${scope === 'local' ? 'refs/heads/' : 'refs/remotes/'}${node.path}/`)}
      <Button block class={`rail-row branch-folder ${containsSelection ? 'contains-selection' : ''}`} style={`padding-left:${7 + Math.min(depth, 8) * 13}px`} aria-label={`${scope === 'local' ? 'Local' : 'Remote'} branch folder ${node.path}`} aria-expanded={expanded} title={node.path} onclick={() => toggleFolder(key)}><Icon name={expanded ? 'chevron-down' : 'chevron-right'} size={12} /><Icon name="folder" size={14} /><span class="truncate">{node.name}</span><span class="rail-count">{node.count}</span></Button>
      {#if expanded}<div transition:disclosureSlide>{@render branchNodes(node.children, scope, depth + 1)}</div>{/if}
    {:else}
      {@const branch = node.branch}
      <Button block selected={ref === branch.fullName} disabled={!!git.busy} class={`rail-row branch-leaf ${branch.current ? "current-branch" : ""} ${dropTarget === branch.fullName ? "branch-drop-target" : ""}`} style={`--branch-color:${refColor(branch)};padding-left:${19 + Math.min(depth, 8) * 13}px`} aria-current={branch.current ? "true" : undefined} aria-label={branch.name} title={`View history of ${branch.name}`} onclick={(event) => { if (!clickAfterDrag(event)) chooseRef(branch.fullName); }} ondblclick={(event) => { if (!clickAfterDrag(event)) gitActions.checkout(branch); }} oncontextmenu={(event) => gitActions.openBranch(branch, event)} data-branch-ref={branch.fullName} draggable={false} onpointerdown={(event) => startBranchDrag(event, branch)} ondragstart={(event) => event.preventDefault()}><span class="branch-glyph" style:color={refColor(branch)}><Icon name="git-branch" size={14} /></span><span class="truncate">{node.name}</span>{#if branch.current}<span title="Active checkout"><Icon name="monitor" size={13} /></span>{/if}</Button>
    {/if}
  {/each}
{/snippet}

<svelte:window onfocus={() => { if (data && !git.busy && !git.loading) void git.load(ref, limit); }} onkeydown={searchKey} onclick={(event) => { if (!(event.target instanceof Element) || !event.target.closest('.column-options')) columnMenu = false; }} />

{#snippet loadingRailRows(count: number)}
  <div class="rail-skeleton" aria-hidden="true">{#each Array(count) as _, index}<div class="rail-placeholder"><Skeleton width="12px" height="12px"/><Skeleton width={`${[65,80,52,72][index % 4]}%`} height="11px"/></div>{/each}</div>
{/snippet}

<div class="project-git" class:branch-dragging={!!draggedBranch} data-testid="project-git" bind:this={workspace}>
  <header class="git-toolbar drag" class:window-inset={windowInset} aria-label="Project toolbar">
    <div class="toolbar-heading">{#if heading}{@render heading()}{:else}<strong class="standalone-title">{project}</strong>{/if}</div>
    <div class="toolbar-actions no-drag" role="group" aria-label="Repository actions">
      <Button icon size="xs" class="rail-toggle" selected={showRail} aria-label="Toggle repository navigation" title="Branches and worktrees" onclick={() => showRail = !showRail}><Icon name="list-filter" size={16} /></Button>
      <div class="toolbar-group">
        <Button size="sm" class="pull-action" aria-label="Pull" disabled={!data?.upstream || !!git.busy || !!data?.operation} loading={git.busy === 'pull'} onclick={() => void act('pull')} title="Pull with fast-forward only"><span class="sync-arrow-window button-icon" aria-hidden="true"><Icon name="arrow-down" size={15} class="sync-arrow" /></span><span class="git-action-label">Pull</span></Button>
        <Button size="sm" variant="primary" class="push-action" aria-label="Push" disabled={!data?.head || !data?.branch || !!git.busy || !!data?.operation} loading={git.busy === 'push' || git.busy === 'publish'} onclick={() => gitActions.push()} title="Push current branch"><span class="sync-arrow-window button-icon" aria-hidden="true"><Icon name="arrow-up" size={16} class="sync-arrow" /></span><span class="git-action-label">Push</span></Button>
      </div>
      <span class="toolbar-divider"></span>
      <div class="toolbar-group">
        <Button size="sm" aria-label="Branch" disabled={!data?.head || !!git.busy || !!data?.operation} loading={git.busy === 'create-branch'} onclick={() => openNewBranch()} title="Create a branch from the current checkout"><Icon name="git-branch" size={16} /><span class="git-action-label">Branch</span></Button>
        <Button size="sm" aria-label="Create PR" disabled={!data?.branch || !!git.busy || !!data?.operation || !data?.remotes.some(r=>r.host==='github.com'&&r.repo)} onclick={()=>openPullRequest()} title="Create a GitHub pull request"><Icon name="git-pull-request" size={16}/><span class="git-action-label">Create PR</span></Button>
        <Button size="sm" aria-label="Stash changes" disabled={!data?.head || !data?.files.length || !!git.busy || !!data?.operation} loading={git.busy === 'stash'} onclick={() => gitActions.stash()} title="Stash staged, unstaged and untracked changes"><Icon name="archive" size={16} /><span class="git-action-label">Stash</span></Button>
        <Button size="sm" aria-label="Pop latest stash" disabled={!git.stashes.length || !!data?.files.length || !!git.busy || !!data?.operation} loading={git.busy === 'pop'} onclick={() => gitActions.pop()} title={data?.files.length ? 'Commit or stash current changes before popping a stash' : 'Pop latest stash'}><Icon name="archive-restore" size={16} /><span class="git-action-label">Pop</span></Button>
      </div>
    </div>
    <div class="toolbar-project-actions">{#if actions}{@render actions()}{/if}</div>
  </header>
  {#if newPR}{#key newPR}<ProjectGitPullRequest {...newPR} onClose={()=>newPR=null}/>{/key}{/if}
  <div class="git-notices">
  {#if publishBranch}
    {@const request = publishBranch}
    {#key request}<ProjectGitPublish {...request} onClose={() => { if (publishBranch === request) publishBranch = null; }} />{/key}
  {/if}
  {#if data?.operation}<div class="operation-notice" role="status"><Icon name="git-merge" size={16}/><strong>{({ merge:'Merge', rebase:'Rebase', 'cherry-pick':'Cherry-pick', revert:'Revert' } as Record<string,string>)[data.operation] ?? 'Git operation'} in progress</strong><span>{['merge','rebase','cherry-pick','revert'].includes(data.operation) ? (!data.files.length && ['cherry-pick','revert'].includes(data.operation) ? 'No changes remain for this commit. Skip it to continue.' : 'Resolve conflicts, stage the files, then continue.') : 'Finish or abort this operation in Git.'}</span>{#if ['merge','rebase','cherry-pick','revert'].includes(data.operation)}<Button size="xs" variant="secondary" disabled={!!git.busy || data.files.some(f => f.conflict)} onclick={() => gitActions.continueOperation()}>Continue</Button>{#if ['rebase','cherry-pick','revert'].includes(data.operation)}<Button size="xs" disabled={!!git.busy} onclick={() => gitActions.skipOperation()}>Skip commit…</Button>{/if}<Button size="xs" disabled={!!git.busy} onclick={() => gitActions.abortOperation()}>Abort…</Button>{/if}</div>{/if}
  {#if git.error}<div class="git-error" role="alert"><Icon name="circle-alert" size={16} /><span>{git.error}</span><Button size="xs" onclick={() => void git.load(ref, limit)}>Refresh</Button></div>{/if}
  </div>
  <div class="git-workbench" class:rail-open={showRail} class:resizing={!!resizingSidebar} bind:clientWidth={workbenchWidth} style={`--rail-width:${railWidth}px;--inspector-width:${inspectorWidth}px`}>
    <Button variant="bare" class="sidebar-resize rail-resize" role="separator" aria-label="Resize repository sidebar" aria-orientation="vertical" aria-valuemin={180} aria-valuemax={railMax} aria-valuenow={railWidth} title="Drag to resize · Double-click to reset" onpointerdown={(event) => resizeSidebar(event,'rail')} onkeydown={(event) => sidebarKey(event,'rail')} ondblclick={() => setSidebarWidth('rail',sidebarDefaults.rail)}><span class="sr-only">Resize repository sidebar</span></Button>
    <Button variant="bare" class="sidebar-resize inspector-resize" role="separator" aria-label="Resize changes sidebar" aria-orientation="vertical" aria-valuemin={240} aria-valuemax={inspectorMax} aria-valuenow={inspectorWidth} title="Drag to resize · Double-click to reset" onpointerdown={(event) => resizeSidebar(event,'inspector')} onkeydown={(event) => sidebarKey(event,'inspector')} ondblclick={() => setSidebarWidth('inspector',sidebarDefaults.inspector)}><span class="sr-only">Resize changes sidebar</span></Button>
    <!-- svelte-ignore a11y_no_noninteractive_element_interactions -->
    <nav class="repo-rail" aria-busy={initialLoading} aria-label="Repository navigation" oncontextmenu={(event) => gitActions.openRepository(event)} onkeydown={keyboardContext}>
      <details use:disclosureMotion class="repo-section" open>
        <summary><Icon name="git-branch" size={16} /><span>Local</span><span class="section-count">{#if initialLoading}<Skeleton width="16px" height="12px"/>{:else}{data ? data?.refs?.filter(r => !r.remote).length ?? 0 : '—'}{/if}</span><Icon name="chevron-down" class="git-disclosure-chevron" size={13} /></summary>
        <div class="section-content">
        {#if initialLoading}{@render loadingRailRows(5)}{:else}{@render branchNodes(localBranches, 'local', 0)}{/if}
        {#if data && !data.refs?.some(r => !r.remote)}<p class="rail-empty">No local branches yet</p>{/if}
        </div>
      </details>
      <details use:disclosureMotion class="repo-section" open>
        <summary><Icon name="cloud" size={16} /><span>Remote</span><span class="section-count">{#if initialLoading}<Skeleton width="16px" height="12px"/>{:else}{data ? data?.refs?.filter(r => r.remote).length ?? 0 : '—'}{/if}</span><Icon name="chevron-down" class="git-disclosure-chevron" size={13} /></summary>
        <div class="section-content">
        {#if initialLoading}{@render loadingRailRows(4)}{/if}
        {#each remoteGroups as remote (remote.name)}
          <details use:disclosureMotion class="remote-group" open>
            <summary title={remote.repo || remote.host || remote.name} oncontextmenu={(event) => gitActions.openRemote(remote, event)}>
              <Icon name="chevron-down" class="git-disclosure-chevron" size={11} />
              {#if remote.owner}<GitAvatar src={git.remoteAvatars[remote.name]} name={remote.owner} size={20} loading={git.remoteIdentityLoading} />{:else}<Icon name="cloud" size={17} />{/if}
              <span class="remote-copy"><strong>{remote.name}</strong>{#if remote.owner}<small>{remote.owner}</small>{:else if remote.host}<small>{remote.host}</small>{/if}</span>
              <Button size="xs" icon class="remote-fetch" aria-label={`Fetch ${remote.name}`} title={`Fetch from ${remote.name}`} disabled={!!git.busy || git.loading} loading={git.busy === 'fetch' && git.fetchingRemote === remote.name} onclick={(event) => { event.preventDefault(); event.stopPropagation(); gitActions.fetch(remote.name); }}><Icon name="refresh-cw" size={14}/></Button>
              {#if remote.host}<GitProviderIcon host={remote.host}/>{/if}
            </summary>
            {@render branchNodes(remote.tree, 'remote', 0)}
            {#if !remote.branches.length}<p class="rail-empty">Fetch to load branches</p>{/if}
          </details>
        {/each}
        {#if data && !remoteGroups.length}<p class="rail-empty">No remotes configured</p>{/if}
        </div>
      </details>
      <details use:disclosureMotion class="repo-section" open>
        <summary><Icon name="layers" size={16} /><span>Worktrees</span><span class="section-count">{#if initialLoading}<Skeleton width="16px" height="12px"/>{:else}{data ? data?.worktrees?.length ?? 0 : '—'}{/if}</span><Icon name="chevron-down" class="git-disclosure-chevron" size={13} /></summary>
        <div class="section-content">
        {#if initialLoading}{@render loadingRailRows(2)}{/if}
        {#each data?.worktrees ?? [] as tree, index (tree.path)}
          <Button block selected={tree.path === data?.path} disabled={tree.blocked || tree.prunable} class="rail-row worktree-row" aria-label={index === 0 ? `Main checkout ${tree.branch || 'Detached HEAD'}` : `${tree.branch || 'Detached HEAD'} ${tree.path}`} oncontextmenu={(event) => gitActions.openWorktree(tree, event)} title={tree.blocked ? `${tree.path} · managed sandbox; open its session to inspect` : tree.path} onclick={() => chooseWorktree(tree.path === configuredPath ? '' : tree.path)}>
            <Icon name={tree.blocked ? 'shield-check' : index === 0 ? 'house' : 'folder'} size={14} /><span class="tree-copy"><span>{tree.branch || 'Detached HEAD'}</span></span>
          </Button>
        {/each}
        </div>
      </details>
      <details use:disclosureMotion class="repo-section" open>
        <summary><Icon name="archive" size={16} /><span>Stashes</span><span class="section-count">{#if initialLoading || (data && git.stashesState === 'loading')}<Skeleton width="16px" height="12px"/>{:else}{data && git.stashesState === 'ready' ? git.stashes.length : '—'}{/if}</span><Icon name="chevron-down" class="git-disclosure-chevron" size={13} /></summary>
        <div class="section-content">
          {#each git.stashes as stash (stash.sha)}<Button block class="rail-row stash-row" title={stash.subject} onclick={(event) => gitActions.openStash(stash, event)} oncontextmenu={(event) => gitActions.openStash(stash, event)}><Icon name="archive" size={14} /><span class="truncate">{stash.subject}</span><small>{stash.selector.replace('stash@', '')}</small></Button>{/each}
          {#if initialLoading || (data && git.stashesState === 'loading')}{@render loadingRailRows(1)}{:else if data && !git.stashes.length}<p class="rail-empty">{git.stashesState === 'ready' ? 'No saved stashes' : 'Stashes unavailable'}</p>{/if}
        </div>
      </details>
    </nav>

    <main class="history-stage" class:with-diff={!!area && diffOpen}>
      <section class="history" aria-label="Commit history">
        <div class="column-options history-settings" bind:this={columnOptions}><Button size="xs" icon aria-label="Configure history columns" aria-expanded={columnMenu} aria-haspopup="menu" onclick={() => void toggleColumnMenu()}><Icon name="settings" size={14} /></Button>{#if columnMenu}<div class="column-menu" onkeydown={menuKey} role="menu" tabindex="-1" aria-label="History columns">{#each gitColumns as column}<MenuItem role="menuitemcheckbox" aria-checked={!columnPrefs.hidden.includes(column.id)} disabled={column.id === 'subject'} onclick={() => toggleColumn(column.id)} icon={!columnPrefs.hidden.includes(column.id) ? '✓' : ''}>{column.label}</MenuItem>{/each}<MenuItem onclick={() => { columnPrefs = defaultGitColumns(); saveGitColumns(columnPrefs); }}>Reset columns</MenuItem></div>{/if}</div>
        {#if searchOpen}
          <div class="history-find" transition:disclosureSlide><label class="history-search"><Icon name="search" size={14} /><input bind:this={searchInput} aria-label="Search loaded commits" placeholder="Message, author, branch or SHA…" bind:value={query} onkeydown={(event) => { if (event.key === "Escape") { event.preventDefault(); event.stopPropagation(); closeSearch(); } }} /></label><span>{visible.length} matches{data?.hasMore ? ' in loaded history' : ''}</span><Button size="xs" icon aria-label="Close commit search" onclick={closeSearch}><Icon name="x" size={14} /></Button></div>
        {/if}
        <!-- svelte-ignore a11y_no_noninteractive_element_interactions -->
        <div class="history-list" style={`--columns:${columnTemplate};--table-width:${tableWidth}px;--details-offset:${detailsOffset}px`} onkeydown={historyKey} role="group" aria-label="Commits; Command or Control click to compare two" aria-busy={git.loading}>
          <div class="history-columns">
            {#each shownColumns as column}<div class="column-heading"><span>{column.label}</span><Button variant="bare" role="separator" tabindex={0} aria-label={`Resize ${column.label} column`} aria-orientation="vertical" aria-valuenow={columnWidth(column.id)} aria-valuemin={column.min} aria-valuemax={Math.max(column.max, columnWidth(column.id))} class="column-resize" onpointerdown={(event) => resizeColumn(event, column.id)} onkeydown={(event) => { if (event.key === 'ArrowLeft' || event.key === 'ArrowRight') { event.preventDefault(); event.stopPropagation(); setColumnWidth(column.id, ((event.currentTarget as HTMLElement).parentElement?.getBoundingClientRect().width || columnWidth(column.id)) + (event.key === 'ArrowRight' ? 10 : -10)); } }}><span class="sr-only">Resize {column.label}</span></Button></div>{/each}
          </div>
          {#if data?.files.length && !search}
            <Button block variant="bare" selected={!selected.length} class="commit-row working-row" title={`Uncommitted changes on ${data.branch || 'detached HEAD'}`} oncontextmenu={(event) => gitActions.openRepository(event)} onkeydown={keyboardContext} data-commit="working" aria-label={`Working changes, ${data.files.length} files`} onclick={working}>
              {#each shownColumns as column}
                {#if column.id === 'refs'}<span class="working-state"><span class="wip-label"><Icon name="pencil" size={12} />In progress</span></span>
                {:else if column.id === 'graph'}<span class="working-graph"><svg width={graphWidth} height="34" aria-hidden="true">{#if workingConnection}<path class="working-edge" d={`M${workingX} 26V34`} stroke="currentColor" stroke-width="1.7" fill="none" />{/if}<circle cx={workingX} cy="17" r="9" fill="var(--color-canvas)" stroke="currentColor" stroke-width="1.8" stroke-dasharray="3 2.5" /><circle cx={workingX} cy="17" r="2.5" fill="currentColor" /></svg></span>
                {:else if column.id === 'subject'}<span class="working-copy"><strong>Working changes</strong><span class="working-count">{data.files.length}</span><span class="working-stats">{#if workingCounts.modified}<span class="working-stat modified" title={`${workingCounts.modified} modified files`}><Icon name="pencil" size={12} />{workingCounts.modified}</span>{/if}{#if workingCounts.added}<span class="working-stat added" title={`${workingCounts.added} added files`}><Icon name="plus" size={12} />{workingCounts.added}</span>{/if}{#if workingCounts.deleted}<span class="working-stat deleted" title={`${workingCounts.deleted} deleted files`}><Icon name="minus" size={12} />{workingCounts.deleted}</span>{/if}{#if workingCounts.conflicts}<span class="working-stat deleted" title={`${workingCounts.conflicts} conflicted files`}><Icon name="circle-alert" size={12} />{workingCounts.conflicts}</span>{/if}</span><span class="working-open">Review changes<Icon name="chevron-right" size={13} /></span></span>
                {:else if column.id === 'author'}<span class="commit-author">You</span>{:else}<span></span>{/if}
              {/each}
            </Button>
          {/if}
          {#if initialLoading}
            <div class="history-skeleton" aria-hidden="true">
              {#each Array(16) as _, index}<div class="history-placeholder">
                {#each shownColumns as column}<span class="placeholder-cell">
                  {#if column.id === 'refs'}{#if index % 4 === 0}<Skeleton width={`${index % 8 ? 62 : 82}%`} height="18px"/>{/if}
                  {:else if column.id === 'graph'}<span class="placeholder-node" style={`margin-left:${GRAPH_NODE_X - 9}px`}><Skeleton width="18px" height="18px"/></span>
                  {:else}<Skeleton width={`${column.id === 'subject' ? [76,56,88,65,48][index % 5] : column.id === 'author' ? [70,85,60][index % 3] : 65}%`} height="11px"/>{/if}
                </span>{/each}
              </div>{/each}
            </div>
          {:else if !data && git.error}<div class="git-empty"><Icon name="circle-alert" size={24} /><p>Repository unavailable</p><span>Check the error above, then retry to load the repository.</span></div>
          {:else if !visible.length}<div class="git-empty"><Icon name="git-branch" size={24} /><p>{search ? 'No matching commits' : 'No commits yet'}</p><span>{search ? 'Try a message, author, branch, or commit hash.' : 'Stage your files and create the first commit.'}</span></div>
          {:else}
            {#each visible as commit, commitIndex (commit.sha)}
              {@const row = graphBySHA.get(commit.sha)!}
              {@const tips = refsAt.get(commit.sha) ?? []}
              {@const primary = tips[0]}
              {@const pairedRemote = primary && !primary.remote ? tips.find(branch => branch.remote && branch.name === `${remoteFor(branch)?.name}/${primary.name}`) : undefined}
              {@const pairedOwner = pairedRemote ? remoteFor(pairedRemote) : undefined}
              {@const extraTips = tips.filter(branch => branch !== primary && branch !== pairedRemote)}
              {@const owner = primary ? remoteFor(primary) : undefined}
              {@const checkouts = data?.worktrees.filter(tree => tree.head === commit.sha) ?? []}
              {@const author = git.authors[commit.sha]}
              {@const memberships = branchHistory.get(commit.sha) ?? []}
              {@const nodeX = search ? GRAPH_NODE_X : GRAPH_NODE_X + row.lane * GRAPH_LANE_WIDTH}
              <Button block variant="bare" class={`commit-row ${selected.includes(commit.sha) ? 'commit-selected' : ''} ${tracedLane === row.color ? 'lane-traced' : ''}`} style={`--lane-color:${graphColor(row.color)};--lane-label-ink:${graphLabelColor(row.color)}`} data-commit={commit.sha} data-lane={row.color} aria-pressed={selected.includes(commit.sha)} aria-label={`${commit.subject}, ${commit.sha.slice(0, 8)}, ${commit.author}${tips.length ? ", " + tips.map(branchDescription).join(", ") : ""}${checkouts.length ? ", " + checkouts.map(tree => tree.path === data?.path ? "active checkout" : "worktree " + tree.branch).join(", ") : ""}`} oncontextmenu={(event) => { const branch = eventBranch(event); if (branch) gitActions.openBranch(branch, event); else gitActions.openCommit(commit, event); }} onkeydown={keyboardContext} ondblclick={(event) => { if (clickAfterDrag(event)) return; const branch = eventBranch(event); if (branch) gitActions.checkout(branch); }} onpointerdown={(event) => startBranchDrag(event, eventBranch(event))} ondragstart={(event) => event.preventDefault()} onclick={(e) => { if (!clickAfterDrag(e)) chooseCommit(commit.sha, e); }} onpointerenter={() => hoveredLane = row.color} onpointerleave={() => hoveredLane = null} onfocus={() => focusedLane = row.color} onblur={() => focusedLane = null} {...gitCommitTooltip(commit, memberships, checkouts.map(tree => `${tree.path === data?.path ? "Active checkout" : "Worktree"}: ${tree.path}`))}>
                {#if !columnPrefs.hidden.includes('refs')}<span class="commit-refs">
                  {#if primary}<span class:branch-drop-target={dropTarget === primary.fullName} class="ref-label" role="presentation" data-branch-ref={primary.fullName} draggable={false} onpointerenter={(event) => { const badge = event.currentTarget; const edge = badge.closest('.history-list')?.getBoundingClientRect().right ?? window.innerWidth; badge.style.setProperty('--ref-reveal-width', `${Math.max(120, Math.min(560, edge - badge.getBoundingClientRect().left - 12))}px`); }}>
                    {#if owner?.owner}<GitAvatar src={git.remoteAvatars[owner.name]} name={owner.owner} size={16} loading={git.remoteIdentityLoading} />{:else}<Icon name={primary.remote ? 'cloud' : 'git-branch'} size={13} />{/if}
                    <span class="ref-name">{primary.name}</span>
                    <span class="ref-expanded" aria-hidden="true">
                      {#if owner?.owner}<GitAvatar src={git.remoteAvatars[owner.name]} name={owner.owner} size={16} loading={git.remoteIdentityLoading} />{:else}<Icon name={primary.remote ? 'cloud' : 'git-branch'} size={13} />{/if}
                      <span>{primary.name}</span>
                    </span>
                  </span>{:else if commit.sha === data?.head}<span class="ref-label">HEAD</span>{/if}
                  {#if pairedRemote}<span class="paired-remote" class:branch-drop-target={dropTarget === pairedRemote.fullName} data-branch-ref={pairedRemote.fullName} draggable={false}>{#if pairedOwner?.owner}<GitAvatar src={git.remoteAvatars[pairedOwner.name]} name={pairedOwner.owner} size={17} loading={git.remoteIdentityLoading} />{:else}<Icon name="cloud" size={14} />{/if}</span>{/if}
                  {#if extraTips.length}<span class="extra-refs">+{extraTips.length}</span>{/if}
                  {#if checkouts.length}<span class="checkout-marker"><Icon name={checkouts.some(tree => tree.path === data?.path) ? 'monitor' : 'folder'} size={14} />{#if checkouts.length > 1}<small>{checkouts.length}</small>{/if}</span>{/if}
                </span>
                {/if}
                {#if !columnPrefs.hidden.includes('graph')}<span class="graph-cell">
                  <svg class="commit-graph" width={search ? 40 : graphWidth} height={rowHeight} aria-hidden="true">
                    {#if !search}{#each row.edges as edge}<path class="ancestry-edge" stroke-linecap="round" stroke-linejoin="round" d={graphPath(edge, rowHeight)} stroke={graphColor(edge.color)} stroke-width={tracedLane === edge.color ? 2.5 : 1.7} fill="none" />{/each}{/if}
                    {#if workingConnection && commitIndex <= workingConnection.index}<path class="working-edge" d={graphPath({from:workingConnection.lane,to:commitIndex === workingConnection.index ? row.lane : workingConnection.lane,start:'top',end:commitIndex === workingConnection.index ? 'node' : 'bottom',color:0},rowHeight)} stroke="var(--color-accent-ink)" stroke-width="1.7" fill="none" stroke-linecap="round" />{/if}
                    <circle cx={nodeX} cy={rowHeight / 2} r={commit.parents?.length > 1 ? 5 : 10.5} fill={commit.parents?.length > 1 || selected.includes(commit.sha) ? graphColor(row.color) : 'var(--color-panel)'} stroke={graphColor(row.color)} stroke-width="2" />
                  </svg>
                  {#if commit.parents?.length <= 1}<span class="graph-avatar" style:left={`${nodeX - 9}px`}><GitAvatar src={author?.avatarUrl} name={commit.author} size={18} loading={!!git.identityLoading[commit.sha]} /></span>{/if}
                </span>
                {/if}
                <span class="commit-copy"><span class="commit-subject">{commit.subject}</span></span>
                {#if !columnPrefs.hidden.includes('author')}<span class="commit-author">{commit.author}</span>{/if}{#if !columnPrefs.hidden.includes('sha')}<span class="commit-sha">{commit.sha.slice(0, 7)}</span>{/if}{#if !columnPrefs.hidden.includes('age')}<span class="commit-time">{agoPhrase(commit.date)}</span>{/if}
              </Button>
            {/each}
            {#if data?.hasMore && limit < 1000}<Button block size="xs" disabled={!!git.busy} class="load-more" onclick={() => limit = Math.min(1000, limit + 200)}>Load more commits</Button>{/if}
          {/if}
        </div>
        <div class="history-hint"><div class="history-tools"><Button size="xs" icon aria-label="Search commits" title="Search commits (⌘F)" onclick={() => void openSearch()}><Icon name="search" size={14} /></Button></div><span>{search ? 'Searching loaded history · graph connections hidden' : '⌘ / Ctrl + click two commits to compare'}</span>{#if ref}<Button size="xs" disabled={!!git.busy} onclick={() => chooseRef('')}>Clear branch filter</Button>{/if}</div>
      </section>
      {#if area && diffOpen}
        <section class="diff-stage" aria-label="Selected changes">
          <div class="comparison-bar"><span><Icon name={selected.length === 2 ? 'git-compare-arrows' : 'file-text'} size={14} />{diffTitle}</span><div>{#if selected.length === 2}<Button size="xs" aria-label="Swap comparison direction" onclick={() => selected = [...selected].reverse()}><Icon name="arrow-right-left" size={13} />Swap</Button>{/if}<Button size="xs" icon aria-label="Close diff" onclick={() => diffOpen = false}><Icon name="x" size={14} /></Button></div></div>
          {#if git.diffLoading}<div class="git-empty"><Icon name="loader-circle" class="animate-spin" size={18} /><p>Loading changes…</p></div>
          {:else if git.diffError}<div class="git-empty" role="alert"><Icon name="circle-alert" size={20} /><p>{git.diffError}</p><Button onclick={() => void git.loadDiff(area as GitArea, area === 'working' || selected.length === 2 ? selected[0] : '', area === 'working' ? '' : selected.at(-1) ?? '', path)}>Retry diff</Button></div>
          {:else if file}<ProjectGitDiff {file} />
          {:else}<div class="git-empty"><Icon name="check" size={20} /><p>No file changes in this comparison</p></div>{/if}
          {#if git.diff?.truncated}<p class="truncated-note">This diff exceeds the preview limit. Some changes are not shown.</p>{/if}
        </section>
      {/if}
    </main>

    <aside class="git-inspector" aria-busy={initialLoading} aria-label={selected.length ? 'Commit details' : 'Staging and commit'}>
      {#if selected.length}
        <div class="selection-details">
          <div class="inspector-heading"><h3>{area === 'working' ? 'Compare working changes' : selected.length === 2 ? 'Compare commits' : 'Commit'}</h3><code>{selected.at(-1)?.slice(0, 8)}</code></div>
          {#if area === 'working'}<div class="compare-endpoints"><span><small>From</small><code>{selected[0]?.slice(0,8)}</code></span><Icon name="arrow-down" size={14}/><span>Working copy</span></div>{/if}
          {#if selected.length === 2}<div class="compare-endpoints"><span><small>From</small><code>{selected[0].slice(0, 8)}</code></span><Icon name="arrow-down" size={14} /><span><small>To</small><code>{selected[1].slice(0, 8)}</code></span></div>{/if}
          {#if currentCommit}<h2 class="selected-subject">{currentCommit.subject}</h2><div class="commit-person"><GitAvatar src={git.authors[currentCommit.sha]?.avatarUrl} name={currentCommit.author} size={30} loading={!!git.identityLoading[currentCommit.sha]} /><span>{currentCommit.author}{#if git.authors[currentCommit.sha]?.login}<small>@{git.authors[currentCommit.sha].login}</small>{/if}<small title={exactTime(currentCommit.date)}>{agoPhrase(currentCommit.date)}</small></span></div>{/if}
          {#if currentCommit && (refsAt.get(currentCommit.sha)?.length ?? 0)}<div class="selected-refs">{#each refsAt.get(currentCommit.sha) ?? [] as branch}<span title={branchDescription(branch)} style={`--lane-color:${refColor(branch)}`}><Icon name={branch.remote ? 'cloud' : 'git-branch'} size={13} />{branch.name}{#if branch.current}<Icon name="monitor" size={13} />{/if}</span>{/each}</div>{/if}
          {#if currentCommit && containedBranches.length}
            <details use:disclosureMotion class="branch-membership"><summary><Icon name="chevron-down" class="git-disclosure-chevron" size={12} />In branch history<span class="rail-count">{containedBranches.length}</span></summary><p>This commit is reachable from these loaded branch tips. Shared commits can appear in several.</p>{#each containedBranches as branch}<div><span class="branch-glyph" style:color={refColor(branch)}><Icon name={branch.remote ? 'cloud' : 'git-branch'} size={13} /></span><span>{branch.name}</span></div>{/each}</details>
          {/if}
          <div class="change-totals"><span>{files.length} changed {files.length === 1 ? 'file' : 'files'}</span><span class="text-good">+{totals.add}</span><span class="text-bad">−{totals.del}</span></div>
        </div>
        <div class="changed-file-list">
          {#each files as f (f.path)}<Button block selected={file?.path === f.path} class="changed-file" title={f.path} aria-label={f.path} onclick={() => { activeFile = f.path; diffOpen = true; }}><span class={`file-status ${f.status === 'deleted' ? 'text-bad' : f.status === 'added' ? 'text-good' : 'text-orange'}`}>{({ added: 'A', deleted: 'D', renamed: 'R', modified: 'M' } as Record<string, string>)[f.status ?? ''] ?? 'M'}</span><GitFilePath path={f.path}/></Button>{/each}
        </div>
        <div class="inspector-bottom"><Button block variant="secondary" disabled={!files.length || git.diffLoading} onclick={() => diffOpen = true}><Icon name="file-text" size={14} />View changes</Button><Button block onclick={working}><Icon name="circle-dot" size={14} />Back to working changes</Button></div>
      {:else}
        <ProjectGitStaging {git} {path} {area} {ref} {limit} oninspect={inspectFile} onact={(action, paths) => void act(action, paths)} oncommitted={working} oncontextfile={(file,staged,paths,event) => gitActions.openFile(file,staged,paths,event)} />
      {/if}
    </aside>
  </div>
  <ProjectGitActionMenu actions={gitActions} />
  {#if newWorktree}
    {@const request = newWorktree}
    {#key request}<ProjectGitWorktree {...request} onClose={() => {if (newWorktree === request) newWorktree = null;}} onCreated={(path) => {if (request.git !== git) return; newWorktree = null; chooseWorktree(path);}} />{/key}
  {/if}
  {#if newBranch}
    {@const request = newBranch}
    <ProjectGitNewBranch {...request} onClose={() => newBranch = null} onCreated={(createdPath, checkout) => { if (request.git !== git) return; newBranch = null; if (checkout) { chooseWorktree(createdPath === configuredPath ? '' : createdPath); working(); } }} />
  {/if}
  <footer class="git-status"><span class="footer-checkout" title={`Active checkout: ${data?.branch || 'Detached HEAD'}`}><Icon name="git-branch" size={13} /><strong>{data?.branch || (data?.head ? 'Detached HEAD' : 'Repository')}</strong></span><span class="repo-path" title={data?.path}>{data?.path || ''}</span><span role="status">{initialLoading ? 'Loading repository…' : git.busy ? `${({ 'create-pr':'Creating pull request', 'prepare-discard': 'Reviewing unstaged changes', discard: 'Discarding unstaged changes', 'create-worktree': 'Creating worktree', 'cherry-pick': 'Cherry-picking commit', revert: 'Reverting commit', 'create-branch': 'Creating branch', stage: 'Staging files', unstage: 'Unstaging files', checkout:'Checking out branch', merge:'Merging branches', rebase:'Rebasing branch', continue:'Continuing operation', abort:'Aborting operation', stash:'Stashing changes', pop:'Popping stash', apply:'Applying stash', drop:'Dropping stash', commit: 'Creating commit', amend: 'Amending commit', fetch: 'Fetching', pull: 'Pulling', 'set-upstream': 'Setting upstream', skip: 'Skipping commit', publish: 'Publishing branch', push: 'Pushing' } as Record<string, string>)[git.busy]}…` : git.notice || (data?.upstream ? `Tracks ${data.upstream}` : '')}</span></footer>
</div>

<style>
  .repo-rail :global(.branch-leaf),.ref-label,.paired-remote { user-select:none; touch-action:none; -webkit-user-drag:none; }
  .project-git { container-type:inline-size; display:grid; grid-template-columns:minmax(0,1fr); grid-template-rows:auto auto minmax(0,1fr) auto; min-width:0; min-height:0; height:100%; color:var(--color-ink); background:var(--color-panel); }
  .git-toolbar { min-width:0; display:grid; grid-template-columns:minmax(0,1fr) auto minmax(0,1fr); gap:8px; align-items:center; height:48px; padding:0 12px; border-bottom:1px solid var(--color-edge); }
  .git-toolbar.window-inset { padding-left:82px; }
  .toolbar-heading { min-width:0; overflow:hidden; }
  .toolbar-project-actions { display:flex; justify-content:flex-end; min-width:0; }
  .toolbar-group { display:flex; align-items:center; gap:3px; }
  .standalone-title { flex:1; font-size:15px; }
  .toolbar-actions { display:flex; justify-content:center; gap:6px; padding:3px; border:1px solid var(--color-edge); border-radius:8px; background:var(--color-canvas); align-items:center; flex-shrink:0; }
  .toolbar-actions :global(button) { font-size:13px; height:28px; }
  .toolbar-actions :global(.push-action) { padding-inline:12px; font-weight:600; transition:background-color 180ms cubic-bezier(.16,1,.3,1); }
  .toolbar-actions :global(.push-action:enabled:hover) { background:color-mix(in srgb,var(--color-accent) 25%,var(--color-accent-fill)); }
  .toolbar-actions :global(.push-action) { --arrow-travel:-20px; --arrow-duration:260ms; }
  .toolbar-actions :global(.pull-action) { --arrow-travel:20px; }
  .toolbar-actions :global(button:enabled:hover .sync-arrow) { animation:sync-arrow-travel var(--arrow-duration,440ms) cubic-bezier(.45,0,.55,1) 1; }
  .sync-arrow-window { display:grid; place-items:center; width:16px; height:16px; flex-shrink:0; overflow:hidden; }
  @keyframes sync-arrow-travel {
    0%,100% { transform:translateY(0); }
    49% { transform:translateY(var(--arrow-travel)); }
    50% { transform:translateY(calc(-1 * var(--arrow-travel))); }
  }
  @media (prefers-reduced-motion:reduce) {
    .toolbar-actions :global(.push-action) { transition:none; }
    .toolbar-actions :global(button:enabled:hover .sync-arrow) { animation:none; }
  }
  .toolbar-divider { width:1px; height:16px; background:var(--color-edge); margin:0 4px; }
  .git-notices { grid-row:2; min-width:0; }
  .operation-notice { display:flex; align-items:center; gap:8px; padding:7px 12px; color:var(--color-warn); background:var(--color-canvas); border-bottom:1px solid var(--color-edge); font-size:13px; }
  .operation-notice span { flex:1; color:var(--color-ink-2); }
  .git-error { display:flex; align-items:flex-start; gap:8px; padding:10px 14px; color:var(--color-bad); background:color-mix(in srgb,var(--color-bad) 5%,var(--color-panel)); font-size:12px; }
  .git-error span { flex:1; overflow-wrap:anywhere; white-space:pre-wrap; }
  .git-workbench { position:relative; grid-row:3; display:grid; grid-template-columns:var(--rail-width) minmax(0,1fr) var(--inspector-width); min-width:0; min-height:0; overflow:hidden; }
  .git-workbench :global(.sidebar-resize) { position:absolute; top:0; bottom:0; z-index:4; width:8px; height:auto; padding:0; border-radius:0; cursor:col-resize; touch-action:none; background:transparent; }
  .git-workbench :global(.rail-resize) { left:var(--rail-width); transform:translateX(-50%); }
  .git-workbench :global(.inspector-resize) { right:var(--inspector-width); transform:translateX(50%); }
  .git-workbench :global(.sidebar-resize:hover),.git-workbench :global(.sidebar-resize:focus-visible) { background:var(--color-accent-fill); box-shadow:inset 0 0 0 1px var(--color-accent); outline:none; }
  .git-workbench.resizing { cursor:col-resize; user-select:none; }
  .git-workbench.resizing :global(*) { cursor:col-resize; user-select:none; }
  .repo-rail { min-height:0; overflow:auto; border-right:1px solid var(--color-edge); background:var(--color-canvas); padding:0 0 8px; }
  .repo-rail :global(.rail-row) { transition:none; height:28px; gap:7px; font-size:13px; border-radius:0; text-align:left; font-weight:400; min-width:0; }
  .repo-rail :global(.rail-row:not([aria-pressed="true"])) { color:var(--color-ink-2); }
  .repo-rail :global(.rail-row > svg) { flex-shrink:0; }
  .repo-rail :global(.branch-folder) { gap:6px; font-size:13px; }
  .repo-rail :global(.branch-folder.contains-selection) { color:var(--color-accent-ink); }
  .rail-count { margin-left:auto; color:var(--color-faint); font-size:12px; font-variant-numeric:tabular-nums; }
  details { margin-top:10px; }
  summary { list-style:none; display:flex; align-items:center; gap:6px; padding:5px 7px; cursor:pointer; color:var(--color-ink-2); font-size:13px; font-weight:600; }
  summary::-webkit-details-marker { display:none; }
  :global(.git-disclosure-chevron) { transition:transform 180ms ease-out; }
  @media (prefers-reduced-motion:reduce) { :global(.git-disclosure-chevron) { transition:none; } }
  details:not([open]) > summary :global(.git-disclosure-chevron) { transform:rotate(-90deg); }
  .repo-section { margin-top:0; }
  .repo-section:not([open]) + .repo-section > summary { border-top:0; }
  .repo-section > summary { position:sticky; top:0; z-index:1; min-height:34px; gap:8px; padding:6px 12px; border-block:1px solid var(--color-edge); border-radius:0; background:var(--color-panel); color:var(--color-ink); font-size:13px; font-weight:600; }
  .repo-section > summary:hover { background:var(--color-sel); }
  .repo-section > summary:focus-visible { outline:2px solid var(--color-accent); outline-offset:2px; }
  .repo-section > summary > :global(svg:first-child) { color:var(--color-ink-2); }
  .repo-section > summary > :global(.git-disclosure-chevron) { color:var(--color-faint); }
  .section-count { margin-left:auto; min-width:21px; padding:1px 5px; border-radius:4px; background:var(--color-canvas); color:var(--color-ink-2); font-size:12px; font-weight:500; text-align:center; font-variant-numeric:tabular-nums; }
  .section-content { padding:4px 0 0; }
  .rail-placeholder { display:flex; align-items:center; gap:9px; height:28px; padding:0 12px; }
  .rail-placeholder > :global(.skeleton:first-child) { flex-shrink:0; }
  .history-placeholder { height:34px; align-items:center; border-bottom:1px solid color-mix(in srgb,var(--color-edge) 20%,transparent); }
  .placeholder-cell { display:flex; align-items:center; min-width:0; }
  .placeholder-node { display:block; flex-shrink:0; border-radius:50%; overflow:hidden; }
  .history-skeleton { mask-image:linear-gradient(to bottom,#000 65%,transparent); }
  .rail-empty { color:var(--color-faint); font-size:12px; line-height:1.6; padding:6px 10px; }
  .repo-rail :global(.worktree-row) { padding:3px 12px; height:28px; min-height:28px; align-items:center; }
  .repo-rail :global(.branch-drop-target),.branch-drop-target { outline:2px solid var(--color-accent); outline-offset:-2px; background:var(--color-accent-fill); }
  .repo-rail :global(.stash-row) { gap:6px; }
  .repo-rail :global(.stash-row small) { margin-left:auto; font-size:12px; color:var(--color-faint); }
  .branch-glyph { display:inline-flex; flex-shrink:0; }
  .remote-group { margin-top:4px; }
  .remote-group :global(.remote-fetch) { flex-shrink:0; opacity:0; pointer-events:none; transition:none; }
  .remote-group summary:hover :global(.remote-fetch),.remote-group summary:focus-within :global(.remote-fetch),.remote-group :global(.remote-fetch[aria-busy="true"]) { opacity:1; pointer-events:auto; }
  .remote-group summary { min-height:28px; height:28px; gap:7px; }
  .remote-copy { display:flex; align-items:baseline; gap:7px; min-width:0; flex:1; }
  .remote-copy strong { font-size:13px; font-weight:500; }
  .remote-copy small { font-size:12px; font-weight:400; color:var(--color-faint); overflow:hidden; text-overflow:ellipsis; white-space:nowrap; }
  .tree-copy { display:flex; align-items:center; min-width:0; gap:8px; flex:1; }
  .tree-copy > span { min-width:0; }
  .tree-copy > span { white-space:nowrap; overflow:hidden; text-overflow:ellipsis; }
  .history-stage { display:grid; grid-template-rows:minmax(0,1fr); min-height:0; min-width:0; }
  .history-stage.with-diff { grid-template-rows:minmax(150px,0.9fr) minmax(180px,1.1fr); }
  .history { container:history / inline-size; display:grid; grid-template-columns:minmax(0,1fr); grid-template-rows:minmax(0,1fr) auto; min-height:0; min-width:0; position:relative; }
  .history-search { display:flex; align-items:center; gap:6px; padding:0 7px; border:1px solid var(--color-edge); border-radius:5px; background:var(--color-canvas); width:210px; min-width:90px; height:28px; color:var(--color-faint); }
  .history-search:focus-within { border-color:var(--color-accent); }
  .history-search input { width:100%; min-width:0; outline:none; background:transparent; color:var(--color-ink); font-size:13px; }
  .history :global(.working-row) { height:34px; border-radius:0; border-top:1px solid var(--color-edge); border-bottom:1px solid var(--color-edge); font-size:13px; }
  .history-list :global(.commit-row.working-row) { background:var(--color-canvas); border-bottom:1px solid var(--color-edge); }
  .history-list :global(.commit-row.working-row[aria-pressed="true"]) { background:var(--color-accent-fill); }
  .history-list :global(.commit-row.working-row:hover),.history-list :global(.commit-row.working-row:focus-visible) { background:color-mix(in srgb,var(--color-accent) 20%,var(--color-panel)); }
  .working-state { display:flex; justify-content:flex-start; min-width:0; }
  .wip-label { display:inline-flex; align-items:center; gap:5px; padding:2px 7px; border:1px solid color-mix(in srgb,var(--color-accent) 40%,transparent); border-radius:4px; color:var(--color-accent-ink); background:var(--color-canvas); font-size:12px; line-height:18px; white-space:nowrap; }
  .working-stats { display:flex; gap:10px; margin-left:5px; }
  .working-stat { display:inline-flex; align-items:center; gap:3px; font:12px var(--font-mono); font-variant-numeric:tabular-nums; }
  .working-stat.modified { color:var(--color-orange); }
  .working-stat.added { color:var(--color-good); }
  .working-stat.deleted { color:var(--color-bad); }
  .working-open { --cue-bg:color-mix(in srgb,var(--color-accent) 12%,var(--color-panel)); position:relative; z-index:1; display:flex; flex-shrink:0; align-items:center; gap:4px; margin-left:auto; padding:2px 6px; border-radius:4px; background:var(--cue-bg); box-shadow:0 0 6px 3px var(--cue-bg); font-size:12px; color:var(--color-accent-ink); opacity:0; transition:opacity 120ms ease-out; }
  :global(.working-row:hover) .working-open,:global(.working-row:focus-visible) .working-open { opacity:1; }
  @media (prefers-reduced-motion:reduce) { .working-open { transition:none; } }
  .working-count { font-size:12px; color:var(--color-faint); font-variant-numeric:tabular-nums; }
  .history-list { overflow:auto; min-height:0; min-width:0; }
  .history-columns,.history-placeholder,.history-list :global(.commit-row) { display:grid; grid-template-columns:var(--columns); gap:10px; min-width:var(--table-width); width:100%; padding:0 12px 0 8px; }
  .history-columns { position:sticky; top:0; z-index:2; align-items:center; height:26px; color:var(--color-ink-2); font-size:12px; background:var(--color-canvas); border-bottom:1px solid var(--color-edge); }
  .column-heading > span { overflow:hidden; white-space:nowrap; text-overflow:ellipsis; }
  .history-list :global(.commit-row) { transition:none; height:34px; border-radius:0; text-align:left; border-bottom:1px solid color-mix(in srgb,var(--color-edge) 35%,transparent); font-weight:400; --lane-tint:color-mix(in srgb,var(--lane-color) 7%,var(--color-panel)); background:transparent; }
  .history-list :global(.commit-row.lane-traced) { --lane-tint:color-mix(in srgb,var(--lane-color) 13%,var(--color-panel)); }
  .history-list :global(.commit-row:hover) { --lane-tint:color-mix(in srgb,var(--lane-color) 18%,var(--color-panel)); }
  .history-list :global(.commit-row:not(.working-row):not(.commit-selected):hover),.history-list :global(.commit-row:not(.working-row):not(.commit-selected):focus-visible) { background:linear-gradient(to right,transparent var(--details-offset),var(--color-sel) var(--details-offset)); }
  .history-list :global(.commit-row.commit-selected) { background:var(--color-accent-fill); color:var(--color-ink); box-shadow:inset 0 0 0 1px var(--color-accent-ink); }
  .graph-cell { height:34px; position:relative; background:linear-gradient(to left,var(--lane-tint),transparent); border-right:1px solid color-mix(in srgb,var(--lane-color) 55%,transparent); }
  .commit-graph { display:block; overflow:visible; }
  .graph-avatar { display:grid; position:absolute; top:8px; }
  .commit-copy { display:flex; align-items:center; min-width:0; }
  .commit-subject { overflow:hidden; text-overflow:ellipsis; white-space:nowrap; font-size:13px; min-width:0; }
  .commit-refs { display:flex; align-items:center; justify-content:flex-start; gap:5px; min-width:0; height:100%; }
  .ref-label { position:relative; display:flex; align-items:center; gap:5px; min-width:0; border:1px solid var(--lane-color); color:var(--lane-label-ink); background:var(--lane-color); border-radius:4px; padding:0 5px; font-size:12px; font-weight:600; line-height:18px; transition:background-color 160ms ease; }
  .ref-label > .ref-name { overflow:hidden; text-overflow:ellipsis; white-space:nowrap; }
  .ref-label:hover { z-index:5; background:color-mix(in srgb,var(--lane-color) 88%,var(--lane-label-ink)); }
  .ref-expanded { position:absolute; z-index:1; left:-1px; top:-1px; display:flex; align-items:flex-start; gap:5px; width:max-content; min-width:calc(100% + 2px); max-width:var(--ref-reveal-width, min(560px, 60vw)); padding:0 5px; border:1px solid var(--lane-color); border-radius:4px; background:var(--lane-color); color:var(--lane-label-ink); box-shadow:0 3px 8px #0003; opacity:0; visibility:hidden; pointer-events:none; transition:opacity 140ms ease,background-color 160ms ease,visibility 0s 140ms; }
  .ref-expanded > span:last-child { min-width:0; white-space:normal; overflow-wrap:anywhere; }
  .ref-expanded > :global(svg) { flex-shrink:0; margin-top:2px; }
  .ref-label:hover .ref-expanded { opacity:1; visibility:visible; pointer-events:auto; background:color-mix(in srgb,var(--lane-color) 88%,var(--lane-label-ink)); transition-delay:0s; }
  .project-git.branch-dragging .ref-label .ref-expanded { opacity:0; visibility:hidden; pointer-events:none; transition:none; }
  @media (prefers-reduced-motion:reduce) { .ref-label,.ref-expanded { transition:none; } }
  .ref-label > :global(svg) { color:inherit; flex-shrink:0; }
  .extra-refs { color:var(--color-ink-2); font-size:12px; flex-shrink:0; }
  .paired-remote { display:flex; align-items:center; justify-content:center; width:18px; height:18px; flex-shrink:0; color:var(--lane-color); }
  .checkout-marker { display:flex; align-items:center; min-width:16px; gap:2px; color:var(--lane-color); flex-shrink:0; }
  .checkout-marker small { font-size:11px; font-variant-numeric:tabular-nums; line-height:16px; }
  .commit-author { color:var(--color-faint); font-size:12px; min-width:0; text-overflow:ellipsis; overflow:hidden; white-space:nowrap; }
  .commit-sha { color:var(--color-faint); font-family:var(--font-mono); font-size:12px; }
  code,.working-count,.section-count,.rail-count,.commit-sha,.commit-time,.extra-refs,.checkout-marker small,.history-find > span,.commit-person small[title] { font-family:var(--font-mono); font-variant-numeric:tabular-nums; }
  .repo-rail :global(.stash-row small) { font-family:var(--font-mono); }
  .change-totals > .text-good,.change-totals > .text-bad { font-family:var(--font-mono); }
  .commit-time { color:var(--color-faint); font-size:12px; min-width:0; text-align:right; }
  .history-list :global(.load-more) { margin:8px 0; }
  .repo-rail :global(.branch-leaf.current-branch) { background:color-mix(in srgb,var(--branch-color) 14%,var(--color-canvas)); color:var(--color-ink); font-weight:600; box-shadow:inset 0 0 0 1px color-mix(in srgb,var(--branch-color) 35%,transparent); }
  .repo-rail :global(.branch-leaf.current-branch[aria-pressed="true"]) { box-shadow:inset 0 0 0 1px var(--color-accent-ink); }
  .history-find { position:absolute; top:31px; right:10px; z-index:5; display:flex; align-items:center; gap:8px; max-width:calc(100% - 20px); padding:7px; border:1px solid var(--color-edge); border-radius:7px; background:var(--color-canvas); box-shadow:0 8px 24px #0003; }
  .history-find > span { color:var(--color-ink-2); font-size:12px; white-space:nowrap; }
  .history-search { width:260px; }
  .column-heading { position:relative; min-width:0; height:100%; display:flex; align-items:center; }
  .column-heading > span { overflow:hidden; text-overflow:ellipsis; white-space:nowrap; }
  .column-heading :global(.column-resize) { height:100%; padding:0; border-radius:0; position:absolute; right:-6px; top:0; bottom:0; width:9px; cursor:col-resize; touch-action:none; border-right:1px solid transparent; }
  .column-heading :global(.column-resize:hover),.column-heading :global(.column-resize:focus-visible) { border-color:var(--color-accent); background:var(--color-accent-fill); outline:none; }
  .history-tools { display:flex; gap:2px; }
  .column-options { position:relative; }
  .history-settings { position:absolute; top:0; right:4px; z-index:4; background:var(--color-canvas); border-radius:4px; }
  .column-menu { position:absolute; top:29px; right:0; z-index:20; width:220px; border:1px solid var(--color-edge); border-radius:8px; padding:4px; background:var(--color-canvas); box-shadow:0 8px 24px #0003; color:var(--color-ink); }
  .working-copy { display:flex; align-items:center; gap:9px; min-width:0; }
  .working-copy strong { font-weight:500; }
  .working-graph { display:flex; align-items:center; height:34px; color:var(--color-accent-ink); }
  .history-hint > span { overflow:hidden; text-overflow:ellipsis; white-space:nowrap; }
  .history-hint { display:flex; align-items:center; justify-content:space-between; min-height:28px; padding:4px 12px; border-top:1px solid var(--color-edge); font-size:12px; color:var(--color-faint); }
  .diff-stage { display:grid; grid-template-rows:auto minmax(0,1fr); min-width:0; min-height:0; border-top:1px solid var(--color-edge); }
  .comparison-bar { display:flex; align-items:center; justify-content:space-between; gap:8px; padding:5px 12px; background:var(--color-canvas); border-bottom:1px solid var(--color-edge); }
  .comparison-bar > span,.comparison-bar > div { display:flex; align-items:center; gap:7px; font-size:12px; }
  .comparison-bar > span { color:var(--color-ink-2); }
  .truncated-note { color:var(--color-warn); font-size:12px; padding:5px 12px; }
  .git-empty { display:grid; align-content:center; justify-items:center; gap:8px; min-height:120px; padding:24px; text-align:center; color:var(--color-faint); }
  .git-empty p { color:var(--color-ink-2); font-size:13px; overflow-wrap:anywhere; }
  .git-empty span { font-size:12px; max-width:260px; }
  .git-inspector { border-left:1px solid var(--color-edge); min-width:0; min-height:0; display:grid; grid-template-rows:auto minmax(0,1fr) auto; background:var(--color-canvas); }
  .inspector-heading { display:flex; align-items:center; justify-content:space-between; height:41px; padding:6px 12px; border-bottom:1px solid var(--color-edge); }
  .inspector-heading h3 { font-size:14px; font-weight:600; }
  .inspector-heading > code { color:var(--color-faint); font-size:12px; }
  .file-status { width:12px; flex-shrink:0; text-align:center; font-family:var(--font-mono); font-size:12px; }
  .selection-details { min-width:0; }
  .selected-subject { padding:16px 14px 10px; font-size:16px; line-height:1.45; font-weight:600; overflow-wrap:anywhere; }
  .commit-person { display:flex; align-items:center; gap:9px; padding:0 14px 12px; font-size:14px; }
  .commit-person small { display:block; font-size:12px; color:var(--color-faint); margin-top:2px; }
  .selected-refs { display:flex; flex-wrap:wrap; gap:4px; padding:0 14px 12px; }
  .selected-refs span { display:flex; align-items:center; gap:5px; font-size:12px; overflow-wrap:anywhere; color:var(--color-ink); background:color-mix(in srgb,var(--lane-color) 10%,var(--color-panel)); padding:3px 5px; border-radius:4px; }
  .selected-refs :global(svg) { color:var(--lane-color); flex-shrink:0; }
  .branch-membership { margin:0; padding:0 8px 10px; max-height:180px; overflow:auto; }
  .branch-membership summary { font-size:12px; }
  .branch-membership p { padding:5px 7px; color:var(--color-faint); font-size:12px; line-height:1.5; }
  .branch-membership > div { display:flex; align-items:center; gap:6px; padding:3px 7px; font-size:12px; overflow-wrap:anywhere; }
  .branch-membership > div > span:last-child { min-width:0; }
  .change-totals { display:flex; gap:7px; font-size:12px; padding:11px 14px; border-top:1px solid var(--color-edge); border-bottom:1px solid var(--color-edge); }
  .change-totals > span:first-child { margin-right:auto; color:var(--color-ink-2); }
  .changed-file-list { padding:6px; overflow:auto; min-height:0; }
  .changed-file-list :global(.changed-file) { min-height:28px; height:auto; text-align:left; gap:7px; padding:5px 7px; }
  .inspector-bottom { display:grid; gap:5px; padding:10px; border-top:1px solid var(--color-edge); }
  .compare-endpoints { display:flex; align-items:center; gap:10px; padding:12px 14px 0; }
  .compare-endpoints > span { display:flex; gap:6px; align-items:center; font-size:12px; }
  .compare-endpoints small { color:var(--color-faint); font-size:12px; }
  .compare-endpoints > :global(svg) { transform:rotate(-90deg); color:var(--color-faint); }
  .git-status { grid-row:4; display:flex; align-items:center; justify-content:space-between; gap:12px; padding:5px 12px; font-size:12px; color:var(--color-faint); border-top:1px solid var(--color-edge); background:var(--color-canvas); min-width:0; }
  .footer-checkout { display:flex; align-items:center; gap:6px; min-width:0; max-width:40%; color:var(--color-ink-2); }
  .footer-checkout strong { overflow:hidden; white-space:nowrap; text-overflow:ellipsis; font-weight:500; }
  .repo-path { flex:1; overflow:hidden; white-space:nowrap; text-overflow:ellipsis; min-width:0; }
  .git-status > span:last-child { flex-shrink:0; }
  :global(.rail-toggle) { display:none; }
  @container (max-width:1150px) { .git-action-label { display:none; } .toolbar-actions :global(button) { width:28px; padding:0; } .toolbar-actions :global(.push-action) { width:30px; padding:0; } }
  @container (max-width:760px) { .git-toolbar { grid-template-columns:auto minmax(0,1fr) auto; } .toolbar-heading :global(.project-title) { display:none; } .toolbar-actions { gap:2px; } .operation-notice { flex-wrap:wrap; } }
  @container (max-width:950px) { .git-workbench { grid-template-columns:minmax(0,1fr) var(--inspector-width); position:relative; } .repo-rail { display:none; } .git-workbench :global(.rail-resize) { display:none; } .rail-open .repo-rail { display:block; position:absolute; inset:0 auto 0 0; width:280px; z-index:10; box-shadow:8px 0 20px #0002; } :global(.rail-toggle) { display:inline-flex; } }
  @container (max-width:580px) { .git-workbench :global(.sidebar-resize) { display:none; } .git-workbench { grid-template-columns:minmax(0,1fr); overflow:auto; grid-template-rows:max-content max-content; align-content:start; } .history-stage { height:330px; min-height:330px; } .history-stage.with-diff { height:550px; min-height:550px; } .git-inspector { height:620px; border-left:0; border-top:1px solid var(--color-edge); min-height:0; grid-template-rows:auto auto auto; align-content:start; } .changed-file-list { overflow:visible; } .git-toolbar { gap:4px; padding-inline:8px; } .git-toolbar.window-inset { padding-left:82px; } .toolbar-divider { margin-inline:0; } .git-status > span:last-child { max-width:50%; overflow:hidden; text-overflow:ellipsis; white-space:nowrap; } }
</style>
