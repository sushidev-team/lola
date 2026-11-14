import { tick } from 'svelte';
import { ProjectGitService } from '@bindings/desktop';
import { confirm } from './confirm.svelte';
import { store } from './store.svelte';
import { gitErrorInfo } from './giterror';
import { projectGitState, type ProjectGitState } from './projectgit.svelte';
import type { GitBranchAction, GitCommit, GitRef, GitRemote, GitStash, GitWorktree, GitFile } from './projectgit';
import type { IconName } from './icons';

type Context = { git: ProjectGitState; ref: string; limit: number; configuredPath?: string };
export type GitDestination = { remote: string; name: string; trackingOnly?: boolean };
type Callbacks = { compareBranches?: (source: GitRef, target: GitRef) => void; isManagedWorktree?: (tree: GitWorktree) => boolean; removeManagedWorktree?: (tree: GitWorktree, deleteBranch: boolean, onRemoved: () => Promise<void>) => void; createPullRequest?: (branch:GitRef,target?:GitRef)=>void; viewHistory: (ref: string) => void; openWorktree: (path: string) => void; viewCommit: (sha: string) => void; inspectFile?: (file: GitFile, staged: boolean) => void; stageFiles?: (paths: string[], staged: boolean) => void; createWorktree?: (branch: GitRef) => void; compareWorking?: (sha: string) => void; publishBranch?: (branch: GitRef, destination?: GitDestination) => void; createBranch?: (sha: string, label: string) => void; compareCommit?: (sha: string) => void; canCompareCommit?: (sha: string) => boolean };
export type GitMenuItem = { label: string; icon: IconName; run: () => void; disabled?: boolean; danger?: boolean; separator?: boolean };
export type GitMenuRequest = { title: string; subtitle?: string; x: number; y: number; items: GitMenuItem[]; anchor: HTMLElement | null };

