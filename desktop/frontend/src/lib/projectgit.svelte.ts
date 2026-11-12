import { ProjectGitService } from '@bindings/desktop';
import type { GitArea, GitDiff, GitSnapshot, GitAuthorIdentity, GitStash, GitBranchAction, GitIdentities } from './projectgit';

/** Shared per-checkout state keeps operation locks and commit drafts across view mounts. */
export class ProjectGitState {
  stashes = $state<GitStash[]>([]);
  authors = $state<Record<string, GitAuthorIdentity>>({});
  remoteAvatars = $state<Record<string, string>>({});
  identityLoading = $state<Record<string, boolean>>({});
  remoteIdentityLoading = $state(false);
  private resolvedIdentities = new Set<string>();
  private refreshedRemotes = new Set<string>();
  snapshot = $state<GitSnapshot | null>(null);
  diff = $state<GitDiff | null>(null);
  loading = $state(false);
  diffLoading = $state(false);
  busy = $state('');
  error = $state('');
  diffError = $state('');
  notice = $state('');
  message = $state('');
  amend = $state(false);
  amendHead = $state('');
  amendLoading = $state(false);
  private amendSequence = 0;
  get subject(): string { return this.message.split('\n', 1)[0]; }
  set subject(value: string) { this.message = value + (this.body ? `\n\n${this.body}` : ''); }
  get body(): string { return this.message.includes('\n') ? this.message.slice(this.message.indexOf('\n') + 1).replace(/^\n/, '') : ''; }
  set body(value: string) { this.message = this.subject + (value ? `\n\n${value}` : ''); }
  private loadSequence = 0;
  private diffSequence = 0;
  constructor(readonly project: string, readonly worktree: string) {}

  async setAmend(value: boolean) {
    this.amend = value;
    this.amendHead = value ? this.snapshot?.head ?? '' : '';
    const sequence = ++this.amendSequence;
    this.amendLoading = false;
    const head = this.snapshot?.head;
    if (!value || this.message.trim() || !head) return;
    this.amendLoading = true;
    try {
      const message = await ProjectGitService.LastCommitMessage(this.project, this.worktree, head);
      if (sequence === this.amendSequence && this.amend && !this.message.trim() && this.snapshot?.head === head) this.message = message.replace(/\n$/, '');
    } catch (error) {
      if (sequence === this.amendSequence && this.amend) this.error = String(error);
    } finally {
      if (sequence === this.amendSequence) this.amendLoading = false;
    }
  }

  async load(ref = '', limit = 200) {
    const sequence = ++this.loadSequence;
    this.identityLoading = {};
    this.remoteIdentityLoading = false;
    this.loading = true;
    this.error = '';
    // Keep branch rows mounted while a different ref loads, so double-click
    // checkout still receives its second click. Stores are isolated by checkout.
    try {
      const result = await ProjectGitService.Snapshot(this.project, this.worktree, ref, limit);
      if (sequence === this.loadSequence) this.snapshot = {
        ...result, commits: (result.commits ?? []).map(c => ({ ...c, parents: c.parents ?? [], refs: c.refs ?? [] })),
        remotes: result.remotes ?? [], refs: result.refs ?? [], worktrees: result.worktrees ?? [], files: result.files ?? [],
      };
      if (sequence === this.loadSequence) { void this.loadIdentities(sequence, (result.commits ?? []).map(c => c.sha)); void this.loadStashes(sequence); }
    } catch (error) {
      if (sequence === this.loadSequence) this.error = String(error);
    } finally {
      if (sequence === this.loadSequence) this.loading = false;
    }
  }

  private async loadStashes(sequence: number) {
    try {
      const stashes = await ProjectGitService.Stashes(this.project, this.worktree);
      if (sequence === this.loadSequence) this.stashes = stashes ?? [];
    } catch { /* Stash metadata must not block history; action failures are surfaced separately. */ }
  }

