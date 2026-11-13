import { tick } from 'svelte';
import { confirm } from './confirm.svelte';
import type { ProjectGitState } from './projectgit.svelte';
import type { GitBranchAction, GitCommit, GitRef, GitRemote, GitStash, GitWorktree, GitFile } from './projectgit';
import type { IconName } from './icons';

type Context = { git: ProjectGitState; ref: string; limit: number };
export type GitDestination = { remote: string; name: string; trackingOnly?: boolean };
type Callbacks = { createPullRequest?: (branch:GitRef,target?:GitRef)=>void; viewHistory: (ref: string) => void; openWorktree: (path: string) => void; viewCommit: (sha: string) => void; inspectFile?: (file: GitFile, staged: boolean) => void; stageFiles?: (paths: string[], staged: boolean) => void; createWorktree?: (branch: GitRef) => void; compareWorking?: (sha: string) => void; publishBranch?: (branch: GitRef, destination?: GitDestination) => void; createBranch?: (sha: string, label: string) => void; compareCommit?: (sha: string) => void; canCompareCommit?: (sha: string) => boolean };
export type GitMenuItem = { label: string; icon: IconName; run: () => void; disabled?: boolean; danger?: boolean; separator?: boolean };
export type GitMenuRequest = { title: string; subtitle?: string; x: number; y: number; items: GitMenuItem[]; anchor: HTMLElement | null };