/** One controller per mounted repository. Requests capture exact refs and SHA facts. */
export class GitActions {
  request = $state<GitMenuRequest | null>(null);
  private disposed = false;
  constructor(private context: () => Context, private callbacks: Callbacks) {}
  dispose() { this.disposed = true; this.close(); }
  close(restoreFocus = false) {
    const anchor = this.request?.anchor;
    this.request = null;
    if (restoreFocus && anchor?.isConnected) anchor.focus();
  }
  private menu(title: string, items: GitMenuItem[], event: MouseEvent, subtitle?: string) {
    event.preventDefault(); event.stopPropagation();
    const anchor = event.currentTarget instanceof HTMLElement ? event.currentTarget : event.target instanceof HTMLElement ? event.target.closest('button') : null;
    const rect = anchor?.getBoundingClientRect();
    this.request = { title, subtitle, items, anchor, x: event.clientX || rect?.left || 8, y: event.clientY || rect?.bottom || 8 };
  }
  private current() { const data = this.context().git.snapshot; return data?.refs.find(r => !r.remote && (r.current || r.name === data.branch)); }
  private blocked() { return !!this.context().git.busy; }
  private owner(branch: GitRef) { return !branch.remote ? this.context().git.snapshot?.worktrees.find(t => t.branch === branch.name) : undefined; }
  private mutationBlocked(branch?: GitRef) {
    const owner = branch && this.owner(branch);
    return this.blocked() || !!this.context().git.snapshot?.operation || !!owner?.blocked || !!owner?.prunable;
  }
  private canCreatePR(branch?:GitRef) {
    const remotes=this.context().git.snapshot?.remotes??[];
    return !!branch&&!this.mutationBlocked(branch)&&remotes.some(r=>r.host==='github.com'&&r.repo&&(!branch.remote||branch.fullName.startsWith(`refs/remotes/${r.name}/`)));
  }
  private upstream(branch: GitRef) { return branch.upstream || (branch.current ? this.context().git.snapshot?.upstream : '') || ''; }
  private destination(branch: GitRef): GitDestination | undefined {
    const upstream = this.upstream(branch);
    const fullName = branch.remote ? branch.fullName : upstream.startsWith('refs/') ? upstream : `refs/remotes/${upstream}`;
    const remote = this.context().git.snapshot?.remotes?.filter(r => fullName.startsWith(`refs/remotes/${r.name}/`)).sort((a,b) => b.name.length-a.name.length)[0];
    const name = remote ? fullName.slice(`refs/remotes/${remote.name}/`.length) : '';
    return remote && name !== 'HEAD' ? {remote:remote.name,name} : undefined;
  }
  private tracking(branch: GitRef) {
    if (!this.callbacks.publishBranch || this.mutationBlocked(branch)) return;
    this.close();
    this.callbacks.publishBranch(branch,{...(this.destination(branch) ?? {remote:'',name:branch.name}),trackingOnly:true});
  }
  private branchRequest(action: string, source?: GitRef, target?: GitRef): GitBranchAction {
    return { action, source: source?.fullName ?? '', sourceSHA: source?.sha ?? '', target: target?.fullName ?? '', targetSHA: target?.sha ?? '' };
  }
  private async execute(context: Context, request: GitBranchAction) {
    if (this.disposed || context.git !== this.context().git) return;
    let active = context;
    // Open a branch's existing checkout before starting an operation there, so
    // conflicts and Continue/Abort belong to the visible working copy.
    const ownedRef = request.action === 'merge' ? request.target : request.action === 'rebase' ? request.source : '';
    const owner = ownedRef && context.git.snapshot?.worktrees.find(tree => `refs/heads/${tree.branch}` === ownedRef);
    if (owner && !owner.blocked && !owner.prunable && owner.path !== context.git.snapshot?.path) {
      this.callbacks.openWorktree(owner.path);
      await tick();
      active = this.context();
      if (active.git.project !== context.git.project || (active.git.worktree !== owner.path && active.git.snapshot?.path !== owner.path)) return;
    }
    const deletingSelected = ['delete-branch', 'remove-worktree-branch'].includes(request.action) && active.ref === request.source;
    const path = await active.git.branchAction(request, deletingSelected ? '' : active.ref, active.limit);
    if (path === null && active.git === this.context().git && active.git.error) {
      const info = gitErrorInfo(active.git.error, request.action);
      store.setFlash(info.title, info.kind, {description:info.description,action:this.recovery()});
    }
    if (path !== null && active.git === this.context().git && deletingSelected && this.context().ref === request.source) this.callbacks.viewHistory('');
    if (path !== null && active.git === this.context().git && ['checkout', 'merge', 'rebase'].includes(request.action)) this.callbacks.openWorktree(path);
  }
  private action(action: string, source?: GitRef, target?: GitRef) { void this.execute(this.context(), this.branchRequest(action, source, target)); }
  recovery(): {label:string;run:()=>void} | undefined {
    const context=this.context(), git=context.git, request=git.failedAction;
    if (!git.error || !request) return;
    const info=gitErrorInfo(git.error,request.action);
    const source=git.snapshot?.refs.find(branch=>!branch.remote && branch.fullName===request.source);
    if (!source || this.mutationBlocked(source)) return;
    const valid=()=>!this.disposed && context.git===this.context().git && git.failedAction===request && !this.mutationBlocked(source) && git.snapshot?.refs.some(branch=>branch.fullName===source.fullName && branch.sha===source.sha);
    if (info.recovery==='pull' && ['push','publish'].includes(request.action) && this.upstream(source)) {
      return {label:'Pull changes',run:()=>{if(valid()) void this.execute(context,this.branchRequest('pull',source));}};
    }
    if (info.recovery==='merge' && request.action==='pull') {
      const upstream=this.upstream(source), full=upstream.startsWith('refs/')?upstream:`refs/remotes/${upstream}`;
      const remote=git.snapshot?.refs.find(branch=>branch.remote && branch.fullName===full);
      if(remote) return {label:'Merge remote changes',run:()=>{if(valid() && git.snapshot?.refs.some(branch=>branch.fullName===remote.fullName && branch.sha===remote.sha)) void this.execute(context,this.branchRequest('merge',remote,source));}};
    }
  }
  push(branch = this.current()) {
    if (!branch || this.mutationBlocked(branch)) return;
    this.close();
    const upstream = this.upstream(branch);
    if (!upstream && this.callbacks.publishBranch) this.callbacks.publishBranch(branch);
    else this.action('push', branch);
  }
  checkout(branch: GitRef) {
    this.close();
    const owner = this.owner(branch);
    if (owner && !owner.blocked && !owner.prunable && owner.path !== this.context().git.snapshot?.path) { this.callbacks.openWorktree(owner.path); return; }
    if (!this.mutationBlocked(branch)) this.action('checkout', branch);
  }
  private managedRemoval(tree: GitWorktree | undefined) {
    return !!tree && !!this.callbacks.removeManagedWorktree && (tree.blocked || !!this.callbacks.isManagedWorktree?.(tree));
  }
  private deletionBlocked(branch: GitRef) {
    if (this.managedRemoval(this.owner(branch))) return this.mutationBlocked();
    return this.mutationBlocked(branch) || branch.current || !!this.owner(branch) || (branch.remote && !this.destination(branch));
  }
  private deleteBranch(branch: GitRef) {
    if (this.deletionBlocked(branch)) return;
    const tree = this.owner(branch);
    if (tree && this.managedRemoval(tree)) { this.removeWorktree(tree, true); return; }
    this.close();
    const context = this.context(), request = this.branchRequest('delete-branch', branch);
    confirm.ask({
      title: branch.remote ? 'Delete remote branch?' : 'Delete branch?',
      subject: branch.name,
      body: branch.remote
        ? 'Deletes this branch from the remote repository for everyone.'
        : 'Deletes the local branch. The remote branch is kept.',
      confirmLabel: branch.remote ? 'Delete remote branch' : 'Delete branch',
      onConfirm: () => { if (context.git === this.context().git && !this.deletionBlocked(branch)) void this.execute(context, request); },
    });
  }
  private removalBlocked(tree: GitWorktree) {
    if (this.managedRemoval(tree)) return this.mutationBlocked();
    const {git, configuredPath} = this.context();
    return this.mutationBlocked() || tree.blocked || tree.prunable || tree.locked ||
      tree.path === git.snapshot?.worktrees[0]?.path || tree.path === (configuredPath || (!git.worktree ? git.snapshot?.path : ''));
  }
  private removeWorktree(tree: GitWorktree, deleteBranch: boolean) {
    if (this.removalBlocked(tree) || (deleteBranch && !tree.branch)) return;
    this.close();
    if (this.managedRemoval(tree)) {
      const context = this.context();
      this.callbacks.removeManagedWorktree?.(tree, deleteBranch, async () => {
        const root = projectGitState(context.git.project, '');
        const source = `refs/heads/${tree.branch}`;
        // Session cleanup may already have deleted an owned branch. A borrowed
        // branch gets a separate, tip-pinned Git deletion only after removal.
        if (this.context().git.project === context.git.project && this.context().git.worktree === tree.path) this.callbacks.openWorktree('');
        await root.load('', context.limit);
        if (root.error) throw new Error(`Session removed; repository refresh failed: ${root.error}`);
        if (deleteBranch && root.snapshot?.refs.some(ref => ref.fullName === source)) {
          const result = await root.branchAction({action:'delete-branch',source,sourceSHA:tree.head,target:'',targetSHA:''}, '', context.limit);
          if (result === null) throw new Error(`Worktree removed; local branch ${tree.branch} was kept: ${root.error}`);
        }
        if (this.context().git.project === context.git.project) {
          if (this.context().git.worktree === tree.path) this.callbacks.openWorktree('');
          if (deleteBranch && this.context().ref === source) this.callbacks.viewHistory('');
        }
      });
      return;
    }
    const context = this.context();
    const request = {action:deleteBranch ? 'remove-worktree-branch' : 'remove-worktree', source:tree.branch ? `refs/heads/${tree.branch}` : '', sourceSHA:tree.head, target:tree.path, targetSHA:''};
    confirm.ask({
      title: deleteBranch ? 'Remove worktree and branch?' : 'Remove worktree?',
      subject: deleteBranch ? `${tree.branch}\n${tree.path}` : tree.path,
      body: deleteBranch ? 'Removes this checkout and its local branch. Remote branches are kept.' : tree.branch ? 'Removes this checkout. Local and remote branches are kept.' : 'Removes this detached checkout. Commits reachable only from it may be lost.',
      confirmLabel: deleteBranch ? 'Remove worktree and branch' : 'Remove worktree',
      onConfirm: async () => {
        if (context.git !== this.context().git || this.removalBlocked(tree)) return;
        // Leave the checkout before deleting it, keeping refreshes and any
        // partial-failure error attached to a directory that still exists.
        if (tree.path === context.git.snapshot?.path) {
          this.callbacks.openWorktree('');
          await tick();
          const active = this.context();
          if (active.git.project !== context.git.project || active.git.worktree) return;
        }
        await this.execute(this.context(), request);
      },
    });
  }
  private integrateBranch(action: 'merge' | 'rebase', source: GitRef, target: GitRef) {
    this.close();
    const changedBranch = action === 'merge' ? target : source;
    if (changedBranch.remote || this.mutationBlocked(changedBranch)) return;
    this.action(action, source, target);
  }
  dropBranches(source: GitRef, target: GitRef, event: MouseEvent) {
    if (source.fullName === target.fullName) return;
    const destination = target.remote ? this.destination(target) : undefined;
    this.menu(`${source.name} → ${target.name}`, [
      { label: `Merge ${source.name} into ${target.name}`, icon: 'git-merge', disabled: target.remote || this.mutationBlocked(target), run: () => this.integrateBranch('merge', source, target) },
      { label: `Rebase ${source.name} onto ${target.name}`, icon: 'git-branch', disabled: source.remote || this.mutationBlocked(source), run: () => this.integrateBranch('rebase', source, target) },
      ...(this.callbacks.createPullRequest ? [{label:'Create PR…',icon:'git-pull-request' as const,disabled:!this.canCreatePR(source),run:()=>this.callbacks.createPullRequest?.(source,target)}] : []),
      ...(!source.remote && destination && this.callbacks.publishBranch ? [
        {label:`Push ${source.name} to ${target.name}…`,icon:'arrow-up' as const,disabled:this.mutationBlocked(source),run:() => this.callbacks.publishBranch?.(source,destination)},
        {label:`Set upstream to ${target.name}…`,icon:'cloud' as const,disabled:this.mutationBlocked(source),run:() => this.callbacks.publishBranch?.(source,{...destination,trackingOnly:true})},
      ] : []),
      ...(this.callbacks.compareBranches ? [{label:'Compare branches',icon:'git-compare-arrows' as const,separator:true,run:()=>this.callbacks.compareBranches?.(source,target)}] : []),
    ], event);
    if (!target.remote && !this.mutationBlocked(target)) void this.labelMerge(source, target);
  }
  private async labelMerge(source: GitRef, target: GitRef) {
    const menu = this.request, context = this.context();
    try {
      const fastForward = await ProjectGitService.CanFastForward(context.git.project, context.git.worktree, this.branchRequest('merge', source, target));
      if (!fastForward || this.disposed || this.request !== menu || this.context().git !== context.git) return;
      if (![source, target].every(ref => context.git.snapshot?.refs.some(current => current.fullName === ref.fullName && current.sha === ref.sha))) return;
      if (menu) menu.items[0].label = `Fast-forward ${target.name} to ${source.name}`;
    } catch { /* Keep the generic Merge label when the outcome cannot be proven. */ }
  }
  canDrop(source: GitRef, target: GitRef) {
    return source.fullName === target.fullName || (!target.remote && !this.mutationBlocked(target)) || (!source.remote && !this.mutationBlocked(source));
  }
  openBranch(branch: GitRef, event: MouseEvent) {
    const current = this.current(), busy = this.mutationBlocked();
    const remote = this.context().git.snapshot?.remotes?.filter(r => branch.name.startsWith(r.name + '/')).sort((a,b) => b.name.length-a.name.length)[0];
    const tree = this.context().git.snapshot?.worktrees.find(t => t.branch === branch.name);
    const items: GitMenuItem[] = branch.remote
      ? [{ label: 'Fetch remote', icon: 'refresh-cw', disabled: busy || !remote, run: () => { if (remote) this.fetch(remote.name); } }]
      : [
        { label: 'Pull changes', icon: 'arrow-down', disabled: this.mutationBlocked(branch) || !this.upstream(branch), run: () => this.action('pull', branch) },
        { label: this.upstream(branch) ? 'Push branch' : 'Publish branch…', icon: 'arrow-up', disabled: this.mutationBlocked(branch), run: () => this.push(branch) },
      ];
    items.push(
      { label: tree && tree.path !== this.context().git.snapshot?.path ? 'Open checkout' : branch.remote ? 'Check out tracking branch' : 'Check out branch', icon: 'git-branch', separator:true, disabled: (tree && tree.path !== this.context().git.snapshot?.path ? this.blocked() : busy) || branch.current || !!tree?.blocked || !!tree?.prunable, run: () => this.checkout(branch) },
      { label: 'Solo branch', icon: 'list', run: () => this.callbacks.viewHistory(branch.fullName) },
    );
    const create: GitMenuItem[] = [];
    if (this.callbacks.createPullRequest) create.push({label:'Create PR…',icon:'git-pull-request',disabled:!this.canCreatePR(branch),run:()=>this.callbacks.createPullRequest?.(branch)});
    if (this.callbacks.createBranch) create.push({label:'Create branch here…',icon:'git-branch',disabled:busy,run:()=>this.callbacks.createBranch?.(branch.sha,branch.name)});
    if (this.callbacks.createWorktree) create.push({label:'Create worktree…',icon:'folder',disabled:busy,run:()=>this.callbacks.createWorktree?.(branch)});
    if (create.length) { create[0].separator = true; items.push(...create); }
    if (current && current.fullName !== branch.fullName) items.push(
      { label: `Merge into ${current.name}`, icon: 'git-merge', separator: true, disabled: this.mutationBlocked(current), run: () => this.integrateBranch('merge', branch, current) },
      { label: `Rebase ${current.name} onto this`, icon: 'git-branch', disabled: this.mutationBlocked(current), run: () => this.integrateBranch('rebase', current, branch) },
    );
    const canSetUpstream = !branch.remote && !!this.callbacks.publishBranch;
    if (canSetUpstream) items.push({label:'Set upstream…',icon:'cloud',separator:true,disabled:this.mutationBlocked(branch),run:()=>this.tracking(branch)});
    items.push({ label: 'Copy branch name', icon: 'copy', separator: !canSetUpstream, run: () => void this.copy(branch.name) });
    items.push({ label: branch.remote ? 'Delete remote branch…' : 'Delete branch…', icon: 'trash-2', danger: true, separator: true, disabled: this.deletionBlocked(branch), run: () => this.deleteBranch(branch) });
    if (!branch.remote && tree) items.push({label:'Remove worktree and branch…',icon:'trash-2',danger:true,disabled:this.removalBlocked(tree),run:()=>this.removeWorktree(tree,true)});
    this.menu(branch.name, items, event);
  }