  async branchAction(request: GitBranchAction, ref = '', limit = 200): Promise<string | null> {
    if (this.busy) return null;
    this.busy = request.action; this.error = ''; this.notice = '';
    try {
      const result = await ProjectGitService.BranchAction(this.project, this.worktree, request);
      await this.load(ref, limit);
      this.notice = ({ checkout: 'Branch checked out.', pull: 'Branch updated.', publish: 'Branch published; upstream configured.', push: 'Branch pushed.', fetch: 'Remote refs updated.', 'cherry-pick': 'Commit cherry-picked.', revert: 'Commit reverted.', merge: 'Branches merged.', rebase: 'Branch rebased.', continue: 'Operation continued.', abort: 'Operation aborted.' } as Record<string, string>)[request.action] ?? 'Repository updated.';
      return result.path;
    } catch (error) {
      // Merge/rebase failures can leave resolvable conflicts. Refresh those facts
      // without losing the actionable Git error to load()'s clearing behavior.
      await this.load(ref, limit);
      this.error = String(error);
      return null;
    } finally { this.busy = ''; }
  }

  async createBranch(name: string, startSHA: string, checkout: boolean, ref = '', limit = 200): Promise<string | null> {
    if (this.busy) return null;
    this.busy = 'create-branch'; this.error = ''; this.notice = '';
    try {
      const result = await ProjectGitService.CreateBranch(this.project, this.worktree, name, startSHA, checkout);
      await this.load(checkout ? '' : ref, limit);
      this.notice = checkout ? `Created and checked out ${name}.` : `Created ${name}.`;
      return result.path;
    } catch (error) {
      // A post-checkout hook can fail after Git switched successfully.
      await this.load(ref, limit);
      this.error = String(error);
      return null;
    } finally { this.busy = ''; }
  }

  async createWorktree(source: string, sha: string, path: string, newBranch: string, ref = '', limit = 200): Promise<string | null> {
    if (this.busy) return null;
    this.busy = 'create-worktree'; this.error = ''; this.notice = '';
    try {
      const result = await ProjectGitService.CreateWorktree(this.project, this.worktree, source, sha, path, newBranch);
      await this.load(ref, limit);
      this.notice = 'Worktree created.';
      return result.path;
    } catch (error) {
      await this.load(ref, limit);
      this.error = String(error);
      return null;
    } finally { this.busy = ''; }
  }

  async stashAction(action: 'stash' | 'apply' | 'pop' | 'drop', sha: string, expectedHead: string, ref = '', limit = 200): Promise<boolean> {
    if (this.busy) return false;
    this.busy = action; this.error = ''; this.notice = '';
    try {
      if (action === 'stash') await ProjectGitService.StashPush(this.project, this.worktree, 'Saved from Lola', expectedHead);
      else if (action === 'drop') await ProjectGitService.StashDrop(this.project, this.worktree, sha);
      else await ProjectGitService.StashApply(this.project, this.worktree, sha, expectedHead, action === 'pop');
      await this.load(ref, limit);
      this.notice = { stash: 'Changes stashed.', apply: 'Stash applied.', pop: 'Stash popped.', drop: 'Stash dropped.' }[action];
      return true;
    } catch (error) {
      await this.load(ref, limit);
      this.error = String(error);
      return false;
    } finally { this.busy = ''; }
  }

  private applyIdentities(result: GitIdentities | undefined) {
    if (result?.authors) {
      const authors = { ...this.authors };
      for (const [sha, author] of Object.entries(result.authors)) if (author) {
        const previous = authors[sha];
        authors[sha] = { login: author.login || previous?.login || '', avatarUrl: author.avatarUrl || previous?.avatarUrl || '' };
      }
      this.authors = authors;
    }
    if (result?.remoteAvatars) {
      const remotes = { ...this.remoteAvatars };
      for (const [name, source] of Object.entries(result.remoteAvatars)) if (source) remotes[name] = source;
      this.remoteAvatars = remotes;
    }
  }