/** One controller per mounted repository. Requests capture exact refs and SHA facts. */
export class GitActions {
  request = $state<GitMenuRequest | null>(null);
  constructor(private context: () => Context, private callbacks: Callbacks) {}
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
    if (context.git !== this.context().git) return;
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
    const path = await active.git.branchAction(request, active.ref, active.limit);
    if (path !== null && active.git === this.context().git && ['checkout', 'merge', 'rebase'].includes(request.action)) this.callbacks.openWorktree(path);
  }
  private action(action: string, source?: GitRef, target?: GitRef) { void this.execute(this.context(), this.branchRequest(action, source, target)); }
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
  private confirmBranch(action: 'merge' | 'rebase', source: GitRef, target: GitRef) {
    this.close();
    const context = this.context(), request = this.branchRequest(action, source, target);
    const merge = action === 'merge';
    confirm.ask({
      title: merge ? 'Merge branches?' : 'Rebase branch?',
      body: merge ? `Merge ${source.name} (${source.sha.slice(0, 8)}) into ${target.name} (${target.sha.slice(0, 8)})?` : `Rebase ${source.name} (${source.sha.slice(0, 8)}) onto ${target.name} (${target.sha.slice(0, 8)})?`,
      detail: merge ? `The ${target.name} checkout must be clean. Conflicts stay in that checkout for you to resolve.` : `This rewrites commits on ${source.name}. If published, collaborators still have the original history. The checkout must be clean; no automatic stash or force push is performed.`,
      confirmLabel: merge ? 'Merge branches' : 'Rebase branch',
      onConfirm: () => void this.execute(context, request),
    });
  }
  dropBranches(source: GitRef, target: GitRef, event: MouseEvent) {
    if (source.fullName === target.fullName) return;
    const destination = target.remote ? this.destination(target) : undefined;
    this.menu(`${source.name} → ${target.name}`, [
      ...(!source.remote && destination && this.callbacks.publishBranch ? [
        {label:`Push ${source.name} to ${target.name}…`,icon:'arrow-up' as const,disabled:this.mutationBlocked(source),run:() => this.callbacks.publishBranch?.(source,destination)},
        {label:`Set upstream to ${target.name}…`,icon:'cloud' as const,disabled:this.mutationBlocked(source),run:() => this.callbacks.publishBranch?.(source,{...destination,trackingOnly:true})},
      ] : []),
      ...(this.callbacks.createPullRequest ? [{label:'Create PR…',icon:'git-pull-request' as const,disabled:!this.canCreatePR(source),run:()=>this.callbacks.createPullRequest?.(source,target)}] : []),
      { label: `Merge ${source.name} into ${target.name}…`, icon: 'git-merge', disabled: target.remote || this.mutationBlocked(target), run: () => this.confirmBranch('merge', source, target) },
      { label: `Rebase ${source.name} onto ${target.name}…`, icon: 'git-branch', disabled: source.remote || this.mutationBlocked(source), run: () => this.confirmBranch('rebase', source, target) },
      { label: 'Cancel', icon: 'x', run: () => this.close(true) },
    ], event);
  }
  canDrop(source: GitRef, target: GitRef) {
    return source.fullName === target.fullName || (!target.remote && !this.mutationBlocked(target)) || (!source.remote && !this.mutationBlocked(source));
  }
  openBranch(branch: GitRef, event: MouseEvent) {
    const current = this.current(), busy = this.mutationBlocked();
    const remote = this.context().git.snapshot?.remotes?.filter(r => branch.name.startsWith(r.name + '/')).sort((a,b) => b.name.length-a.name.length)[0];
    const tree = this.context().git.snapshot?.worktrees.find(t => t.branch === branch.name);
    const items: GitMenuItem[] = [
      { label: 'View branch history', icon: 'list', run: () => this.callbacks.viewHistory(branch.fullName) },
      { label: tree && tree.path !== this.context().git.snapshot?.path ? 'Open checkout' : branch.remote ? 'Check out tracking branch' : 'Check out branch', icon: 'git-branch', disabled: (tree && tree.path !== this.context().git.snapshot?.path ? this.blocked() : busy) || branch.current || !!tree?.blocked || !!tree?.prunable, run: () => this.checkout(branch) },
    ];
    if(this.callbacks.createPullRequest)items.push({label:'Create PR…',icon:'git-pull-request',disabled:!this.canCreatePR(branch),run:()=>this.callbacks.createPullRequest?.(branch)});
    if (this.callbacks.createBranch) items.push({ label: 'Create branch here…', icon: 'git-branch', disabled: busy || !!this.context().git.snapshot?.operation, run: () => this.callbacks.createBranch?.(branch.sha, branch.name) });
    if (this.callbacks.createWorktree) items.push({label:'Create worktree…',icon:'folder',disabled:busy || !!this.context().git.snapshot?.operation,run:() => this.callbacks.createWorktree?.(branch)});
    if (branch.remote) items.push({ label: 'Fetch remote', icon: 'refresh-cw', disabled: busy || !remote, run: () => { if (remote) this.fetch(remote.name); } });
    else items.push(
      { label: 'Pull changes', icon: 'arrow-down', disabled: this.mutationBlocked(branch) || !this.upstream(branch), run: () => this.action('pull', branch) },
      { label: this.upstream(branch) ? 'Push branch' : 'Publish branch…', icon: 'arrow-up', disabled: this.mutationBlocked(branch), run: () => this.push(branch) },
      ...(this.callbacks.publishBranch ? [{label:'Set upstream…',icon:'cloud' as const,disabled:this.mutationBlocked(branch),run:()=>this.tracking(branch)}] : []),
    );
    if (current && current.fullName !== branch.fullName) items.push(
      { label: `Merge into ${current.name}…`, icon: 'git-merge', separator: true, disabled: this.mutationBlocked(current), run: () => this.confirmBranch('merge', branch, current) },
      { label: `Rebase ${current.name} onto this…`, icon: 'git-branch', disabled: this.mutationBlocked(current), run: () => this.confirmBranch('rebase', current, branch) },
    );
    items.push({ label: 'Copy branch name', icon: 'copy', separator: true, run: () => void this.copy(branch.name) });
    this.menu(branch.name, items, event, branch.current ? 'Checked out here' : branch.remote ? 'Remote branch' : 'Local branch');
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
    this.menu(tree.branch || 'Detached HEAD', items, event, tree.blocked ? 'Managed sandbox — open its session to act' : tree.path);
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
    if (data?.operation && ['merge', 'rebase', 'cherry-pick', 'revert'].includes(data.operation)) void this.execute(context, { action: 'continue', source: data.operation, sourceSHA: data.head, target: '', targetSHA: '' });
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
