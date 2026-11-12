import { describe, expect, it } from 'vitest';
import { workingGraphConnection, branchTree, branchesByCommit, commitGraph, graphPath, selectCommits, stagedFiles, unstagedFiles, type GitFile, type GitRef } from './projectgit';

describe('Git history selection and topology', () => {
  it('connects working changes only to HEAD and reserves a lane past unrelated newer commits', () => {
    const rows=commitGraph([{sha:'newer',parents:['base']},{sha:'head',parents:['base']},{sha:'base',parents:[]}]);
    expect(workingGraphConnection(rows,'newer')).toEqual({index:0,lane:rows[0].lane,target:rows[0].lane});
    const link=workingGraphConnection(rows,'head')!;
    expect(link.index).toBe(1);expect(link.target).toBe(rows[1].lane);
    expect(link.lane).toBeGreaterThanOrEqual(Math.max(rows[0].width,rows[1].width));
    expect(workingGraphConnection(rows,'hidden')).toBeNull();
  });
  it('limits modifier selection to two commits and toggles existing selection', () => {
    expect(selectCommits(['a'], 'b', true)).toEqual(['a', 'b']);
    expect(selectCommits(['a', 'b'], 'c', true)).toEqual(['b', 'c']);
    expect(selectCommits(['a', 'b'], 'a', true)).toEqual(['b']);
    expect(selectCommits(['a', 'b'], 'c', false)).toEqual(['c']);
  });
  it('joins both parents of a merge and keeps independent lanes continuous', () => {
    const graph = commitGraph([{ sha: 'merge', parents: ['main', 'topic'] }, { sha: 'topic', parents: ['base'] }, { sha: 'main', parents: ['base'] }, { sha: 'base', parents: [] }]);
    expect(graph[0].edges.filter(e => e.start === 'node').map(e => e.to)).toEqual([0, 1]);
    expect(graph[1].lane).toBe(1);
    expect(graph[1].edges).toContainEqual({ from: 0, to: 0, color: graph[0].color, start: 'top', end: 'bottom' });
    expect(graph[2].edges.filter(e => e.start === 'node').map(e => e.to)).toEqual([0]);
    expect(graph[3].lane).toBe(0);
    expect(graph[3].color).toBe(graph[0].color);
    expect(graph[3].edges.filter(e => e.start === 'node')).toHaveLength(0);
  });
  it('preserves main lineage through interleaved topic commits and every row boundary', () => {
    const graph = commitGraph([
      { sha: 'merge', parents: ['main', 'topic'] },
      { sha: 'topic', parents: ['topic-old'] },
      { sha: 'main', parents: ['main-old'] },
      { sha: 'topic-old', parents: ['base'] },
      { sha: 'main-old', parents: ['base'] },
      { sha: 'base', parents: ['root'] },
      { sha: 'root', parents: [] },
    ]);
    const rows = new Map(graph.map(row => [row.sha, row]));
    expect(rows.get('base')!.color).toBe(rows.get('merge')!.color);
    expect(rows.get('root')!.color).toBe(rows.get('merge')!.color);
    expect(rows.get('topic-old')!.color).toBe(rows.get('topic')!.color);
    expect(rows.get('topic')!.color).not.toBe(rows.get('main')!.color);
    for (let i = 0; i < graph.length - 1; i++) {
      const outgoing = graph[i].edges.filter(e => e.end === 'bottom');
      const incoming = graph[i + 1].edges.filter(e => e.start === 'top');
      for (const edge of outgoing) expect(incoming).toContainEqual(expect.objectContaining({ from: edge.to, color: edge.color }));
      for (const edge of incoming) expect(outgoing).toContainEqual(expect.objectContaining({ to: edge.from, color: edge.color }));
    }
  });
  it('draws disconnected roots without inventing a parent and handles duplicate parent identities', () => {
    const graph = commitGraph([{ sha: 'a', parents: [] }, { sha: 'b', parents: ['c', 'c'] }, { sha: 'c', parents: [] }]);
    expect(graph[0].edges).toHaveLength(0);
    expect(graph[1].edges).toHaveLength(1);
    expect(graph[2].color).toBe(graph[1].color);
    expect(graphPath({ from: 0, to: 1, color: 0, start: 'node', end: 'bottom' })).toBe('M16 19H28Q40 19 40 31V38');
    expect(graphPath({ from: 1, to: 0, color: 0, start: 'node', end: 'bottom' })).toBe('M40 19H28Q16 19 16 31V38');
  });
});

