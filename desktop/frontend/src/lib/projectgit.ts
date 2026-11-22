export type GitCommit = { sha: string; parents: string[]; subject: string; author: string; email: string; date: string; refs: string[] };
export type GitRef = { name: string; fullName: string; sha: string; remote: boolean; current: boolean; upstream?: string; ahead?: number; behind?: number };
export type GitRemote = { name: string; host: string; owner: string; repo: string; avatarUrl: string };
export type GitAuthorIdentity = { login: string; avatarUrl: string };
export type GitIdentities = { authors?: Record<string, GitAuthorIdentity | undefined> | null; remoteAvatars?: Record<string, string | undefined> | null };
export type GitWorktree = { path: string; branch: string; head: string; locked: boolean; prunable: boolean; blocked: boolean; changes?: number; added?: number; modified?: number; deleted?: number; conflicts?: number };
export type GitFile = { path: string; oldPath: string; index: string; worktree: string; untracked: boolean; conflict: boolean };
export type GitStash = { sha: string; selector: string; subject: string; date: string };
export type GitBranchAction = { action: string; source: string; target: string; sourceSHA: string; targetSHA: string; remote?: string; remoteBranch?: string; mainline?: number; message?: string; forceDeleteBranch?: boolean; forceRemoveWorktree?: boolean };
export type GitSnapshot = { operation?: string; mergeMessage?: string; path: string; branch: string; head: string; upstream: string; commits: GitCommit[]; refs: GitRef[]; remotes?: GitRemote[]; worktrees: GitWorktree[]; files: GitFile[]; signing: { enabled: boolean; format: string; key: string }; hasMore: boolean };
export type GitDiffFile = { path: string; oldPath?: string; status?: string; additions: number; deletions: number; binary?: boolean; tooLarge?: boolean; patch?: string };
export type GitDiff = { files: GitDiffFile[]; truncated?: boolean };
export type GitArea = 'working' | 'commit' | 'staged' | 'unstaged' | 'untracked' | 'conflict';

export type BranchNode =
  | { kind: 'branch'; name: string; path: string; branch: GitRef }
  | { kind: 'folder'; name: string; path: string; count: number; children: BranchNode[] };

/** Split only the display hierarchy; leaf refs keep their exact Git identity. */
export function branchTree(branches: GitRef[], prefix = ''): BranchNode[] {
  const folders = new Map<string, GitRef[]>();
  const leaves: BranchNode[] = [];
  for (const branch of branches) {
    if (!branch.name.startsWith(prefix)) continue;
    const name = branch.name.slice(prefix.length);
    const slash = name.indexOf('/');
    if (slash < 0) leaves.push({ kind: 'branch', name, path: branch.name, branch });
    else {
      const folder = name.slice(0, slash);
      const group = folders.get(folder) ?? [];
      group.push(branch);
      folders.set(folder, group);
    }
  }
  const groups: BranchNode[] = [...folders].sort(([a], [b]) => a.localeCompare(b)).map(([name, refs]) => ({
    kind: 'folder', name, path: prefix + name, count: refs.length, children: branchTree(refs, prefix + name + '/'),
  }));
  return [...groups, ...leaves.sort((a, b) => a.name.localeCompare(b.name))];
}

export function selectCommits(selected: string[], sha: string, additive: boolean): string[] {
  if (!additive) return [sha];
  if (selected.includes(sha)) return selected.filter(s => s !== sha);
  return [...selected.slice(-1), sha];
}

/** Match the tree's folder-first order in the flat list, at every path level. */
export function folderFirstFiles<T extends { path: string }>(files: T[]): T[] {
  return [...files].sort((a, b) => {
    const left = a.path.split('/'), right = b.path.split('/');
    for (let i = 0; i < Math.min(left.length, right.length); i++) {
      const folders = Number(i < right.length - 1) - Number(i < left.length - 1);
      if (folders) return folders;
      const name = left[i].localeCompare(right[i]);
      if (name) return name;
    }
    return 0;
  });
}

export function stagedFiles(files: GitFile[]): GitFile[] {
  return files.filter(f => !f.conflict && !f.untracked && f.index !== ' ' && f.index !== '?' && !!f.index);
}
export function unstagedFiles(files: GitFile[]): GitFile[] {
  return files.filter(f => f.conflict || f.untracked || (!!f.worktree && f.worktree !== ' ' && f.worktree !== '?'));
}

/**
 * Branch membership means reachability, not exclusive ownership. Only follow
 * loaded ancestry from loaded tips: a missing tip or parent is not evidence.
 */