  private async loadIdentities(sequence: number, shas: string[]) {
    // Disk cache paints first. Optional refresh never delays history or replaces a
    // usable cached image with an empty result during an offline/auth failure.
    const missing = [...new Set(shas)].filter(sha => !this.authors[sha] && !this.resolvedIdentities.has(sha));
    const remotes = (this.snapshot?.remotes ?? []).map(remote => remote.name);
    const needsRemotes = remotes.some(name => !this.refreshedRemotes.has(name));
    this.identityLoading = Object.fromEntries(missing.map(sha => [sha, true]));
    this.remoteIdentityLoading = needsRemotes;
    if (!missing.length && !needsRemotes) return;
    const batches = missing.length ? Array.from({ length: Math.ceil(missing.length / 200) }, (_, index) => missing.slice(index * 200, (index + 1) * 200)) : [[]];
    try {
      for (const batch of batches) {
        if (sequence !== this.loadSequence) return;
        try {
          const cached = await ProjectGitService.CachedIdentities(this.project, batch);
          if (sequence !== this.loadSequence) return;
          this.applyIdentities(cached);
        } catch { /* A missing/unreadable cache does not prevent the live lookup. */ }
        if (sequence !== this.loadSequence) return;
        try {
          const fresh = await ProjectGitService.Identities(this.project, batch);
          if (sequence !== this.loadSequence) return;
          this.applyIdentities(fresh);
          for (const sha of batch) this.resolvedIdentities.add(sha);
          for (const name of remotes) this.refreshedRemotes.add(name);
        } catch { /* Keep cached identities and let the repository remain usable. */ }
        if (sequence === this.loadSequence) {
          const pending = { ...this.identityLoading };
          for (const sha of batch) delete pending[sha];
          this.identityLoading = pending;
        }
      }
    } finally {
      if (sequence === this.loadSequence) { this.identityLoading = {}; this.remoteIdentityLoading = false; }
    }
  }

  clearDiff() {
    this.diffSequence++;
    this.diff = null;
    this.diffError = '';
    this.diffLoading = false;
  }

  async loadDiff(area: GitArea, from = '', to = '', path = '') {
    const sequence = ++this.diffSequence;
    this.diffLoading = true;
    this.diff = null;
    this.diffError = '';
    try {
      const result = await ProjectGitService.Diff(this.project, this.worktree, from, to, area, path);
      if (sequence === this.diffSequence) this.diff = { ...result, files: result.files ?? [] };
    } catch (error) {
      if (sequence === this.diffSequence) this.diffError = String(error);
    } finally {
      if (sequence === this.diffSequence) this.diffLoading = false;
    }
  }

  async act(action: 'stage' | 'unstage' | 'commit' | 'amend' | 'fetch' | 'pull' | 'push', paths: string[] = [], ref = '', limit = 200, expectedHead = ''): Promise<boolean> {
    if (this.busy) return false;
    this.busy = action;
    this.error = '';
    this.notice = '';
    try {
      switch (action) {
        case 'stage': await ProjectGitService.Stage(this.project, this.worktree, paths); break;
        case 'unstage': await ProjectGitService.Unstage(this.project, this.worktree, paths); break;
        case 'commit': await ProjectGitService.Commit(this.project, this.worktree, this.message); this.message = ''; this.amend = false; this.amendHead = ''; break;
        case 'amend': await ProjectGitService.Amend(this.project, this.worktree, this.message, expectedHead); this.message = ''; this.amend = false; this.amendHead = ''; break;
        case 'fetch': await ProjectGitService.Fetch(this.project, this.worktree); break;
        case 'pull': await ProjectGitService.Pull(this.project, this.worktree); break;
        case 'push': await ProjectGitService.Push(this.project, this.worktree); break;
      }
      this.notice = { stage: 'Files staged.', unstage: 'Files unstaged.', commit: 'Commit created.', amend: 'Commit amended.', fetch: 'Remote refs updated.', pull: 'Branch is up to date.', publish: 'Branch published; upstream configured.', push: 'Branch pushed.' }[action];
      await this.load(ref, limit);
      return true;
    } catch (error) {
      this.error = String(error);
      return false;
    } finally { this.busy = ''; }
  }
}

const checkouts = new Map<string, ProjectGitState>();
export function projectGitState(project: string, worktree = ''): ProjectGitState {
  const configured = checkouts.get(JSON.stringify([project, '']));
  if (worktree && configured?.snapshot?.path === worktree) return configured;
  const key = JSON.stringify([project, worktree]);
  let state = checkouts.get(key);
  if (!state) { state = new ProjectGitState(project, worktree); checkouts.set(key, state); }
  return state;
}