describe('Loaded branch membership', () => {
  const ref = (name: string, sha: string, remote = false, current = false): GitRef => ({
    name, sha, remote, current, fullName: `refs/${remote ? 'remotes' : 'heads'}/${name}`,
  });
  it('attributes shared ancestry without assigning a topic its main-only descendants', () => {
    const main = ref('main', 'merge', false, true), topic = ref('feature/topic', 'topic'), remote = ref('origin/main', 'main', true);
    const result = branchesByCommit([
      { sha: 'merge', parents: ['main', 'topic'] },
      { sha: 'topic', parents: ['base'] },
      { sha: 'main', parents: ['base'] },
      { sha: 'base', parents: [] },
    ], [remote, topic, main]);
    expect(result.get('merge')).toEqual([main]);
    expect(result.get('topic')).toEqual([main, topic]);
    expect(result.get('main')).toEqual([main, remote]);
    expect(result.get('base')).toEqual([main, topic, remote]);
  });
  it('stops at unloaded parents and ignores missing branch tips', () => {
    const loaded = ref('main', 'tip'), missing = ref('origin/main', 'unloaded-tip', true);
    const result = branchesByCommit([
      { sha: 'tip', parents: ['unloaded-parent'] },
      { sha: 'old', parents: [] },
    ], [missing, loaded]);
    expect(result.get('tip')).toEqual([loaded]);
    expect(result.get('old')).toEqual([]);
    expect(result.has('unloaded-tip')).toBe(false);
    expect(result.has('unloaded-parent')).toBe(false);
  });
  it('deduplicates converging ancestry and refs, orders peers deterministically, and accepts unsorted commits', () => {
    const current = ref('z-current', 'tip', false, true), a = ref('a-local', 'tip'), b = ref('b-local', 'tip'), remote = ref('origin/a', 'tip', true);
    const result = branchesByCommit([
      { sha: 'base', parents: [] },
      { sha: 'right', parents: ['base'] },
      { sha: 'tip', parents: ['left', 'right', 'right'] },
      { sha: 'left', parents: ['base'] },
    ], [b, remote, a, b, current]);
    expect(result.get('base')).toEqual([current, a, b, remote]);
  });
});

describe('Git index groups', () => {
  it('shows partially staged files in both groups and untracked files only unstaged', () => {
    const base: GitFile = { path: 'a', oldPath: '', index: 'M', worktree: 'M', untracked: false, conflict: false };
    const files = [base, { ...base, path: 'b', index: ' ', worktree: 'M' }, { ...base, path: 'c', index: '?', worktree: '?', untracked: true }, { ...base, path: 'd', index: 'A', worktree: ' ' }];
    expect(stagedFiles(files).map(f => f.path)).toEqual(['a', 'd']);
    expect(unstagedFiles(files).map(f => f.path)).toEqual(['a', 'b', 'c']);
  });
});


describe('Branch folders', () => {
  const ref = (name: string): GitRef => ({ name, fullName: `refs/remotes/${name}`, sha: name, remote: true, current: false });
  it('nests every slash below its remote and keeps exact leaf refs', () => {
    const branch = ref('origin/dependabot/npm_and_yarn/desktop/vite');
    const result = branchTree([ref('origin/main'), branch, ref('origin/feature/login'), ref('upstream/other')], 'origin/');
    expect(result).toEqual([
      { kind: 'folder', name: 'dependabot', path: 'origin/dependabot', count: 1, children: [
        { kind: 'folder', name: 'npm_and_yarn', path: 'origin/dependabot/npm_and_yarn', count: 1, children: [
          { kind: 'folder', name: 'desktop', path: 'origin/dependabot/npm_and_yarn/desktop', count: 1, children: [
            { kind: 'branch', name: 'vite', path: branch.name, branch },
          ] },
        ] },
      ] },
      { kind: 'folder', name: 'feature', path: 'origin/feature', count: 1, children: [
        { kind: 'branch', name: 'login', path: 'origin/feature/login', branch: ref('origin/feature/login') },
      ] },
      { kind: 'branch', name: 'main', path: 'origin/main', branch: ref('origin/main') },
    ]);
  });
  it('counts descendants and keeps same-named leaves from distinct folders separate', () => {
    const branches = [ref('feature/api/login'), ref('feature/web/login'), ref('fix/login')];
    const result = branchTree(branches);
    expect(result.map(node => node.kind === 'folder' && [node.path, node.count])).toEqual([['feature', 2], ['fix', 1]]);
    expect(branches.map(branch => branch.name)).toEqual(['feature/api/login', 'feature/web/login', 'fix/login']);
  });
});
