import { createAttachmentKey, fromAction } from 'svelte/attachments';
import { tooltip } from './tooltip';
import { exactTime } from './time';
import type { GitCommit, GitRef } from './projectgit';

const commitTooltipKey = createAttachmentKey();
type AttachmentProps = { [key: symbol]: ReturnType<typeof fromAction<HTMLElement, Parameters<typeof tooltip>[1]>> };
const attachments = new WeakMap<GitCommit, { signature: string; props: AttachmentProps }>();

/** Attach to the shared Button's DOM element without adding a wrapper to the graph. */
export function gitCommitTooltip(commit: GitCommit, branches: GitRef[], checkouts: string[] = []) {
  const ordered = [...new Map(branches.map(branch => [branch.fullName, branch])).values()].sort((a, b) => Number(b.current) - Number(a.current) || Number(a.remote) - Number(b.remote) || a.name.localeCompare(b.name));
  const grouped = ordered.filter(branch => {
    if (!branch.remote) return true;
    if (branch.name.endsWith('/HEAD') && ordered.some(other => other.fullName !== branch.fullName && !other.name.endsWith('/HEAD') && other.sha === branch.sha)) return false;
    return !ordered.some(local => !local.remote && local.sha === branch.sha && branch.name.slice(branch.name.indexOf('/') + 1) === local.name);
  });
  const active = checkouts.some(path => path.startsWith('Active checkout:'));
  const others = checkouts.filter(path => !path.startsWith('Active checkout:')).length;
  const date = new Date(commit.date);
  const content = {
    heading: commit.subject,
    text: commit.author,
    commit: {
      author: commit.author,
      date: Number.isNaN(date.getTime()) ? commit.date : date.toLocaleString(undefined, { year: date.getFullYear() === new Date().getFullYear() ? undefined : 'numeric', month: 'short', day: 'numeric', hour: '2-digit', minute: '2-digit' }),
      exactDate: exactTime(commit.date),
      sha: commit.sha,
      branches: grouped.slice(0, 3).map(branch => ({ name: branch.name, current: branch.current, remote: branch.remote })),
      moreBranches: grouped.slice(3).map(branch => branch.name),
      checkoutLabel: active ? `Checked out here${others ? ` · ${others} ${others === 1 ? 'worktree' : 'worktrees'}` : ''}` : others ? `${others} ${others === 1 ? 'worktree' : 'worktrees'}` : undefined,
      checkoutDetail: checkouts.join('; ') || undefined,
    },
    delay: 500,
    anchor: 'pointer' as const,
    hoverTarget: '.commit-copy',
  };
  const signature = JSON.stringify(content);
  const cached = attachments.get(commit);
  if (cached?.signature === signature) return cached.props;
  // A fresh attachment function destroys the previous action, cancelling its
  // pending hover timer. Row styling and newly mapped checkout arrays must not
  // replace it while the pointer is stationary. Weak keys expire with history.
  const props = { [commitTooltipKey]: fromAction(tooltip, () => content) };
  attachments.set(commit, { signature, props });
  return props;
}