export function branchesByCommit(commits: Pick<GitCommit, 'sha' | 'parents'>[], refs: GitRef[]): Map<string, GitRef[]> {
  const bySHA = new Map(commits.map(commit => [commit.sha, commit]));
  const result = new Map(commits.map(commit => [commit.sha, [] as GitRef[]]));
  const ordered = [...new Map(refs.map(ref => [ref.fullName, ref])).values()].sort((a, b) =>
    Number(b.current) - Number(a.current) || Number(a.remote) - Number(b.remote) ||
    (a.fullName < b.fullName ? -1 : a.fullName > b.fullName ? 1 : 0));
  for (const ref of ordered) {
    const visited = new Set<string>();
    const pending = [ref.sha];
    while (pending.length) {
      const sha = pending.pop()!;
      if (visited.has(sha)) continue;
      visited.add(sha);
      const commit = bySHA.get(sha);
      if (!commit) continue;
      result.get(sha)!.push(ref);
      pending.push(...(commit.parents ?? []));
    }
  }
  return result;
}

type Lane = { sha: string; color: number };
export type GraphEdge = { from: number; to: number; color: number; start: 'top' | 'node'; end: 'node' | 'bottom' };
export type GraphRow = { sha: string; lane: number; color: number; width: number; edges: GraphEdge[] };

/** A working-copy edge ends at HEAD, never at an unrelated newest commit. */
export function workingGraphConnection(rows: GraphRow[], head: string) {
  const index = rows.findIndex(row => row.sha === head);
  if (index < 0) return null;
  const target = rows[index].lane;
  const crossesTarget = (edge: GraphEdge) =>
    Math.min(edge.from, edge.to) <= target && target <= Math.max(edge.from, edge.to);
  const above = rows.slice(0, index);
  // The connector stops at HEAD's center. Only passing tracks can obstruct
  // it on that row; HEAD's own outgoing ancestry starts below it.
  const blocked = above.some(row => row.lane === target || row.edges.some(crossesTarget))
    || rows[index].edges.some(edge => edge.start === 'top' && edge.end === 'bottom' && crossesTarget(edge));
  const lane = blocked ? Math.max(rows[index].width, ...above.map(row => row.width)) : target;
  return { index, lane, target };
}

export type WorktreeChange = { tree: GitWorktree; lane: number };

/**
 * Work in progress in the OTHER checkouts, keyed by the commit each one sits on.
 * Each gets a lane of its own to the right of everything its commit's row
 * draws (and of the active checkout's working edge when that runs past), so its
 * edge never passes through a commit that does not own it. A checkout whose
 * HEAD is outside the loaded history is left out rather than attached to a
 * wrong commit.
 */
export function worktreeChanges(rows: GraphRow[], worktrees: GitWorktree[], activePath: string, working: { index: number; lane: number } | null): Map<string, WorktreeChange[]> {
  const index = new Map(rows.map((row, i) => [row.sha, i]));
  const out = new Map<string, WorktreeChange[]>();
  for (const tree of worktrees) {
    const at = index.get(tree.head);
    if (at === undefined || !tree.changes || tree.blocked || tree.prunable || tree.path === activePath) continue;
    const list = out.get(tree.head) ?? [];
    const base = Math.max(rows[at].width, working && at <= working.index ? working.lane + 1 : 0);
    list.push({ tree, lane: base + list.length });
    out.set(tree.head, list);
  }
  return out;
}

export function defaultBranchTip(refs: GitRef[], branch: string, commits: Pick<GitCommit, 'sha'>[]): string | undefined {
  const names = branch ? [branch] : ['main', 'master'];
  for (const name of names) {
    const local = refs.find(ref => ref.fullName === 'refs/heads/' + name);
    if (local) {
      if (!local.ahead && (local.behind ?? 0) > 0) {
        const upstream = refs.find(ref => ref.remote && ref.fullName === local.upstream);
        if (upstream && commits.some(commit => commit.sha === upstream.sha)) return upstream.sha;
      }
      return local.sha;
    }
    const tip = refs.find(ref => ref.fullName === 'refs/remotes/origin/' + name)
      ?? refs.find(ref => ref.remote && ref.name.slice(ref.name.indexOf('/') + 1) === name);
    if (tip) return tip.sha;
  }
}

/** Translate the pending ancestry tracks into columns, leaving the first
 * column empty above the default tip and between unrelated histories. */
function graphColumns(lanes: Lane[], pinned: boolean, nextMain: string | undefined) {
  let side = pinned ? 1 : 0;
  return lanes.map(entry => pinned && entry.color === 0 && entry.sha === nextMain ? 0 : side++);
}