  fetch(remote: string) { const context = this.context(); void this.execute(context, { action: 'fetch', source: remote, target: '', sourceSHA: '', targetSHA: '' }); }
  openRemote(remote: GitRemote, event: MouseEvent) {
    this.menu(remote.name, [
      { label: 'Fetch remote', icon: 'refresh-cw', disabled: this.blocked(), run: () => this.fetch(remote.name) },
      { label: 'Copy repository name', icon: 'copy', run: () => void this.copy(remote.repo || remote.name) },
    ], event, remote.repo || remote.host);
  }
  openWorktree(tree: GitWorktree, event: MouseEvent) {
    const branch = this.context().git.snapshot?.refs.find(r => !r.remote && r.name === tree.branch);
    const disabled = tree.blocked || tree.prunable || this.mutationBlocked();
    const items: GitMenuItem[] = [{ label: 'Open checkout', icon: 'folder', disabled: tree.blocked || tree.prunable, run: () => this.callbacks.openWorktree(tree.path) }];
    if(branch&&this.callbacks.createPullRequest)items.push({label:'Create PR…',icon:'git-pull-request',disabled:disabled||!this.canCreatePR(branch),run:()=>this.callbacks.createPullRequest?.(branch)});
    if (branch) items.push(
      { label: 'Pull changes', icon: 'arrow-down', disabled:disabled || !this.upstream(branch), run: () => this.action('pull', branch) },
      { label: this.upstream(branch) ? 'Push branch' : 'Publish branch…', icon: 'arrow-up', disabled, run: () => this.push(branch) },
    );
    if (branch && this.callbacks.createWorktree) items.push({label:'Create worktree…',icon:'folder',disabled:this.mutationBlocked(),run:()=>this.callbacks.createWorktree?.(branch)});
    if (branch && this.callbacks.publishBranch) items.push({label:'Set upstream…',icon:'cloud',disabled,run:()=>this.tracking(branch)});
    if (tree.path === this.context().git.snapshot?.path) items.push(...this.recoveryItems());
    items.push({ label: 'Copy checkout path', icon: 'copy', run: () => void this.copy(tree.path) });
    items.push({label:'Remove worktree…',icon:'trash-2',danger:true,separator:true,disabled:this.removalBlocked(tree),run:()=>this.removeWorktree(tree,false)});
    if (tree.branch) items.push({label:'Remove worktree and branch…',icon:'trash-2',danger:true,disabled:this.removalBlocked(tree),run:()=>this.removeWorktree(tree,true)});
    this.menu(tree.branch || 'Detached HEAD', items, event, this.managedRemoval(tree) ? 'Managed worktree — removal uses its Lola session' : tree.path);
  }
  openCommit(commit: GitCommit, event: MouseEvent) {
    this.menu(commit.subject, [
      { label: 'View changes', icon: 'file-text', run: () => this.callbacks.viewCommit(commit.sha) },
      ...(this.callbacks.createBranch ? [{ label: 'Create branch here…', icon: 'git-branch' as const, disabled: this.blocked() || !!this.context().git.snapshot?.operation, run: () => this.callbacks.createBranch?.(commit.sha, commit.subject) }] : []),
      ...(this.callbacks.compareCommit ? [{ label: 'Compare with selected commit', icon: 'git-compare-arrows' as const, disabled: this.callbacks.canCompareCommit ? !this.callbacks.canCompareCommit(commit.sha) : false, run: () => this.callbacks.compareCommit?.(commit.sha) }] : []),
      ...(this.callbacks.compareWorking ? [{label:'Compare against working changes',icon:'git-compare-arrows' as const,run:() => this.callbacks.compareWorking?.(commit.sha)}] : []),
      {label:'Cherry-pick commit…',icon:'git-branch',separator:true,disabled:this.blocked() || !!this.context().git.snapshot?.operation || !this.context().git.snapshot?.head,run:() => this.chooseCommitParent('cherry-pick',commit,event)},
      {label:'Revert commit…',icon:'rotate-ccw',disabled:this.blocked() || !!this.context().git.snapshot?.operation || !this.context().git.snapshot?.head,run:() => this.chooseCommitParent('revert',commit,event)},
      { label: 'Copy commit SHA', icon: 'copy', run: () => void this.copy(commit.sha) },
    ], event, commit.sha.slice(0, 8));
  }
  private chooseCommitParent(action: 'cherry-pick' | 'revert', commit: GitCommit, event: MouseEvent) {
    if (commit.parents.length < 2) {this.confirmCommit(action,commit);return;}
    this.menu(action === 'revert' ? 'Revert merge relative to…' : 'Cherry-pick merge relative to…',commit.parents.map((sha,index)=>({label:`Parent ${index+1} · ${sha.slice(0,8)}${index===0?' (first parent)':''}`,icon:'git-branch',disabled:this.mutationBlocked(),run:()=>this.confirmCommit(action,commit,index+1)})),event);
  }
  private confirmCommit(action: 'cherry-pick' | 'revert', commit: GitCommit, mainline = 0) {
    this.close();
    const context = this.context(), data = context.git.snapshot;
    if (!data?.head || data.operation || context.git.busy) return;
    const request: GitBranchAction = {action,source:'',sourceSHA:commit.sha,target:data.branch ? `refs/heads/${data.branch}` : '',targetSHA:data.head,...(commit.parents.length > 1 ? {mainline} : {})};
    const label = action === 'cherry-pick' ? 'Cherry-pick commit' : 'Revert commit';
    confirm.ask({title:`${label}?`,body:`${commit.subject} (${commit.sha.slice(0,8)}) → ${data.branch || 'Detached HEAD'}`,detail:(action === 'cherry-pick' ? 'Apply this change as a new commit in the current checkout.' : 'Create a new commit that reverses this change, preserving existing history.') + (commit.parents.length > 1 ? ` This merge is interpreted relative to parent ${mainline} (${commit.parents[mainline-1].slice(0,8)}).` : '') + ' The checkout must be clean. Conflicts stay here to resolve, continue or abort.',confirmLabel:label,onConfirm:() => void this.execute(context,request)});
  }
  openRepository(event: MouseEvent) {
    const current = this.current(), git = this.context().git;
    this.menu(git.snapshot?.branch || 'Repository', [
      ...(current&&this.callbacks.createPullRequest?[{label:'Create PR…',icon:'git-pull-request' as const,disabled:!this.canCreatePR(current),run:()=>this.callbacks.createPullRequest?.(current)}]:[]),
      { label: 'Show all branch history', icon: 'list', run: () => this.callbacks.viewHistory('') },
      ...(this.callbacks.createBranch ? [{ label: 'New branch…', icon: 'git-branch' as const, disabled: this.blocked() || !git.snapshot?.head || !!git.snapshot?.operation, run: () => this.callbacks.createBranch?.(git.snapshot?.head ?? '', git.snapshot?.branch || 'Detached HEAD') }] : []),
      ...(current && this.callbacks.createWorktree ? [{label:'Create worktree…',icon:'folder' as const,disabled:this.mutationBlocked(),run:()=>this.callbacks.createWorktree?.(current)}] : []),
      ...this.recoveryItems(),
      { label: 'Refresh repository', icon: 'refresh-cw', disabled: !!git.busy || git.loading, run: () => { const c = this.context(); void c.git.load(c.ref, c.limit); } },
      { label: 'Fetch all remotes', icon: 'refresh-cw', disabled: this.blocked(), run: () => { const c = this.context(); void c.git.act('fetch', [], c.ref, c.limit); } },
      ...(current ? [
        { label: 'Pull changes', icon: 'arrow-down' as const, disabled: this.mutationBlocked(current) || !this.upstream(current), run: () => this.action('pull', current) },
        { label: this.upstream(current) ? 'Push branch' : 'Publish branch…', icon: 'arrow-up' as const, disabled: this.mutationBlocked(current), run: () => this.push(current) },
      ] : []),
      ...(current && this.callbacks.publishBranch ? [{label:'Set upstream…',icon:'cloud' as const,disabled:this.mutationBlocked(current),run:()=>this.tracking(current)}] : []),
      { label: 'Stash changes…', icon: 'archive', separator: true, disabled: this.mutationBlocked() || !git.snapshot?.head || !git.snapshot?.files.length, run: () => this.stash() },
      { label: 'Pop latest stash…', icon: 'archive-restore', disabled: this.mutationBlocked() || !!git.snapshot?.files.length || !git.stashes.length, run: () => this.pop() },
    ], event);
  }
  stash() {
    this.close(); const context = this.context(), head = context.git.snapshot?.head ?? '';
    confirm.ask({ title: 'Stash working changes?', body: `Save the staged, unstaged and untracked files in ${context.git.snapshot?.branch || 'this checkout'} to a new stash?`, detail: 'Ignored files stay in place. Stashed files are removed from the working copy until you apply or pop the stash.', confirmLabel: 'Stash changes', onConfirm: () => { if (context.git === this.context().git) void context.git.stashAction('stash', '', head, context.ref, context.limit); } });
  }
  pop() { const stash = this.context().git.stashes[0]; if (stash) this.confirmStash('pop', stash); }
  private confirmStash(action: 'apply' | 'pop' | 'drop', stash: GitStash) {
    this.close(); const context = this.context(), head = context.git.snapshot?.head ?? '';
    const label = { apply: 'Apply stash', pop: 'Pop stash', drop: 'Drop stash' }[action];
    confirm.ask({ title: `${label}?`, body: `${label} ${stash.selector} (${stash.sha.slice(0, 8)}): ${stash.subject}?`, detail: action === 'drop' ? 'This removes the saved stash. It does not change files in your checkout.' : action === 'pop' ? 'Apply to this clean checkout, then remove the saved stash only if applying succeeds. Conflicts keep the stash.' : 'Apply to this clean checkout and keep the saved stash.', confirmLabel: label, onConfirm: () => { if (context.git === this.context().git) void context.git.stashAction(action, stash.sha, head, context.ref, context.limit); } });
  }
  openStash(stash: GitStash, event: MouseEvent) {
    this.menu(stash.subject, [
      { label: 'Apply stash…', icon: 'archive-restore', disabled: this.mutationBlocked() || !!this.context().git.snapshot?.files.length, run: () => this.confirmStash('apply', stash) },
      { label: 'Pop stash…', icon: 'archive-restore', disabled: this.mutationBlocked() || !!this.context().git.snapshot?.files.length, run: () => this.confirmStash('pop', stash) },
      { label: 'Drop stash…', icon: 'trash-2', separator: true, danger: true, disabled: this.blocked(), run: () => this.confirmStash('drop', stash) },
    ], event, `${stash.selector} · ${stash.sha.slice(0, 8)}`);
  }
  openFile(file: GitFile, staged: boolean, paths: string[], event: MouseEvent) {
    this.menu(paths.length > 1 ? `${paths.length} selected files` : file.path, [
      {label:'View changes',icon:'file-text',run:()=>this.callbacks.inspectFile?.(file,staged)},
      {label:`${staged?'Unstage':'Stage'} ${paths.length>1?`${paths.length} files`:'file'}`,icon:staged?'minus':'plus',disabled:this.blocked(),run:()=>this.callbacks.stageFiles?.(paths,staged)},
      {label:paths.length>1?'Copy file paths':'Copy file path',icon:'copy',separator:true,run:()=>void this.copy(paths.join('\n'))},
    ],event);
  }
  private recoveryItems(): GitMenuItem[] {
    const data=this.context().git.snapshot,op=data?.operation;
    if (!op || !['merge','rebase','cherry-pick','revert'].includes(op)) return [];
    return [
      {label:`Continue ${op}`,icon:'play',separator:true,disabled:this.blocked() || !!data?.files.some(f=>f.conflict),run:()=>this.continueOperation()},
      ...(['rebase','cherry-pick','revert'].includes(op) ? [{label:'Skip current commit…',icon:'arrow-right' as const,disabled:this.blocked(),run:()=>this.skipOperation()}] : []),
      {label:`Abort ${op}…`,icon:'x',disabled:this.blocked(),run:()=>this.abortOperation()},
    ];
  }
  skipOperation() {
    const context=this.context(),data=context.git.snapshot,operation=data?.operation;
    if (!operation || !['rebase','cherry-pick','revert'].includes(operation) || this.blocked()) return;
    const request={action:'skip',source:operation,sourceSHA:data.head,target:'',targetSHA:''};
    confirm.ask({title:'Skip current commit?',body:`Skip the current commit in this ${operation}?`,detail:'Any conflict resolutions for this commit will be discarded. Git will continue with the remaining commits.',confirmLabel:'Skip commit',onConfirm:()=>void this.execute(context,request)});
  }
  continueOperation() {
    const context = this.context(), data = context.git.snapshot;
    if (data?.operation && ['merge', 'rebase', 'cherry-pick', 'revert'].includes(data.operation)) void this.execute(context, { action: 'continue', source: data.operation, sourceSHA: data.head, target: '', targetSHA: '', ...(data.operation === 'merge' && context.git.message.trim() ? {message:context.git.message} : {}) });
  }
  abortOperation() {
    const context = this.context(), operation = context.git.snapshot?.operation;
    if (!operation || !['merge', 'rebase', 'cherry-pick', 'revert'].includes(operation)) return;
    const request = { action: 'abort', source: operation, sourceSHA: context.git.snapshot?.head ?? '', target: '', targetSHA: '' };
    confirm.ask({ title: `Abort ${operation}?`, body: `Return this checkout to its state before the ${operation}?`, detail: 'Conflict resolutions made during this operation will be discarded.', confirmLabel: `Abort ${operation}`, onConfirm: () => void this.execute(context, request) });
  }
  private async copy(text: string) {
    try { await navigator.clipboard.writeText(text); } catch { this.context().git.error = 'Could not copy to the clipboard.'; }
  }
}