export function commitGraph(commits: Pick<GitCommit, 'sha' | 'parents'>[], defaultTip?: string): GraphRow[] {
  // Resolve convergence before drawing. In topological order all children are
  // seen before their parent, so the earliest lineage wins a shared first-parent
  // ancestor even when the topic side is listed before the main side of a merge.
  const colors = new Map<string, number>();
  const pinned = !!defaultTip;
  let nextMain = defaultTip;
  if (defaultTip) colors.set(defaultTip, 0);
  let nextColor = pinned ? 1 : 0;
  for (const commit of commits) {
    if (!colors.has(commit.sha)) colors.set(commit.sha, nextColor++);
    const color = colors.get(commit.sha)!;
    [...new Set(commit.parents ?? [])].forEach((sha, i) => {
      if (i === 0) colors.set(sha, Math.min(colors.get(sha) ?? color, color));
      else if (!colors.has(sha)) colors.set(sha, nextColor++);
    });
  }
  let lanes: Lane[] = [];
  return commits.map(commit => {
    const color = colors.get(commit.sha)!;
    const existing = lanes.findIndex(l => l.sha === commit.sha && l.color === color);
    const before = existing < 0 ? [...lanes, { sha: commit.sha, color }] : lanes;
    const lane = existing < 0 ? lanes.length : existing;
    const columns = graphColumns(before, pinned, nextMain);
    const node = columns[lane];
    const after = before.filter(entry => entry.sha !== commit.sha);
    const parents = [...new Set(commit.parents ?? [])];
    // Carry a branch's own track down to its first parent. Several tracks
    // may end at the same SHA and should meet only at that commit's node.
    const targets = parents.map((sha, i) => {
      const trackColor = i === 0 ? color : colors.get(sha)!;
      const pending = after.find(entry => entry.sha === sha && entry.color === trackColor);
      if (pending) return pending;
      const entry = { sha, color: trackColor };
      if (i === 0) after.splice(before.slice(0, lane).filter(l => l.sha !== commit.sha).length, 0, entry);
      else after.push(entry);
      return entry;
    });
    if (commit.sha === nextMain) nextMain = parents[0];
    const nextColumns = graphColumns(after, pinned, nextMain);
    const edges: GraphEdge[] = [];
    lanes.forEach((entry, i) => {
      if (entry.sha === commit.sha) {
        edges.push({ from: columns[i], to: node, color: entry.color, start: 'top', end: 'node' });
      } else {
        edges.push({ from: columns[i], to: nextColumns[after.indexOf(entry)], color: entry.color, start: 'top', end: 'bottom' });
      }
    });
    targets.forEach(target => {
      edges.push({ from: node, to: nextColumns[after.indexOf(target)], color: target.color, start: 'node', end: 'bottom' });
    });
    lanes = after;
    return { sha: commit.sha, lane: node, color, width: Math.max(...columns, ...nextColumns) + 1, edges };
  });
}

export const graphColors = ['var(--color-info)', 'var(--color-magenta)', 'var(--color-good)', 'var(--color-orange)', 'var(--color-accent)', 'var(--color-warn)'];
export function graphColor(color: number): string { return graphColors[color % graphColors.length]; }

export const GRAPH_LANE_WIDTH = 24;
export const GRAPH_NODE_X = 16;

/** SVG paths meet at row boundaries so adjacent rows form continuous lanes. */
export function graphPath(edge: GraphEdge, height = 38): string {
  const x1 = GRAPH_NODE_X + edge.from * GRAPH_LANE_WIDTH, x2 = GRAPH_NODE_X + edge.to * GRAPH_LANE_WIDTH;
  const y1 = edge.start === 'top' ? 0 : height / 2;
  const y2 = edge.end === 'node' ? height / 2 : height;
  if (x1 === x2) return `M${x1} ${y1}V${y2}`;
  const direction = Math.sign(x2 - x1);
  const radius = Math.min(Math.abs(x2 - x1) / 2, y2 - y1);
  // Leave the side of a commit before rounding down into its parent's lane.
  if (edge.start === 'node') return `M${x1} ${y1}H${x2 - direction * radius}Q${x2} ${y1} ${x2} ${y1 + radius}V${y2}`;
  if (edge.end === 'node') return `M${x1} ${y1}V${y2 - radius}Q${x1} ${y2} ${x1 + direction * radius} ${y2}H${x2}`;
  // Through lanes remain vertical at both row boundaries.
  const bend = (y2 - y1) * 0.75;
  return `M${x1} ${y1}C${x1} ${y1 + bend} ${x2} ${y2 - bend} ${x2} ${y2}`;
}
